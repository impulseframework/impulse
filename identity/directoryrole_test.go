package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// TestManager_DirectoryRole pins the rule of the role a tenant is opened to its
// directory's people with: it is a role of the tenant, and one the request's caller
// could give there directly, asked of the grant of the method that gives a role, for no
// person in particular. The grant answers first: a role the caller may not give is
// Forbidden in the same words whether the tenant holds it or not, so the refusal tells
// nobody of a role the grant keeps from them; a role the caller could give that the
// tenant does not hold is a BadRequest; a call outside any request is the application's
// own act; a manager that cannot ask refuses the request, never passes it. The rule
// writes nothing.
func TestManager_DirectoryRole(t *testing.T) {
	t.Parallel()

	nobody := impulse.NilUUID.String()
	tests := []struct {
		name string
		role accesstypes.Role
		// outside says the call is made outside any request.
		outside bool
		// wantBadRequest and wantForbidden are what the refusal says; both empty where
		// the role may be named.
		wantBadRequest string
		wantForbidden  string
		wantAsked      []string
	}{
		{name: "a role of the tenant the caller may give", role: "Reader", wantAsked: []string{"give domain/alpha " + nobody + " Reader"}},
		{
			name: "a role of the tenant the caller may not give is refused", role: "Warden",
			wantForbidden: "you may not give the role Warden in this tenant, so you cannot make it the role everyone from its directory gets",
			wantAsked:     []string{"give domain/alpha " + nobody + " Warden"},
		},
		{
			name: "a role the caller may not give is refused in the same words where the tenant does not hold it: the refusal is no word on whether it exists", role: "Ghost",
			wantForbidden: "you may not give the role Ghost in this tenant, so you cannot make it the role everyone from its directory gets",
			wantAsked:     []string{"give domain/alpha " + nobody + " Ghost"},
		},
		{
			name: "a role the caller could give that the tenant does not hold is refused", role: "Steward",
			wantBadRequest: `"Steward" is not a role of this tenant`, wantAsked: []string{"give domain/alpha " + nobody + " Steward"},
		},
		{
			name: "a role that exists nowhere is refused", role: "Nobody",
			wantBadRequest: `"Nobody" is not a role of this tenant`, wantAsked: []string{"give domain/alpha " + nobody + " Nobody"},
		},
		{name: "a call outside any request is the application's own act, held to the role's existence alone", role: "Warden", outside: true},
		{name: "and still refuses a role the tenant does not hold", role: "Steward", outside: true, wantBadRequest: `"Steward" is not a role of this tenant`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			grants := &fakeGrants{noGive: []accesstypes.Role{"Warden", "Ghost"}}
			m, _ := newManager(t, store, wardenRoles, WithRoleGrants(grants))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if !tt.outside {
					ctx = asCallerThrough(ctx, managedAuth, erin)
				}

				return m.DirectoryRole(ctx, txn, "alpha", tt.role)
			})
			switch {
			case tt.wantBadRequest != "":
				if !httpio.HasBadRequest(err) || !strings.Contains(httpio.Message(err), tt.wantBadRequest) {
					t.Fatalf("DirectoryRole() error = %v, want a BadRequest saying %q", err, tt.wantBadRequest)
				}
			case tt.wantForbidden != "":
				if !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), tt.wantForbidden) {
					t.Fatalf("DirectoryRole() error = %v, want Forbidden saying %q", err, tt.wantForbidden)
				}
			case err != nil:
				t.Fatalf("DirectoryRole() error = %v, want the role admitted", err)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the rule writes nothing", store.writes)
			}
		})
	}

	t.Run("a manager with no grants refuses a request it cannot judge", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.DirectoryRole(asCallerThrough(ctx, managedAuth, erin), txn, "alpha", "Reader")
		})
		if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "identity.WithRoleGrants") {
			t.Errorf("DirectoryRole() error = %v, want an error naming identity.WithRoleGrants", err)
		}
	})

	t.Run("a grant that cannot be read fails the rule", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles, WithRoleGrants(&fakeGrants{err: errStore}))
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.DirectoryRole(asCallerThrough(ctx, managedAuth, erin), txn, "alpha", "Reader")
		})
		if !errors.Is(err, errStore) {
			t.Errorf("DirectoryRole() error = %v, want the grants' own error", err)
		}
	})
}
