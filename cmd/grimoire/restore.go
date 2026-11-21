// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/runlevel-six/kitchen-grimoire/internal/backup"
	"github.com/runlevel-six/kitchen-grimoire/internal/product"
)

// restoreCmd restores a backup file into a data directory. The server must
// be stopped (scaled to zero) first: it holds the database open.
func restoreCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defDir := getenv(product.EnvPrefix + "DATA_DIR")
	if defDir == "" {
		defDir = "/data"
	}
	from := fs.String("from", "", "backup file (grimoire-*.tar.gz)")
	dataDir := fs.String("data-dir", defDir, "data directory to restore into")
	force := fs.Bool("force", false, "replace the database and images in a non-empty data directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" {
		fmt.Fprintf(stderr, "%s restore: --from is required\n", product.Binary)
		return 2
	}

	f, err := os.Open(*from)
	if err != nil {
		fmt.Fprintf(stderr, "%s restore: %v\n", product.Binary, err)
		return 1
	}
	defer f.Close()
	m, err := backup.Restore(context.Background(), f, *dataDir, *force)
	if err != nil {
		fmt.Fprintf(stderr, "%s restore: %v\n", product.Binary, err)
		return 1
	}
	fmt.Fprintf(stdout, "restored %s into %s\n", *from, *dataDir)
	fmt.Fprintf(stdout, "  created:  %s (by %s %s)\n", m.CreatedAt, product.Binary, m.AppVersion)
	fmt.Fprintf(stdout, "  schema:   migration %d\n", m.SchemaVersion)
	fmt.Fprintf(stdout, "  contents: %d recipes (including trash), %d images, %d files verified\n",
		m.Counts.Recipes, m.Counts.Images, len(m.Files))
	return 0
}
