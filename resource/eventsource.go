package resource

import (
	"context"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/internal/actor"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The columns a change record keeps who made the change in, by their names in the table.
const (
	authColumn              = "Auth"
	actorUserIDColumn       = "ActorUserId"
	actorUsernameColumn     = "ActorUsername"
	principalUserIDColumn   = "PrincipalUserId"
	principalUsernameColumn = "PrincipalUsername"
	principalRoleColumn     = "PrincipalRole"
	sessionIDColumn         = "SessionId"
	processColumn           = "Process"
	rpcColumn               = "Rpc"
)

// eventSourceColumnList is the who columns of a change record as a statement selects them.
const eventSourceColumnList = authColumn + ", " + actorUserIDColumn + ", " + actorUsernameColumn + ", " +
	principalUserIDColumn + ", " + principalUsernameColumn + ", " + principalRoleColumn + ", " +
	sessionIDColumn + ", " + processColumn + ", " + rpcColumn

// EventSource is who made a change: the person, what their session acted as, the session,
// the process, and the RPC method the change was made in, each where there is one. The
// zero value names nobody, and a tracked write refuses it.
type EventSource struct {
	auth              string
	actorUserID       impulse.NullUUID
	actorUsername     string
	principalUserID   impulse.NullUUID
	principalUsername string
	principalRole     string
	sessionID         string
	process           string
	rpc               string
}

// UserEvent answers who made a change in a person's request: the person, by the id of
// their account and their username on the day; who or what their session acted as, when
// it acted as someone else; the session; and the auth the request came through. In a
// session acting as someone else the actor is the real person, so every change carries
// who really made it. In an RPC method it carries the method's name too, as the method's
// handler handed it to the request (WithRPC): the code that makes the write passes
// nothing for it.
func UserEvent(ctx context.Context) EventSource {
	sess := sessioninfo.DataFromCtx(ctx)

	source := EventSource{sessionID: sess.ID.String(), rpc: rpcFrom(ctx)}
	if auth, ok := AuthFrom(ctx); ok {
		source.auth = auth
	}

	imp := sess.Impersonation
	if imp == nil {
		source.actorUserID, source.actorUsername = validUserID(sess.UserID), sess.Username

		return source
	}
	source.actorUserID, source.actorUsername = validUserID(imp.ActorID), imp.Actor
	if role, isRole := imp.Principal.Role(); isRole {
		source.principalRole = string(role)

		return source
	}
	source.principalUserID, source.principalUsername = validUserID(sess.UserID), sess.Username

	return source
}

// ProcessEvent answers a process that acted alone, by its name.
func ProcessEvent(processName string) EventSource {
	return EventSource{process: processName}
}

// UserProcessEvent answers a process that acted for the person of the request: what
// UserEvent answers, the RPC method's name among it, with the process's name.
func UserProcessEvent(ctx context.Context, processName string) EventSource {
	source := UserEvent(ctx)
	source.process = processName

	return source
}

// AccountProcessEvent answers a process that acted for a person who has no session yet,
// as a sign-in does before it starts one: the person by the id of their account and
// their username, the process by its name, and the auth the request came through where
// one is bound. It names no session and no RPC method. It is the one value that names a
// person the request's session did not carry: pass it where the code has just proven who
// the person is. The person is an actor.Account, a type of the library's internal tree,
// so only the library's own packages can call it.
func AccountProcessEvent(ctx context.Context, account actor.Account, processName string) EventSource {
	source := EventSource{
		actorUserID:   impulse.NullUUID{UUID: account.UserID(), Valid: true},
		actorUsername: account.Username(),
		process:       processName,
	}
	if auth, ok := AuthFrom(ctx); ok {
		source.auth = auth
	}

	return source
}

// validUserID answers id where it is valid and the null id otherwise, so that an id that
// is not valid is never kept.
func validUserID(id impulse.NullUUID) impulse.NullUUID {
	if !id.Valid {
		return impulse.NullUUID{}
	}

	return id
}

// IsZero reports whether the value names nobody: no person, no session, no process and
// no RPC method.
func (e EventSource) IsZero() bool {
	return e == EventSource{}
}

// String writes who made the change for a person to read. It names people by username
// and holds no id; nothing stores it.
func (e EventSource) String() string {
	person := e.person()
	through := e.through()
	switch {
	case person != "" && through != "":
		return person + ", through " + through
	case person != "":
		return person
	case e.process != "" && e.rpc != "":
		return "the process " + e.process + ", through the RPC " + e.rpc
	case e.process != "":
		return "the process " + e.process
	case e.rpc != "":
		return "the RPC " + e.rpc
	default:
		return ""
	}
}

// person writes the person of the value, in one of its three forms: the username, the
// username acting as another person, or the username acting as a role. A value with no
// person answers the empty text.
func (e EventSource) person() string {
	switch {
	case e.actorUsername == "":
		return ""
	case e.principalUsername != "":
		return e.actorUsername + ", acting as " + e.principalUsername
	case e.principalRole != "":
		return e.actorUsername + ", acting as the role " + e.principalRole
	default:
		return e.actorUsername
	}
}

// through writes what a person's change went through: the RPC method, the process, or
// both, the RPC first. A value with neither answers the empty text.
func (e EventSource) through() string {
	switch {
	case e.rpc != "" && e.process != "":
		return "the RPC " + e.rpc + " and the process " + e.process
	case e.rpc != "":
		return "the RPC " + e.rpc
	case e.process != "":
		return "the process " + e.process
	default:
		return ""
	}
}

// columns answers the who columns of a change record that the value fills, each as a
// text: a column it leaves empty is not among them.
func (e EventSource) columns() map[string]any {
	columns := make(map[string]any, 9)
	set := func(column, value string) {
		if value != "" {
			columns[column] = value
		}
	}
	set(authColumn, e.auth)
	if e.actorUserID.Valid {
		set(actorUserIDColumn, e.actorUserID.String())
	}
	set(actorUsernameColumn, e.actorUsername)
	if e.principalUserID.Valid {
		set(principalUserIDColumn, e.principalUserID.String())
	}
	set(principalUsernameColumn, e.principalUsername)
	set(principalRoleColumn, e.principalRole)
	set(sessionIDColumn, e.sessionID)
	set(processColumn, e.process)
	set(rpcColumn, e.rpc)

	return columns
}

// eventSourceColumns are the who columns of a row of a table that keeps who made a change
// (DataChangeEvents, FeatureFlagChanges), as a read scans them.
type eventSourceColumns struct {
	Auth              spanner.NullString `spanner:"Auth"`
	ActorUserID       spanner.NullString `spanner:"ActorUserId"`
	ActorUsername     spanner.NullString `spanner:"ActorUsername"`
	PrincipalUserID   spanner.NullString `spanner:"PrincipalUserId"`
	PrincipalUsername spanner.NullString `spanner:"PrincipalUsername"`
	PrincipalRole     spanner.NullString `spanner:"PrincipalRole"`
	SessionID         spanner.NullString `spanner:"SessionId"`
	Process           spanner.NullString `spanner:"Process"`
	RPC               spanner.NullString `spanner:"Rpc"`
}

// eventSourceFromColumns rebuilds who made a change from the nine columns a row keeps it
// in. A column that is NULL is empty in the value, and an id that does not parse is left
// empty.
func eventSourceFromColumns(columns *eventSourceColumns) EventSource {
	return EventSource{
		auth:              columns.Auth.StringVal,
		actorUserID:       parsedUserID(columns.ActorUserID),
		actorUsername:     columns.ActorUsername.StringVal,
		principalUserID:   parsedUserID(columns.PrincipalUserID),
		principalUsername: columns.PrincipalUsername.StringVal,
		principalRole:     columns.PrincipalRole.StringVal,
		sessionID:         columns.SessionID.StringVal,
		process:           columns.Process.StringVal,
		rpc:               columns.RPC.StringVal,
	}
}

// parsedUserID reads an account's id from a column, the null id where the column is NULL
// or does not hold one.
func parsedUserID(column spanner.NullString) impulse.NullUUID {
	if !column.Valid {
		return impulse.NullUUID{}
	}
	id, err := impulse.NullUUIDFromString(column.StringVal)
	if err != nil {
		return impulse.NullUUID{}
	}

	return id
}
