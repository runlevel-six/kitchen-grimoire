// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/runlevel-six/kitchen-grimoire/internal/product"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// migrate applies pending migrations (or lists them with --status). It needs
// only the data directory, so it works before the rest of the server is
// configured, e.g. from a one-off Kubernetes Job.
func migrate(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defDir := getenv(product.EnvPrefix + "DATA_DIR")
	if defDir == "" {
		defDir = "/data"
	}
	dataDir := fs.String("data-dir", defDir, "data directory holding "+store.DBFile)
	status := fs.Bool("status", false, "list migrations and whether each is applied; change nothing")
	reindex := fs.Bool("reindex", false, "after migrating, rebuild the full-text search index")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx := context.Background()
	db, err := store.OpenDataDir(ctx, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "%s migrate: %v\n", product.Binary, err)
		return 1
	}
	defer db.Close()

	if *status {
		st, err := store.Status(ctx, db)
		if err != nil {
			fmt.Fprintf(stderr, "%s migrate: %v\n", product.Binary, err)
			return 1
		}
		for _, s := range st {
			applied := "pending"
			if s.AppliedAt != "" {
				applied = "applied " + s.AppliedAt
			}
			fmt.Fprintf(stdout, "%04d_%-24s %s\n", s.Version, s.Name, applied)
		}
		return 0
	}

	done, err := store.Migrate(ctx, db)
	for _, m := range done {
		fmt.Fprintf(stdout, "applied %04d_%s\n", m.Version, m.Name)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s migrate: %v\n", product.Binary, err)
		return 1
	}
	if len(done) == 0 {
		fmt.Fprintln(stdout, "database is up to date")
	}
	if *reindex {
		n, err := store.New(db).Reindex(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s migrate: reindex: %v\n", product.Binary, err)
			return 1
		}
		fmt.Fprintf(stdout, "reindexed %d recipes\n", n)
	}
	return 0
}
