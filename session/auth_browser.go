package session

import (
	"context"
	"net/http"

	"github.com/impulseframework/impulse/session/internal/browsername"
)

// browserCtxKey is the context key for the name of the request's browser, set by
// StartSession.
type browserCtxKey struct{}

// withBrowser returns ctx with the name of r's browser, worked out from its User-Agent
// header (browsername.Of): what a session the request makes keeps, so the person tells
// their sessions apart on their account page. The header itself is kept nowhere.
func withBrowser(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, browserCtxKey{}, browsername.Of(r.UserAgent()))
}

// browserFromCtx returns the name of the request's browser that withBrowser put in ctx,
// empty where none was put or the browser was not recognized.
func browserFromCtx(ctx context.Context) string {
	name, _ := ctx.Value(browserCtxKey{}).(string)

	return name
}
