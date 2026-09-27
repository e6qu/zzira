#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Upgrade a database the way a deploy does: the base build migrates it and
# builds the demo company, then this checkout's build migrates the same
# database and builds the company over it twice. A migration that cannot run
# over the previous schema, or a demo that cannot run over what the previous
# build (or itself) left behind, fails here instead of on a deploy.
#
# Usage: DATABASE_URL=<an empty database> scripts/upgrade-test.sh <base-ref>
set -euo pipefail

base=${1:?usage: upgrade-test.sh <base-ref>}
: "${DATABASE_URL:?set DATABASE_URL to an empty database}"

root=$(git rev-parse --show-toplevel)
work=$(mktemp -d)
cleanup() {
  git -C "$root" worktree remove --force "$work/base" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

tables=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'")
if [ "$tables" != "0" ]; then
  echo "upgrade-test: $DATABASE_URL is not empty ($tables tables); it must start from nothing" >&2
  exit 1
fi

git -C "$root" worktree add --detach "$work/base" "$base" >/dev/null
(cd "$work/base" && go build -o "$work/zzira-base" ./cmd/server)
(cd "$root" && go build -o "$work/zzira-head" ./cmd/server)
export DATA_DIR="$work/data"
mkdir -p "$DATA_DIR"

step() {
  local name=$1
  shift
  local started=$SECONDS
  echo "== $name"
  "$@"
  echo "   $((SECONDS - started))s"
}

step "base ($base): migrate" "$work/zzira-base" -mode=migrate
step "base: build the demo company" "$work/zzira-base" -mode=demo
step "head: migrate the base's schema" "$work/zzira-head" -mode=migrate
step "head: build the demo company over the base's" "$work/zzira-head" -mode=demo
step "head: build it again over its own" "$work/zzira-head" -mode=demo

# A re-run finds a sprint by its name on its board; two of one name are what
# made the demo reopen a closed sprint.
duplicates=$(psql "$DATABASE_URL" -tAc \
  "SELECT count(*) FROM (SELECT board_id, name FROM sprints GROUP BY board_id, name HAVING count(*) > 1) AS d")
if [ "$duplicates" != "0" ]; then
  echo "upgrade-test: $duplicates board/sprint names are held by more than one sprint" >&2
  exit 1
fi
echo "upgrade from $base: ok"
