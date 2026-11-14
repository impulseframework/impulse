package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// defaultsCollection is the release the default-role tests compile against:
// Widgets with two fields in the domain scope, Reports and the DoThing method
// in the global scope, no condition vocabulary.
type defaultsCollection struct{}

func (defaultsCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	return map[accesstypes.Permission][]accesstypes.Resource{
		"Read":    {"Widgets", "Widgets.name", "Widgets.price", "Reports"},
		"List":    {"Widgets", "Widgets.name", "Widgets.price"},
		"Update":  {"Widgets", "Widgets.name", "Widgets.price"},
		"Execute": {"DoThing"},
	}
}

func (defaultsCollection) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	base, _ := splitResourceField(string(res))
	switch base {
	case "Widgets":
		return accesstypes.DomainPermissionScope
	case "Reports", "DoThing":
		return accesstypes.GlobalPermissionScope
	default:
		return ""
	}
}

func (defaultsCollection) IsResourceImmutable(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (defaultsCollection) AttributeComparisonType(accesstypes.PermissionScope, accesstypes.Resource, string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (defaultsCollection) AttributeIsColumn(accesstypes.PermissionScope, accesstypes.Resource, string) bool {
	return false
}

func (defaultsCollection) AttributeIsList(accesstypes.PermissionScope, accesstypes.Resource, string) bool {
	return false
}

func (defaultsCollection) SubjectSetComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (defaultsCollection) SubjectValueComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (defaultsCollection) IsComputedResource(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (defaultsCollection) MethodTarget(accesstypes.PermissionScope, accesstypes.Resource) (accesstypes.Resource, bool) {
	return "", false
}

func (defaultsCollection) ConcealingKeys(accesstypes.PermissionScope, accesstypes.Resource) (order, keys []accesstypes.Tag) {
	return nil, nil
}

// readerRunnerFile declares the release's default roles the tests share: the
// global role Runner, which executes DoThing and reads Reports, and the domain
// role Reader, which reads Widgets and its name field.
func readerRunnerFile() *RoleConfig {
	return &RoleConfig{Roles: ScopedRoles{
		Global: []*Role{{
			Name:        "Runner",
			Description: "Runs the global method and reads the reports.",
			Permissions: map[accesstypes.Permission][]Grant{
				"Execute": {{Resource: "DoThing"}},
				"Read":    {{Resource: "Reports"}},
			},
		}},
		Domain: []*Role{{
			Name:        "Reader",
			Description: "Reads widgets and their names.",
			Permissions: map[accesstypes.Permission][]Grant{
				"Read": {{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}}},
			},
		}},
	}}
}

// compileDefaults compiles a role file against defaultsCollection.
func compileDefaults(t *testing.T, config *RoleConfig) *defaultRoles {
	t.Helper()
	defaults, err := compileDefaultRoles(defaultsCollection{}, config)
	if err != nil {
		t.Fatalf("compileDefaultRoles() error = %v", err)
	}

	return defaults
}

// compileWithDefaults compiles store records under the release's defaults
// and returns the snapshot with its findings.
func compileWithDefaults(t *testing.T, defaults *defaultRoles, records *policy.Records) (*snapshot, []policyFinding) {
	t.Helper()
	snap, findings, err := newSnapshot(records, defaults, defaultsCollection{}, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	return snap, findings
}

// decisionText renders a decision for comparison: granted, denied, or the
// covering condition texts.
func decisionText(d resourceDecision) string {
	switch {
	case d.granted:
		return "granted"
	case len(d.conditions) > 0:
		return "conditional " + strings.Join(d.conditions, " | ")
	default:
		return "denied"
	}
}

// Test_newSnapshot_defaultsAndStoreRowsAgree pins that a role file compiles to
// exactly what the same policy compiles to as store rows: a global default
// role is a custom role held in the global partition, a domain default role a
// custom role held in every domain. Every user, scope, permission and
// resource decides the same under both constructions.
func Test_newSnapshot_defaultsAndStoreRowsAgree(t *testing.T) {
	t.Parallel()

	memberships := []policy.Membership{
		{Scope: globalPolicy, Member: userSubject("erin"), Role: "Runner"},
		{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Reader"},
		{Scope: tenant1Policy, Member: userSubject("frank"), Role: "Reader"},
		{Scope: globalPolicy, Member: userSubject("gale"), Role: "Reader"},
	}
	fromFile, _ := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), &policy.Records{Memberships: memberships})
	fromRows, _ := compileWithDefaults(t, nil, &policy.Records{
		Roles: []policy.Role{{Scope: globalPolicy, Name: "Runner"}, {Scope: everyDomainPolicy, Name: "Reader"}},
		Grants: []policy.Grant{
			{Scope: globalPolicy, Subject: roleSubject("Runner"), Perm: "Execute", Resource: "DoThing"},
			{Scope: globalPolicy, Subject: roleSubject("Runner"), Perm: "Read", Resource: "Reports"},
			{Scope: everyDomainPolicy, Subject: roleSubject("Reader"), Perm: "Read", Resource: "Widgets"},
			{Scope: everyDomainPolicy, Subject: roleSubject("Reader"), Perm: "Read", Resource: "Widgets", Field: "name"},
		},
		Memberships: memberships,
	})

	scopes := []accesstypes.Scope{accesstypes.GlobalScope(), tenant1Scope, tenant2Scope, accesstypes.DomainScope("never-seen")}
	resources := []accesstypes.Resource{"Widgets", "Widgets.name", "Widgets.price", "Reports", "DoThing"}
	for _, user := range []accesstypes.User{"erin", "frank", "gale", "nobody"} {
		for _, scope := range scopes {
			for _, perm := range []accesstypes.Permission{"Read", "Execute", "List"} {
				a := fromFile.decideUserResources(user, scope, perm, resources...)
				b := fromRows.decideUserResources(user, scope, perm, resources...)
				for i, res := range resources {
					if got, want := decisionText(a[i]), decisionText(b[i]); got != want {
						t.Errorf("%s %s on %s in %s: from the file = %s, from rows = %s", user, perm, res, scope, got, want)
					}
				}
			}
			if got, want := fromFile.userHasGrants(scope, user), fromRows.userHasGrants(scope, user); got != want {
				t.Errorf("userHasGrants(%s, %s): from the file = %v, from rows = %v", scope, user, got, want)
			}
			if diff := cmp.Diff(fromRows.userPermissions(scope, user), fromFile.userPermissions(scope, user)); diff != "" {
				t.Errorf("userPermissions(%s, %s) from the file differs from rows (-rows +file):\n%s", scope, user, diff)
			}
		}
	}
	for _, role := range []accesstypes.Role{"Runner", "Reader", "Ghost"} {
		for _, scope := range scopes {
			if diff := cmp.Diff(fromRows.rolePermissions(scope, role), fromFile.rolePermissions(scope, role)); diff != "" {
				t.Errorf("rolePermissions(%s, %s) from the file differs from rows (-rows +file):\n%s", scope, role, diff)
			}
		}
	}
}

// Test_snapshot_everyDomainMembership pins that a membership held in every
// domain reaches every tenant, including one the store holds no row in, and
// never the global partition; and that a membership held in one domain
// reaches that domain alone. The domain default's grants are shared: no
// per-domain copy exists for a domain the store has no rows in.
func Test_snapshot_everyDomainMembership(t *testing.T) {
	t.Parallel()

	snap, findings := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), &policy.Records{
		Memberships: []policy.Membership{
			{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Reader"},
			{Scope: tenant1Policy, Member: userSubject("frank"), Role: "Reader"},
			{Scope: globalPolicy, Member: userSubject("gale"), Role: "Runner"},
		},
	})
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
	if len(snap.domains) != 1 {
		t.Errorf("compiled %d domain overlays, want 1 (tenant1, the only domain with rows)", len(snap.domains))
	}

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		perm  accesstypes.Permission
		res   accesstypes.Resource
		want  string
	}{
		{name: "every-domain membership reaches a tenant with rows", user: "erin", scope: tenant1Scope, perm: "Read", res: "Widgets.name", want: "granted"},
		{name: "every-domain membership reaches a tenant without rows", user: "erin", scope: accesstypes.DomainScope("never-seen"), perm: "Read", res: "Widgets.name", want: "granted"},
		{name: "every-domain membership does not reach the global partition", user: "erin", scope: accesstypes.GlobalScope(), perm: "Read", res: "Widgets.name", want: "denied"},
		{name: "a field the role does not name stays denied", user: "erin", scope: tenant2Scope, perm: "Read", res: "Widgets.price", want: "denied"},
		{name: "one-domain membership reaches its domain", user: "frank", scope: tenant1Scope, perm: "Read", res: "Widgets", want: "granted"},
		{name: "one-domain membership does not reach another domain", user: "frank", scope: tenant2Scope, perm: "Read", res: "Widgets", want: "denied"},
		{name: "global membership reaches the global partition", user: "gale", scope: accesstypes.GlobalScope(), perm: "Execute", res: "DoThing", want: "granted"},
		{name: "global membership does not reach a tenant", user: "gale", scope: tenant1Scope, perm: "Execute", res: "DoThing", want: "denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideUserResources(tt.user, tt.scope, tt.perm, tt.res)
			if decisionText(got[0]) != tt.want {
				t.Errorf("decide(%s, %s, %s, %s) = %s, want %s", tt.user, tt.scope, tt.perm, tt.res, decisionText(got[0]), tt.want)
			}
		})
	}

	footholds := []struct {
		name  string
		scope accesstypes.Scope
		user  accesstypes.User
		role  accesstypes.Role
		want  bool
	}{
		{name: "every-domain member has a foothold in an unseen tenant", scope: accesstypes.DomainScope("never-seen"), user: "erin", role: "Reader", want: true},
		{name: "a domain default has a foothold in every tenant", scope: accesstypes.DomainScope("other"), user: "nobody", role: "Reader", want: true},
		{name: "a domain default has none in the global partition", scope: accesstypes.GlobalScope(), user: "erin", role: "Reader", want: false},
		{name: "a global default has a foothold in the global partition", scope: accesstypes.GlobalScope(), user: "gale", role: "Runner", want: true},
	}
	for _, tt := range footholds {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := snap.roleHasGrants(tt.scope, tt.role); got != tt.want {
				t.Errorf("roleHasGrants(%s, %s) = %v, want %v", tt.scope, tt.role, got, tt.want)
			}
			if tt.user == "nobody" {
				return
			}
			if got := snap.userHasGrants(tt.scope, tt.user); got != tt.want {
				t.Errorf("userHasGrants(%s, %s) = %v, want %v", tt.scope, tt.user, got, tt.want)
			}
		})
	}
}

// Test_snapshot_domainOverlay pins how a domain's own rows layer over what
// reaches every domain: a custom role held in every domain is held in each
// tenant; the same custom role's rows in one tenant add to it there alone; a
// custom role held in one tenant exists there alone; a user's every-domain
// memberships and one-domain memberships combine in that domain; and a user
// the domain's rows never touch resolves exactly as every-domain policy says.
func Test_snapshot_domainOverlay(t *testing.T) {
	t.Parallel()

	snap, findings := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), &policy.Records{
		Roles: []policy.Role{
			{Scope: everyDomainPolicy, Name: "Pricer"},
			{Scope: tenant1Policy, Name: "Pricer"},
			{Scope: tenant2Policy, Name: "Local"},
		},
		Grants: []policy.Grant{
			{Scope: everyDomainPolicy, Subject: roleSubject("Pricer"), Perm: "Read", Resource: "Widgets", Field: "price"},
			{Scope: tenant1Policy, Subject: roleSubject("Pricer"), Perm: "Update", Resource: "Widgets", Field: "name"},
			{Scope: tenant2Policy, Subject: roleSubject("Local"), Perm: "List", Resource: "Widgets"},
			{Scope: tenant2Policy, Subject: userSubject("hank"), Perm: "List", Resource: "Widgets", Field: "name", Condition: "price < 10"},
		},
		Memberships: []policy.Membership{
			{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Reader"},
			{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Pricer"},
			{Scope: tenant2Policy, Member: userSubject("frank"), Role: "Local"},
			{Scope: everyDomainPolicy, Member: userSubject("frank"), Role: "Reader"},
			{Scope: tenant1Policy, Member: userSubject("gina"), Role: "Reader"},
			{Scope: tenant2Policy, Member: userSubject("gina"), Role: "Pricer"},
		},
	})
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
	if got := len(snap.domains); got != 2 {
		t.Errorf("compiled %d domain overlays, want 2", got)
	}

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		perm  accesstypes.Permission
		res   accesstypes.Resource
		want  string
	}{
		{name: "every-domain default reaches tenant1", user: "erin", scope: tenant1Scope, perm: "Read", res: "Widgets.name", want: "granted"},
		{name: "every-domain custom role reaches tenant1", user: "erin", scope: tenant1Scope, perm: "Read", res: "Widgets.price", want: "granted"},
		{name: "the custom role's tenant1 rows add in tenant1", user: "erin", scope: tenant1Scope, perm: "Update", res: "Widgets.name", want: "granted"},
		{name: "the custom role's tenant1 rows do not add in tenant2", user: "erin", scope: tenant2Scope, perm: "Update", res: "Widgets.name", want: "denied"},
		{name: "every-domain custom role reaches an unseen tenant", user: "erin", scope: accesstypes.DomainScope("never-seen"), perm: "Read", res: "Widgets.price", want: "granted"},
		{name: "one-domain custom role reaches its tenant", user: "frank", scope: tenant2Scope, perm: "List", res: "Widgets", want: "granted"},
		{name: "one-domain custom role does not reach another tenant", user: "frank", scope: tenant1Scope, perm: "List", res: "Widgets", want: "denied"},
		{name: "every-domain and one-domain memberships combine", user: "frank", scope: tenant2Scope, perm: "Read", res: "Widgets.name", want: "granted"},
		{name: "a one-domain default membership reaches its tenant", user: "gina", scope: tenant1Scope, perm: "Read", res: "Widgets.name", want: "granted"},
		{name: "a one-domain default membership does not reach another", user: "gina", scope: tenant2Scope, perm: "Read", res: "Widgets.name", want: "denied"},
		{name: "a one-domain membership of an every-domain custom role", user: "gina", scope: tenant2Scope, perm: "Read", res: "Widgets.price", want: "granted"},
		{name: "a direct grant in one domain carries its condition", user: "hank", scope: tenant2Scope, perm: "List", res: "Widgets.name", want: "conditional price < 10"},
		{name: "a direct grant in one domain stays there", user: "hank", scope: tenant1Scope, perm: "List", res: "Widgets.name", want: "denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideUserResources(tt.user, tt.scope, tt.perm, tt.res)
			if decisionText(got[0]) != tt.want {
				t.Errorf("decide(%s, %s, %s, %s) = %s, want %s", tt.user, tt.scope, tt.perm, tt.res, decisionText(got[0]), tt.want)
			}
		})
	}

	// The role answers follow the same layering.
	roles := []struct {
		name  string
		role  accesstypes.Role
		scope accesstypes.Scope
		perm  accesstypes.Permission
		res   accesstypes.Resource
		want  string
	}{
		{name: "Pricer in tenant1 holds both layers", role: "Pricer", scope: tenant1Scope, perm: "Update", res: "Widgets.name", want: "granted"},
		{name: "Pricer in tenant2 holds the every-domain layer", role: "Pricer", scope: tenant2Scope, perm: "Read", res: "Widgets.price", want: "granted"},
		{name: "Pricer in tenant2 lacks tenant1's rows", role: "Pricer", scope: tenant2Scope, perm: "Update", res: "Widgets.name", want: "denied"},
		{name: "Local exists in tenant2 alone", role: "Local", scope: tenant1Scope, perm: "List", res: "Widgets", want: "denied"},
	}
	for _, tt := range roles {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideRoleResources(tt.role, tt.scope, tt.perm, tt.res)
			if decisionText(got[0]) != tt.want {
				t.Errorf("decideRole(%s, %s, %s, %s) = %s, want %s", tt.role, tt.scope, tt.perm, tt.res, decisionText(got[0]), tt.want)
			}
		})
	}
}

// Test_newSnapshot_findings pins what a load reports about the store: a custom
// role whose name a default of its kind has is shadowed — its grants are
// skipped and its members hold the default's grants; memberships naming a
// role neither the release nor the store defines, as seen from where they
// are held, are orphaned — reported once per (scope, role) with the members
// sorted, and granting nothing: a global membership of a domain default, a
// membership of a role nothing defines, and a membership held in every domain
// of a custom role held in one domain alone, which does not reach even that
// domain; and the findings come out sorted by kind then text. Each finding is
// an error for the reload hook and a Warning for the deploy check.
func Test_newSnapshot_findings(t *testing.T) {
	t.Parallel()

	snap, findings := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), &policy.Records{
		Roles: []policy.Role{
			{Scope: tenant1Policy, Name: "Reader"},
			{Scope: tenant2Policy, Name: "Local"},
		},
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("Reader"), Perm: "Update", Resource: "Widgets"},
			{Scope: tenant2Policy, Subject: roleSubject("Local"), Perm: "List", Resource: "Widgets"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: userSubject("erin"), Role: "Reader"},
			{Scope: globalPolicy, Member: userSubject("gale"), Role: "Reader"},
			{Scope: tenant1Policy, Member: userSubject("ivy"), Role: "Ghost"},
			{Scope: tenant1Policy, Member: userSubject("hank"), Role: "Ghost"},
			{Scope: everyDomainPolicy, Member: userSubject("jo"), Role: "Local"},
			{Scope: globalPolicy, Member: userSubject("kim"), Role: "Runner"},
		},
	})

	want := []policyFinding{
		&ShadowedRole{Scope: tenant1Policy, Role: "Reader"},
		&OrphanedMembership{Scope: globalPolicy, Role: "Reader", Users: []accesstypes.User{"gale"}},
		&OrphanedMembership{Scope: tenant1Policy, Role: "Ghost", Users: []accesstypes.User{"hank", "ivy"}},
		&OrphanedMembership{Scope: everyDomainPolicy, Role: "Local", Users: []accesstypes.User{"jo"}},
	}
	sortFindings(want)
	if diff := cmp.Diff(want, findings, cmpopts.EquateComparable(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("findings (-want +got):\n%s", diff)
	}
	for _, f := range findings {
		var asError error = f
		var shadowed *ShadowedRole
		var orphaned *OrphanedMembership
		if !errors.As(asError, &shadowed) && !errors.As(asError, &orphaned) {
			t.Errorf("finding %T is not reachable through errors.As", f)
		}
		if f.String() != asError.Error() {
			t.Errorf("finding prints %q as a Warning and %q as an error, want the same line", f.String(), asError.Error())
		}
	}

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		perm  accesstypes.Permission
		res   accesstypes.Resource
		want  string
	}{
		{name: "the shadowed role's member holds the default's grants", user: "erin", scope: tenant1Scope, perm: "Read", res: "Widgets.name", want: "granted"},
		{name: "the shadowed role's own grants are skipped", user: "erin", scope: tenant1Scope, perm: "Update", res: "Widgets", want: "denied"},
		{name: "a global membership of a domain default grants nothing", user: "gale", scope: accesstypes.GlobalScope(), perm: "Read", res: "Widgets", want: "denied"},
		{name: "an orphaned membership grants nothing", user: "ivy", scope: tenant1Scope, perm: "Read", res: "Widgets", want: "denied"},
		{name: "an every-domain membership of a one-domain role grants nothing there", user: "jo", scope: tenant2Scope, perm: "List", res: "Widgets", want: "denied"},
		{name: "a resolved membership is unaffected", user: "kim", scope: accesstypes.GlobalScope(), perm: "Execute", res: "DoThing", want: "granted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideUserResources(tt.user, tt.scope, tt.perm, tt.res)
			if decisionText(got[0]) != tt.want {
				t.Errorf("decide(%s, %s, %s, %s) = %s, want %s", tt.user, tt.scope, tt.perm, tt.res, decisionText(got[0]), tt.want)
			}
		})
	}
}

// Test_snapshot_thousandDomains pins the scale the layering is for: a
// thousand tenants, each with a custom role and a member of its own, compile
// to a thousand overlays, while the release's domain defaults and the
// every-domain memberships are compiled once and reach every tenant and any
// tenant beyond the thousand.
func Test_snapshot_thousandDomains(t *testing.T) {
	t.Parallel()

	const n = 1000
	records := &policy.Records{
		Memberships: []policy.Membership{{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Reader"}},
	}
	for i := range n {
		scope := accesstypes.DomainPolicyScope(accesstypes.Domain(fmt.Sprintf("tenant-%04d", i)))
		role := accesstypes.Role(fmt.Sprintf("Local-%04d", i))
		records.Roles = append(records.Roles, policy.Role{Scope: scope, Name: role})
		records.Grants = append(records.Grants, policy.Grant{Scope: scope, Subject: roleSubject(string(role)), Perm: "List", Resource: "Widgets"})
		records.Memberships = append(records.Memberships, policy.Membership{Scope: scope, Member: userSubject(fmt.Sprintf("user-%04d", i)), Role: role})
	}

	started := time.Now()
	snap, findings := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), records)
	t.Logf("compiled %d domains in %s", n, time.Since(started))
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want none", len(findings))
	}
	if got := len(snap.domains); got != n {
		t.Errorf("compiled %d domain overlays, want %d", got, n)
	}

	// The default's grant map is one object shared by every tenant: the
	// every-domain entry and the unseen tenant's answer are the same map.
	shared := snap.roleGrants(accesstypes.DomainScope("beyond"), "Reader")
	if len(shared) == 0 {
		t.Fatal("Reader holds nothing in a tenant beyond the thousand")
	}
	for i := 0; i < n; i += 97 {
		domain := accesstypes.DomainScope(accesstypes.Domain(fmt.Sprintf("tenant-%04d", i)))
		user := accesstypes.User(fmt.Sprintf("user-%04d", i))
		other := accesstypes.DomainScope(accesstypes.Domain(fmt.Sprintf("tenant-%04d", (i+1)%n)))
		if got := snap.decideUserResources("erin", domain, "Read", "Widgets.name"); !got[0].granted {
			t.Errorf("erin in %s: Read Widgets.name = %s, want granted", domain, decisionText(got[0]))
		}
		if got := snap.decideUserResources(user, domain, "List", "Widgets"); !got[0].granted {
			t.Errorf("%s in %s: List Widgets = %s, want granted", user, domain, decisionText(got[0]))
		}
		if got := snap.decideUserResources(user, other, "List", "Widgets"); got[0].granted {
			t.Errorf("%s in %s: List Widgets granted, want denied", user, other)
		}
		if got := snap.roleGrants(domain, "Reader"); len(got) != len(shared) {
			t.Errorf("Reader in %s holds %d entries, want the shared %d", domain, len(got), len(shared))
		}
	}
	if got := snap.decideUserResources("erin", accesstypes.DomainScope("beyond"), "Read", "Widgets.name"); !got[0].granted {
		t.Errorf("erin in a tenant beyond the thousand: Read Widgets.name = %s, want granted", decisionText(got[0]))
	}
}

// Test_snapshot_permissionListings pins the management listings answered from
// the snapshot: a user's effective permissions by name — scope-wide, per
// resource, all-fields grants as Resource.* and field grants as
// Resource.field, a conditional grant counted as held, each list sorted — and
// a role's grants in the same shape, inheritance and layering folded.
func Test_snapshot_permissionListings(t *testing.T) {
	t.Parallel()

	snap, _ := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), &policy.Records{
		Roles: []policy.Role{{Scope: everyDomainPolicy, Name: "Pricer"}, {Scope: globalPolicy, Name: "Exporter"}},
		Grants: []policy.Grant{
			{Scope: everyDomainPolicy, Subject: roleSubject("Pricer"), Perm: "Update", Resource: "Widgets", Field: "*"},
			{Scope: everyDomainPolicy, Subject: roleSubject("Pricer"), Perm: "List", Resource: "Widgets", Field: "price", Condition: "price < 10"},
			{Scope: globalPolicy, Subject: roleSubject("Exporter"), Perm: "Export", Resource: ""},
			{Scope: globalPolicy, Subject: roleSubject("Exporter"), Perm: "Approve", Resource: "", Condition: "now < '2030-01-01T00:00:00Z'"},
			{Scope: tenant1Policy, Subject: userSubject("erin"), Perm: "List", Resource: "Widgets"},
		},
		Memberships: []policy.Membership{
			{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Reader"},
			{Scope: everyDomainPolicy, Member: userSubject("erin"), Role: "Pricer"},
			{Scope: globalPolicy, Member: userSubject("erin"), Role: "Exporter"},
			{Scope: globalPolicy, Member: userSubject("erin"), Role: "Runner"},
		},
	})

	users := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		want  accesstypes.UserScopePermissions
	}{
		{
			name:  "a tenant lists defaults, custom roles and the tenant's direct grant",
			user:  "erin",
			scope: tenant1Scope,
			want: accesstypes.UserScopePermissions{Resources: map[accesstypes.Resource][]accesstypes.Permission{
				"Widgets":       {"List", "Read"},
				"Widgets.*":     {"Update"},
				"Widgets.name":  {"Read"},
				"Widgets.price": {"List"},
			}},
		},
		{
			name:  "another tenant lacks the direct grant",
			user:  "erin",
			scope: tenant2Scope,
			want: accesstypes.UserScopePermissions{Resources: map[accesstypes.Resource][]accesstypes.Permission{
				"Widgets":       {"Read"},
				"Widgets.*":     {"Update"},
				"Widgets.name":  {"Read"},
				"Widgets.price": {"List"},
			}},
		},
		{
			name:  "the global partition lists scope-wide grants sorted, conditional ones counted",
			user:  "erin",
			scope: accesstypes.GlobalScope(),
			want: accesstypes.UserScopePermissions{
				ScopeWide: []accesstypes.Permission{"Approve", "Export"},
				Resources: map[accesstypes.Resource][]accesstypes.Permission{"DoThing": {"Execute"}, "Reports": {"Read"}},
			},
		},
		{
			name:  "an unknown user holds nothing",
			user:  "nobody",
			scope: tenant1Scope,
			want:  accesstypes.UserScopePermissions{Resources: map[accesstypes.Resource][]accesstypes.Permission{}},
		},
	}
	for _, tt := range users {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, snap.userPermissions(tt.scope, tt.user)); diff != "" {
				t.Errorf("userPermissions(%s, %s) (-want +got):\n%s", tt.scope, tt.user, diff)
			}
		})
	}

	roles := []struct {
		name  string
		role  accesstypes.Role
		scope accesstypes.Scope
		want  accesstypes.RolePermissionCollection
	}{
		{
			name:  "a domain default answers from the file in any tenant",
			role:  "Reader",
			scope: accesstypes.DomainScope("never-seen"),
			want:  accesstypes.RolePermissionCollection{"Read": {Resources: []accesstypes.Resource{"Widgets", "Widgets.name"}}},
		},
		{
			name:  "a custom role answers its rows",
			role:  "Pricer",
			scope: tenant1Scope,
			want: accesstypes.RolePermissionCollection{
				"List":   {Resources: []accesstypes.Resource{"Widgets.price"}},
				"Update": {Resources: []accesstypes.Resource{"Widgets.*"}},
			},
		},
		{
			name:  "scope-wide grants mark the permission scope-wide",
			role:  "Exporter",
			scope: accesstypes.GlobalScope(),
			want:  accesstypes.RolePermissionCollection{"Approve": {ScopeWide: true}, "Export": {ScopeWide: true}},
		},
		{
			name:  "a domain default holds nothing in the global partition",
			role:  "Reader",
			scope: accesstypes.GlobalScope(),
			want:  accesstypes.RolePermissionCollection{},
		},
	}
	for _, tt := range roles {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, snap.rolePermissions(tt.scope, tt.role)); diff != "" {
				t.Errorf("rolePermissions(%s, %s) (-want +got):\n%s", tt.scope, tt.role, diff)
			}
		})
	}
}

// Test_snapshot_overlayInheritance pins that a role-to-role membership held
// in one domain folds the parent's every-domain grants into the member role
// there: inheritance edges, like grants, layer over what reaches every
// domain.
func Test_snapshot_overlayInheritance(t *testing.T) {
	t.Parallel()

	snap, _ := compileWithDefaults(t, compileDefaults(t, readerRunnerFile()), &policy.Records{
		Roles: []policy.Role{{Scope: everyDomainPolicy, Name: "Pricer"}, {Scope: tenant1Policy, Name: "Senior"}},
		Grants: []policy.Grant{
			{Scope: everyDomainPolicy, Subject: roleSubject("Pricer"), Perm: "Read", Resource: "Widgets", Field: "price"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: roleSubject("Senior"), Role: "Pricer"},
			{Scope: tenant1Policy, Member: roleSubject("Senior"), Role: "Reader"},
			{Scope: tenant1Policy, Member: userSubject("erin"), Role: "Senior"},
		},
	})

	want := accesstypes.RolePermissionCollection{"Read": {Resources: []accesstypes.Resource{"Widgets", "Widgets.name", "Widgets.price"}}}
	if diff := cmp.Diff(want, snap.rolePermissions(tenant1Scope, "Senior")); diff != "" {
		t.Errorf("rolePermissions(tenant1, Senior) (-want +got):\n%s", diff)
	}
	if got := snap.decideUserResources("erin", tenant1Scope, "Read", "Widgets.price", "Widgets.name"); !got[0].granted || !got[1].granted {
		t.Errorf("erin through Senior in tenant1 = %s, %s; want both granted", decisionText(got[0]), decisionText(got[1]))
	}
	if got := snap.decideUserResources("erin", tenant2Scope, "Read", "Widgets.price"); got[0].granted {
		t.Error("erin holds Senior's grants in tenant2, want the edge to stay in tenant1")
	}
	if got := slices.Sorted(func(yield func(accesstypes.Role) bool) {
		for r := range snap.domains["tenant1"].roleGrants {
			if !yield(r) {
				return
			}
		}
	}); !slices.Equal(got, []accesstypes.Role{"Senior"}) {
		t.Errorf("tenant1 overlays roles %v, want only Senior (the roles its rows change)", got)
	}
}
