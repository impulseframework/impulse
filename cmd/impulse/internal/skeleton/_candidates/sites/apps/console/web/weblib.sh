#!/usr/bin/env bash
set -euo pipefail

# Rides this workspace on a local checkout of the Impulse Framework repository (beside this
# application as impulse/, or IMPULSE), the package-manager counterpart of the replace line --dev-root writes:
# while attached, the published pins in package.json are rewritten to file:.yalc specs, so a
# dirty `git status` IS the off-baseline flag. Never commit the yalc-modified package.json; the
# .yalc/ machinery itself is gitignored. The checkout's web/ workspace carries the three packages:
# the framework-neutral client @impulseframework/resource (the generated zz_gen_api.ts imports
# it), the Angular library @impulseframework/resource-angular, which depends on it, and the
# Firestore live transport @impulseframework/resource-firestore.
#
#   weblib.sh local     build the packages, publish them to the local yalc store, attach, bun install
#   weblib.sh push      rebuild the packages and update every attached consumer; then restart the
#                       dev server (`overmind restart console`), which does not watch node_modules
#   weblib.sh restore   detach and reinstall the pinned registry versions

GUI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMPULSE="${IMPULSE:-"$GUI_DIR/../../../../impulse"}"
WEB="$IMPULSE/web"
if [ ! -d "$WEB" ]; then
  echo "weblib.sh: no framework checkout at $IMPULSE; clone github.com/impulseframework/impulse beside this application or set IMPULSE" >&2
  exit 1
fi

build_and_publish() {
  (cd "$WEB" && bun run build)
  (cd "$WEB/dist/resource" && yalc publish --push)
  (cd "$WEB/dist/resource-angular" && yalc publish --push)
  (cd "$WEB/dist/resource-firestore" && yalc publish --push)
}

PACKAGES=(@impulseframework/resource @impulseframework/resource-angular @impulseframework/resource-firestore)

# yalc add rewrites the dependencies alone. Every other spec package.json holds for the three
# packages takes the same file:.yalc spec, the overrides entry first of all: bun installs what
# an override names in place of what the dependencies name, so an override left at the
# published version brings in the registry's client in place of the attached one, and the build
# fails on every export that release lacks.
attach_pins() {
  local name
  for name in "${PACKAGES[@]}"; do
    sed "s|\"$name\"\([[:space:]]*:[[:space:]]*\)\"[^\"]*\"|\"$name\"\1\"file:.yalc/$name\"|g" package.json > package.json.attaching
    mv package.json.attaching package.json
  done
}

case "${1:-}" in
  local)
    build_and_publish
    cd "$GUI_DIR"
    yalc add "${PACKAGES[@]}"
    attach_pins
    bun install
    ;;
  push)
    build_and_publish
    # The dev server bundles the packages with the application code (angular.json's serve
    # options keep them out of Vite's prebundle), so a restart is all a push still needs: no
    # cache to clear, and a plain reload in the browser shows the new build.
    echo "weblib.sh: packages pushed; restart the dev server to pick them up: overmind restart console"
    ;;
  restore)
    cd "$GUI_DIR"
    yalc remove --all
    rm -rf .yalc yalc.lock
    git checkout -- package.json
    if git ls-files --error-unmatch bun.lock > /dev/null 2>&1; then
      git checkout -- bun.lock
      bun install --frozen-lockfile
    else
      # No lockfile is committed here (an application made with --dev-root starts without one),
      # so the bun.lock in this directory was written while attached and records the yalc copies:
      # it goes, and the install writes one from the registry pins.
      rm -f bun.lock
      bun install
    fi
    ;;
  *)
    echo "usage: weblib.sh <local|push|restore>" >&2
    exit 1
    ;;
esac
