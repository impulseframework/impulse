package app

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// ImpersonationID is the route parameter naming an impersonated session on the watch
// desk's revoke route.
const ImpersonationID httpio.ParamType = "impersonationID"

// activeImpersonation is one live view-as or act-as-role session as the watch desk lists
// it: who established it, as whom, why, and when it expires. Both people are named by
// username, which is what a person reads: Principal is the role's name, or the username
// of the person viewed as, never the id of the account the record names them by.
type activeImpersonation struct {
	SessionID impulse.UUID `json:"sessionId"`
	Actor     string       `json:"actor"`
	Principal string       `json:"principal"`
	Kind      string       `json:"kind"`
	Reason    string       `json:"reason"`
	StartedAt time.Time    `json:"startedAt"`
	ExpiresAt time.Time    `json:"expiresAt"`
}

// ActiveImpersonations lists every live impersonated session in the service for the
// Governor's watch desk. Its gate is the same manual Execute registration the mint route
// checks (resources.ViewAsUser): whoever may establish a view-as session may see the
// live ones.
//
// Demonstrates: impersonation.active-list.
func (a *App) ActiveImpersonations() http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		if err := a.checkWatchDesk(r); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		imps, err := a.API().ActiveImpersonations(ctx, &session.ImpersonationQuery{})
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.AuthAPI.ActiveImpersonations()"))
		}

		out := make([]activeImpersonation, 0, len(imps))
		for _, imp := range imps {
			described, err := a.describe(ctx, imp)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			out = append(out, described)
		}

		return httpio.NewEncoder(w).Ok(out)
	})
}

// RevokeImpersonation ends one impersonated session from the watch desk: the revoked
// console's next request is refused and its banner explains.
//
// Demonstrates: impersonation.revoke.
func (a *App) RevokeImpersonation() http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		if err := a.checkWatchDesk(r); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		id := httpio.Param[impulse.UUID](r, ImpersonationID)
		if err := a.API().DestroyImpersonatedSession(ctx, id); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.AuthAPI.DestroyImpersonatedSession()"))
		}

		w.WriteHeader(http.StatusNoContent)

		return nil
	})
}

// checkWatchDesk answers whether the caller may operate the watch desk: Execute on
// ViewAsUser in the global scope, fail-closed.
func (a *App) checkWatchDesk(r *http.Request) error {
	perms := a.UserPermissions(r)
	env := accesstypes.NewEnvironment().WithNow(time.Now())
	decisions, err := perms.Check(r.Context(), env, accesstypes.GlobalScope(), accesstypes.Execute, resources.ViewAsUser)
	if err != nil {
		return errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if !decisions[resources.ViewAsUser].IsGranted() {
		return httpio.NewForbiddenMessagef("user %s does not have Execute on %s", perms.User(), resources.ViewAsUser)
	}

	return nil
}

// describe flattens the library's record for the wire. The record names the person
// viewed as by the id of their account, so their username is read from the account; an
// account deleted since the session began has no name left, and the entry carries none.
func (a *App) describe(ctx context.Context, imp *sessioninfo.Impersonation) (activeImpersonation, error) {
	out := activeImpersonation{
		SessionID: imp.SessionID,
		Actor:     imp.Actor,
		Reason:    imp.Reason,
		StartedAt: imp.StartedAt,
		ExpiresAt: imp.ExpiresAt,
	}
	if role, ok := imp.Principal.Role(); ok {
		out.Kind, out.Principal = kindRole, string(role)

		return out, nil
	}
	user, ok := imp.Principal.User()
	if !ok {
		return out, nil
	}
	out.Kind = kindUser
	id, err := impulse.UUIDFromString(string(user))
	if err != nil {
		return out, errors.Wrapf(err, "the impersonation record of session %s names a user who is no account", imp.SessionID)
	}
	account, err := a.API().User(ctx, id)
	switch {
	case err == nil:
		out.Principal = account.Username
	case !httpio.HasNotFound(err):
		return out, errors.Wrap(err, "session.AuthAPI.User()")
	}

	return out, nil
}
