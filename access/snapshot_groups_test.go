package access

import (
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// groupSubject is a fixture shorthand for a group that holds a role.
func groupSubject(id accesstypes.Group) policy.Subject {
	return policy.Subject{Kind: policy.SubjectGroup, Name: string(id)}
}

// groupFixture is the records the group tests start from: the custom role
// Editor reads employees in tenant1 and Auditor lists widgets there, and the
// group "night" is kept in tenant1 with no member and no role.
func groupFixture() *policy.Records {
	return &policy.Records{
		Roles: []policy.Role{{Scope: tenant1Policy, Name: "Editor"}, {Scope: tenant1Policy, Name: "Auditor"}},
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: tenant1Policy, Subject: roleSubject("Auditor"), Perm: "List", Resource: "widgets"},
		},
		Groups: []policy.Group{{ID: "night", Scope: tenant1Policy}},
	}
}

// holds reports whether the snapshot grants user perm on resource in scope.
func holds(snap *snapshot, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resource accesstypes.Resource) bool {
	return snap.decideUserResources(user, scope, perm, resource)[0].granted
}

// Test_snapshot_groups pins what a group gives its members: a member gains the
// group's roles and loses them on leaving, a group's role change reaches every
// member, a group whose rows are gone gives nothing, and a role held both by a
// person's own membership and through a group is held once, and stays while
// either holds it.
func Test_snapshot_groups(t *testing.T) {
	t.Parallel()

	member := func(user accesstypes.User) policy.GroupMember {
		return policy.GroupMember{Group: "night", User: user}
	}
	groupRole := func(role accesstypes.Role) policy.Membership {
		return policy.Membership{Scope: tenant1Policy, Member: groupSubject("night"), Role: role}
	}
	own := func(user accesstypes.User, role accesstypes.Role) policy.Membership {
		return policy.Membership{Scope: tenant1Policy, Member: userSubject(string(user)), Role: role}
	}

	type want struct {
		user     accesstypes.User
		perm     accesstypes.Permission
		resource accesstypes.Resource
		granted  bool
	}
	tests := []struct {
		name    string
		prepare func(r *policy.Records)
		want    []want
	}{
		{
			name: "a member holds the group's role and a person outside the group does not",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("ann")}
				r.Memberships = []policy.Membership{groupRole("Editor")}
			},
			want: []want{{"ann", "Read", "employees", true}, {"bob", "Read", "employees", false}},
		},
		{
			name: "a person who left the group no longer holds its role",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("bob")}
				r.Memberships = []policy.Membership{groupRole("Editor")}
			},
			want: []want{{"ann", "Read", "employees", false}, {"bob", "Read", "employees", true}},
		},
		{
			name: "a role given to the group reaches every member",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("ann"), member("bob")}
				r.Memberships = []policy.Membership{groupRole("Editor"), groupRole("Auditor")}
			},
			want: []want{
				{"ann", "Read", "employees", true},
				{"bob", "Read", "employees", true},
				{"ann", "List", "widgets", true},
				{"bob", "List", "widgets", true},
			},
		},
		{
			name: "a role taken from the group is lost by every member",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("ann"), member("bob")}
				r.Memberships = []policy.Membership{groupRole("Auditor")}
			},
			want: []want{
				{"ann", "Read", "employees", false},
				{"bob", "Read", "employees", false},
				{"ann", "List", "widgets", true},
				{"bob", "List", "widgets", true},
			},
		},
		{
			name: "a deleted group takes its roles with it",
			prepare: func(r *policy.Records) {
				r.Groups = nil
				r.GroupMembers = []policy.GroupMember{member("ann")}
				r.Memberships = []policy.Membership{groupRole("Editor")}
			},
			want: []want{{"ann", "Read", "employees", false}},
		},
		{
			name: "a role held by a person's own membership and through the group is held",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("ann")}
				r.Memberships = []policy.Membership{own("ann", "Editor"), groupRole("Editor")}
			},
			want: []want{{"ann", "Read", "employees", true}},
		},
		{
			name: "the person's own membership stands when the group loses the role",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("ann")}
				r.Memberships = []policy.Membership{own("ann", "Editor")}
			},
			want: []want{{"ann", "Read", "employees", true}},
		},
		{
			name: "the group's role stands when the person's own membership goes",
			prepare: func(r *policy.Records) {
				r.GroupMembers = []policy.GroupMember{member("ann")}
				r.Memberships = []policy.Membership{groupRole("Editor")}
			},
			want: []want{{"ann", "Read", "employees", true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			records := groupFixture()
			tt.prepare(records)
			snap := compileSnapshot(t, records)
			for _, w := range tt.want {
				if got := holds(snap, w.user, tenant1Scope, w.perm, w.resource); got != w.granted {
					t.Errorf("%s %s on %s granted = %v, want %v", w.user, w.perm, w.resource, got, w.granted)
				}
			}
		})
	}
}

// Test_snapshot_groups_heldOnce pins that a role a person holds by their own
// membership and through a group is one role in the compiled policy: the
// person's roles list it once, so two people with the same roles share one
// compiled grant set whichever way each holds them.
func Test_snapshot_groups_heldOnce(t *testing.T) {
	t.Parallel()

	records := groupFixture()
	records.GroupMembers = []policy.GroupMember{{Group: "night", User: "ann"}}
	records.Memberships = []policy.Membership{
		{Scope: tenant1Policy, Member: userSubject("ann"), Role: "Editor"},
		{Scope: tenant1Policy, Member: groupSubject("night"), Role: "Editor"},
		{Scope: tenant1Policy, Member: userSubject("bob"), Role: "Editor"},
	}
	snap := compileSnapshot(t, records)

	overlay := snap.domains["tenant1"]
	if diff := cmp.Diff([]accesstypes.Role{"Editor"}, overlay.userRoles["ann"]); diff != "" {
		t.Errorf("ann's roles in tenant1 (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(snap.userPermissions(tenant1Scope, "bob"), snap.userPermissions(tenant1Scope, "ann")); diff != "" {
		t.Errorf("ann's permissions differ from bob's, who holds the role once (-bob +ann):\n%s", diff)
	}
}

// Test_snapshot_groups_places pins where a group's role reaches: a group kept
// for the whole application gives its members a role globally, in one tenant or
// in every tenant, as a person holds one; a group kept in one tenant gives its
// roles in that tenant alone, and a row that says otherwise is skipped and
// reported.
func Test_snapshot_groups_places(t *testing.T) {
	t.Parallel()

	records := &policy.Records{
		Roles: []policy.Role{
			{Scope: globalPolicy, Name: "Operator"},
			{Scope: everyDomainPolicy, Name: "Viewer"},
			{Scope: tenant1Policy, Name: "Editor"},
			{Scope: tenant2Policy, Name: "Editor"},
		},
		Grants: []policy.Grant{
			{Scope: globalPolicy, Subject: roleSubject("Operator"), Perm: "Execute", Resource: "Reindex"},
			{Scope: everyDomainPolicy, Subject: roleSubject("Viewer"), Perm: "List", Resource: "widgets"},
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: tenant2Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
		},
		Groups:       []policy.Group{{ID: "staff", Scope: globalPolicy}, {ID: "night", Scope: tenant1Policy}},
		GroupMembers: []policy.GroupMember{{Group: "staff", User: "sam"}, {Group: "night", User: "ann"}},
		Memberships: []policy.Membership{
			{Scope: globalPolicy, Member: groupSubject("staff"), Role: "Operator"},
			{Scope: everyDomainPolicy, Member: groupSubject("staff"), Role: "Viewer"},
			{Scope: tenant2Policy, Member: groupSubject("staff"), Role: "Editor"},
			{Scope: tenant1Policy, Member: groupSubject("night"), Role: "Editor"},
			// Rows no management call writes: a tenant's group given a role in
			// another tenant, in every tenant and for the whole application.
			{Scope: tenant2Policy, Member: groupSubject("night"), Role: "Editor"},
			{Scope: everyDomainPolicy, Member: groupSubject("night"), Role: "Viewer"},
			{Scope: globalPolicy, Member: groupSubject("night"), Role: "Operator"},
		},
	}
	snap, findings, err := newSnapshot(records, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	global := accesstypes.GlobalScope()
	checks := []struct {
		name     string
		user     accesstypes.User
		scope    accesstypes.Scope
		perm     accesstypes.Permission
		resource accesstypes.Resource
		want     bool
	}{
		{"a global group's global role", "sam", global, "Execute", "Reindex", true},
		{"a global group's role in every tenant, in tenant1", "sam", tenant1Scope, "List", "widgets", true},
		{"a global group's role in every tenant, in tenant2", "sam", tenant2Scope, "List", "widgets", true},
		{"a global group's role in one tenant, there", "sam", tenant2Scope, "Read", "employees", true},
		{"a global group's role in one tenant, elsewhere", "sam", tenant1Scope, "Read", "employees", false},
		{"a tenant's group in its own tenant", "ann", tenant1Scope, "Read", "employees", true},
		{"a tenant's group in another tenant", "ann", tenant2Scope, "Read", "employees", false},
		{"a tenant's group in every tenant", "ann", tenant2Scope, "List", "widgets", false},
		{"a tenant's group in every tenant, at home", "ann", tenant1Scope, "List", "widgets", false},
		{"a tenant's group for the whole application", "ann", global, "Execute", "Reindex", false},
	}
	for _, c := range checks {
		if got := holds(snap, c.user, c.scope, c.perm, c.resource); got != c.want {
			t.Errorf("%s: granted = %v, want %v", c.name, got, c.want)
		}
	}

	var skipped []string
	for _, f := range findings {
		var s *SkippedGroupRole
		if !errors.As(f, &s) {
			t.Errorf("finding %v, want only skipped group roles", f)

			continue
		}
		if s.Group != "night" || !strings.Contains(s.Reason, "kept in the tenant tenant1") {
			t.Errorf("finding %v, want the tenant's group named with why", s)
		}
		skipped = append(skipped, s.Scope.String()+" "+string(s.Role))
	}
	if len(skipped) != 3 {
		t.Errorf("skipped %v, want the three rows held outside the group's tenant", skipped)
	}
}

// Test_snapshot_groups_superAdministrator pins that no group makes a super
// administrator: a row that gives a group the role is skipped and reported, its
// members are no super administrators and hold nothing of the role's, and a
// person's own membership still does.
func Test_snapshot_groups_superAdministrator(t *testing.T) {
	t.Parallel()

	defaults, err := compileDefaultRoles(grammarCollection{}, &RoleConfig{}, []accesstypes.Resource{"DoThing"})
	if err != nil {
		t.Fatalf("compileDefaultRoles() error = %v", err)
	}
	records := &policy.Records{
		Groups:       []policy.Group{{ID: "staff", Scope: globalPolicy}},
		GroupMembers: []policy.GroupMember{{Group: "staff", User: "mallory"}},
		Memberships: []policy.Membership{
			{Scope: globalPolicy, Member: groupSubject("staff"), Role: accesstypes.SuperAdministrator},
			{Scope: globalPolicy, Member: userSubject("root"), Role: accesstypes.SuperAdministrator},
		},
	}
	snap, findings, err := newSnapshot(records, defaults, grammarCollection{}, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	if snap.userIsSuperAdministrator("mallory") {
		t.Error("a member of a group that was given the Super Administrator role is a super administrator, want not")
	}
	if holds(snap, "mallory", accesstypes.GlobalScope(), "Execute", "DoThing") {
		t.Error("a member of the group executes the method the role holds, want nothing through the group")
	}
	if !snap.userIsSuperAdministrator("root") || !holds(snap, "root", accesstypes.GlobalScope(), "Execute", "DoThing") {
		t.Error("the person who holds the role by their own membership lost it")
	}
	var skipped *SkippedGroupRole
	if len(findings) != 1 || !errors.As(findings[0], &skipped) || skipped.Role != accesstypes.SuperAdministrator || skipped.Group != "staff" {
		t.Errorf("findings = %v, want the group's Super Administrator row reported", findings)
	}
}

// Test_Records_Hash_groups pins that a change to a group's members or to where
// a group is kept changes the records' hash, so the engine recompiles for it,
// and that row order does not.
func Test_Records_Hash_groups(t *testing.T) {
	t.Parallel()

	base := func() *policy.Records {
		r := groupFixture()
		r.Groups = append(r.Groups, policy.Group{ID: "staff", Scope: globalPolicy})
		r.GroupMembers = []policy.GroupMember{{Group: "night", User: "ann"}, {Group: "staff", User: "sam"}}
		r.Memberships = []policy.Membership{{Scope: tenant1Policy, Member: groupSubject("night"), Role: "Editor"}}

		return r
	}
	want := base().Hash()

	reordered := base()
	reordered.Groups[0], reordered.Groups[1] = reordered.Groups[1], reordered.Groups[0]
	reordered.GroupMembers[0], reordered.GroupMembers[1] = reordered.GroupMembers[1], reordered.GroupMembers[0]
	if reordered.Hash() != want {
		t.Error("the hash changed with the order of the rows")
	}

	changes := map[string]func(r *policy.Records){
		"a member added": func(r *policy.Records) {
			r.GroupMembers = append(r.GroupMembers, policy.GroupMember{Group: "night", User: "bob"})
		},
		"a member removed": func(r *policy.Records) {
			r.GroupMembers = r.GroupMembers[:1]
		},
		"a group removed": func(r *policy.Records) {
			r.Groups = r.Groups[:1]
		},
		"a group kept elsewhere": func(r *policy.Records) {
			r.Groups[0].Scope = tenant2Policy
		},
		"a group's role taken away": func(r *policy.Records) {
			r.Memberships = nil
		},
		"the role held by a person": func(r *policy.Records) {
			r.Memberships[0].Member = userSubject("night")
		},
	}
	for name, change := range changes {
		r := base()
		change(r)
		if r.Hash() == want {
			t.Errorf("%s: the hash did not change", name)
		}
	}
}
