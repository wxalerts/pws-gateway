package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wxalerts/pws-gateway/internal/app"
	"github.com/wxalerts/pws-gateway/internal/platform/nmdbus"
	"github.com/wxalerts/pws-gateway/internal/platform/rpiboot"
	"github.com/wxalerts/pws-gateway/internal/update"
	"github.com/wxalerts/pws-gateway/internal/version"
)

// envNoHandOver stops the hand over to an app slot binary, set by the
// hand over itself so it cannot loop and usable to force the built-in binary.
const envNoHandOver = "PWSGW_NO_HANDOVER"

// handOverToAppSlot runs the active app slot binary instead of this one when
// it is newer. An app update installs under the data directory, which is the
// only place the hardened service may write, and this is what makes the
// service run it. Failing to exec is not fatal: the built-in binary carries
// on, and the updater's start check reports the failed hand over as a
// rollback.
func handOverToAppSlot(args []string) {
	if os.Getenv(envNoHandOver) != "" || (len(args) > 0 && args[0] != "run" && !strings.HasPrefix(args[0], "-")) {
		return
	}
	self, err := update.VersionNumber(version.Version)
	if err != nil {
		return
	}
	dir := dataDirFromArgs(args)
	target, ok := update.ReexecTarget(filepath.Join(dir, "update", "app"), self)
	if !ok {
		return
	}
	env := append(os.Environ(), envNoHandOver+"=1")
	if err := syscall.Exec(target, append([]string{target}, os.Args[1:]...), env); err != nil { //nolint:gosec // target is the active app slot, installed after its hash was verified
		slog.Error("hand over to the app slot failed, running the built-in binary", "target", target, "err", err)
	}
}

// dataDirFromArgs finds --data-dir without parsing the whole command line,
// because the hand over happens before logging and flags are set up.
func dataDirFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "--data-dir" || a == "-data-dir":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--data-dir="):
			return strings.TrimPrefix(a, "--data-dir=")
		case strings.HasPrefix(a, "-data-dir="):
			return strings.TrimPrefix(a, "-data-dir=")
		}
	}
	return envOr(envDataDir, defaultDataDir)
}

// m6Deps builds the optional hardware adapters of the updater and the
// network manager from what the host actually has.
func m6Deps(f runFlags, stop context.CancelFunc, logger *slog.Logger) app.M6Deps {
	d := app.M6Deps{ManifestBase: f.manifestBase, AllowInsecureManifest: os.Getenv("PWSGW_UPDATE_INSECURE") == "1"}
	d.Restart = func() {
		logger.Info("restarting to switch to the updated binary")
		stop()
	}
	if _, err := os.Stat(f.autobootFile); err == nil {
		d.Slots = rpiboot.New(rpiboot.Config{AutobootFile: f.autobootFile}, rpiboot.OSHost{})
		logger.Info("A/B boot slots available", "autoboot", f.autobootFile)
	} else {
		logger.Info("no autoboot.txt, OS image updates are off", "path", f.autobootFile)
	}
	if _, err := os.Stat("/run/NetworkManager"); err == nil {
		if nm, err := nmdbus.New(nmdbus.Options{}); err == nil {
			d.Net = nm
		} else {
			logger.Warn("NetworkManager adapter unavailable", "err", err)
		}
	} else {
		logger.Info("NetworkManager is not running, the network page is read only")
	}
	return d
}
