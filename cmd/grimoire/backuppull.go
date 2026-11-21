// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/backup"
	"github.com/runlevel-six/kitchen-grimoire/internal/product"
)

// backupPull downloads a backup from a running server's admin port, checks
// it end to end, gives it its final name, and prunes old backups. It runs
// as the backup CronJob (§14.3). On any failure it exits non-zero and
// leaves no final file, so a half-downloaded archive is never mistaken for
// a good backup.
func backupPull(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backup-pull", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "admin base URL of the server, e.g. http://grimoire-admin:9090")
	out := fs.String("out", "", "directory to write the backup into")
	keep := fs.Int("keep", 14, "number of backups to keep in --out (older ones are deleted)")
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *out == "" || *keep < 1 {
		fmt.Fprintf(stderr, "%s backup-pull: --from and --out are required, and --keep must be at least 1\n", product.Binary)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	path, m, err := pull(ctx, *from, *out, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup-pull: %v\n", product.Binary, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (schema %d, %d recipes, %d images, %d files)\n",
		path, m.SchemaVersion, m.Counts.Recipes, m.Counts.Images, len(m.Files))

	removed, err := backup.Prune(*out, *keep)
	for _, p := range removed {
		fmt.Fprintf(stdout, "pruned %s\n", p)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s backup-pull: prune: %v\n", product.Binary, err)
		return 1
	}
	return 0
}

// pull downloads, verifies, and renames one backup. It returns the final
// path.
func pull(ctx context.Context, from, outDir string, now time.Time) (string, backup.Manifest, error) {
	var m backup.Manifest
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return "", m, err
	}
	url := strings.TrimSuffix(from, "/") + "/admin/backup"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", m, err
	}
	req.Header.Set("User-Agent", product.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", m, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	// Download to a hidden partial file in the same directory (so the
	// final rename is atomic), and delete it on any failure.
	f, err := os.CreateTemp(outDir, ".partial-*")
	if err != nil {
		return "", m, err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", m, fmt.Errorf("download: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", m, err
	}

	// Verify what is on disk, not what came over the wire: read the file
	// back from the start through the whole archive.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", m, err
	}
	m, err = backup.Verify(f)
	if err != nil {
		return "", m, err
	}
	if err := f.Close(); err != nil {
		return "", m, err
	}

	final := filepath.Join(outDir, backup.FileName(now))
	if _, err := os.Lstat(final); !errors.Is(err, fs.ErrNotExist) {
		return "", m, fmt.Errorf("%s already exists", final)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", m, err
	}
	ok = true
	return final, m, nil
}
