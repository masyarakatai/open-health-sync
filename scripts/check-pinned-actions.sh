#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Checks that every action a workflow uses is pinned by a full commit SHA
# (owner/repo@<40 hex>, the release it resolves to in a comment), so a moved
# tag cannot change what CI runs. Local actions (./path) and docker images
# pinned by digest (docker://image@sha256:...) pass too. Dependabot keeps the
# pins current.
#
#   scripts/check-pinned-actions.sh [DIR]   # default: .github/workflows
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
dir="${1:-$root/.github/workflows}"

status=0
while IFS= read -r hit; do
  file="${hit%%:*}"
  rest="${hit#*:}"
  line="${rest%%:*}"
  ref="$(printf '%s' "${rest#*:}" | sed -E 's/^[[:space:]-]*uses:[[:space:]]*//; s/[[:space:]]+#.*$//; s/^["'\'']//; s/["'\'']$//')"
  case "$ref" in
    ./*) continue ;;
    docker://*@sha256:*) continue ;;
  esac
  if ! printf '%s' "$ref" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$'; then
    echo "check-pinned-actions: ${file#"$root"/}:$line uses $ref, not pinned by a commit SHA" >&2
    status=1
  fi
done < <(grep -HnE '^[[:space:]-]*uses:' "$dir"/*.yml "$dir"/*.yaml 2>/dev/null || true)

if [ "$status" -eq 0 ]; then
  echo "check-pinned-actions: every action is pinned by a commit SHA"
fi
exit "$status"
