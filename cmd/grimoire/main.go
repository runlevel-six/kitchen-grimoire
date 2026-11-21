// SPDX-License-Identifier: Apache-2.0

// Command grimoire is the Kitchen Grimoire server.
//
// Usage:
//
//	grimoire serve      run the web server
//	grimoire version    print build information
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"kitchen-grimoire/internal/config"
	"kitchen-grimoire/internal/httpx"
	"kitchen-grimoire/internal/product"
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
  %[1]s version [--json] print build information
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

	srv := httpx.NewServer(cfg.Listen, httpx.NewMux(), log)
	if err := httpx.Serve(ctx, srv, log); err != nil {
		log.Error("server failed", "err", err)
		return 1
	}
	return 0
}
