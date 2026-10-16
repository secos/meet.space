// Command server runs meet.space.
//
//	server                 serve HTTP and run the background workers
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
	"meet.space/internal/migrate"
	"meet.space/internal/site"
	"meet.space/internal/tmpl"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := config.FromEnviron()
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

	// OPEN(Q19): what to do when the schema is behind, and when the domain
	// map can't be loaded or refreshed, is an open owner question. For now
	// a failed initial load stops the server rather than serving custom
	// domains as unknown hosts.
	domains := site.NewDomains(cfg.PublicHost, cfg.OriginHost)
	if err := domains.Load(ctx, pool); err != nil {
		return fmt.Errorf("load custom domains: %w", err)
	}

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

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", cfg.ListenAddr, "public_host", cfg.PublicHost, "env", cfg.Env)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
