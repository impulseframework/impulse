package resource

import (
	"context"
	"net/http"

	"github.com/impulseframework/impulse/accesstypes"
)

type boundAuthKey struct{}

// BindAuth is the middleware that says which auth a group's requests came through:
// the name of the auth package whose session, or whose tokens, authenticate them. The
// generated router mounts it in front of each outlet's group when the application
// declares several auths, and a surface written by hand mounts it in front of its own
// routes. SessionPermissions then answers such a request only from that auth's
// permission engine. An empty name binds nothing.
func BindAuth(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if name == "" {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), boundAuthKey{}, name)))
		})
	}
}

// BoundAuth returns the auth the request came through (BindAuth), or the empty
// string for a request no group bound: the name an application with several auths
// chooses a request's permission engine by.
func BoundAuth(ctx context.Context) string {
	name, _ := ctx.Value(boundAuthKey{}).(string)

	return name
}

// otherAuthRefusal holds a request bound to an auth (BindAuth) to that auth's
// permission engine. It returns the fail-closed permissions (otherAuthPermissions)
// when the checker forUser yields for the session's person answers from another
// auth's engine, or from one opened under no auth's name, and for a role principal
// when the checker forRole yields does; the forUser checker is judged first. It
// returns nil when both answer from the bound auth's engine, when forRole yields
// none (the role is then answered by the fail-closed role checker), and for a
// request no group bound, whose checkers are not asked.
func otherAuthRefusal[U SessionUserChecker, R RolePermissions](
	ctx context.Context, principal accesstypes.Principal, forUser func(accesstypes.User) U, forRole func(accesstypes.Role) R,
) UserPermissions {
	bound := BoundAuth(ctx)
	if bound == "" {
		return nil
	}
	role, isRole := principal.Role()
	var person accesstypes.User
	if isRole {
		person = roleSessionUser(ctx)
	} else {
		person, _ = principal.User()
	}
	if engine := forUser(person).Auth(); engine != bound {
		return otherAuthPermissions{user: person, bound: bound, engine: engine}
	}
	if !isRole || forRole == nil {
		return nil
	}
	var rolePerms RolePermissions = forRole(role)
	if rolePerms == nil {
		return nil
	}
	if engine := rolePerms.Auth(); engine != bound {
		return otherAuthPermissions{user: person, bound: bound, engine: engine}
	}

	return nil
}
