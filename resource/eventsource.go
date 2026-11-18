package resource

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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

// The words UserEvent writes between the parts of an event source, which EventActor
// reads back. TestEventActor holds the two together: it reads every shape UserEvent
// writes.
const (
	eventSessionWords       = ", session "
	eventImpersonatingWords = " impersonating "
	eventAsRoleWords        = " as role "
)

// eventAccountRE matches a person as an event source writes one who has an account: the
// username, a space, and the account's id in parentheses.
var eventAccountRE = regexp.MustCompile(`^(.*) \([0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}\)$`)

// EventActor reads who acted out of an event source, for a page that shows a person who
// changed something. For a source UserEvent wrote, in any of its shapes, name is the
// username the person signed in under and person is true; an impersonated session
// answers its actor, the real person who made the change. Any other source names no
// person, a process's (ProcessEvent) and a caller's own words among them: person is
// false and name is the source as written.
func EventActor(source string) (name string, person bool) {
	end := strings.LastIndex(source, eventSessionWords)
	if end < 0 {
		return source, false
	}
	actor := source[:end]
	if cut := strings.Index(actor, eventImpersonatingWords); cut >= 0 {
		actor = actor[:cut]
	} else if cut := strings.LastIndex(actor, eventAsRoleWords); cut >= 0 {
		actor = actor[:cut]
	}
	if account := eventAccountRE.FindStringSubmatch(actor); account != nil {
		actor = account[1]
	}
	if actor == "" {
		return source, false
	}

	return actor, true
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
