package resource

import (
	"context"
	"net/http"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// BindAuth is the middleware that says which auth a group's requests came through: the
// name of the auth package whose session, or whose tokens, authenticate them. The
// generated router mounts it in front of each outlet's group when the application
// declares several auths, and a surface written by hand mounts it in front of its own
// routes. It writes the one record of a request's auth, which AuthFrom reads, and holds
// the request to it: SessionPermissions then answers the request only from that auth's
// permission engine, and nothing mounted under the group binds it to another auth.
// WithAuth leaves it as it is; a second BindAuth that names another auth, and an
// outlet's generated routes mounted for another auth (live.Subscribing), refuse the
// request with a server error whose cause names both. An empty name binds nothing.
func BindAuth(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if name == "" {
			return next
		}

		return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
			ctx, err := bindAuth(r.Context(), name)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(r.Context(), err)
			}
			next.ServeHTTP(w, r.WithContext(ctx))

			return nil
		})
	}
}

// reboundAuthFmt is the refusal of a group's bind that names another auth than the one
// the request is bound to: the auth the request is bound to, the auth asked, the auth
// asked.
const reboundAuthFmt = "this request is bound to the %s auth, and a route under it asks resource.BindAuth to bind it to the %s auth. " +
	"A request comes through one auth, the one its outlet's group binds: take the second resource.BindAuth out of the route, " +
	"or serve the route from the %s auth's own outlet"

// bindAuth returns ctx bound to the auth name by a group (BindAuth), or the refusal
// (reboundAuthFmt) when ctx is bound to another auth already, by a group or by the
// generated routes (WithAuth).
func bindAuth(ctx context.Context, name string) (context.Context, error) {
	if held, ok := AuthFrom(ctx); ok && held != name {
		return ctx, errors.Newf(reboundAuthFmt, held, name, name)
	}

	return context.WithValue(ctx, authKey{}, authRecord{name: name, byGroup: true}), nil
}

// BoundAuth returns the auth a group bound the request to (BindAuth), or the empty
// string for a request no group bound, one whose generated routes alone bound it
// (WithAuth) among them: the name an application with several auths chooses a request's
// permission engine by, and the name SessionPermissions holds the engine to.
func BoundAuth(ctx context.Context) string {
	if record, ok := ctx.Value(authKey{}).(authRecord); ok && record.byGroup {
		return record.name
	}

	return ""
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
