package collector

import (
	"context"
	"io"
	"sync"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
)

// Source yields rtl_433 lines. *rtl433.Supervisor implements it directly.
// The channel closing means the source is finished and the pipeline should
// drain and stop.
type Source interface {
	Lines() <-chan rtl433.Line
}

// ChanSource adapts a plain channel to Source.
type ChanSource <-chan rtl433.Line

// Lines implements Source.
func (c ChanSource) Lines() <-chan rtl433.Line { return c }

// ReplaySource runs a Replay in the background and closes its channel when
// the recording ends or the context is canceled.
type ReplaySource struct {
	lines chan rtl433.Line
	mu    sync.Mutex
	err   error
}

// NewReplayFile starts replaying a .jsonl file.
func NewReplayFile(ctx context.Context, r *rtl433.Replay, path string) *ReplaySource {
	return startReplay(func(out chan<- rtl433.Line) error { return r.RunFile(ctx, path, out) })
}

// NewReplayReader starts replaying lines from a reader.
func NewReplayReader(ctx context.Context, r *rtl433.Replay, src io.Reader) *ReplaySource {
	return startReplay(func(out chan<- rtl433.Line) error { return r.Run(ctx, src, out) })
}

func startReplay(run func(chan<- rtl433.Line) error) *ReplaySource {
	rs := &ReplaySource{lines: make(chan rtl433.Line, 256)}
	go func() {
		err := run(rs.lines)
		rs.mu.Lock()
		rs.err = err
		rs.mu.Unlock()
		close(rs.lines)
	}()
	return rs
}

// Lines implements Source.
func (r *ReplaySource) Lines() <-chan rtl433.Line { return r.lines }

// Err returns the replay error once Lines is closed.
func (r *ReplaySource) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
