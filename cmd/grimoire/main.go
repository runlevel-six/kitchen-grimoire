// SPDX-License-Identifier: Apache-2.0

// Command grimoire is the Kitchen Grimoire server.
//
// Usage:
//
//	grimoire serve      run the web server
//	grimoire migrate    apply or list database migrations
//	grimoire backup-pull download a backup from the admin port
//	grimoire restore    restore a backup into a data directory
//	grimoire doctor     check the runtime environment (SQLite features)
//	grimoire version    print build information
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/config"
	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/ingest"
	"github.com/runlevel-six/kitchen-grimoire/internal/metrics"
	"github.com/runlevel-six/kitchen-grimoire/internal/product"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/safefetch"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
	"github.com/runlevel-six/kitchen-grimoire/internal/web"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run is main without the process-global side effects, so it can be tested.
// It returns the process exit code.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(getenv, stderr)
	case "doctor":
		return doctor(args[1:], stdout, stderr)
	case "migrate":
		return migrate(args[1:], stdout, stderr, getenv)
	case "backup-pull":
		return backupPull(args[1:], stdout, stderr)
	case "restore":
		return restoreCmd(args[1:], stdout, stderr, getenv)
	case "version", "--version", "-v":
		return version(args[1:], stdout)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n", product.Binary, args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `%s: %s server

Usage:
  %[1]s serve            run the web server (configured by %[3]s* environment variables)
  %[1]s migrate [--status] [--reindex] [--data-dir DIR]
                         apply pending database migrations, or list them
  %[1]s backup-pull --from URL --out DIR [--keep N] [--timeout D]
                         download a backup from the admin port, verify it, prune old ones
  %[1]s restore --from FILE [--data-dir DIR] [--force]
                         verify and restore a backup (the server must be stopped)
  %[1]s doctor [DIR]
                         check SQLite features in a scratch dir (default: the OS temp dir)
  %[1]s version [--json]
                         print build information
`, product.Binary, product.Name, product.EnvPrefix)
}

func version(args []string, stdout io.Writer) int {
	info := product.Info()
	if len(args) > 0 && args[0] == "--json" {
		_ = json.NewEncoder(stdout).Encode(info)
		return 0
	}
	fmt.Fprintf(stdout, "%s %s (commit %s, %s)\n", product.Binary, info.Version, orNone(info.Commit), info.GoVersion)
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func serve(getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		// No logger yet; config errors go to stderr as plain text.
		fmt.Fprintf(stderr, "%s: invalid configuration:\n%v\n", product.Binary, err)
		return 1
	}

	log := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	info := product.Info()
	log.Info("starting", "version", info.Version, "commit", info.Commit, "go", info.GoVersion,
		"auth_mode", cfg.AuthMode, "data_dir", cfg.DataDir)
	if cfg.AuthMode == config.AuthNone {
		log.Warn("AUTHENTICATION DISABLED: every request is treated as user 'dev'. Never use this outside development.")
	}

	// ctx is cancelled on SIGINT (Ctrl-C) or SIGTERM (what Kubernetes sends
	// when stopping a pod); Serve then shuts down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open the database and bring the schema up to date before taking
	// traffic. There is one replica (§14.3), so nothing else can race us.
	db, err := store.OpenDataDir(ctx, cfg.DataDir)
	if err != nil {
		log.Error("open database", "err", err)
		return 1
	}
	defer db.Close()
	applied, err := store.Migrate(ctx, db)
	if err != nil {
		log.Error("migrate", "err", err)
		return 1
	}
	for _, m := range applied {
		log.Info("applied migration", "version", m.Version, "name", m.Name)
	}

	st := store.New(db)
	recipes := recipe.NewService(st)
	go purgeLoop(ctx, log, recipes, cfg.TrashDays)

	mux := httpx.NewMux(db.PingContext)
	if cfg.Dev {
		seedSamples(ctx, log, recipes)
	}
	files, err := imagestore.New(filepath.Join(cfg.DataDir, "images"))
	if err != nil {
		log.Error("image store", "err", err)
		return 1
	}
	go imageGCLoop(ctx, log, files, st)
	am := newAppMetrics(st)
	scrapes := am.reg.Counter("grimoire_scrape_total", "URL import attempts by outcome.", "result")
	fetcher := safefetch.New(safefetch.Config{Timeout: cfg.FetchTimeout, AllowHosts: cfg.FetchAllowHosts, ExtraDeny: cfg.FetchDeny})
	web.New(log, recipes, web.Images{Files: files, Repo: st}, web.Options{
		TrashDays:  cfg.TrashDays,
		Search:     st,
		Fetcher:    fetcher,
		Duplicates: st,
		OnScrape:   func(result string) { scrapes.Inc(result) },
		Favorites:  st,
		Bulk:       ingest.New(recipes, st, files),
		Reviewer:   st,
		Tokens:     st,
		Meals:      st,
		BaseURL:    cfg.BaseURL.String(),
	}).Register(mux)

	srv := httpx.NewServer(cfg.Listen, handlerChain(cfg, log, st, mux, am.reg), log)
	// The admin port (metrics, backup stream) has no auth; see adminHandler.
	admin := httpx.NewServer(cfg.AdminListen, adminHandler(log, am, db, files), log)
	if err := serveAll(ctx, log, srv, admin); err != nil {
		log.Error("server failed", "err", err)
		return 1
	}
	return 0
}

// doctor verifies that the SQLite features the app relies on work in this
// environment (e.g. inside the distroless image). It prints the result as
// JSON and exits non-zero on any failure.
func doctor(args []string, stdout, stderr io.Writer) int {
	dir := os.TempDir()
	if len(args) > 0 {
		dir = args[0]
	}
	res, err := store.Check(context.Background(), dir)
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	if err != nil {
		fmt.Fprintf(stderr, "%s doctor: %v\n", product.Binary, err)
		return 1
	}
	return 0
}

// purgeLoop empties trash older than days, once at start-up and then every
// few hours, until ctx is cancelled (FR-01).
func purgeLoop(ctx context.Context, log *slog.Logger, svc *recipe.Service, days int) {
	const every = 6 * time.Hour
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		n, err := svc.PurgeTrash(ctx, days)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("trash purge failed", "err", err)
		case n > 0:
			log.Info("purged trash", "recipes", n, "older_than_days", days)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// handlerChain wraps the router in the middleware, outermost first:
// request metrics, request logging, panic recovery, security headers, cross-origin (CSRF)
// protection, and authentication.
func handlerChain(cfg *config.Config, log *slog.Logger, users httpx.UserStore, mux *http.ServeMux, reg *metrics.Registry) http.Handler {
	auth := httpx.NewAuth(httpx.AuthConfig{
		None:           cfg.AuthMode == config.AuthNone,
		TrustedProxies: cfg.TrustedProxies,
		UserHeader:     cfg.UserHeader,
		NameHeader:     cfg.NameHeader,
		EmailHeader:    cfg.EmailHeader,
		GroupsHeader:   cfg.GroupsHeader,
		Admins:         cfg.Admins,
		AdminGroups:    cfg.AdminGroups,
		// /api/v1/capture authenticates with its own bearer token (§8.3).
		Public: []string{"/healthz", "/readyz", "/static/", "/sw.js", "/api/v1/capture"},
	}, users, log)

	// Rejects cross-site state-changing requests using Sec-Fetch-Site and
	// Origin (Go 1.25+). Same-site browser requests and non-browser clients
	// pass. The public origin is trusted explicitly because the proxy may
	// present a different Host.
	cop := http.NewCrossOriginProtection()
	if err := cop.AddTrustedOrigin(cfg.BaseURL.Scheme + "://" + cfg.BaseURL.Host); err != nil {
		log.Warn("cross-origin protection: bad base URL origin", "err", err)
	}

	var h http.Handler = auth.Middleware(mux)
	h = cop.Handler(h)
	h = httpx.SecurityHeaders(h)
	if cfg.BaseURL.Scheme == "https" {
		h = httpx.HSTS(h)
	}
	h = httpx.Recover(log, h)
	h = httpx.Logging(log, httpx.MuxRoute(mux), h)
	return metrics.Middleware(reg, httpx.MuxRoute(mux), h)
}

// seedSamples adds the invented sample recipes to an empty development
// database, so `make dev` has something to show.
func seedSamples(ctx context.Context, log *slog.Logger, svc *recipe.Service) {
	if _, n, err := svc.List(ctx, recipe.ListOptions{Limit: 1}); err != nil || n > 0 {
		return
	}
	for _, r := range web.SampleRecipes() {
		if err := svc.Create(ctx, r, ""); err != nil {
			log.Warn("seeding sample recipe failed", "title", r.Title, "err", err)
		}
	}
	log.Info("seeded sample recipes (dev mode, empty database)")
}

// imageGCLoop removes image files no recipe references anymore (after a
// photo is removed or a recipe is purged). Files younger than an hour are
// kept: an upload writes the file just before recording it.
func imageGCLoop(ctx context.Context, log *slog.Logger, files *imagestore.Store, st *store.Store) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var removed int
		err := files.Walk(func(sha, ext string) error {
			if fi, err := os.Stat(files.Path(sha, ext)); err != nil || time.Since(fi.ModTime()) < time.Hour {
				return nil
			}
			inUse, err := st.ImageInUse(ctx, sha)
			if err != nil || inUse {
				return err
			}
			removed++
			return files.Delete(sha, ext)
		})
		if err != nil && ctx.Err() == nil {
			log.Error("image GC failed", "err", err)
		} else if removed > 0 {
			log.Info("image GC removed unused files", "images", removed)
		}
	}
}
