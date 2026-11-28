// Package request holds what a request's context carries about the request itself, for
// the packages that may not import one another to read it: the auth the request came
// through, which package resource binds (WithAuth, BindAuth), and the RPC method the
// request runs, which the method's handler hands it (resource.WithRPC). Package
// eventsource reads both to say who made a change.
package request

import "context"

// Auth is the one record of the auth a request came through: the auth's name, and
// whether a group bound it (resource.BindAuth), which holds the request to that auth.
type Auth struct {
	Name    string
	ByGroup bool
}

type authKey struct{}

type rpcKey struct{}

// WithAuth answers the context with the record of the auth the request came through. It
// holds no rule: package resource decides when a request is bound, and to what.
func WithAuth(ctx context.Context, auth Auth) context.Context {
	return context.WithValue(ctx, authKey{}, auth)
}

// AuthFrom answers the record of the auth the request came through, and whether anything
// bound one.
func AuthFrom(ctx context.Context) (auth Auth, ok bool) {
	auth, ok = ctx.Value(authKey{}).(Auth)

	return auth, ok
}

// WithRPC answers the context with the name of the RPC method the request runs.
func WithRPC(ctx context.Context, method string) context.Context {
	return context.WithValue(ctx, rpcKey{}, method)
}

// RPCFrom answers the name of the RPC method the request runs, and the empty text where
// none was handed.
func RPCFrom(ctx context.Context) string {
	method, _ := ctx.Value(rpcKey{}).(string)

	return method
}
