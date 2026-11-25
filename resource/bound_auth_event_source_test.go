package resource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/eventsource"
)

// TestUserEvent_readsTheBind pins where the two packages meet: who made a change reads the
// auth a request is bound to, whether its generated routes bound it (WithAuth) or its
// outlet's group did (BindAuth), names none where nothing bound one, and reads the RPC
// method's name a handler handed (WithRPC).
func TestUserEvent_readsTheBind(t *testing.T) {
	t.Parallel()

	person := accountCtx("alice@example.com", aliceID, nil)
	columns := map[string]any{"ActorUserId": aliceID.String(), "ActorUsername": "alice@example.com", "SessionId": sessionColumn}
	with := func(column string, value any) map[string]any {
		want := map[string]any{column: value}
		for name, held := range columns {
			want[name] = held
		}

		return want
	}
	var byGroup context.Context
	BindAuth("crew")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		byGroup = r.Context()
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(person, http.MethodGet, "/", http.NoBody))
	if byGroup == nil {
		t.Fatal("BindAuth(crew) served nothing")
	}

	tests := []struct {
		name string
		ctx  context.Context
		want map[string]any
	}{
		{name: "bound by the generated routes", ctx: WithAuth(person, "crew"), want: with("Auth", "crew")},
		{name: "bound by the outlet's group", ctx: byGroup, want: with("Auth", "crew")},
		{name: "bound by nothing", ctx: person, want: columns},
		{name: "in an RPC method", ctx: WithRPC(person, "CloseOrder"), want: with("Rpc", "CloseOrder")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, eventsource.UserEvent(tt.ctx).Columns()); diff != "" {
				t.Errorf("eventsource.UserEvent().Columns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
