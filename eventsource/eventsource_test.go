package eventsource

import (
	"context"
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/internal/actor"
	"github.com/impulseframework/impulse/internal/request"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// sessionColumn is the session every test context of the package carries (sessionCtx,
// accountCtx), as its column holds it.
const sessionColumn = "de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"

// aliceID and bobID are the accounts of the tests' two people, and bobUser the person as
// the permission store names them: by the id of their account, as text. The usernames
// alice and bob are what people read.
var (
	aliceID = impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	bobID   = impulse.Must(impulse.UUIDFromString("b0b00000-0000-4000-8000-000000000002"))
	bobUser = accesstypes.User(bobID.String())
)

// sessionCtx is a context with a session of the username, acting as imp's principal where
// imp is not nil. A copy of package resource's helper of the name.
func sessionCtx(username string, imp *sessioninfo.Impersonation) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo:   &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString(sessionColumn)), Username: username},
		Impersonation: imp,
	})
}

// accountCtx is sessionCtx for a session that belongs to an account, as an Auth's
// sessions do: UserID is the account's id. A copy of package resource's helper of the
// name.
func accountCtx(username string, userID impulse.UUID, imp *sessioninfo.Impersonation) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo:   &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString(sessionColumn)), Username: username},
		Impersonation: imp,
		UserID:        impulse.NullUUIDFromUUID(userID),
	})
}

// withCrew binds ctx to the auth crew, as an outlet's generated routes do
// (resource.WithAuth).
func withCrew(ctx context.Context) context.Context {
	return request.WithAuth(ctx, request.Auth{Name: "crew", ByGroup: false})
}

// withRPC hands ctx the name of the RPC method it runs, as the method's generated handler
// does (resource.WithRPC).
func withRPC(ctx context.Context, method string) context.Context {
	return request.WithRPC(ctx, method)
}

// TestProcessEvent pins a process that acted alone: its name in Process and nothing else,
// so no RPC method, whatever the request it runs in was handed.
func TestProcessEvent(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(map[string]any{"Process": "MigrateFeatures"}, ProcessEvent("MigrateFeatures").Columns()); diff != "" {
		t.Errorf("ProcessEvent().Columns() mismatch (-want +got):\n%s", diff)
	}
}

// TestAccountProcessEvent pins a process that acted for a person who has no session yet:
// the person's account and username, the process, and the auth where one is bound, and
// no session and no RPC method, whatever the request carries; its sentence names the
// person through the process.
func TestAccountProcessEvent(t *testing.T) {
	t.Parallel()

	pat := actor.NewAccount(aliceID, "pat")
	tests := []struct {
		name string
		ctx  context.Context
		want map[string]any
	}{
		{
			name: "with no auth bound",
			ctx:  context.Background(),
			want: map[string]any{"ActorUserId": aliceID.String(), "ActorUsername": "pat", "Process": "AcceptInvitation"},
		},
		{
			name: "with an auth bound",
			ctx:  withCrew(context.Background()),
			want: map[string]any{"Auth": "crew", "ActorUserId": aliceID.String(), "ActorUsername": "pat", "Process": "AcceptInvitation"},
		},
		{
			name: "in a request with a session and an RPC method, neither is named",
			ctx:  withRPC(withCrew(accountCtx("bob", bobID, nil)), "CloseOrder"),
			want: map[string]any{"Auth": "crew", "ActorUserId": aliceID.String(), "ActorUsername": "pat", "Process": "AcceptInvitation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := AccountProcessEvent(tt.ctx, pat, "AcceptInvitation")
			if diff := cmp.Diff(tt.want, source.Columns()); diff != "" {
				t.Errorf("AccountProcessEvent().Columns() mismatch (-want +got):\n%s", diff)
			}
			if got, want := source.String(), "pat, through the process AcceptInvitation"; got != want {
				t.Errorf("AccountProcessEvent().String() = %q, want %q", got, want)
			}
		})
	}
}

// TestWithRPC pins the name of the RPC method a request is handed: a name handed empty
// sets no Rpc, and of two names handed the later is recorded.
func TestWithRPC(t *testing.T) {
	t.Parallel()

	person := accountCtx("alice@example.com", aliceID, nil)
	tests := []struct {
		name string
		ctx  context.Context
		want map[string]any
	}{
		{
			name: "a name handed empty sets none",
			ctx:  withRPC(person, ""),
			want: map[string]any{"ActorUserId": aliceID.String(), "ActorUsername": "alice@example.com", "SessionId": sessionColumn},
		},
		{
			name: "of two names handed, the later is recorded",
			ctx:  withRPC(withRPC(person, "CloseOrder"), "ReopenOrder"),
			want: map[string]any{"ActorUserId": aliceID.String(), "ActorUsername": "alice@example.com", "SessionId": sessionColumn, "Rpc": "ReopenOrder"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, UserEvent(tt.ctx).Columns()); diff != "" {
				t.Errorf("UserEvent().Columns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// sentenceCase is one value and the sentence a person reads for it.
type sentenceCase struct {
	name   string
	source EventSource
	want   string
}

// sentenceCases are the values of every form of the sentence, each with its sentence:
// the person first, in one of three forms, then what the change went through, the RPC
// method, the process, or both with the RPC first; a value with no person names what
// acted; and the zero value is the empty text.
func sentenceCases() []sentenceCase {
	bob := accountCtx("bob", bobID, nil)
	aliceAsBob := accountCtx("bob", bobID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
	})
	aliceAsSupport := sessionCtx("alice", &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("Support"),
	})

	return []sentenceCase{
		{name: "a person", source: UserEvent(bob), want: "bob"},
		{name: "a person acting as a person", source: UserEvent(aliceAsBob), want: "alice, acting as bob"},
		{name: "a person acting as a role", source: UserEvent(aliceAsSupport), want: "alice, acting as the role Support"},
		{name: "a person in an RPC", source: UserEvent(withRPC(bob, "CloseOrder")), want: "bob, through the RPC CloseOrder"},
		{name: "a person acting as a person in an RPC", source: UserEvent(withRPC(aliceAsBob, "CloseOrder")), want: "alice, acting as bob, through the RPC CloseOrder"},
		{name: "a person acting as a role in an RPC", source: UserEvent(withRPC(aliceAsSupport, "CloseOrder")), want: "alice, acting as the role Support, through the RPC CloseOrder"},
		{name: "a person through a process", source: UserProcessEvent(bob, "Import"), want: "bob, through the process Import"},
		{name: "a person acting as a person through a process", source: UserProcessEvent(aliceAsBob, "Import"), want: "alice, acting as bob, through the process Import"},
		{name: "a person acting as a role through a process", source: UserProcessEvent(aliceAsSupport, "Import"), want: "alice, acting as the role Support, through the process Import"},
		{name: "a person through a process in an RPC", source: UserProcessEvent(withRPC(bob, "ImportOrders"), "Import"), want: "bob, through the RPC ImportOrders and the process Import"},
		{name: "a person acting as a person through a process in an RPC", source: UserProcessEvent(withRPC(aliceAsBob, "ImportOrders"), "Import"), want: "alice, acting as bob, through the RPC ImportOrders and the process Import"},
		{name: "a person acting as a role through a process in an RPC", source: UserProcessEvent(withRPC(aliceAsSupport, "ImportOrders"), "Import"), want: "alice, acting as the role Support, through the RPC ImportOrders and the process Import"},
		{name: "a process", source: ProcessEvent("MigrateFeatures"), want: "the process MigrateFeatures"},
		{
			name:   "a process and an RPC and no person",
			source: EventSource{process: "Import", rpc: "ImportOrders"},
			want:   "the process Import, through the RPC ImportOrders",
		},
		{
			name:   "an RPC and nothing else",
			source: EventSource{rpc: "ImportOrders"},
			want:   "the RPC ImportOrders",
		},
		{name: "nothing", source: EventSource{}, want: ""},
	}
}

// TestEventSource_String pins the sentence a person reads for who made a change: the
// person first, in one of three forms, then what the change went through, the RPC method,
// the process, or both with the RPC first; a value with no person names what acted; and
// the zero value is the empty text.
func TestEventSource_String(t *testing.T) {
	t.Parallel()

	tests := sentenceCases()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.source.String(); got != tt.want {
				t.Errorf("EventSource.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEventSource_IsZero pins the value that names nobody: the zero value alone. A value
// that holds an RPC method and nothing else names something.
func TestEventSource_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source EventSource
		want   bool
	}{
		{name: "the zero value", source: EventSource{}, want: true},
		{name: "a process with no name", source: ProcessEvent(""), want: true},
		{name: "a process", source: ProcessEvent("MigrateFeatures"), want: false},
		{name: "a person", source: UserEvent(accountCtx("bob", bobID, nil)), want: false},
		{name: "an RPC method alone", source: EventSource{rpc: "CloseOrder"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.source.IsZero(); got != tt.want {
				t.Errorf("EventSource.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestUserEvent pins the columns who made a change is kept in, for each kind of session:
// a person as themselves is the actor; a session acting as a person names the real
// person as the actor and the person acted as as the principal; a session acting as a
// role names the actor and the role. Each with an account and without, where an id that
// is not valid is left empty; the auth where the request's context binds one; the session
// always. In an RPC method each sets Rpc to the method's name beside the columns its
// session sets, and outside one none; UserProcessEvent sets the process beside all of it.
func TestUserEvent(t *testing.T) {
	t.Parallel()

	alice, bob := aliceID.String(), bobID.String()
	actorAlice := impulse.NullUUIDFromUUID(aliceID)

	tests := []struct {
		name string
		ctx  context.Context
		want map[string]any
	}{
		{
			name: "a person as themselves: the account, the username, the session",
			ctx:  accountCtx("alice@example.com", aliceID, nil),
			want: map[string]any{"ActorUserId": alice, "ActorUsername": "alice@example.com", "SessionId": sessionColumn},
		},
		{
			name: "a person as themselves, the auth bound: the auth too",
			ctx:  withCrew(accountCtx("alice@example.com", aliceID, nil)),
			want: map[string]any{"Auth": "crew", "ActorUserId": alice, "ActorUsername": "alice@example.com", "SessionId": sessionColumn},
		},
		{
			name: "a person of no account: the username alone",
			ctx:  sessionCtx("alice@example.com", nil),
			want: map[string]any{"ActorUsername": "alice@example.com", "SessionId": sessionColumn},
		},
		{
			name: "acting as a person: the actor, and the person acted as",
			ctx: accountCtx("bob@partner.org", bobID, &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.UserPrincipal(bobUser),
			}),
			want: map[string]any{
				"ActorUserId": alice, "ActorUsername": "alice@example.com",
				"PrincipalUserId": bob, "PrincipalUsername": "bob@partner.org", "SessionId": sessionColumn,
			},
		},
		{
			name: "acting as a person, an actor with no account here: no actor's id",
			ctx: withCrew(accountCtx("bob@partner.org", bobID, &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorRealm: "admin-portal", Principal: accesstypes.UserPrincipal(bobUser),
			})),
			want: map[string]any{
				"Auth": "crew", "ActorUsername": "alice@example.com",
				"PrincipalUserId": bob, "PrincipalUsername": "bob@partner.org", "SessionId": sessionColumn,
			},
		},
		{
			name: "acting as a person of no account: no principal's id",
			ctx: sessionCtx("bob@partner.org", &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.UserPrincipal("bob@partner.org"),
			}),
			want: map[string]any{
				"ActorUserId": alice, "ActorUsername": "alice@example.com",
				"PrincipalUsername": "bob@partner.org", "SessionId": sessionColumn,
			},
		},
		{
			name: "acting as a role: the actor and the role",
			ctx: sessionCtx("alice@example.com", &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}),
			want: map[string]any{"ActorUserId": alice, "ActorUsername": "alice@example.com", "PrincipalRole": "PartnerViewer", "SessionId": sessionColumn},
		},
		{
			name: "acting as a role, an actor with no account here, the auth bound",
			ctx:  withCrew(sessionCtx("alice@example.com", &sessioninfo.Impersonation{Actor: "alice@example.com", Principal: accesstypes.RolePrincipal("PartnerViewer")})),
			want: map[string]any{"Auth": "crew", "ActorUsername": "alice@example.com", "PrincipalRole": "PartnerViewer", "SessionId": sessionColumn},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, UserEvent(tt.ctx).Columns()); diff != "" {
				t.Errorf("UserEvent().Columns() mismatch (-want +got):\n%s", diff)
			}
			inRPC := maps.Clone(tt.want)
			inRPC["Rpc"] = "CloseOrder"
			if diff := cmp.Diff(inRPC, UserEvent(withRPC(tt.ctx, "CloseOrder")).Columns()); diff != "" {
				t.Errorf("UserEvent() in an RPC: columns() mismatch (-want +got):\n%s", diff)
			}
			throughProcess := maps.Clone(inRPC)
			throughProcess["Process"] = "nightly"
			if diff := cmp.Diff(throughProcess, UserProcessEvent(withRPC(tt.ctx, "CloseOrder"), "nightly").Columns()); diff != "" {
				t.Errorf("UserProcessEvent() in an RPC: columns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
