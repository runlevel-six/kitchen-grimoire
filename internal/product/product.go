// SPDX-License-Identifier: Apache-2.0

// Package product holds the names and build metadata that identify this
// program. Every user-visible product string comes from here, so a rename
// (OQ-11) touches one file plus the module path.
package product

import "runtime/debug"

const (
	// Name is the human-readable product name.
	Name = "Kitchen Grimoire"
	// Binary is the name of the server executable.
	Binary = "grimoire"
	// EnvPrefix prefixes every configuration environment variable.
	EnvPrefix = "GRIMOIRE_"
	// RepoURL is used in the honest User-Agent and in docs links.
	RepoURL = "https://github.com/runlevel-six/kitchen-grimoire"
)

// Version and Commit are set at build time with
//
//	-ldflags "-X github.com/runlevel-six/kitchen-grimoire/internal/product.Version=v1.2.3"
//
// When they are not set, Info falls back to the VCS data the Go toolchain
// embeds in the binary.
var (
	Version = ""
	Commit  = ""
)

// BuildInfo describes the running binary.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go"`
}

// Info returns the build metadata for the running binary.
func Info() BuildInfo {
	bi := BuildInfo{Version: Version, Commit: Commit}
	if info, ok := debug.ReadBuildInfo(); ok {
		bi.GoVersion = info.GoVersion
		if bi.Version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			bi.Version = info.Main.Version
		}
		if bi.Commit == "" {
			var dirty bool
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					bi.Commit = s.Value
				case "vcs.modified":
					dirty = s.Value == "true"
				}
			}
			if dirty && bi.Commit != "" {
				bi.Commit += "-dirty"
			}
		}
	}
	if bi.Version == "" {
		bi.Version = "dev"
	}
	return bi
}

// UserAgent is the honest User-Agent sent by the server-side fetcher (§8.2).
func UserAgent() string {
	ua := "KitchenGrimoire/" + Info().Version
	if RepoURL != "" {
		ua += " (+" + RepoURL + ")"
	}
	return ua
}
