package eventsource

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// storedOf fills a Stored from the value's Columns, by name, as a read fills one from the
// row the value was written to.
func storedOf(source EventSource) Stored {
	columns := source.Columns()
	text := func(name string) string {
		value, _ := columns[name].(string)

		return value
	}

	return Stored{
		Auth: text("Auth"), ActorUserID: text("ActorUserId"), ActorUsername: text("ActorUsername"),
		PrincipalUserID: text("PrincipalUserId"), PrincipalUsername: text("PrincipalUsername"), PrincipalRole: text("PrincipalRole"),
		SessionID: text("SessionId"), Process: text("Process"), RPC: text("Rpc"),
	}
}

// TestStored_String pins that a row's columns answer the sentence of the value they were
// written from, for every form of the sentence, and that a row that sets nothing answers
// the empty text.
func TestStored_String(t *testing.T) {
	t.Parallel()

	for _, tt := range sentenceCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := storedOf(tt.source).String(); got != tt.want {
				t.Errorf("Stored.String() = %q, want %q (the value's own sentence)", got, tt.want)
			}
		})
	}
	if got := (Stored{}).String(); got != "" {
		t.Errorf("Stored{}.String() = %q, want the empty text", got)
	}
}

// TestColumnNames pins the nine names in the order the change history table declares
// them, and that the columns the two fullest values set (a session acting as a person and
// one acting as a role, each in an RPC and through a process) are the nine, each among
// them.
func TestColumnNames(t *testing.T) {
	t.Parallel()

	want := []string{"Auth", "ActorUserId", "ActorUsername", "PrincipalUserId", "PrincipalUsername", "PrincipalRole", "SessionId", "Process", "Rpc"}
	if diff := cmp.Diff(want, ColumnNames()); diff != "" {
		t.Errorf("ColumnNames() mismatch (-want +got):\n%s", diff)
	}
	fullest := func(principal accesstypes.Principal) EventSource {
		ctx := accountCtx("bob", bobID, &sessioninfo.Impersonation{Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: principal})

		return UserProcessEvent(withRPC(withCrew(ctx), "ImportOrders"), "Import")
	}
	set := map[string]bool{}
	for _, source := range []EventSource{fullest(accesstypes.UserPrincipal(bobUser)), fullest(accesstypes.RolePrincipal("Support"))} {
		for name := range source.Columns() {
			if !slices.Contains(ColumnNames(), name) {
				t.Errorf("Columns() sets %q, which ColumnNames() does not name", name)
			}
			set[name] = true
		}
	}
	for _, name := range ColumnNames() {
		if !set[name] {
			t.Errorf("no value sets the column %q that ColumnNames() names", name)
		}
	}
}
