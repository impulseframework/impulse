package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
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
			wantForbidden: "You may not give the role Warden in this tenant, so you cannot make it the role everyone from its directory gets.",
			wantAsked:     []string{"give domain/alpha " + nobody + " Warden"},
		},
		{
			name: "a role the caller may not give is refused in the same words where the tenant does not hold it: the refusal is no word on whether it exists", role: "Ghost",
			wantForbidden: "You may not give the role Ghost in this tenant, so you cannot make it the role everyone from its directory gets.",
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
				toldPlainly(t, err, tt.wantForbidden, "ask someone who may give this one")
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

// TestManager_DirectoryRoles pins the list the role a tenant is opened with is picked
// from: the roles of the tenant that the request's caller could give there directly,
// asked of the grant DirectoryRole asks, for no person in particular, and no other. The
// list offers what the method takes: a role it leaves out is one DirectoryRole refuses.
// A call outside any request is the application's own and lists every role of the
// tenant; a manager that cannot ask refuses the request, never lists; and no tenant
// named is an error.
//
// The list is read outside any transaction: the tenant's roles come from the permission
// engine, whose listing the database refuses under a transaction's context (the fake
// engine refuses it there as well), and the grants are asked in a transaction of the
// list's own, since the grant of a tenant's method may read a row.
func TestManager_DirectoryRoles(t *testing.T) {
	t.Parallel()

	nobody := impulse.NilUUID.String()
	reader := access.RoleInfo{Name: "Reader", Description: "the Reader role"}
	warden := access.RoleInfo{Name: "Warden", Description: "the Warden role"}
	tests := []struct {
		name string
		// noGive are the roles the caller may not give.
		noGive []accesstypes.Role
		// outside says the call is made outside any request.
		outside   bool
		want      []access.RoleInfo
		wantAsked []string
	}{
		{
			name: "a caller is listed the roles of the tenant they may give", noGive: []accesstypes.Role{"Warden"},
			want: []access.RoleInfo{reader}, wantAsked: []string{"give domain/alpha " + nobody + " Reader", "give domain/alpha " + nobody + " Warden"},
		},
		{
			name: "a caller who may give every role is listed every role of the tenant",
			want: []access.RoleInfo{reader, warden}, wantAsked: []string{"give domain/alpha " + nobody + " Reader", "give domain/alpha " + nobody + " Warden"},
		},
		{
			name: "a caller who may give none is listed none", noGive: []accesstypes.Role{"Reader", "Warden"},
			want: []access.RoleInfo{}, wantAsked: []string{"give domain/alpha " + nobody + " Reader", "give domain/alpha " + nobody + " Warden"},
		},
		{name: "a call outside any request lists every role of the tenant and asks nothing", noGive: []accesstypes.Role{"Warden"}, outside: true, want: []access.RoleInfo{reader, warden}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			grants := &fakeGrants{noGive: tt.noGive}
			m, _ := newManager(t, store, wardenRoles, WithRoleGrants(grants))

			ctx := t.Context()
			if !tt.outside {
				ctx = asCallerThrough(ctx, managedAuth, erin)
			}
			got, err := m.DirectoryRoles(ctx, fakeDatabase{}, "alpha")
			if err != nil {
				t.Fatalf("DirectoryRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("DirectoryRoles() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if len(grants.askedOutside) != 0 {
				t.Errorf("grants asked outside a transaction = %v, want none: the grant of a tenant's method may read a row", grants.askedOutside)
			}
			// Each role the list offers is one the method takes, and each it leaves out
			// is one the method refuses.
			for _, role := range []accesstypes.Role{"Reader", "Warden"} {
				offered := false
				for _, listed := range got {
					offered = offered || listed.Name == role
				}
				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					if !tt.outside {
						ctx = asCallerThrough(ctx, managedAuth, erin)
					}

					return m.DirectoryRole(ctx, txn, "alpha", role)
				})
				if offered != (err == nil) {
					t.Errorf("the list offers %s = %v, and DirectoryRole() error = %v: the list offers what the method takes", role, offered, err)
				}
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the list writes nothing", store.writes)
			}
		})
	}

	t.Run("a transaction the database runs again lists each role once", func(t *testing.T) {
		t.Parallel()

		grants := &fakeGrants{}
		m, _ := newManager(t, groupStore(), wardenRoles, WithRoleGrants(grants))
		got, err := m.DirectoryRoles(asCallerThrough(t.Context(), managedAuth, erin), ranTwice{}, "alpha")
		if err != nil {
			t.Fatalf("DirectoryRoles() error = %v", err)
		}
		if diff := cmp.Diff([]access.RoleInfo{reader, warden}, got); diff != "" {
			t.Errorf("DirectoryRoles() over a transaction run twice mismatch (-want +got):\n%s", diff)
		}
		if len(grants.asked) != 4 {
			t.Errorf("the grants were asked %d times, want 4: each role in each run of the transaction", len(grants.asked))
		}
	})

	t.Run("a manager with no grants refuses a request it cannot judge", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles)
		_, err := m.DirectoryRoles(asCallerThrough(t.Context(), managedAuth, erin), fakeDatabase{}, "alpha")
		if err == nil || !strings.Contains(err.Error(), "identity.WithRoleGrants") {
			t.Errorf("DirectoryRoles() error = %v, want an error naming identity.WithRoleGrants", err)
		}
	})

	t.Run("a grant that cannot be read fails the list", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles, WithRoleGrants(&fakeGrants{err: errStore}))
		_, err := m.DirectoryRoles(asCallerThrough(t.Context(), managedAuth, erin), fakeDatabase{}, "alpha")
		if !errors.Is(err, errStore) {
			t.Errorf("DirectoryRoles() error = %v, want the grants' own error", err)
		}
	})

	t.Run("no tenant named is an error", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles, WithRoleGrants(&fakeGrants{}))
		_, err := m.DirectoryRoles(t.Context(), fakeDatabase{}, "")
		if err == nil || !strings.Contains(err.Error(), "no tenant was named") {
			t.Errorf("DirectoryRoles() error = %v, want one that says no tenant was named", err)
		}
	})
}

// TestManager_RolesToGive pins the list a role is picked from to give one in a tenant by
// hand with one method: the roles of the tenant that the request's caller could give
// there with that method, asked of the role file's grant on it before anybody is named
// (RoleGrants.CouldGive), and no other, so a role one method alone takes from the
// caller is in that method's list and in no other's. A call outside any request is the
// application's own and lists every role of the tenant; a manager that cannot ask
// refuses the request, never lists; and no tenant named is an error. The question
// reads no row, so the list opens no transaction and asks under none.
func TestManager_RolesToGive(t *testing.T) {
	t.Parallel()

	reader := access.RoleInfo{Name: "Reader", Description: "the Reader role"}
	warden := access.RoleInfo{Name: "Warden", Description: "the Warden role"}
	asked := []string{"AssignMembersRole could give alpha Reader", "AssignMembersRole could give alpha Warden"}
	invited := []string{"InviteMembersUser could give alpha Reader", "InviteMembersUser could give alpha Warden"}
	byInvitation := map[accesstypes.Role]accesstypes.Resource{"Warden": invites}
	tests := []struct {
		name string
		// method is the method whose list is asked for; the one that gives a person a
		// role where a case names none.
		method accesstypes.Resource
		// noneGives are the roles no method that gives a role takes from the caller.
		noneGives []accesstypes.Role
		// onlyBy are the roles one method alone takes from the caller.
		onlyBy map[accesstypes.Role]accesstypes.Resource
		// outside says the call is made outside any request.
		outside   bool
		want      []access.RoleInfo
		wantAsked []string
	}{
		{name: "a caller is listed the roles of the tenant the method takes from them", noneGives: []accesstypes.Role{"Warden"}, want: []access.RoleInfo{reader}, wantAsked: asked},
		{name: "a caller every role is taken from is listed every role of the tenant", want: []access.RoleInfo{reader, warden}, wantAsked: asked},
		{name: "a caller the method takes no role from is listed none", noneGives: []accesstypes.Role{"Reader", "Warden"}, want: []access.RoleInfo{}, wantAsked: asked},
		{name: "a role the invitation alone takes from the caller is in the invitation's list", method: invites, onlyBy: byInvitation, want: []access.RoleInfo{reader, warden}, wantAsked: invited},
		{name: "and in no other method's: the list of the method that gives a person a role leaves it out", onlyBy: byInvitation, want: []access.RoleInfo{reader}, wantAsked: asked},
		{name: "a call outside any request lists every role of the tenant and asks nothing", noneGives: []accesstypes.Role{"Warden"}, outside: true, want: []access.RoleInfo{reader, warden}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			grants := &fakeGrants{noneGives: tt.noneGives, onlyBy: tt.onlyBy}
			m, _ := newManager(t, store, wardenRoles, WithRoleGrants(grants))

			ctx := t.Context()
			if !tt.outside {
				ctx = asCallerThrough(ctx, managedAuth, erin)
			}
			method := tt.method
			if method == "" {
				method = assigns
			}
			got, err := m.RolesToGive(ctx, method, "alpha")
			if err != nil {
				t.Fatalf("RolesToGive() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RolesToGive() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the list writes nothing", store.writes)
			}
		})
	}

	t.Run("a manager with no grants refuses a request it cannot judge", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles)
		_, err := m.RolesToGive(asCallerThrough(t.Context(), managedAuth, erin), assigns, "alpha")
		if err == nil || !strings.Contains(err.Error(), "identity.WithRoleGrants") {
			t.Errorf("RolesToGive() error = %v, want an error naming identity.WithRoleGrants", err)
		}
	})

	t.Run("a grant that cannot be read fails the list", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles, WithRoleGrants(&fakeGrants{err: errStore}))
		_, err := m.RolesToGive(asCallerThrough(t.Context(), managedAuth, erin), assigns, "alpha")
		if !errors.Is(err, errStore) {
			t.Errorf("RolesToGive() error = %v, want the grants' own error", err)
		}
	})

	t.Run("no tenant named is an error", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), wardenRoles, WithRoleGrants(&fakeGrants{}))
		_, err := m.RolesToGive(t.Context(), assigns, "")
		if err == nil || !strings.Contains(err.Error(), "no tenant was named") {
			t.Errorf("RolesToGive() error = %v, want one that says no tenant was named", err)
		}
	})
}

// ranTwice is a database that runs a transaction's function twice before it commits, as
// the application's database does when it takes the transaction back and tries again.
type ranTwice struct{}

func (ranTwice) ExecuteFunc(ctx context.Context, f func(ctx context.Context, txn resource.ReadWriteTransaction) error) error {
	if err := (fakeDatabase{}).ExecuteFunc(ctx, f); err != nil {
		return err
	}

	return fakeDatabase{}.ExecuteFunc(ctx, f)
}

// TestManager_DirectoryName pins that the manager names a directory as the session
// library's registrations do: the directory's method and connection are the ones
// asked, and the name and the label come back as the library answered them, so the
// words of a warning are the registrations' and never the manager's own.
func TestManager_DirectoryName(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, groupStore(), wardenRoles)
	m.sessions = fakeSessions{
		named:  map[string]string{"6f2c1b7e-0000-4000-8000-000000000001": "Lakeside"},
		labels: map[sessioninfo.AuthMethod]string{sessioninfo.MethodAzure: "Microsoft", sessioninfo.MethodGoogle: "Google"},
	}
	// The manager is what the rules of a tenant's directory are named by.
	var namer resource.DirectoryNamer = m.DirectoryName

	tests := []struct {
		name      string
		directory signin.Directory
		wantName  string
		wantLabel string
	}{
		{name: "a tenant a registration names", directory: signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-0000-4000-8000-000000000001"}, wantName: "Lakeside", wantLabel: "Microsoft"},
		{name: "a tenant no registration names has its button's label", directory: signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-0000-4000-8000-000000000002"}, wantLabel: "Microsoft"},
		{name: "the label is the one of the directory's own method", directory: signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "acme.example"}, wantLabel: "Google"},
		{name: "a method the registrations do not know has neither", directory: signin.Directory{Method: sessioninfo.MethodWorkOS, Connection: "conn_01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name, label := namer(tt.directory)
			if name != tt.wantName || label != tt.wantLabel {
				t.Errorf("DirectoryName() = (%q, %q), want (%q, %q)", name, label, tt.wantName, tt.wantLabel)
			}
		})
	}
}
