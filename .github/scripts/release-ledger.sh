#!/usr/bin/env bash
# release-ledger refuses a release that would leave the upgrade ledger's last step behind.
# It runs on release-please's branch, where .release-please-manifest.json already carries
# the version the release pull request takes, and reads the release the last step of
# cmd/impulse/internal/ledger/ledger.go names (its release("vX.Y.Z") call). A step may
# name the release its pull request will take, or an earlier one, never a later one: impulse
# upgrade walks an application through the steps up to the running release, and a step
# naming a release that does not exist is a walk no release can finish. The check fails
# unless the release being cut is the step's release or a later one.
set -euo pipefail

manifest=.release-please-manifest.json
ledger=cmd/impulse/internal/ledger/ledger.go

if [ ! -f "$manifest" ]; then
  echo "release-ledger: no $manifest" >&2
  exit 2
fi
if [ ! -f "$ledger" ]; then
  echo "release-ledger: no $ledger" >&2
  exit 2
fi

release=$(jq -r '."."' "$manifest")
if ! [[ "$release" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release-ledger: $manifest names '$release' at \".\", not a version" >&2
  exit 2
fi

# The last step of the ledger is the last release("vX.Y.Z") in the file.
step=$(grep -oE 'release\("v[0-9]+\.[0-9]+\.[0-9]+"\)' "$ledger" | tail -n 1 | sed -E 's/^release\("v([^"]+)"\)$/\1/')
if [ -z "$step" ]; then
  echo "release-ledger: no release(\"vX.Y.Z\") step in $ledger" >&2
  exit 2
fi

# The lower of the two versions sorts first; the step may equal the release.
lowest=$(printf '%s\n%s\n' "$release" "$step" | sort -V | head -n 1)
if [ "$lowest" != "$step" ]; then
  echo "$ledger: the last step names v$step, but the release being cut is v$release; a step names the release its pull request will take, never one that the release leaves behind. Move the step to v$release, or release it with the change that needs it." >&2
  exit 1
fi
echo "release-ledger: v$release is being cut; the ledger's last step names v$step"
