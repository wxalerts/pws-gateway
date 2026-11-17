package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrDeferred means the download gave up after too many failed attempts. It
// is not a failure of the release: the update is retried later and reported
// as state "deferred".
var ErrDeferred = errors.New("update: download deferred after repeated failures")

// Download defaults.
const (
	DefaultMaxFailures   = 5
	DefaultHeaderTimeout = 30 * time.Second
	backoffBase          = 2 * time.Second
	backoffCap           = 5 * time.Minute
	copyBuf              = 32 << 10
)

// Downloader fetches an artifact with HTTP range requests into a partial
// file, verifying a hash per chunk as bytes arrive. A resumed download hashes
// what is already on disk, keeps the verified prefix and asks the server for
// the rest, so an interruption at 50 percent costs the other 50 percent only.
type Downloader struct {
	Client *http.Client
	// MaxFailures is how many attempts may end without verified progress
	// before Fetch gives up with ErrDeferred. Zero means DefaultMaxFailures.
	MaxFailures int
	// HeaderTimeout bounds the wait for response headers, the stand-in for
	// "latency too high" on a weak LTE link. Zero means DefaultHeaderTimeout.
	HeaderTimeout time.Duration
	// Sleep waits between attempts. nil sleeps on the real clock.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnRequest, when set, sees every request (tests assert Range headers).
	OnRequest func(*http.Request)
}

func (d *Downloader) maxFailures() int {
	if d.MaxFailures > 0 {
		return d.MaxFailures
	}
	return DefaultMaxFailures
}

func (d *Downloader) sleep(ctx context.Context, dur time.Duration) error {
	if d.Sleep != nil {
		return d.Sleep(ctx, dur)
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Backoff returns the wait before retry number n (1 based): exponential from
// two seconds, capped at five minutes.
func Backoff(n int) time.Duration {
	dur := backoffBase
	for i := 1; i < n && dur < backoffCap; i++ {
		dur *= 2
	}
	return min(dur, backoffCap)
}

// Progress reports verified bytes so far and the total.
type Progress func(done, total int64)

// Fetch downloads art from url into path, which may already hold a partial
// download. failures is the persisted count of consecutive failed attempts;
// it is updated in place so the caller can store it. On success path holds
// the complete, hash-verified file.
func (d *Downloader) Fetch(ctx context.Context, art Artifact, url, path string, failures *int, progress Progress) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	off, err := verifiedPrefix(f, art)
	if err != nil {
		return err
	}
	for off < art.Size {
		if *failures >= d.maxFailures() {
			return ErrDeferred
		}
		newOff, aerr := d.attempt(ctx, art, url, f, off, progress)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if aerr == nil && newOff == art.Size {
			break
		}
		if newOff > off {
			*failures = 0 // verified progress: a flaky link is not a dead one
		} else {
			*failures++
		}
		off = newOff
		if *failures >= d.maxFailures() {
			return errors.Join(ErrDeferred, aerr)
		}
		if err := d.sleep(ctx, Backoff(max(*failures, 1))); err != nil {
			return err
		}
	}
	return verifyWhole(f, art)
}

// verifiedPrefix truncates the partial file to its longest valid prefix and
// returns the offset to resume from. Complete chunks are checked against the
// manifest; a trailing partial chunk is kept and is checked when it completes.
func verifiedPrefix(f *os.File, art Artifact) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := st.Size()
	if size > art.Size {
		size = 0
	}
	full := size / art.ChunkSize
	good := int64(0)
	h := sha256.New()
	for i := int64(0); i < full; i++ {
		h.Reset()
		sr := io.NewSectionReader(f, i*art.ChunkSize, art.ChunkSize)
		if _, err := io.Copy(h, sr); err != nil {
			return 0, err
		}
		if hex.EncodeToString(h.Sum(nil)) != art.Chunks[i] {
			break
		}
		good++
	}
	keep := size
	if good < full {
		keep = good * art.ChunkSize
	}
	if err := f.Truncate(keep); err != nil {
		return 0, err
	}
	return keep, nil
}

func verifyWhole(f *os.File, art Artifact) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() != art.Size {
		return fmt.Errorf("update: %s is %d bytes, manifest says %d", art.Name, st.Size(), art.Size)
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, art.Size)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != art.SHA256 {
		return fmt.Errorf("update: %s does not match the manifest sha256", art.Name)
	}
	return nil
}

// attempt makes one ranged request from off and writes verified chunks. It
// returns the new verified-or-kept offset: on a chunk hash mismatch the file
// is cut back to that chunk's start so only the bad chunk is fetched again.
func (d *Downloader) attempt(ctx context.Context, art Artifact, url string, f *os.File, off int64, progress Progress) (int64, error) {
	// Hash state for the chunk that off lies inside.
	chunkStart := off - off%art.ChunkSize
	h := sha256.New()
	if off > chunkStart {
		if _, err := io.Copy(h, io.NewSectionReader(f, chunkStart, off-chunkStart)); err != nil {
			return off, err
		}
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return off, err
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-")
	if d.OnRequest != nil {
		d.OnRequest(req)
	}
	hdr := d.HeaderTimeout
	if hdr <= 0 {
		hdr = DefaultHeaderTimeout
	}
	timer := time.AfterFunc(hdr, cancel)
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	timer.Stop()
	if err != nil {
		return off, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusPartialContent:
		start, ok := contentRangeStart(resp.Header.Get("Content-Range"))
		if !ok || start != off {
			return off, fmt.Errorf("server answered range from %d, wanted %d", start, off)
		}
	case resp.StatusCode == http.StatusOK && off == 0:
	case resp.StatusCode == http.StatusOK:
		// The server ignored the range. Start over rather than splice.
		if err := f.Truncate(0); err != nil {
			return off, err
		}
		return 0, errors.New("server ignored the range request")
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		if err := f.Truncate(0); err != nil {
			return off, err
		}
		return 0, errors.New("range not satisfiable, restarting")
	default:
		return off, fmt.Errorf("server answered %s", resp.Status)
	}
	return d.copyVerified(f, art, resp.Body, off, h, progress)
}

func (d *Downloader) copyVerified(f *os.File, art Artifact, body io.Reader, off int64, h hash.Hash, progress Progress) (int64, error) {
	buf := make([]byte, copyBuf)
	for off < art.Size {
		chunkIdx := off / art.ChunkSize
		chunkEnd := min((chunkIdx+1)*art.ChunkSize, art.Size)
		want := min(int64(len(buf)), chunkEnd-off)
		n, rerr := body.Read(buf[:want])
		if n > 0 {
			if _, err := f.WriteAt(buf[:n], off); err != nil {
				return off, err
			}
			h.Write(buf[:n])
			off += int64(n)
			if off == chunkEnd {
				if hex.EncodeToString(h.Sum(nil)) != art.Chunks[chunkIdx] {
					start := chunkIdx * art.ChunkSize
					if err := f.Truncate(start); err != nil {
						return off, err
					}
					return start, fmt.Errorf("chunk %d failed its hash", chunkIdx)
				}
				h.Reset()
				if progress != nil {
					progress(off, art.Size)
				}
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) && off == art.Size {
				break
			}
			if err := f.Sync(); err != nil {
				return off, err
			}
			if errors.Is(rerr, io.EOF) {
				rerr = io.ErrUnexpectedEOF
			}
			return off, rerr
		}
	}
	return off, f.Sync()
}

// contentRangeStart parses "bytes START-END/TOTAL".
func contentRangeStart(v string) (int64, bool) {
	rest, ok := strings.CutPrefix(v, "bytes ")
	if !ok {
		return 0, false
	}
	startStr, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(startStr, 10, 64)
	return n, err == nil
}
