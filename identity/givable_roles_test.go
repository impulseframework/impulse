package identity

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// givableRoles are the roles of the tests' engine for the whole application's role
// lists: the Super Administrator role beside Steward for the whole application, Reader
// and Warden in alpha, and Reader in every tenant, where the engine lists the Super
// Administrator role too, as no role file can, so the list is seen to offer it for the
// whole application alone.
var givableRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {accesstypes.SuperAdministrator, "Steward"},
	alphaScope:  {"Reader", "Warden"},
	everyScope:  {"Reader", accesstypes.SuperAdministrator},
}

// TestManager_GivableRoles pins the list one of the whole application's dialogs picks a
// role from: the roles that exist where the role is to be held that the caller could
// give there with the dialog's own method, asked of the role file's grant on that
// method inside a transaction of the list's own. Each method's list is its own: a role
// one method alone takes from the caller is in that method's list and in no other's.
// The Super Administrator role is offered to a caller who holds it and to nobody else,
// and for the whole application alone; a call outside any request lists every role and
// asks nothing.
func TestManager_GivableRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		method    accesstypes.Resource
		scope     accesstypes.PolicyScope
		super     bool
		outside   bool
		noneGives []accesstypes.Role
		onlyBy    map[accesstypes.Role]accesstypes.Resource
		want      []string
		wantAsked []string
	}{
		{
			name: "for the whole application, a caller who does not hold the Super Administrator role is not offered it", method: assigns, scope: globalScope,
			want: []string{"Steward"}, wantAsked: []string{"AssignMembersRole could give global Steward"},
		},
		{
			name: "for the whole application, a super administrator is offered the Super Administrator role too", method: assigns, scope: globalScope, super: true,
			want: []string{"Steward", string(accesstypes.SuperAdministrator)}, wantAsked: []string{"AssignMembersRole could give global " + string(accesstypes.SuperAdministrator), "AssignMembersRole could give global Steward"},
		},
		{
			name: "in one tenant, the tenant's roles the method's grant lets the caller give", method: assigns, scope: alphaScope, noneGives: []accesstypes.Role{"Warden"},
			want: []string{"Reader"}, wantAsked: []string{"AssignMembersRole could give alpha Reader", "AssignMembersRole could give alpha Warden"},
		},
		{
			name: "a role the invitation alone takes from the caller is in the invitation's list", method: invites, scope: alphaScope, onlyBy: map[accesstypes.Role]accesstypes.Resource{"Warden": invites},
			want: []string{"Reader", "Warden"}, wantAsked: []string{"InviteMembersUser could give alpha Reader", "InviteMembersUser could give alpha Warden"},
		},
		{
			name: "and in no other method's: the list of the method that gives a person a role leaves it out", method: assigns, scope: alphaScope, onlyBy: map[accesstypes.Role]accesstypes.Resource{"Warden": invites},
			want: []string{"Reader"}, wantAsked: []string{"AssignMembersRole could give alpha Reader", "AssignMembersRole could give alpha Warden"},
		},
		{
			name: "in every tenant, the roles held there, and never the Super Administrator role, even to a caller who holds it", method: assigns, scope: everyScope, super: true,
			want: []string{"Reader"}, wantAsked: []string{"AssignMembersRole could give " + everyScope.String() + " Reader"},
		},
		{
			name: "a call outside any request lists every role and asks nothing", method: assigns, scope: globalScope, outside: true, noneGives: []accesstypes.Role{"Steward"},
			want: []string{"Steward", string(accesstypes.SuperAdministrator)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var held []access.MembershipKey
			if tt.super {
				held = append(held, erinSuper)
			}
			grants := &fakeGrants{noneGives: tt.noneGives, onlyBy: tt.onlyBy}
			store := superStore(held...)
			m, _ := newManager(t, store, givableRoles, WithRoleGrants(grants))
			ctx := t.Context()
			if !tt.outside {
				ctx = asCallerThrough(ctx, managedAuth, erin)
			}

			roles, err := m.GivableRoles(ctx, fakeDatabase{}, tt.method, tt.scope)
			if err != nil {
				t.Fatalf("GivableRoles() error = %v", err)
			}
			got := make([]string, 0, len(roles))
			for _, role := range roles {
				got = append(got, string(role.Name))
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GivableRoles() (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if len(grants.askedOutside) != 0 {
				t.Errorf("questions asked outside a transaction = %v, want none", grants.askedOutside)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the list writes nothing", store.writes)
			}
		})
	}

	t.Run("a manager with no grants refuses a request it cannot judge", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, superStore(), givableRoles)
		if _, err := m.GivableRoles(asCallerThrough(t.Context(), managedAuth, erin), fakeDatabase{}, assigns, globalScope); err == nil {
			t.Error("GivableRoles() error = nil, want a refusal: the manager cannot judge the request")
		}
	})

	t.Run("a tenant's administrator's role of a tenant that is directory only is offered only to a caller the tenant admits", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			enters []accesstypes.Domain
			want   []string
		}{
			{want: []string{"Reader"}},
			{enters: []accesstypes.Domain{"alpha"}, want: []string{"Reader", "Warden"}},
		} {
			m := outsiderManager(t, groupStore())
			roles, err := m.GivableRoles(asEntering(t.Context(), erin, tt.enters...), fakeDatabase{}, assigns, alphaScope)
			if err != nil {
				t.Fatalf("GivableRoles() error = %v", err)
			}
			got := make([]string, 0, len(roles))
			for _, role := range roles {
				got = append(got, string(role.Name))
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GivableRoles(caller entering %v) (-want +got):\n%s", tt.enters, diff)
			}
		}
	})
}

// TestGivingMethod pins which method a list of the roles to give answers for, from the
// filter on the method: the one method one equality names, among the methods whose
// role field picks from the list; no rows for a request that names none, since the
// list is one method's at a time; and anything else refused in words that say what to
// send and name the methods.
func TestGivingMethod(t *testing.T) {
	t.Parallel()

	methods := []accesstypes.Resource{assigns, invites}
	eq := func(value any) resource.Condition {
		return resource.Condition{Field: "Method", Operator: "eq", Value: value}
	}
	tests := []struct {
		name    string
		on      []resource.Condition
		want    accesstypes.Resource
		wantOK  bool
		wantErr bool
	}{
		{name: "no condition names no method, and the list holds no rows"},
		{name: "one method", on: []resource.Condition{eq("InviteMembersUser")}, want: invites, wantOK: true},
		{name: "a method that picks no role from the list", on: []resource.Condition{eq("DisableMembersUser")}, wantErr: true},
		{name: "no name", on: []resource.Condition{eq("")}, wantErr: true},
		{name: "two conditions", on: []resource.Condition{eq("AssignMembersRole"), eq("InviteMembersUser")}, wantErr: true},
		{name: "a set of methods", on: []resource.Condition{{Field: "Method", Operator: "in", Values: []any{"AssignMembersRole", "InviteMembersUser"}}}, wantErr: true},
		{name: "every method but one", on: []resource.Condition{{Field: "Method", Operator: "ne", Value: "AssignMembersRole"}}, wantErr: true},
		{name: "a value that is no text", on: []resource.Condition{eq(7)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := GivingMethod("MembersGivableRoles", methods, tt.on)
			if tt.wantErr {
				if !httpio.HasBadRequest(err) {
					t.Errorf("GivingMethod() error = %v, want a bad request", err)
				}
				message := httpio.Message(err)
				if !strings.Contains(message, "one method") || !strings.Contains(message, "method:eq:<name>") || !strings.Contains(message, "AssignMembersRole, InviteMembersUser") {
					t.Errorf("GivingMethod() refusal = %q, want what to send and the methods the list answers for", message)
				}

				return
			}
			if err != nil {
				t.Fatalf("GivingMethod() error = %v", err)
			}
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("GivingMethod() = %q, %t, want %q, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestGivingPlaces pins where a list of the roles to give from the whole application's
// management lists roles for, from the filter on the tenant: one tenant's roles for one
// equality on its id; the whole application's and every tenant's for none, and for an
// application without tenants the whole application's alone; anything else refused in
// words that say what to send.
func TestGivingPlaces(t *testing.T) {
	t.Parallel()

	eq := func(value any) resource.Condition {
		return resource.Condition{Field: "Domain", Operator: "eq", Value: value}
	}
	tests := []struct {
		name       string
		tenantWord string
		on         []resource.Condition
		want       []accesstypes.PolicyScope
		wantErr    bool
	}{
		{name: "no condition, with tenants", tenantWord: "sector", want: []accesstypes.PolicyScope{globalScope, everyScope}},
		{name: "no condition, without tenants", want: []accesstypes.PolicyScope{globalScope}},
		{name: "one tenant", tenantWord: "sector", on: []resource.Condition{eq("alpha")}, want: []accesstypes.PolicyScope{alphaScope}},
		{name: "no tenant named", tenantWord: "sector", on: []resource.Condition{eq("")}, want: []accesstypes.PolicyScope{globalScope, everyScope}},
		{name: "two conditions", tenantWord: "sector", on: []resource.Condition{eq("alpha"), eq("beta")}, wantErr: true},
		{name: "a set of tenants", tenantWord: "sector", on: []resource.Condition{{Field: "Domain", Operator: "in", Values: []any{"alpha"}}}, wantErr: true},
		{name: "every tenant but one", tenantWord: "sector", on: []resource.Condition{{Field: "Domain", Operator: "ne", Value: "alpha"}}, wantErr: true},
		{name: "a value that is no text", tenantWord: "sector", on: []resource.Condition{eq(7)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := GivingPlaces("MembersGivableRoles", tt.tenantWord, tt.on)
			if tt.wantErr {
				if !httpio.HasBadRequest(err) {
					t.Errorf("GivingPlaces() error = %v, want a bad request", err)
				}
				if !strings.Contains(httpio.Message(err), "one place at a time") || !strings.Contains(httpio.Message(err), tt.tenantWord+"'s id") {
					t.Errorf("GivingPlaces() refusal = %q, want what to send, in the application's word for a tenant (%s)", httpio.Message(err), tt.tenantWord)
				}

				return
			}
			if err != nil {
				t.Fatalf("GivingPlaces() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
				t.Errorf("GivingPlaces() (-want +got):\n%s", diff)
			}
		})
	}
}
