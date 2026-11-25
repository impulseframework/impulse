package resource

import (
	"context"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/internal/actor"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// sessionColumn is the session every test context of the package carries (sessionCtx,
// accountCtx), as its column holds it.
const sessionColumn = "de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"

// TestProcessEvent pins a process that acted alone: its name in Process and nothing else,
// so no RPC method, whatever the request it runs in was handed.
func TestProcessEvent(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(map[string]any{"Process": "MigrateFeatures"}, ProcessEvent("MigrateFeatures").columns()); diff != "" {
		t.Errorf("ProcessEvent().columns() mismatch (-want +got):\n%s", diff)
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
			ctx:  WithAuth(context.Background(), "crew"),
			want: map[string]any{"Auth": "crew", "ActorUserId": aliceID.String(), "ActorUsername": "pat", "Process": "AcceptInvitation"},
		},
		{
			name: "in a request with a session and an RPC method, neither is named",
			ctx:  WithRPC(WithAuth(accountCtx("bob", bobID, nil), "crew"), "CloseOrder"),
			want: map[string]any{"Auth": "crew", "ActorUserId": aliceID.String(), "ActorUsername": "pat", "Process": "AcceptInvitation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := AccountProcessEvent(tt.ctx, pat, "AcceptInvitation")
			if diff := cmp.Diff(tt.want, source.columns()); diff != "" {
				t.Errorf("AccountProcessEvent().columns() mismatch (-want +got):\n%s", diff)
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
			ctx:  WithRPC(person, ""),
			want: map[string]any{"ActorUserId": aliceID.String(), "ActorUsername": "alice@example.com", "SessionId": sessionColumn},
		},
		{
			name: "of two names handed, the later is recorded",
			ctx:  WithRPC(WithRPC(person, "CloseOrder"), "ReopenOrder"),
			want: map[string]any{"ActorUserId": aliceID.String(), "ActorUsername": "alice@example.com", "SessionId": sessionColumn, "Rpc": "ReopenOrder"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, UserEvent(tt.ctx).columns()); diff != "" {
				t.Errorf("UserEvent().columns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEventSource_String pins the sentence a person reads for who made a change: the
// person first, in one of three forms, then what the change went through, the RPC method,
// the process, or both with the RPC first; a value with no person names what acted; and
// the zero value is the empty text.
func TestEventSource_String(t *testing.T) {
	t.Parallel()

	bob := accountCtx("bob", bobID, nil)
	aliceAsBob := accountCtx("bob", bobID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
	})
	aliceAsSupport := sessionCtx("alice", &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("Support"),
	})
	tests := []struct {
		name   string
		source EventSource
		want   string
	}{
		{name: "a person", source: UserEvent(bob), want: "bob"},
		{name: "a person acting as a person", source: UserEvent(aliceAsBob), want: "alice, acting as bob"},
		{name: "a person acting as a role", source: UserEvent(aliceAsSupport), want: "alice, acting as the role Support"},
		{name: "a person in an RPC", source: UserEvent(WithRPC(bob, "CloseOrder")), want: "bob, through the RPC CloseOrder"},
		{name: "a person acting as a person in an RPC", source: UserEvent(WithRPC(aliceAsBob, "CloseOrder")), want: "alice, acting as bob, through the RPC CloseOrder"},
		{name: "a person acting as a role in an RPC", source: UserEvent(WithRPC(aliceAsSupport, "CloseOrder")), want: "alice, acting as the role Support, through the RPC CloseOrder"},
		{name: "a person through a process", source: UserProcessEvent(bob, "Import"), want: "bob, through the process Import"},
		{name: "a person acting as a person through a process", source: UserProcessEvent(aliceAsBob, "Import"), want: "alice, acting as bob, through the process Import"},
		{name: "a person acting as a role through a process", source: UserProcessEvent(aliceAsSupport, "Import"), want: "alice, acting as the role Support, through the process Import"},
		{name: "a person through a process in an RPC", source: UserProcessEvent(WithRPC(bob, "ImportOrders"), "Import"), want: "bob, through the RPC ImportOrders and the process Import"},
		{name: "a person acting as a person through a process in an RPC", source: UserProcessEvent(WithRPC(aliceAsBob, "ImportOrders"), "Import"), want: "alice, acting as bob, through the RPC ImportOrders and the process Import"},
		{name: "a person acting as a role through a process in an RPC", source: UserProcessEvent(WithRPC(aliceAsSupport, "ImportOrders"), "Import"), want: "alice, acting as the role Support, through the RPC ImportOrders and the process Import"},
		{name: "a process", source: ProcessEvent("MigrateFeatures"), want: "the process MigrateFeatures"},
		{
			name:   "a process and an RPC and no person",
			source: eventSourceFromColumns(&eventSourceColumns{Process: spanner.NullString{StringVal: "Import", Valid: true}, RPC: spanner.NullString{StringVal: "ImportOrders", Valid: true}}),
			want:   "the process Import, through the RPC ImportOrders",
		},
		{
			name:   "an RPC and nothing else",
			source: eventSourceFromColumns(&eventSourceColumns{RPC: spanner.NullString{StringVal: "ImportOrders", Valid: true}}),
			want:   "the RPC ImportOrders",
		},
		{name: "nothing", source: EventSource{}, want: ""},
	}
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
		{name: "an RPC method alone", source: eventSourceFromColumns(&eventSourceColumns{RPC: spanner.NullString{StringVal: "CloseOrder", Valid: true}}), want: false},
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

// TestEventSourceFromColumns pins that a value rebuilt from the columns it sets is the
// value, an RPC method's among them, and that an id that does not parse is left empty.
func TestEventSourceFromColumns(t *testing.T) {
	t.Parallel()

	asColumns := func(source EventSource) *eventSourceColumns {
		columns := source.columns()
		column := func(name string) spanner.NullString {
			value, ok := columns[name].(string)

			return spanner.NullString{StringVal: value, Valid: ok}
		}

		return &eventSourceColumns{
			Auth: column("Auth"), ActorUserID: column("ActorUserId"), ActorUsername: column("ActorUsername"),
			PrincipalUserID: column("PrincipalUserId"), PrincipalUsername: column("PrincipalUsername"), PrincipalRole: column("PrincipalRole"),
			SessionID: column("SessionId"), Process: column("Process"), RPC: column("Rpc"),
		}
	}
	aliceAsBob := accountCtx("bob", bobID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
	})
	values := []struct {
		name   string
		source EventSource
	}{
		{name: "a person, by an auth", source: UserEvent(WithAuth(accountCtx("bob", bobID, nil), "crew"))},
		{name: "a person acting as a person, through a process, in an RPC", source: UserProcessEvent(WithRPC(aliceAsBob, "ImportOrders"), "Import")},
		{name: "a person acting as a role", source: UserEvent(sessionCtx("alice", &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Support")}))},
		{name: "a person in an RPC", source: UserEvent(WithRPC(accountCtx("bob", bobID, nil), "CloseOrder"))},
		{name: "a process", source: ProcessEvent("MigrateFeatures")},
		{name: "nothing", source: EventSource{}},
	}
	for _, tt := range values {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := eventSourceFromColumns(asColumns(tt.source)); got != tt.source {
				t.Errorf("eventSourceFromColumns() = %+v, want %+v", got, tt.source)
			}
		})
	}

	t.Run("an id that does not parse is left empty", func(t *testing.T) {
		t.Parallel()

		got := eventSourceFromColumns(&eventSourceColumns{
			ActorUserID:   spanner.NullString{StringVal: "not an id", Valid: true},
			ActorUsername: spanner.NullString{StringVal: "bob", Valid: true},
		})
		if diff := cmp.Diff(map[string]any{"ActorUsername": "bob"}, got.columns()); diff != "" {
			t.Errorf("columns() of the rebuilt value mismatch (-want +got):\n%s", diff)
		}
	})
}
