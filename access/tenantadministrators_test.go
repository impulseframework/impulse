package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// firstTenant and secondTenant are the tenants tenant1Policy and tenant2Policy hold
// policy for, as a domain.
const (
	firstTenant  accesstypes.Domain = "tenant1"
	secondTenant accesstypes.Domain = "tenant2"
)

// Test_administersTenant pins the one statement of where a role must be held to
// make its holder a tenant's administrator: one of the roles the application
// names, held in the tenant itself by the person's own membership or through a
// group kept in that tenant, and nowhere else.
func TestAdministersTenant(t *testing.T) {
	t.Parallel()

	administrators := []accesstypes.Role{"Keeper", "Warden"}
	global := globalPolicy
	tests := []struct {
		name       string
		scope      accesstypes.PolicyScope
		role       accesstypes.Role
		kept       *accesstypes.PolicyScope
		wantDomain accesstypes.Domain
		want       bool
	}{
		{name: "the person's own membership in the tenant", scope: tenant1Policy, role: "Keeper", wantDomain: firstTenant, want: true},
		{name: "the second role the application names", scope: tenant2Policy, role: "Warden", wantDomain: secondTenant, want: true},
		{name: "a group kept in the tenant holds it there", scope: tenant1Policy, role: "Keeper", kept: &tenant1Policy, wantDomain: firstTenant, want: true},
		{name: "a role the application does not name", scope: tenant1Policy, role: "Reader"},
		{name: "held in every tenant", scope: everyDomainPolicy, role: "Keeper"},
		{name: "held in the global partition", scope: globalPolicy, role: "Keeper"},
		{name: "a group kept for the whole application holds it in the tenant", scope: tenant1Policy, role: "Keeper", kept: &global},
		{name: "a group kept in another tenant", scope: tenant1Policy, role: "Keeper", kept: &tenant2Policy},
		{name: "a group kept for the whole application holds it in every tenant", scope: everyDomainPolicy, role: "Keeper", kept: &global},
		{name: "the tenant with no name", scope: accesstypes.DomainPolicyScope(""), role: "Keeper"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			domain, ok := AdministersTenant(administrators, tt.scope, tt.role, tt.kept)
			if ok != tt.want || domain != tt.wantDomain {
				t.Errorf("AdministersTenant() = (%q, %v), want (%q, %v)", domain, ok, tt.wantDomain, tt.want)
			}
		})
	}
	if _, ok := AdministersTenant(nil, tenant1Policy, "Keeper", nil); ok {
		t.Error("AdministersTenant() with no role named: somebody administers a tenant")
	}
}

// administratorClient is superClient told that Reader, the role file's domain
// role, and Editor, a custom role made in firstTenant, administer a tenant.
func administratorClient(t *testing.T) (*Client, UserManager) {
	t.Helper()
	client, _ := superClient(t, WithTenantAdministratorRoles("Reader", "Editor"))
	manager := client.UserManager()
	if err := manager.AddRole(t.Context(), tenant1Policy, "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRole(t.Context(), tenant1Policy, "Clerk", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}

	return client, manager
}

// administered asks both places who administers what, and fails the test where
// they disagree: the store, which a sign-in is decided by, and the snapshot,
// which every request is. It answers the tenants the store lists.
func administered(t *testing.T, client *Client, user accesstypes.User) []accesstypes.Domain {
	t.Helper()
	ctx := t.Context()
	listed, err := client.UserManager().TenantsAdministered(ctx, user)
	if err != nil {
		t.Fatalf("TenantsAdministered(%s) error = %v", user, err)
	}
	for _, domain := range []accesstypes.Domain{firstTenant, secondTenant} {
		answered, err := client.UserAdministersTenant(ctx, user, domain)
		if err != nil {
			t.Fatalf("UserAdministersTenant(%s, %s) error = %v", user, domain, err)
		}
		bound, err := client.ForUser(user).AdministersTenant(ctx, domain)
		if err != nil {
			t.Fatalf("UserChecker.AdministersTenant(%s, %s) error = %v", user, domain, err)
		}
		if want := contains(listed, domain); answered != want || bound != want {
			t.Errorf("%s in %s: the store says %v, the snapshot %v and the bound checker %v: the sign-in and the request would disagree", user, domain, want, answered, bound)
		}
	}

	return listed
}

// contains reports whether domains holds domain.
func contains(domains []accesstypes.Domain, domain accesstypes.Domain) bool {
	for _, d := range domains {
		if d == domain {
			return true
		}
	}

	return false
}

// Test_tenantAdministrators_whoAdministers pins who administers a tenant, asked
// of the store and of the snapshot alike: a person who holds one of the named
// roles in the tenant itself, by their own membership or through the tenant's
// own group; never one who holds it in every tenant or through a group kept for
// the whole application, and never the holder of another role.
func Test_tenantAdministrators_whoAdministers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, manager := administratorClient(t)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	own := mustAddGroup(t, manager, tenant1Policy, "Keepers")
	wide := mustAddGroup(t, manager, globalPolicy, "Support")
	everywhere := mustAddGroup(t, manager, globalPolicy, "Operations")
	must(manager.AddGroupRoles(ctx, own, tenant1Policy, "Editor"))
	must(manager.AddGroupRoles(ctx, wide, tenant1Policy, "Reader"))
	must(manager.AddGroupRoles(ctx, everywhere, everyDomainPolicy, "Reader"))

	must(manager.AddUserRoles(ctx, tenant1Policy, "ann", "Reader"))
	must(manager.AddUserRoles(ctx, everyDomainPolicy, "eve", "Reader"))
	must(manager.AddGroupUsers(ctx, own, "gil"))
	must(manager.AddGroupUsers(ctx, wide, "sam"))
	must(manager.AddGroupUsers(ctx, everywhere, "ops"))
	must(manager.AddUserRoles(ctx, tenant1Policy, "cal", "Clerk"))
	must(manager.AddUserRoles(ctx, globalPolicy, "gus", "Steward"))
	must(manager.AddUserRoles(ctx, tenant1Policy, "two", "Reader"))
	must(manager.AddUserRoles(ctx, tenant2Policy, "two", "Reader"))

	tests := []struct {
		name string
		user accesstypes.User
		want []accesstypes.Domain
	}{
		{name: "a membership of their own in the tenant", user: "ann", want: []accesstypes.Domain{firstTenant}},
		{name: "the role held in every tenant", user: "eve"},
		{name: "through a group kept in the tenant", user: "gil", want: []accesstypes.Domain{firstTenant}},
		{name: "through a group kept for the whole application, which holds the role in the tenant", user: "sam"},
		{name: "through a group kept for the whole application, which holds the role in every tenant", user: "ops"},
		{name: "another role in the tenant", user: "cal"},
		{name: "a role of the whole application", user: "gus"},
		{name: "a membership of their own in each of two tenants", user: "two", want: []accesstypes.Domain{firstTenant, secondTenant}},
		{name: "nobody the store knows", user: "zed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, administered(t, client, tt.user)); diff != "" {
				t.Errorf("TenantsAdministered(%s) (-want +got):\n%s", tt.user, diff)
			}
		})
	}
}

// Test_tenantAdministrators_endAtTheNextQuestion pins when a person stops
// administering a tenant: the role taken away, the place in the group taken
// away, or the group's role taken away, each answered at the next question of
// the store and of the snapshot, with no sign-in in between.
func Test_tenantAdministrators_endAtTheNextQuestion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, manager := administratorClient(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	none := []accesstypes.Domain(nil)
	one := []accesstypes.Domain{firstTenant}

	must(manager.AddUserRoles(ctx, tenant1Policy, "ann", "Reader"))
	if diff := cmp.Diff(one, administered(t, client, "ann")); diff != "" {
		t.Fatalf("ann holds the role in the tenant (-want +got):\n%s", diff)
	}
	must(manager.DeleteUserRoles(ctx, tenant1Policy, "ann", "Reader"))
	if diff := cmp.Diff(none, administered(t, client, "ann")); diff != "" {
		t.Errorf("ann's role was taken away (-want +got):\n%s", diff)
	}

	keepers := mustAddGroup(t, manager, tenant1Policy, "Keepers")
	must(manager.AddGroupRoles(ctx, keepers, tenant1Policy, "Editor"))
	must(manager.AddGroupUsers(ctx, keepers, "gil", "hal"))
	if diff := cmp.Diff(one, administered(t, client, "gil")); diff != "" {
		t.Fatalf("gil is in the tenant's group (-want +got):\n%s", diff)
	}
	must(manager.DeleteGroupUsers(ctx, keepers, "gil"))
	if diff := cmp.Diff(none, administered(t, client, "gil")); diff != "" {
		t.Errorf("gil was taken out of the group (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(one, administered(t, client, "hal")); diff != "" {
		t.Errorf("hal is still in the group (-want +got):\n%s", diff)
	}
	must(manager.DeleteGroupRoles(ctx, keepers, tenant1Policy, "Editor"))
	if diff := cmp.Diff(none, administered(t, client, "hal")); diff != "" {
		t.Errorf("the group's role was taken away (-want +got):\n%s", diff)
	}
}

// Test_tenantAdministrators_periods pins that an assignment outside its period
// is no assignment: a membership, a place in the tenant's group and the group's
// role each administer nothing before their start and from their end on, by the
// store's clock for a sign-in and by the engine's own for a request, with
// nothing written when a period begins or ends.
func Test_tenantAdministrators_periods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newFakeStore()
	clock := newMovedClock(noon)
	store.clock = clock.now
	opts := defaultClientOptions()
	opts.tenantAdministrators = []accesstypes.Role{"Keeper"}
	e := testEngine(t, store, opts)
	e.clock = clock.now
	stored := newStoreManager(store)
	stored.onPolicyChange = e.policyChanged
	manager := newUserManager(stored, nil, e, opts.tenantAdministrators)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	must(manager.AddRole(ctx, tenant1Policy, "Keeper", aDescription))
	must(manager.AssignUserRole(ctx, tenant1Policy, "ann", "Keeper", between(1, 2)))
	keepers, err := manager.AddGroup(ctx, tenant1Policy, "Keepers", aDescription)
	must(err)
	must(manager.AssignGroupRole(ctx, keepers, tenant1Policy, "Keeper", between(3, 6)))
	must(manager.AssignGroupUser(ctx, keepers, "gil", between(4, 5)))

	writes := store.writes
	steps := []struct {
		hours    float64
		ann, gil bool
	}{
		{hours: 0.5},
		{hours: 1, ann: true},
		{hours: 1.5, ann: true},
		{hours: 2},
		{hours: 3.5},
		{hours: 4, gil: true},
		{hours: 4.5, gil: true},
		{hours: 5},
		{hours: 7},
		{hours: 1.25, ann: true},
	}
	for _, step := range steps {
		clock.set(hoursOn(step.hours))
		for user, want := range map[accesstypes.User]bool{"ann": step.ann, "gil": step.gil} {
			answered, err := e.userAdministersTenant(ctx, user, firstTenant)
			if err != nil {
				t.Fatalf("userAdministersTenant() error = %v", err)
			}
			listed, err := manager.TenantsAdministered(ctx, user)
			if err != nil {
				t.Fatalf("TenantsAdministered() error = %v", err)
			}
			if answered != want || contains(listed, firstTenant) != want {
				t.Errorf("%v hours on, %s administers the tenant: the snapshot says %v and the store %v, want %v", step.hours, user, answered, contains(listed, firstTenant), want)
			}
		}
	}
	if store.writes != writes {
		t.Errorf("the store took %d writes while the periods began and ended, want none", store.writes-writes)
	}
}

// Test_tenantAdministrators_withoutTheOption pins the default: an engine nobody
// told which roles administer a tenant answers that nobody does, whatever a
// person holds.
func Test_tenantAdministrators_withoutTheOption(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, _ := superClient(t)
	if err := client.UserManager().AddUserRoles(ctx, tenant1Policy, "ann", "Reader"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	if got := administered(t, client, "ann"); len(got) != 0 {
		t.Errorf("TenantsAdministered() = %v on a client built without WithTenantAdministratorRoles, want none", got)
	}
	if roles := client.TenantAdministratorRoles(); len(roles) != 0 {
		t.Errorf("TenantAdministratorRoles() = %v, want none", roles)
	}
	told, _ := superClient(t, WithTenantAdministratorRoles("Reader"))
	if diff := cmp.Diff([]accesstypes.Role{"Reader"}, told.TenantAdministratorRoles()); diff != "" {
		t.Errorf("TenantAdministratorRoles() (-want +got):\n%s", diff)
	}
}
