#!/usr/bin/env bash
# Mechanical guard for benchmark/README.md's isolation rule: no verifier or
# fixture content from this repo's benchmark/fixtures/ may ever land in any
# ref of bookang869/revi-hermes-target, committed or not, live or later
# reverted -- comments alone don't protect against a human repeating the
# same manual authoring steps 32 times (docs/observability-part-b.md
# "Locked: verifier isolation", second /grill-me pass, 2026-08-20).
#
# Scans every blob reachable from any ref (all branches and tags, not just
# the current checkout) for the "revi-benchmark-verifier" marker every
# verifier template carries (benchmark/fixtures/_template/verify.sh) and
# for any file literally named verify.sh, as a second check in case the
# marker comment was stripped. A leak stays reachable in git history (and
# so keeps failing this check) even if a later commit deletes it, until an
# actual gc/prune -- which is the property we want, since Hermes's clone
# can still see it via git log --all in the meantime.
#
# Usage: scripts/benchmark/check-isolation.sh <path-to-local-clone-of-revi-hermes-target>
# Run before dispatching any benchmark trial, and before merging any
# fixture-authoring commit in that repo.
set -euo pipefail

TARGET_REPO="${1:?usage: check-isolation.sh <path-to-revi-hermes-target-clone>}"
MARKER="revi-benchmark-verifier"

cd "$TARGET_REPO"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
	echo "error: $TARGET_REPO is not a git repository" >&2
	exit 2
}

violations=0

marker_hits=$(git rev-list --all 2>/dev/null | xargs -r git grep -l "$MARKER" 2>/dev/null | sort -u || true)
if [ -n "$marker_hits" ]; then
	echo "ISOLATION VIOLATION: '$MARKER' found in revi-hermes-target history:" >&2
	echo "$marker_hits" >&2
	violations=1
fi

name_hits=$(git rev-list --all 2>/dev/null | xargs -r -I{} git ls-tree -r --name-only {} 2>/dev/null | grep -x 'verify\.sh' || true)
if [ -n "$name_hits" ]; then
	echo "ISOLATION VIOLATION: a file named verify.sh exists somewhere in revi-hermes-target history." >&2
	violations=1
fi

if [ "$violations" -ne 0 ]; then
	exit 1
fi

echo "OK: no isolation violation found in $TARGET_REPO"
