package update_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wxalerts/pws-gateway/internal/update"
	"github.com/wxalerts/pws-gateway/internal/update/updatetest"
)

// payload is 40 KiB of non-repeating bytes, served in 4 KiB chunks.
func payload() []byte {
	b := make([]byte, 40<<10)
	for i := range b {
		b[i] = byte((i*7 + i/251) % 256)
	}
	return b
}

const chunk = 4 << 10

// flaky serves content with Range support and lets a test cut or corrupt
// specific responses.
type flaky struct {
	mu      sync.Mutex
	content []byte
	ranges  []string
	// cutAt > 0 makes the next response send that many bytes of the body
	// and then close the connection short.
	cutAt int
	// corrupt flips a byte at this absolute offset in the next response.
	corrupt   int
	failAll   bool
	ignoreRng bool
}

func (f *flaky) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.ranges = append(f.ranges, r.Header.Get("Range"))
	cut, corrupt, failAll, ign := f.cutAt, f.corrupt, f.failAll, f.ignoreRng
	f.cutAt, f.corrupt = 0, 0
	f.mu.Unlock()
	if failAll {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if ign {
		r.Header.Del("Range")
	}
	content := f.content
	if corrupt > 0 {
		content = append([]byte(nil), content...)
		content[corrupt] ^= 0xff
	}
	if cut > 0 {
		// Announce the full length, send part, then drop the connection.
		rec := httptest.NewRecorder()
		http.ServeContent(rec, r, "x", time.Time{}, bytes.NewReader(content))
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		body := rec.Body.Bytes()
		_, _ = w.Write(body[:min(cut, len(body))])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	http.ServeContent(w, r, "x", time.Time{}, bytes.NewReader(content))
}

func (f *flaky) rangesSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ranges...)
}

func setup(t *testing.T) (*flaky, *httptest.Server, update.Artifact, string) {
	t.Helper()
	data := payload()
	f := &flaky{content: data}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	art := updatetest.Artifact(update.KindApp, "pwsgw", srv.URL, data, chunk)
	return f, srv, art, filepath.Join(t.TempDir(), "pwsgw.part")
}

func noSleep(context.Context, time.Duration) error { return nil }

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func rng(off int) string { return "bytes=" + strconv.Itoa(off) + "-" }

func TestDownloadPlain(t *testing.T) {
	_, srv, art, path := setup(t)
	d := &update.Downloader{Sleep: noSleep}
	fails := 0
	if err := d.Fetch(context.Background(), art, srv.URL, path, &fails, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if sha(got) != art.SHA256 {
		t.Fatal("content mismatch")
	}
}

// A download interrupted at 50 percent resumes with a ranged request from the
// byte where it stopped.
func TestDownloadResumesFromInterruptedByte(t *testing.T) {
	f, srv, art, path := setup(t)
	half := len(f.content) / 2
	f.cutAt = half
	var sleeps []time.Duration
	d := &update.Downloader{Sleep: func(_ context.Context, dur time.Duration) error {
		sleeps = append(sleeps, dur)
		return nil
	}}
	fails := 0
	var done int64
	if err := d.Fetch(context.Background(), art, srv.URL, path, &fails, func(n, _ int64) { done = n }); err != nil {
		t.Fatal(err)
	}
	r := f.rangesSeen()
	if len(r) != 2 || r[0] != rng(0) || r[1] != rng(half) {
		t.Fatalf("ranges = %v, want 0 then %d", r, half)
	}
	if done != art.Size || fails != 0 || len(sleeps) != 1 {
		t.Fatalf("done=%d fails=%d sleeps=%v", done, fails, sleeps)
	}
	got, _ := os.ReadFile(path)
	if sha(got) != art.SHA256 {
		t.Fatal("content mismatch after resume")
	}
}

// A fresh process finds a partial file from an earlier run and continues it.
func TestDownloadResumesPartialFileFromEarlierRun(t *testing.T) {
	f, srv, art, path := setup(t)
	have := 20<<10 + 100
	if err := os.WriteFile(path, f.content[:have], 0o600); err != nil {
		t.Fatal(err)
	}
	d := &update.Downloader{Sleep: noSleep}
	fails := 0
	if err := d.Fetch(context.Background(), art, srv.URL, path, &fails, nil); err != nil {
		t.Fatal(err)
	}
	if r := f.rangesSeen(); len(r) != 1 || r[0] != rng(have) {
		t.Fatalf("ranges = %v", r)
	}
}

// A chunk whose hash does not match is cut off and fetched again, and the
// download still succeeds.
func TestDownloadRefetchesCorruptChunk(t *testing.T) {
	f, srv, art, path := setup(t)
	f.corrupt = 3*chunk + 17 // inside chunk 3
	d := &update.Downloader{Sleep: noSleep}
	fails := 0
	if err := d.Fetch(context.Background(), art, srv.URL, path, &fails, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if sha(got) != art.SHA256 {
		t.Fatal("content mismatch")
	}
	if r := f.rangesSeen(); len(r) != 2 || r[1] != rng(3*chunk) {
		t.Fatalf("want a second ranged request from the start of the bad chunk, got %v", r)
	}
}

// A corrupt chunk already on disk from an earlier run is discarded on resume.
func TestDownloadDiscardsBadChunkOnDisk(t *testing.T) {
	f, srv, art, path := setup(t)
	bad := append([]byte(nil), f.content[:3*chunk]...)
	bad[chunk+5] ^= 1
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &update.Downloader{Sleep: noSleep}
	fails := 0
	if err := d.Fetch(context.Background(), art, srv.URL, path, &fails, nil); err != nil {
		t.Fatal(err)
	}
	if r := f.rangesSeen(); len(r) != 1 || r[0] != rng(chunk) {
		t.Fatalf("ranges = %v", r)
	}
}

// After the configured number of failures the download is deferred, not
// failed, and the failure count is visible to the caller for persisting.
func TestDownloadDefersAfterNFailures(t *testing.T) {
	f, srv, art, path := setup(t)
	f.failAll = true
	var sleeps []time.Duration
	d := &update.Downloader{MaxFailures: 3, Sleep: func(_ context.Context, dur time.Duration) error {
		sleeps = append(sleeps, dur)
		return nil
	}}
	fails := 0
	err := d.Fetch(context.Background(), art, srv.URL, path, &fails, nil)
	if !errors.Is(err, update.ErrDeferred) {
		t.Fatalf("err = %v, want ErrDeferred", err)
	}
	if fails != 3 || len(f.rangesSeen()) != 3 {
		t.Fatalf("fails=%d requests=%d", fails, len(f.rangesSeen()))
	}
	if len(sleeps) != 2 || sleeps[0] != 2*time.Second || sleeps[1] != 4*time.Second {
		t.Fatalf("sleeps = %v", sleeps)
	}
	// With the count already at the limit a new call defers without a request.
	err = d.Fetch(context.Background(), art, srv.URL, path, &fails, nil)
	if !errors.Is(err, update.ErrDeferred) || len(f.rangesSeen()) != 3 {
		t.Fatalf("err = %v requests = %d", err, len(f.rangesSeen()))
	}
}

func TestDownloadServerIgnoringRangeRestarts(t *testing.T) {
	f, srv, art, path := setup(t)
	if err := os.WriteFile(path, f.content[:2*chunk], 0o600); err != nil {
		t.Fatal(err)
	}
	f.ignoreRng = true
	d := &update.Downloader{Sleep: noSleep}
	fails := 0
	if err := d.Fetch(context.Background(), art, srv.URL, path, &fails, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if sha(got) != art.SHA256 {
		t.Fatal("content mismatch")
	}
}

func TestBackoffCaps(t *testing.T) {
	if update.Backoff(1) != 2*time.Second || update.Backoff(20) != 5*time.Minute {
		t.Fatal(update.Backoff(1), update.Backoff(20))
	}
}
