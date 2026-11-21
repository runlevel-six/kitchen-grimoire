#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# check-private.sh: keep private data out of the repository (docs/DESIGN.md §17).
#
# Reads local/denylist.txt (gitignored; one term per line, '#' comments allowed)
# and fails if any term appears, case-insensitively and as a whole word, in:
#
#   (default)        the lines added by the staged diff
#   --msg FILE       a commit message file (used by the commit-msg hook)
#   --all            every tracked file in the working tree
#   --range A..B     the lines added in a commit range, plus those commits' messages
#
# Without a denylist (e.g. in CI) it only checks that nothing under local/ is tracked.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"
denylist="local/denylist.txt"
fail=0

# 1. Nothing under local/ may ever be tracked or staged.
if [ -n "$(git ls-files -- local/)" ] || [ -n "$(git diff --cached --name-only -- local/)" ]; then
	echo "check-private: files under local/ are tracked or staged:" >&2
	{ git ls-files -- local/; git diff --cached --name-only -- local/; } | sort -u | sed 's/^/  /' >&2
	fail=1
fi

if [ ! -f "$denylist" ]; then
	echo "check-private: no $denylist; only checked that local/ is untracked." >&2
	exit "$fail"
fi

# Strip comments and blank lines into a pattern file.
patterns="$(mktemp)"
trap 'rm -f "$patterns"' EXIT
grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$denylist" > "$patterns" || true
if [ ! -s "$patterns" ]; then
	exit "$fail"
fi

# scan LABEL: reads text on stdin and reports matching lines. Feed it with
# redirection, not a pipe: a piped function runs in a subshell and loses fail=1.
scan() {
	local label="$1" hits
	hits="$(grep -n -i -w -F -f "$patterns" || true)"
	if [ -n "$hits" ]; then
		echo "check-private: denylisted term found in $label:" >&2
		echo "$hits" | sed 's/^/  /' >&2
		fail=1
	fi
}

added_lines() { grep -E '^\+' | grep -v -E '^\+\+\+ ' || true; }

case "${1:-}" in
	"")
		scan "staged changes" < <(git diff --cached --no-color -U0 | added_lines)
		;;
	--msg)
		scan "commit message" < "${2:?--msg needs a file}"
		;;
	--all)
		while IFS= read -r -d '' f; do
			[ -f "$f" ] && scan "$f" < "$f"
		done < <(git ls-files -z)
		;;
	--range)
		range="${2:?--range needs A..B}"
		scan "diff $range" < <(git diff --no-color -U0 "$range" | added_lines)
		scan "commit messages in $range" < <(git log --format='%B' "$range")
		;;
	*)
		echo "usage: $0 [--msg FILE | --all | --range A..B]" >&2
		exit 2
		;;
esac

exit "$fail"
