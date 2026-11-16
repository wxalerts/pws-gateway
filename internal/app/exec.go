package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector/rtl433"
)

// killGrace is how long a child gets between SIGTERM and SIGKILL.
const killGrace = 5 * time.Second

// execStart returns the production StartFunc. It runs name from PATH with
// os/exec, logs the exact argv at info level, and stops the child politely:
// Kill sends SIGTERM and escalates to SIGKILL after grace. rtl_433 releases
// the USB interface cleanly on SIGTERM, which a hard kill can leave claimed.
//
// The command is not bound to ctx. The supervisor calls Kill itself when its
// context ends, and exec.CommandContext would race that with an immediate
// SIGKILL.
func execStart(log *slog.Logger, grace time.Duration) rtl433.StartFunc {
	return func(ctx context.Context, name string, args []string) (rtl433.Process, error) {
		argv := append([]string{name}, args...)
		log.Info("starting rtl_433", "argv", strings.Join(argv, " "))
		cmd := exec.CommandContext(context.WithoutCancel(ctx), name, args...) //nolint:gosec // binary name is operator configuration, args are built by rtl433.Radio
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &child{cmd: cmd, stdout: stdout, stderr: stderr, grace: grace, log: log}, nil
	}
}

type child struct {
	cmd    *exec.Cmd
	stdout io.Reader
	stderr io.Reader
	grace  time.Duration
	log    *slog.Logger

	mu    sync.Mutex
	timer *time.Timer
}

func (c *child) Stdout() io.Reader { return c.stdout }
func (c *child) Stderr() io.Reader { return c.stderr }
func (c *child) PID() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *child) Wait() error {
	err := c.cmd.Wait()
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	return err
}

// Kill sends SIGTERM and arms a SIGKILL for after the grace period.
func (c *child) Kill() error {
	if c.cmd.Process == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		return nil
	}
	err := c.cmd.Process.Signal(syscall.SIGTERM)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	c.timer = time.AfterFunc(c.grace, func() {
		c.log.Warn("rtl_433 ignored SIGTERM, sending SIGKILL", "pid", c.PID())
		if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			c.log.Warn("rtl_433 SIGKILL failed", "pid", c.PID(), "err", err)
		}
	})
	return nil
}
