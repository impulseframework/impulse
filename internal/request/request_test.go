package request

import (
	"context"
	"testing"
)

// TestAuth pins the record of the auth a request came through: what was stored is
// answered, and a context with none answers ok false.
func TestAuth(t *testing.T) {
	t.Parallel()

	if got, ok := AuthFrom(context.Background()); ok || got != (Auth{}) {
		t.Errorf("AuthFrom() of a context with none = %+v, %t, want nothing and false", got, ok)
	}
	for _, want := range []Auth{{Name: "crew"}, {Name: "members", ByGroup: true}} {
		if got, ok := AuthFrom(WithAuth(context.Background(), want)); !ok || got != want {
			t.Errorf("AuthFrom(WithAuth(%+v)) = %+v, %t, want it and true", want, got, ok)
		}
	}
}

// TestRPC pins the name of the RPC method a request runs: what was stored is answered, a
// context with none answers the empty text, and of two names handed the later holds.
func TestRPC(t *testing.T) {
	t.Parallel()

	if got := RPCFrom(context.Background()); got != "" {
		t.Errorf("RPCFrom() of a context with none = %q, want the empty text", got)
	}
	if got := RPCFrom(WithRPC(context.Background(), "CloseOrder")); got != "CloseOrder" {
		t.Errorf("RPCFrom(WithRPC(CloseOrder)) = %q, want CloseOrder", got)
	}
	if got := RPCFrom(WithRPC(WithRPC(context.Background(), "CloseOrder"), "ReopenOrder")); got != "ReopenOrder" {
		t.Errorf("RPCFrom() of two names handed = %q, want the later, ReopenOrder", got)
	}
}
