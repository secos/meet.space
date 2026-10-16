// Command server runs meet.space.
//
//	server                 serve HTTP and run the background workers (the
//	                       email outbox poller)
//	server migrate up      apply pending migrations, then exit
//	server migrate down    revert the latest migration, then exit
//	server seed-dev        create the local test group (development only)
//
// Migrations never run on startup.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meet.space/internal/app"
	"meet.space/internal/assets"
	"meet.space/internal/clock"
	"meet.space/internal/config"
	"meet.space/internal/database"
	"meet.space/internal/email"
	"meet.space/internal/migrate"
	"meet.space/internal/site"
	"meet.space/internal/tmpl"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

// run is the whole program; environ is the environment to read config
// from, so tests can pass their own.
func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) error {
	cfg, err := config.FromEnviron(environ)
	if err != nil {
		return fmt.Errorf("config:\n%w", err)
	}
	switch {
	case len(args) == 0:
		return serve(ctx, cfg, stdout)
	case len(args) == 2 && args[0] == "migrate" && args[1] == "up":
		return migrate.Up(cfg.DatabaseURL)
	case len(args) == 2 && args[0] == "migrate" && args[1] == "down":
		return migrate.Down(cfg.DatabaseURL)
	case len(args) == 1 && args[0] == "seed-dev":
		return seedDev(ctx, cfg, stdout)
	}
	fmt.Fprintln(stderr, "usage: server [migrate up|migrate down|seed-dev]")
	return errors.New("unknown command")
}

// domainReloadInterval is how often serve reloads the custom-domain list;
// tests shorten it.
var domainReloadInterval = site.ReloadInterval

func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	if cfg.Env == config.EnvProduction {
		return slog.New(slog.NewJSONHandler(w, nil))
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

func serve(ctx context.Context, cfg config.Config, stdout io.Writer) error {
	logger := newLogger(cfg, stdout)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The schema must be current and clean before anything is served
	// (decisions.md "Schema version at startup"); deploys migrate first.
	have, want, err := migrate.Check(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if have > want {
		logger.Warn("database schema is newer than this build", "database", have, "build", want)
	}

	// The first load is strict: the server never starts serving custom
	// domains as unknown hosts. After that the list is reloaded every
	// minute, keeping the last good one if a reload fails (decisions.md
	// "Custom-domain list").
	domains := site.NewDomains(cfg.PublicHost, cfg.OriginHost)
	if err := domains.Load(ctx, pool); err != nil {
		return fmt.Errorf("load custom domains: %w", err)
	}
	reloadCtx, stopReload := context.WithCancel(ctx)
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		domains.Reload(reloadCtx, pool, domainReloadInterval, logger)
	}()
	// Runs before pool.Close.
	defer func() {
		stopReload()
		<-reloadDone
	}()

	dev := cfg.Env == config.EnvDevelopment
	assetSet, err := assets.New(cfg.StaticDir, dev)
	if err != nil {
		return err
	}
	renderer, err := tmpl.New(cfg.TemplateDir, assetSet, dev)
	if err != nil {
		return err
	}
	a, err := app.New(app.Deps{
		Config:   cfg,
		Pool:     pool,
		Clock:    clock.Real{},
		Domains:  domains,
		Renderer: renderer,
		Assets:   assetSet,
		Logger:   logger,
	})
	if err != nil {
		return err
	}

	// The SES sender lands in Phase 6. Until then a configured SES_REGION
	// is refused rather than quietly writing real users' login links to
	// the log.
	if cfg.SESRegion != "" {
		return errors.New("SES_REGION is set, but the SES sender is not built yet (Phase 6)")
	}
	outbox := &email.Outbox{
		Pool:       pool,
		Sender:     &email.LogSender{W: stdout},
		Clock:      clock.Real{},
		Logger:     logger,
		FromAuth:   cfg.SESFromAuth,
		FromNotify: cfg.SESFromNotify,
	}
	outboxCtx, stopOutbox := context.WithCancel(ctx)
	outboxDone := make(chan struct{})
	go func() {
		defer close(outboxDone)
		outbox.Run(outboxCtx)
	}()
	// Runs before pool.Close: a send in progress finishes first.
	defer func() {
		stopOutbox()
		<-outboxDone
	}()

	srv := &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	// The bound address, so ":0" in tests shows the real port.
	logger.Info("listening", "addr", ln.Addr().String(), "public_host", cfg.PublicHost, "env", cfg.Env)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
