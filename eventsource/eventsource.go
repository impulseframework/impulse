// Package eventsource holds who made a change, as one value: the person, what their
// session acted as, the session, the process and the RPC method, each where there is
// one. UserEvent makes the value from a person's request, ProcessEvent from a process's
// name, and UserProcessEvent from both; nothing makes one from a text. A table keeps
// the value in nine columns (ColumnNames), of which a row sets those that hold
// something (Columns), and the sentence a person reads is written from the value or
// from a row's columns (EventSource.String, Stored.String).
//
// The package has no database code and imports no database driver, so a library that
// records who acted uses the value whatever database it writes to. Package resource
// keeps it on every change record and reads it back for a record's history.
package eventsource

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/internal/actor"
	"github.com/impulseframework/impulse/internal/request"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The columns a table keeps who made a change in, by their names in the table.
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

// ColumnNames answers the names of the nine columns a table keeps who made a change in, in
// the order the change history table declares them: Auth, ActorUserId, ActorUsername,
// PrincipalUserId, PrincipalUsername, PrincipalRole, SessionId, Process and Rpc.
func ColumnNames() []string {
	return []string{
		authColumn, actorUserIDColumn, actorUsernameColumn, principalUserIDColumn, principalUsernameColumn,
		principalRoleColumn, sessionIDColumn, processColumn, rpcColumn,
	}
}

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
// handler handed it to the request (resource.WithRPC): the code that makes the write
// passes nothing for it.
func UserEvent(ctx context.Context) EventSource {
	sess := sessioninfo.DataFromCtx(ctx)

	var source EventSource
	source.sessionID, source.rpc = sess.ID.String(), request.RPCFrom(ctx)
	if auth, ok := request.AuthFrom(ctx); ok {
		source.auth = auth.Name
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
	var source EventSource
	source.process = processName

	return source
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
	var source EventSource
	source.actorUserID = impulse.NullUUIDFromUUID(account.UserID())
	source.actorUsername, source.process = account.Username(), processName
	if auth, ok := request.AuthFrom(ctx); ok {
		source.auth = auth.Name
	}

	return source
}

// validUserID answers id where it is valid and the null id otherwise, so that an id that
// is not valid is never kept.
func validUserID(id impulse.NullUUID) impulse.NullUUID {
	if !id.Valid {
		var none impulse.NullUUID

		return none
	}

	return id
}

// IsZero reports whether the value names nobody: no person, no session, no process and
// no RPC method.
func (e EventSource) IsZero() bool {
	var zero EventSource

	return e == zero
}

// String writes who made the change for a person to read. It names people by username
// and holds no id; nothing stores it.
func (e EventSource) String() string {
	w := words{
		actorUsername: e.actorUsername, principalUsername: e.principalUsername, principalRole: e.principalRole,
		process: e.process, rpc: e.rpc,
	}

	return w.sentence()
}

// Columns answers who made the change as the columns that hold something, by the names
// every table that keeps who made a change gives them (ColumnNames). It never holds a
// column with an empty text. A write sets them beside its own columns.
func (e EventSource) Columns() map[string]any {
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

// words are the five texts the sentence of who made a change is written from, as the
// value holds them and as a row keeps them: EventSource.String and Stored.String both
// write it here, so the sentence of a row cannot differ from the sentence of the value
// it was written from.
type words struct {
	actorUsername     string
	principalUsername string
	principalRole     string
	process           string
	rpc               string
}

// sentence writes who made the change: the person first, then what the change went
// through; with no person, what acted.
func (w *words) sentence() string {
	person := w.person()
	through := w.through()
	switch {
	case person != "" && through != "":
		return person + ", through " + through
	case person != "":
		return person
	case w.process != "" && w.rpc != "":
		return "the process " + w.process + ", through the RPC " + w.rpc
	case w.process != "":
		return "the process " + w.process
	case w.rpc != "":
		return "the RPC " + w.rpc
	default:
		return ""
	}
}

// person writes the person, in one of its three forms: the username, the username acting
// as another person, or the username acting as a role. Words with no person answer the
// empty text.
func (w *words) person() string {
	switch {
	case w.actorUsername == "":
		return ""
	case w.principalUsername != "":
		return w.actorUsername + ", acting as " + w.principalUsername
	case w.principalRole != "":
		return w.actorUsername + ", acting as the role " + w.principalRole
	default:
		return w.actorUsername
	}
}

// through writes what a person's change went through: the RPC method, the process, or
// both, the RPC first. Words with neither answer the empty text.
func (w *words) through() string {
	switch {
	case w.rpc != "" && w.process != "":
		return "the RPC " + w.rpc + " and the process " + w.process
	case w.rpc != "":
		return "the RPC " + w.rpc
	case w.process != "":
		return "the process " + w.process
	default:
		return ""
	}
}
