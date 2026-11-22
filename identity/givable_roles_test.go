package identity

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
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

// TestManager_GivableGroupRoles pins the list the dialog that gives a group a role
// picks from: the roles the caller could give there with the method that the group
// itself may hold there, by the rule the method holds the group to. A group kept in one
// tenant is offered that tenant's roles in that tenant and no role anywhere else, and
// the grants are asked nothing about a role the group may not hold. No group is offered
// the Super Administrator role, whoever asks: the caller of every case holds it, and a
// call outside any request is held to the group's rule too. A group that does not
// exist is listed no role anywhere, as a group kept somewhere else is, and the grants
// are asked nothing about it: the list never says whether an id is a group's.
func TestManager_GivableGroupRoles(t *testing.T) {
	t.Parallel()

	const gives accesstypes.Resource = "GiveMembersGroupRole"
	roles := maps.Clone(givableRoles)
	roles[betaScope] = []accesstypes.Role{"Reader"}
	asked := func(scope accesstypes.PolicyScope, names ...string) []string {
		questions := make([]string, 0, len(names))
		for _, name := range names {
			questions = append(questions, string(gives)+" could give "+scope.String()+" "+name)
		}

		return questions
	}
	tests := []struct {
		name      string
		group     impulse.UUID
		scope     accesstypes.PolicyScope
		outside   bool
		want      []string
		wantAsked []string
	}{
		{
			name: "a group kept in one tenant, in that tenant: the tenant's roles", group: nightID, scope: alphaScope,
			want: []string{"Reader", "Warden"}, wantAsked: asked(alphaScope, "Reader", "Warden"),
		},
		{name: "a group kept in one tenant, for the whole application: no role", group: nightID, scope: globalScope, want: []string{}},
		{name: "a group kept in one tenant, in every tenant: no role", group: nightID, scope: everyScope, want: []string{}},
		{name: "a group kept in one tenant, in another tenant: no role", group: nightID, scope: betaScope, want: []string{}},
		{
			name: "a group kept for the whole application, there: never the Super Administrator role", group: staffID, scope: globalScope,
			want: []string{"Steward"}, wantAsked: asked(globalScope, "Steward"),
		},
		{
			name: "a group kept for the whole application, in every tenant", group: staffID, scope: everyScope,
			want: []string{"Reader"}, wantAsked: asked(everyScope, "Reader"),
		},
		{
			name: "a group kept for the whole application, in one tenant", group: staffID, scope: alphaScope,
			want: []string{"Reader", "Warden"}, wantAsked: asked(alphaScope, "Reader", "Warden"),
		},
		{
			name: "outside any request the group's rule holds still: no Super Administrator role", group: staffID, scope: globalScope, outside: true,
			want: []string{"Steward"},
		},
		{
			name: "outside any request, a group kept in one tenant holds no role for the whole application", group: nightID, scope: globalScope, outside: true,
			want: []string{},
		},
		{name: "a group that does not exist, in one tenant: no role, as a group kept in another tenant is answered", group: newGroupID, scope: alphaScope, want: []string{}},
		{name: "a group that does not exist, for the whole application: no role", group: newGroupID, scope: globalScope, want: []string{}},
		{name: "a group that does not exist, in every tenant: no role", group: newGroupID, scope: everyScope, want: []string{}},
		{name: "outside any request, a group that does not exist is listed no role", group: newGroupID, scope: alphaScope, outside: true, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grants := &fakeGrants{}
			store := superStore(erinSuper)
			store.groups = groupStore().groups
			m, _ := newManager(t, store, roles, WithRoleGrants(grants))
			ctx := t.Context()
			if !tt.outside {
				ctx = asCallerThrough(ctx, managedAuth, erin)
			}

			offered, err := m.GivableGroupRoles(ctx, fakeDatabase{}, gives, tt.group, tt.scope)
			if err != nil {
				t.Fatalf("GivableGroupRoles() error = %v", err)
			}
			got := make([]string, 0, len(offered))
			for _, role := range offered {
				got = append(got, string(role.Name))
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GivableGroupRoles() (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the list writes nothing", store.writes)
			}
		})
	}
}

// TestGivingGroup pins which group a list of the roles to give answers for, from the
// filter on the group: the one group one equality names beside the method that gives a
// group a role; no rows for that method asked with no group, since a group's list is
// one group's; no group, and the rows, for a method that gives a person a role; and
// anything else refused in words that say what to send.
func TestGivingGroup(t *testing.T) {
	t.Parallel()

	const gives accesstypes.Resource = "GiveMembersGroupRole"
	eq := func(value any) resource.Condition {
		return resource.Condition{Field: "Group", Operator: "eq", Value: value}
	}
	tests := []struct {
		name      string
		method    accesstypes.Resource
		on        []resource.Condition
		want      *impulse.UUID
		wantOK    bool
		wantSaid  string
		wantError bool
	}{
		{name: "the group's method with its group", method: gives, on: []resource.Condition{eq(nightID.String())}, want: &nightID, wantOK: true},
		{name: "the group's method with no group holds no rows", method: gives},
		{name: "a person's method names no group", method: assigns, wantOK: true},
		{name: "no method and no group", method: "", wantOK: true},
		{name: "a group beside a person's method", method: assigns, on: []resource.Condition{eq(nightID.String())}, wantError: true, wantSaid: "method:eq:GiveMembersGroupRole,group:eq:<id>"},
		{name: "a group beside no method", method: "", on: []resource.Condition{eq(nightID.String())}, wantError: true, wantSaid: "method:eq:GiveMembersGroupRole,group:eq:<id>"},
		{name: "two groups", method: gives, on: []resource.Condition{eq(nightID.String()), eq(staffID.String())}, wantError: true, wantSaid: "group:eq:<id>"},
		{name: "a set of groups", method: gives, on: []resource.Condition{{Field: "Group", Operator: "in", Values: []any{nightID.String()}}}, wantError: true, wantSaid: "group:eq:<id>"},
		{name: "a value that is no id", method: gives, on: []resource.Condition{eq("night shift")}, wantError: true, wantSaid: "night shift is no group's id"},
		{name: "no id", method: gives, on: []resource.Condition{eq("")}, wantError: true, wantSaid: "group:eq:<id>"},
		{name: "a value that is no text", method: gives, on: []resource.Condition{eq(7)}, wantError: true, wantSaid: "group:eq:<id>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := GivingGroup("MembersGivableRoles", gives, tt.method, tt.on)
			if tt.wantError {
				if !httpio.HasBadRequest(err) {
					t.Errorf("GivingGroup() error = %v, want a bad request", err)
				}
				if message := httpio.Message(err); !strings.Contains(message, "MembersGivableRoles") || !strings.Contains(message, tt.wantSaid) {
					t.Errorf("GivingGroup() refusal = %q, want the list named and %q", message, tt.wantSaid)
				}

				return
			}
			if err != nil {
				t.Fatalf("GivingGroup() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" || ok != tt.wantOK {
				t.Errorf("GivingGroup() ok = %t, want %t; group (-want +got):\n%s", ok, tt.wantOK, diff)
			}
		})
	}
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
