// Command pwsgw is the personal weather station gateway daemon.
//
// Subcommands: run (default), version, check-config, hash-password.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wxalerts/pws-gateway/internal/app"
	"github.com/wxalerts/pws-gateway/internal/auth"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/logging"
	"github.com/wxalerts/pws-gateway/internal/platform"
	_ "github.com/wxalerts/pws-gateway/internal/platform/generic"
	_ "github.com/wxalerts/pws-gateway/internal/platform/raspberrypi"
	"github.com/wxalerts/pws-gateway/internal/version"
	"github.com/wxalerts/pws-gateway/internal/web"
	webdist "github.com/wxalerts/pws-gateway/web"
)

const (
	defaultDataDir      = "/var/lib/pwsgw"
	defaultListen       = ":443"
	defaultRedirect     = ":80"
	fallbackListen      = ":8443"
	fallbackRedirect    = ":8080"
	sessionPruneEvery   = time.Hour
	envDataDir          = "PWSGW_DATA_DIR"
	envListen           = "PWSGW_LISTEN"
	envHTTPRedirect     = "PWSGW_HTTP_REDIRECT"
	envLogLevel         = "PWSGW_LOG_LEVEL"
	passwordPromptLimit = 4096
)

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = runCmd(args)
	case "version":
		fmt.Println("pwsgw " + version.String())
	case "check-config":
		err = checkConfigCmd(args)
	case "hash-password":
		err = hashPasswordCmd(os.Stdin, os.Stdout)
	case "help":
		usage(os.Stdout)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pwsgw:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: pwsgw [run|version|check-config|hash-password] [flags]")
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

type runFlags struct {
	dataDir, listen, redirect, logLevel string
	rtl433Bin, replay                   string
	replaySpeed                         float64
}

func parseFlags(name string, args []string) (runFlags, error) {
	var f runFlags
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&f.dataDir, "data-dir", envOr(envDataDir, defaultDataDir), "data directory ("+envDataDir+")")
	fs.StringVar(&f.listen, "listen", os.Getenv(envListen), "HTTPS listen address, default from config or "+defaultListen+" ("+envListen+")")
	fs.StringVar(&f.redirect, "http-redirect", os.Getenv(envHTTPRedirect), "HTTP redirect address, default from config or "+defaultRedirect+" ("+envHTTPRedirect+")")
	fs.StringVar(&f.logLevel, "log-level", os.Getenv(envLogLevel), "debug, info, warn or error ("+envLogLevel+")")
	fs.StringVar(&f.rtl433Bin, "rtl433-bin", "rtl_433", "rtl_433 executable, resolved from PATH when not a path")
	fs.StringVar(&f.replay, "replay", "", "development: replay this rtl_433 JSONL file instead of starting rtl_433")
	fs.Float64Var(&f.replaySpeed, "replay-speed", 10, "replay speed multiplier, 0 for as fast as possible")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	return f, nil
}

func checkConfigCmd(args []string) error {
	f, err := parseFlags("check-config", args)
	if err != nil {
		return err
	}
	path := filepath.Join(f.dataDir, config.FileName)
	if _, err := config.Load(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Printf("%s does not exist; defaults will be used\n", path)
			return nil
		}
		return err
	}
	fmt.Printf("%s is valid\n", path)
	return nil
}

func hashPasswordCmd(in io.Reader, out io.Writer) error {
	line, err := bufio.NewReaderSize(in, passwordPromptLimit).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	pw := strings.TrimRight(line, "\r\n")
	if err := auth.CheckPassword(pw); err != nil {
		return err
	}
	h, err := auth.HashPassword(pw, auth.DefaultParams)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, h.PHC())
	return err
}

func runCmd(args []string) error {
	f, err := parseFlags("run", args)
	if err != nil {
		return err
	}
	level := logging.ParseLevel(f.logLevel)
	opts := logging.FromEnv()
	opts.Level = level
	logger, ring := logging.New(opts)
	slog.SetDefault(logger)
	logger.Info("starting pwsgw", "version", version.String(), "data_dir", f.dataDir)

	if err := os.MkdirAll(f.dataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	plat, err := platform.Detect()
	if err != nil {
		return err
	}
	logger.Info("platform detected", "model", plat.Board().Model, "tier", plat.Board().Tier)

	cfgStore := config.NewStore(f.dataDir)
	created, err := cfgStore.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if created {
		if err := cfgStore.Save(); err != nil {
			return fmt.Errorf("write default config: %w", err)
		}
		logger.Info("wrote default configuration", "path", cfgStore.Path())
	}
	cfg := cfgStore.Get()

	authStore, err := auth.NewStore(f.dataDir)
	if err != nil {
		return err
	}
	if err := authStore.Load(); err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	sessions := auth.NewSessions(f.dataDir, auth.SessionsOptions{})
	if err := sessions.Load(); err != nil {
		logger.Warn("ignoring unreadable sessions file", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := app.New(app.Deps{
		DataDir: f.dataDir, Config: cfgStore, Platform: plat, Logger: logger,
		Rtl433Bin: f.rtl433Bin, ReplayFile: f.replay, ReplaySpeed: f.replaySpeed, ReplayRewriteTime: true,
	})
	if err != nil {
		return err
	}

	dist, err := fs.Sub(webdist.Dist, "dist")
	if err != nil {
		return err
	}
	srv, err := web.New(web.Options{
		Platform: plat, Config: cfgStore, Auth: authStore, Sessions: sessions,
		Auditor: auth.Auditor{Logger: logger}, Logs: ring, Dist: dist,
		Stations: rt.Stations(), Readings: rt.Readings(), Status: rt.Status(), Logger: logger,
		Version: version.Version, Commit: version.Commit, BuildDate: version.BuildDate,
	})
	if err != nil {
		return err
	}

	cert, err := loadCert(f.dataDir, cfg.Web, logger)
	if err != nil {
		return err
	}
	httpsLn, err := listenWithFallback("https", firstNonEmpty(f.listen, cfg.Web.Listen, defaultListen), fallbackListen, logger)
	if err != nil {
		return err
	}
	var redirLn net.Listener
	if addr := firstNonEmpty(f.redirect, cfg.Web.HTTPRedirect, defaultRedirect); addr != "" && f.redirect != "off" {
		redirLn, err = listenWithFallback("http redirect", addr, fallbackRedirect, logger)
		if err != nil {
			_ = httpsLn.Close()
			return err
		}
	}
	logger.Info("web server listening", "https", httpsLn.Addr().String())

	go prune(ctx, sessions)

	appDone := make(chan struct{})
	go func() {
		defer close(appDone)
		if err := rt.Run(ctx); err != nil {
			logger.Error("runtime stopped", "err", err)
		}
	}()

	err = srv.Serve(ctx, httpsLn, redirLn, &cert)
	stop()
	<-appDone
	logger.Info("pwsgw stopped")
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func prune(ctx context.Context, s *auth.Sessions) {
	t := time.NewTicker(sessionPruneEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Prune()
		}
	}
}

func loadCert(dataDir string, w config.Web, logger *slog.Logger) (cert tls.Certificate, err error) {
	if w.CertFile != "" {
		return tls.LoadX509KeyPair(w.CertFile, w.KeyFile)
	}
	c, regenerated, err := web.EnsureCert(filepath.Join(dataDir, "tls"), web.LocalCertNames(), time.Now())
	if err != nil {
		return cert, err
	}
	if regenerated {
		logger.Info("generated self-signed certificate", "dir", filepath.Join(dataDir, "tls"))
	}
	return c, nil
}

// listenWithFallback binds addr. A permission error as an unprivileged user
// (the usual cause is a port below 1024) retries on the fallback address so a
// developer can run the daemon without setcap.
func listenWithFallback(what, addr, fallback string, logger *slog.Logger) (net.Listener, error) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err == nil {
		return ln, nil
	}
	if errors.Is(err, os.ErrPermission) && os.Geteuid() != 0 {
		logger.Warn("not permitted to bind, using fallback port", "listener", what, "addr", addr, "fallback", fallback)
		return lc.Listen(context.Background(), "tcp", fallback)
	}
	return nil, fmt.Errorf("listen %s %s: %w", what, addr, err)
}
