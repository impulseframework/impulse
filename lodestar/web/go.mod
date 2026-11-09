// This file exists solely as a Go module boundary: it stops Go tooling (go build/test/vet ./...,
// golangci-lint) from descending into the Angular workspace's tree, where npm dependencies can ship
// stray Go files. The workspace holds the console and the portal; it is not a Go module in any real
// sense and contains no Go code.
module github.com/impulseframework/impulse/lodestar/web

go 1.27.3
