// Package priv is the small, fixed set of root-only host changes the gateway
// can ask for: switching the I2C bus on or off in the boot configuration and
// disabling fake-hwclock. The daemon runs unprivileged and cannot write the
// boot partition, so it drops a request file in its state directory and a
// root systemd unit (deploy/systemd/pwsgw-priv.path) runs "pwsgw priv-apply".
//
// The request names one of a closed list of actions. It carries no path and no
// argument, so a corrupt or planted file can do nothing the list does not.
// The server has no way to write this file; only the local web UI can lead to
// it (AGENTS.md rule 4).
package priv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Action names one privileged operation. This list is the whole interface.
type Action string

// The allowed actions.
const (
	ActI2COn              Action = "i2c_enable"
	ActI2COff             Action = "i2c_disable"
	ActFakeHwclockDisable Action = "fake_hwclock_disable"
	// ActModemSignalPoll turns on ModemManager signal polling, which needs
	// an authorization the service account does not have.
	ActModemSignalPoll Action = "modem_signal_poll"
)

var allowed = map[Action]bool{ActI2COn: true, ActI2COff: true, ActFakeHwclockDisable: true, ActModemSignalPoll: true}

// Request and Result are the two files exchanged through the directory.
type Request struct {
	ID     string `json:"id"`
	Action Action `json:"action"`
}

// Result is the outcome of one request.
type Result struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

const (
	requestFile = "request.json"
	resultFile  = "result.json"
)

// RequestPath is the file the root unit watches.
func RequestPath(dir string) string { return filepath.Join(dir, requestFile) }

// ErrTimeout means the root helper did not answer in time, usually because
// the pwsgw-priv units are not installed.
var ErrTimeout = errors.New("priv: the root helper did not answer; is pwsgw-priv.path installed?")

// Submit writes a request into dir and waits for the helper's result.
func Submit(ctx context.Context, dir string, a Action, wait time.Duration) error {
	if !allowed[a] {
		return fmt.Errorf("priv: action %q is not allowed", a)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	req := Request{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Action: a}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, resultFile))
	tmp := filepath.Join(dir, requestFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, RequestPath(dir)); err != nil {
		return err
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if rb, err := os.ReadFile(filepath.Join(dir, resultFile)); err == nil {
			var res Result
			if json.Unmarshal(rb, &res) == nil && res.ID == req.ID {
				if !res.OK {
					return errors.New("priv: " + res.Error)
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			_ = os.Remove(RequestPath(dir))
			return ErrTimeout
		case <-tick.C:
		}
	}
}

// Runner runs a command, for systemctl.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Executor performs the actions as root.
type Executor struct {
	// BootConfig is /boot/firmware/config.txt.
	BootConfig string
	// ModulesFile lists the modules to load at boot, owned by pwsgw, so
	// i2c-dev is there once I2C is enabled.
	ModulesFile string
	Run         Runner
}

// DefaultExecutor has the Raspberry Pi OS paths.
func DefaultExecutor(run Runner) Executor {
	return Executor{
		BootConfig:  "/boot/firmware/config.txt",
		ModulesFile: "/etc/modules-load.d/pwsgw-i2c.conf",
		Run:         run,
	}
}

// Apply reads the pending request in dir, runs it and writes the result.
// A missing request is not an error: the path unit can fire twice.
func Apply(ctx context.Context, dir string, ex Executor) error {
	b, err := os.ReadFile(RequestPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var req Request
	runErr := json.Unmarshal(b, &req)
	if runErr == nil && !allowed[req.Action] {
		runErr = fmt.Errorf("action %q is not allowed", req.Action)
	}
	if runErr == nil {
		runErr = ex.do(ctx, req.Action)
	}
	res := Result{ID: req.ID, OK: runErr == nil}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	rb, mErr := json.Marshal(res)
	if mErr != nil {
		return mErr
	}
	tmp := filepath.Join(dir, resultFile+".tmp")
	// The unprivileged daemon must be able to read the result.
	if err := os.WriteFile(tmp, rb, 0o644); err != nil { //nolint:gosec // world readable on purpose, holds no secret
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, resultFile)); err != nil {
		return err
	}
	_ = os.Remove(RequestPath(dir))
	return runErr
}

// Do runs one allowed action.
func (ex Executor) Do(ctx context.Context, a Action) error { return ex.do(ctx, a) }

func (ex Executor) do(ctx context.Context, a Action) error {
	switch a {
	case ActI2COn:
		return ex.SetI2C(true)
	case ActI2COff:
		return ex.SetI2C(false)
	case ActFakeHwclockDisable:
		return ex.DisableFakeHwclock(ctx)
	case ActModemSignalPoll:
		if ex.Run == nil {
			return errors.New("no command runner")
		}
		_, err := ex.Run(ctx, "mmcli", "-m", "any", "--signal-setup=60")
		return err
	}
	return fmt.Errorf("unknown action %q", a)
}

var i2cLine = regexp.MustCompile(`^\s*#?\s*dtparam=i2c_arm=(on|off|1|0|true|false|yes|no)\s*$`)

// I2CEnabled reports whether config.txt content has an active
// dtparam=i2c_arm=on line. A later line wins, as the firmware does.
func I2CEnabled(config []byte) bool {
	on := false
	for _, l := range strings.Split(string(config), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if m := i2cLine.FindStringSubmatch(t); m != nil {
			switch m[1] {
			case "on", "1", "true", "yes":
				on = true
			default:
				on = false
			}
		}
	}
	return on
}

// EditI2C returns config.txt content with the I2C bus switched on or off,
// leaving every other line byte for byte as it was.
func EditI2C(config []byte, enable bool) []byte {
	lines := strings.Split(string(config), "\n")
	var out []string
	found := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if i2cLine.MatchString(t) {
			if enable {
				if !found {
					out = append(out, "dtparam=i2c_arm=on")
					found = true
				}
				// Drop duplicates and the disabled spellings.
				continue
			}
			if !strings.HasPrefix(t, "#") {
				out = append(out, "#"+t) // keep a record, switched off
			} else {
				out = append(out, l)
			}
			continue
		}
		out = append(out, l)
	}
	res := strings.Join(out, "\n")
	if enable && !found {
		res = strings.TrimRight(res, "\n") + "\n[all]\ndtparam=i2c_arm=on\n"
	}
	return []byte(res)
}

// SetI2C switches the bus in the boot configuration.
func (ex Executor) SetI2C(enable bool) error {
	cur, err := os.ReadFile(ex.BootConfig)
	if err != nil {
		return err
	}
	next := EditI2C(cur, enable)
	if bytes.Equal(cur, next) && I2CEnabled(cur) == enable {
		return ex.writeModules(enable)
	}
	// One backup of the original, never overwritten, so a bad edit is
	// recoverable by hand from the SD card.
	bak := ex.BootConfig + ".pwsgw-orig"
	if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(bak, cur, 0o644); err != nil { //nolint:gosec // boot config is world readable by design
			return err
		}
	}
	if err := writeAtomic(ex.BootConfig, next); err != nil {
		return err
	}
	return ex.writeModules(enable)
}

func (ex Executor) writeModules(enable bool) error {
	if ex.ModulesFile == "" {
		return nil
	}
	if !enable {
		err := os.Remove(ex.ModulesFile)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeAtomic(ex.ModulesFile, []byte("# Written by pwsgw: the I2C character device for the sensor pages.\ni2c-dev\n"))
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".pwsgw-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // boot config and modules-load.d are world readable by design
		return err
	}
	return os.Rename(tmp, path)
}

// DisableFakeHwclock disables the fake-hwclock units.
func (ex Executor) DisableFakeHwclock(ctx context.Context) error {
	if ex.Run == nil {
		return errors.New("no command runner")
	}
	// The service restores the saved time at boot, the timer or save unit
	// writes it. Both go; a unit that does not exist is not a failure.
	for _, unit := range []string{"fake-hwclock.service", "fake-hwclock-save.timer"} {
		if _, err := ex.Run(ctx, "systemctl", "disable", "--now", unit); err != nil {
			// systemctl is-enabled prints the state even when it exits
			// non-zero; a missing or already disabled unit is fine.
			out, _ := ex.Run(ctx, "systemctl", "is-enabled", unit) //nolint:errcheck // is-enabled exits non-zero for disabled units; only its output matters
			if !isDisabledOrMissing(string(out)) {
				return fmt.Errorf("disable %s: %w", unit, err)
			}
		}
	}
	return nil
}

func isDisabledOrMissing(out string) bool {
	s := strings.TrimSpace(out)
	return s == "disabled" || s == "masked" || s == "not-found" || s == ""
}
