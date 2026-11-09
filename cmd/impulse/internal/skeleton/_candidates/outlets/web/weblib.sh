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
IMPULSE="${IMPULSE:-"$GUI_DIR/../../impulse"}"
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

case "${1:-}" in
  local)
    build_and_publish
    (cd "$GUI_DIR" && yalc add @impulseframework/resource @impulseframework/resource-angular @impulseframework/resource-firestore && bun install)
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
    git checkout -- package.json bun.lock
    bun install --frozen-lockfile
    ;;
  *)
    echo "usage: weblib.sh <local|push|restore>" >&2
    exit 1
    ;;
esac
