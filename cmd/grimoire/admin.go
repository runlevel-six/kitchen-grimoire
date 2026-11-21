// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/backup"
	"github.com/runlevel-six/kitchen-grimoire/internal/httpx"
	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/metrics"
	"github.com/runlevel-six/kitchen-grimoire/internal/product"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// appMetrics holds the registry and the metrics the server updates directly.
type appMetrics struct {
	reg        *metrics.Registry
	lastBackup *metrics.Gauge
}

// newAppMetrics registers the application metrics (§14.5). The HTTP request
// metrics are registered by metrics.Middleware when the handler chain is
// built.
func newAppMetrics(st *store.Store) *appMetrics {
	reg := metrics.NewRegistry()
	info := product.Info()
	reg.GaugeFunc("grimoire_build_info", "Build information; the value is always 1.",
		func(context.Context) (float64, error) { return 1, nil },
		metrics.Label{Name: "version", Value: info.Version},
		metrics.Label{Name: "commit", Value: info.Commit},
		metrics.Label{Name: "go", Value: info.GoVersion})
	reg.GaugeFunc("grimoire_recipes", "Recipes in the library, not counting the trash.",
		func(ctx context.Context) (float64, error) {
			// A slow disk shouldn't hang the scrape.
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			n, err := st.CountLiveRecipes(ctx)
			return float64(n), err
		})
	last := reg.Gauge("grimoire_backup_last_success_timestamp",
		"Unix time of the last backup streamed completely from /admin/backup.")
	return &appMetrics{reg: reg, lastBackup: last}
}

// adminHandler serves the admin port: GET /metrics and GET /admin/backup.
//
// SECURITY: the admin port has NO authentication, by design. It must never
// be routed through an ingress or otherwise exposed outside the cluster;
// the Service is ClusterIP-only and the NetworkPolicy admits only the
// backup job (and a metrics scraper, if any). Anyone who can reach it can
// download the whole library.
func adminHandler(log *slog.Logger, m *appMetrics, db *sql.DB, files *imagestore.Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.reg.Handler())
	mux.HandleFunc("GET /admin/backup", func(w http.ResponseWriter, r *http.Request) {
		// A backup can take longer than the server's WriteTimeout; lift it
		// for this response only. (Logging's writer supports Unwrap, so the
		// controller reaches the real connection.)
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			log.Warn("backup: cannot lift write deadline", "err", err)
		}
		now := time.Now()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+backup.FileName(now)+`"`)
		w.Header().Set("Cache-Control", "no-store")
		man, err := backup.Write(r.Context(), db, files, w, backup.Meta{AppVersion: product.Info().Version, Now: now})
		if err != nil {
			log.Error("backup failed", "err", err)
			// The 200 header and part of the body are already sent, so the
			// status can't change. Abort the connection instead, so the
			// client sees a truncated stream rather than a clean end.
			panic(http.ErrAbortHandler)
		}
		m.lastBackup.SetToCurrentTime()
		log.Info("backup streamed", "recipes", man.Counts.Recipes, "images", man.Counts.Images,
			"files", len(man.Files), "duration", time.Since(now))
	})
	return httpx.Logging(log, httpx.MuxRoute(mux), mux)
}

// serveAll runs every server until ctx is cancelled, then shuts them all
// down gracefully. If one fails (for example its port is taken), the
// others are stopped too. It returns the first error.
func serveAll(ctx context.Context, log *slog.Logger, servers ...*http.Server) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, len(servers))
	for _, srv := range servers {
		go func() {
			err := httpx.Serve(ctx, srv, log)
			if err != nil {
				cancel() // bring the others down too
			}
			errc <- err
		}()
	}
	var first error
	for range servers {
		if err := <-errc; err != nil && first == nil {
			first = err
		}
	}
	return first
}
