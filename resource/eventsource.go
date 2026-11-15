package resource

import (
	"context"
	"fmt"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// UserEvent generates a standard event source string for an action performed by a user.
// It extracts user information from the context. A person is written as their username
// with the id of their account, since the name is what people read and can change, and
// the id is who they are; the session comes last, as the evidence of how they were
// signed in: "username (account id), session id" for an ordinary session. For an
// impersonated session the actor comes first, so every data change carries evidence of
// the real person: "actor (account id) impersonating username (account id), session id"
// for an impersonated user, "actor (account id) as role name, session id" for a role.
// A person with no account here (a session type that keeps none, an actor another
// application authenticated) is written by name alone.
func UserEvent(ctx context.Context) string {
	sess := sessioninfo.DataFromCtx(ctx)

	imp := sess.Impersonation
	if imp == nil {
		return fmt.Sprintf("%s, session %s", person(sess.Username, sess.UserID), sess.ID)
	}
	actor := person(imp.Actor, imp.ActorID)
	if role, isRole := imp.Principal.Role(); isRole {
		return fmt.Sprintf("%s as role %s, session %s", actor, role, sess.ID)
	}

	return fmt.Sprintf("%s impersonating %s, session %s", actor, person(sess.Username, sess.UserID), sess.ID)
}

// person writes a person for an event source: the username with the id of their
// account, or the username alone for one who has no account here.
func person(username string, userID impulse.NullUUID) string {
	if !userID.Valid {
		return username
	}

	return fmt.Sprintf("%s (%s)", username, userID.UUID)
}

// ProcessEvent generates a standard event source string for a system process.
func ProcessEvent(processName string) string {
	return fmt.Sprintf("Process %s", processName)
}

// UserProcessEvent generates a standard event source string for an action performed
// by a user within a specific system process.
func UserProcessEvent(ctx context.Context, processName string) string {
	return fmt.Sprintf("%s: %s", UserEvent(ctx), ProcessEvent(processName))
}
