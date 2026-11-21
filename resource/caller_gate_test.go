package resource

import (
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/httpio"
)

// TestCaller_ExecuteGate pins what a body is answered when it asks the caller's grant
// on another method for a request it builds: the decision the method's own decode would
// reach for that request. A caller without the grant is refused as the decode refuses.
// A condition that reads the request's fields is settled against the request the body
// built, so the same caller is granted for one role and refused for another. What reads
// the method's target row is left for the row.
func TestCaller_ExecuteGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		perms       *fakeUserPermissions
		request     *rateRequest
		wantRefused bool
		wantGranted bool
		wantSettled bool
	}{
		{
			name:        "a grant with no condition",
			perms:       &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Execute: {"RateThing"}}},
			request:     &rateRequest{Role: "Chief"},
			wantGranted: true,
			wantSettled: true,
		},
		{
			name:        "no grant",
			perms:       &fakeUserPermissions{},
			request:     &rateRequest{Role: "Reader"},
			wantRefused: true,
		},
		{
			name:        "a condition on the request's role, for a role it admits",
			perms:       &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"RateThing": "new.role != 'Chief'"}},
			request:     &rateRequest{Role: "Reader"},
			wantGranted: true,
			wantSettled: true,
		},
		{
			name:        "a condition on the request's role, for the role it refuses",
			perms:       &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"RateThing": "new.role != 'Chief'"}},
			request:     &rateRequest{Role: "Chief"},
			wantSettled: true,
		},
		{
			name:    "a condition that reads the target row too",
			perms:   &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"RateThing": "new.role != 'Chief' AND kind = 'open'"}},
			request: &rateRequest{Role: "Reader"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caller := &Caller{Permissions: tt.perms, Scope: accesstypes.GlobalScope(), Env: RequestEnvironment(), collection: rateCollection(t)}
			gate, err := caller.ExecuteGate(t.Context(), "RateThing", tt.request)
			if tt.wantRefused {
				if err == nil || !httpio.HasForbidden(err) {
					t.Fatalf("Caller.ExecuteGate() error = %v, want Forbidden", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("Caller.ExecuteGate() error = %v", err)
			}
			granted, settled := gate.Settled()
			if granted != tt.wantGranted || settled != tt.wantSettled {
				t.Errorf("ExecuteGate.Settled() = (granted %v, settled %v), want (%v, %v)", granted, settled, tt.wantGranted, tt.wantSettled)
			}
			if got := gate.Caller(); got.Permissions != UserPermissions(tt.perms) || got.Scope != caller.Scope {
				t.Errorf("the gate carries the caller %v, want the one that asked", got)
			}
		})
	}

	t.Run("no caller", func(t *testing.T) {
		t.Parallel()

		var none Caller
		if _, err := none.ExecuteGate(t.Context(), "RateThing", &rateRequest{}); err == nil || !strings.Contains(err.Error(), "no caller is stamped") {
			t.Errorf("Caller.ExecuteGate() with no caller: error = %v, want the refusal of a call outside a frame", err)
		}
	})
}

// TestQuerySet_Caller pins the caller a query answers for the list read through it: the
// checker, the scope and the environment the query was checked with, and the collection
// its conditions resolve against. A list that asks the caller's grant on a method is
// then answered as the method's own frame would answer the same person: granted for the
// role the grant's condition admits and refused for the one it does not. A query bound
// to no permissions answers a zero caller, which is refused whatever it asks.
func TestQuerySet_Caller(t *testing.T) {
	t.Parallel()

	perms := &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"RateThing": "new.role != 'Chief'"}}
	env := RequestEnvironment()
	qSet := &QuerySet[enforcementResource]{userPermissions: perms, scope: accesstypes.GlobalScope(), env: env, collection: rateCollection(t)}

	caller := qSet.Caller()
	if caller.IsZero() || caller.Permissions != UserPermissions(perms) || caller.Scope != accesstypes.GlobalScope() || caller.Env != env {
		t.Fatalf("QuerySet.Caller() = %+v, want the checker, scope and environment the query was checked with", caller)
	}
	for role, want := range map[string]bool{"Reader": true, "Chief": false} {
		gate, err := caller.ExecuteGate(t.Context(), "RateThing", &rateRequest{Role: role})
		if err != nil {
			t.Fatalf("Caller.ExecuteGate() for the role %s error = %v", role, err)
		}
		if granted, settled := gate.Settled(); granted != want || !settled {
			t.Errorf("the grant on RateThing for the role %s = (granted %v, settled %v), want (%v, true)", role, granted, settled, want)
		}
	}

	unbound := &QuerySet[enforcementResource]{}
	if none := unbound.Caller(); !none.IsZero() {
		t.Errorf("QuerySet.Caller() of a query bound to no permissions = %+v, want a zero caller", none)
	} else if _, err := none.ExecuteGate(t.Context(), "RateThing", &rateRequest{}); err == nil || !strings.Contains(err.Error(), "no caller is stamped") {
		t.Errorf("Caller.ExecuteGate() of a query bound to no permissions: error = %v, want the refusal of a call with no caller", err)
	}
}

// TestExecuteGate_EnforceProposed pins the judgment of a row the body proposes, one the
// database does not hold: the grant's condition is read against the values handed, with
// the caller the check folded with. A row the condition admits passes, one it does not is
// the frame's refusal, which tells the caller the record in words and keeps the method's
// name for the log, and a condition that reads an attribute the row does not carry is an
// error, never a pass.
func TestExecuteGate_EnforceProposed(t *testing.T) {
	t.Parallel()

	target := ExecuteTarget{Resource: "HeldRoles", Label: "HeldRole", PKColumns: []string{"UserId", "Role"}}
	parse := func(source string) condition.Expr {
		t.Helper()
		expr, err := condition.Parse(source)
		if err != nil {
			t.Fatalf("condition.Parse(%q) error = %v", source, err)
		}

		return expr
	}
	tests := []struct {
		name          string
		cond          string
		row           map[string]any
		wantForbidden bool
		wantErr       string
	}{
		{name: "a row the condition admits", cond: "role != 'Chief' AND everyDomain = false", row: map[string]any{"role": "Reader", "everyDomain": false}},
		{name: "the role the condition refuses", cond: "role != 'Chief' AND everyDomain = false", row: map[string]any{"role": "Chief", "everyDomain": false}, wantForbidden: true},
		{name: "the other fact the condition refuses", cond: "role != 'Chief' AND everyDomain = false", row: map[string]any{"role": "Reader", "everyDomain": true}, wantForbidden: true},
		{name: "an attribute with no value never permits", cond: "role != 'Chief'", row: map[string]any{"role": nil}, wantForbidden: true},
		{name: "the caller's own row", cond: "holder = subject", row: map[string]any{"holder": "ann"}},
		{name: "another person's row", cond: "holder = subject", row: map[string]any{"holder": "bob"}, wantForbidden: true},
		{name: "an attribute the row does not carry", cond: "role != 'Chief' AND kind = 'open'", row: map[string]any{"role": "Reader"}, wantErr: `reads "kind"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := &ExecuteGate{method: "TakeRole", user: "ann", scope: accesstypes.GlobalScope(), env: RequestEnvironment(), cond: parse(tt.cond)}
			err := gate.EnforceProposed(t.Context(), nil, target, tt.row)
			switch {
			case tt.wantErr != "":
				if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ExecuteGate.EnforceProposed() error = %v, want an error containing %q that is no refusal", err, tt.wantErr)
				}
			case tt.wantForbidden:
				plainAnswer(t, err, httpio.HasForbidden, "You may not do this to this held role.", "TakeRole", "HeldRole", "TakeRole may not run against this HeldRole")
			case err != nil:
				t.Errorf("ExecuteGate.EnforceProposed() error = %v, want the row admitted", err)
			}
		})
	}

	t.Run("no condition enforces nothing", func(t *testing.T) {
		t.Parallel()

		var none *ExecuteGate
		if err := none.EnforceProposed(t.Context(), nil, target, nil); err != nil {
			t.Errorf("nil gate EnforceProposed() error = %v, want nil", err)
		}
		open := &ExecuteGate{method: "TakeRole"}
		if err := open.EnforceProposed(t.Context(), nil, target, nil); err != nil {
			t.Errorf("condition-free gate EnforceProposed() error = %v, want nil", err)
		}
	})

	t.Run("a condition settled to false refuses", func(t *testing.T) {
		t.Parallel()

		gate := &ExecuteGate{method: "TakeRole", user: "ann", env: RequestEnvironment(), cond: condition.Truth{Value: false}}
		if err := gate.EnforceProposed(t.Context(), nil, target, map[string]any{}); err == nil || !httpio.HasForbidden(err) {
			t.Errorf("ExecuteGate.EnforceProposed() error = %v, want Forbidden", err)
		}
	})
}

// TestSetCache_For pins the Set the cache builds for each operation of a generated patch
// armed with Enforce. A create and an update ask their permission of every field a
// request may address. A delete carries no field, so its Set asks Delete of the resource
// and nothing of a field: a generated delete patch is armed with it, and so is the
// delete of every row a record's declared cascade takes.
func TestSetCache_For(t *testing.T) {
	t.Parallel()

	var cache SetCache[tenancyResource, tenancyCreateRequest]

	for _, perm := range []accesstypes.Permission{accesstypes.Create, accesstypes.Update} {
		set := cache.For(perm)
		if !set.PermissionRequired("Name", perm) {
			t.Errorf("For(%s): the field Name does not require %s", perm, perm)
		}
	}

	forDelete := cache.For(accesstypes.Delete)
	if got, want := forDelete.Permissions(), []accesstypes.Permission{accesstypes.Delete}; !slices.Equal(got, want) {
		t.Errorf("For(Delete).Permissions() = %v, want %v", got, want)
	}
	if got := forDelete.TagPermissions(); len(got) != 0 {
		t.Errorf("For(Delete) asks a permission of the fields %v, want of none", got)
	}
	if got := forDelete.BaseResource(); got != tenancyTestResource {
		t.Errorf("For(Delete).BaseResource() = %q, want %q", got, tenancyTestResource)
	}
	if cache.For(accesstypes.Delete) != forDelete {
		t.Error("For(Delete) built a second Set, want the one the cache holds")
	}
}
