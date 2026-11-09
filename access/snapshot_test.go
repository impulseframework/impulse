package access

import (
	"bytes"
	"context"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// roleSubject and userSubject are fixture shorthands.
func roleSubject(name string) policy.Subject {
	return policy.Subject{Kind: policy.SubjectRole, Name: name}
}

func userSubject(name string) policy.Subject {
	return policy.Subject{Kind: policy.SubjectUser, Name: name}
}

// compileSnapshot compiles fixture records through the shared compiler.
func compileSnapshot(t *testing.T, records *policy.Records) *snapshot {
	t.Helper()
	snap, _, err := newSnapshot(records, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	return snap
}

// Test_splitResourceField pins the shared splitting rule: checked resources
// and stored grants split identically, on the LAST dot.
func Test_splitResourceField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		obj          string
		wantResource string
		wantField    string
	}{
		{name: "no field", obj: "employees", wantResource: "employees", wantField: ""},
		{name: "named field", obj: "employees.name", wantResource: "employees", wantField: "name"},
		{name: "wildcard field", obj: "employees.*", wantResource: "employees", wantField: "*"},
		{name: "splits on last dot", obj: "a.b.c", wantResource: "a.b", wantField: "c"},
		{name: "global resource", obj: "global", wantResource: "global", wantField: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resource, field := splitResourceField(tt.obj)
			if resource != tt.wantResource || field != tt.wantField {
				t.Errorf("splitResourceField(%q) = (%q, %q), want (%q, %q)", tt.obj, resource, field, tt.wantResource, tt.wantField)
			}
		})
	}
}

// missingFromDecisions derives the legacy missing list from per-resource
// decisions: every resource whose decision is denied, in input order. The
// fixtures here carry no conditions, so it also asserts nothing came back
// conditional.
func missingFromDecisions(t *testing.T, resources []accesstypes.Resource, decisions []resourceDecision) []accesstypes.Resource {
	t.Helper()
	if len(decisions) != len(resources) {
		t.Fatalf("got %d decisions for %d resources", len(decisions), len(resources))
	}
	missing := make([]accesstypes.Resource, 0)
	for i, d := range decisions {
		if len(d.conditions) > 0 {
			t.Fatalf("decision %d = conditional %v, want unconditional decisions from a condition-free store", i, d.conditions)
		}
		if !d.granted {
			missing = append(missing, resources[i])
		}
	}

	return missing
}

func Test_snapshot_decideUserResources(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees", Field: "name"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Editor"), Perm: "Read", Resource: "documents", Field: "*"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Auditor"), Perm: "List", Resource: "widgets"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Chief"), Perm: "Read", Resource: "budgets"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: userSubject("dana"), Perm: "List", Resource: "widgets"},
		},
		Memberships: []policy.Membership{
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: userSubject("erin"), Role: "Editor"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: userSubject("erin"), Role: "Auditor"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: roleSubject("Editor"), Role: "Chief"},
		},
	})

	type args struct {
		user      accesstypes.User
		scope     accesstypes.Scope
		perm      accesstypes.Permission
		resources []accesstypes.Resource
	}
	tests := []struct {
		name        string
		args        args
		wantMissing []accesstypes.Resource
	}{
		{
			name:        "endpoint grant through role",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "named field grant",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees.name"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "endpoint grant gives no field visibility",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees.salary"}},
			wantMissing: []accesstypes.Resource{"employees.salary"},
		},
		{
			name:        "wildcard grant covers unknown fields by implication",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"documents.title", "documents.body"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "wildcard grant does not grant the endpoint",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"documents"}},
			wantMissing: []accesstypes.Resource{"documents"},
		},
		{
			name:        "grants combine across roles",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "List", resources: []accesstypes.Resource{"widgets"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "role inheritance is folded transitively",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"budgets"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "direct user grant without membership",
			args:        args{user: "dana", scope: accesstypes.DomainScope("tenant1"), perm: "List", resources: []accesstypes.Resource{"widgets"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "grants stay inside their domain",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant2"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
		{
			name:        "unknown user",
			args:        args{user: "stranger", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
		{
			name:        "unknown permission",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Fly", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
		{
			name:        "unknown resource",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"spaceships"}},
			wantMissing: []accesstypes.Resource{"spaceships"},
		},
		{
			name:        "missing preserves input order",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"spaceships", "employees", "employees.salary"}},
			wantMissing: []accesstypes.Resource{"spaceships", "employees.salary"},
		},
		{
			name:        "no resources yields empty missing",
			args:        args{user: "erin", scope: accesstypes.DomainScope("tenant1"), perm: "Read"},
			wantMissing: []accesstypes.Resource{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			decisions := snap.decideUserResources(tt.args.user, tt.args.scope, tt.args.perm, tt.args.resources...)
			gotMissing := missingFromDecisions(t, tt.args.resources, decisions)
			if diff := cmp.Diff(tt.wantMissing, gotMissing); diff != "" {
				t.Errorf("snapshot.decideUserResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_snapshot_decideUserResources_conditions pins conditional coverage: a
// resource covered only by conditional grants decides Conditional carrying
// the covering grants' condition texts (OR semantics, canonically ordered,
// deduplicated); any unconditional cover settles the decision as Granted; no
// cover at all stays Denied. The texts are opaque — nothing here parses or
// evaluates them.
func Test_snapshot_decideUserResources_conditions(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("DraftEditor"), Perm: "Update", Resource: "loans", Field: "*", Condition: "state = 'new'"},
			{Scope: tenant1Policy, Subject: roleSubject("ServicingAgent"), Perm: "Update", Resource: "loans", Field: "phone", Condition: "state IN ('new', 'approved')"},
			{Scope: tenant1Policy, Subject: roleSubject("Auditor"), Perm: "Read", Resource: "reports", Condition: "region = subject"},
			{Scope: tenant1Policy, Subject: roleSubject("Chief"), Perm: "Read", Resource: "reports"},
			{Scope: tenant1Policy, Subject: roleSubject("Owner"), Perm: "Update", Resource: "notes", Field: "body", Condition: "owner = subject"},
			{Scope: tenant1Policy, Subject: roleSubject("CoOwner"), Perm: "Update", Resource: "notes", Field: "body", Condition: "owner = subject"},
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "docs", Field: "*"},
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "docs", Field: "title", Condition: "unpublished = false"},
			{Scope: tenant1Policy, Subject: userSubject("hank"), Perm: "Update", Resource: "notes", Field: "body", Condition: "owner = subject"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: userSubject("dana"), Role: "DraftEditor"},
			{Scope: tenant1Policy, Member: userSubject("dana"), Role: "ServicingAgent"},
			{Scope: tenant1Policy, Member: userSubject("erin"), Role: "Auditor"},
			{Scope: tenant1Policy, Member: userSubject("frank"), Role: "Auditor"},
			{Scope: tenant1Policy, Member: userSubject("frank"), Role: "Chief"},
			{Scope: tenant1Policy, Member: userSubject("gina"), Role: "Owner"},
			{Scope: tenant1Policy, Member: userSubject("gina"), Role: "CoOwner"},
			{Scope: tenant1Policy, Member: userSubject("carol"), Role: "Editor"},
		},
	})

	type args struct {
		user      accesstypes.User
		scope     accesstypes.Scope
		perm      accesstypes.Permission
		resources []accesstypes.Resource
	}
	tests := []struct {
		name string
		args args
		want []resourceDecision
	}{
		{
			name: "conditional covering set combines across roles in text order",
			args: args{user: "dana", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"loans.phone"}},
			want: []resourceDecision{{conditions: []string{"state = 'new'", "state IN ('new', 'approved')"}}},
		},
		{
			name: "conditional wildcard covers unknown fields by implication",
			args: args{user: "dana", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"loans.term"}},
			want: []resourceDecision{{conditions: []string{"state = 'new'"}}},
		},
		{
			name: "conditional wildcard does not cover the endpoint",
			args: args{user: "dana", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"loans"}},
			want: []resourceDecision{{}},
		},
		{
			name: "wildcard check answers from wildcard coverage only",
			args: args{user: "dana", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"loans.*"}},
			want: []resourceDecision{{conditions: []string{"state = 'new'"}}},
		},
		{
			name: "conditional endpoint grant",
			args: args{user: "erin", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"reports"}},
			want: []resourceDecision{{conditions: []string{"region = subject"}}},
		},
		{
			name: "any unconditional cover settles as granted across roles",
			args: args{user: "frank", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"reports"}},
			want: []resourceDecision{{granted: true}},
		},
		{
			name: "identical condition texts deduplicate",
			args: args{user: "gina", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"notes.body"}},
			want: []resourceDecision{{conditions: []string{"owner = subject"}}},
		},
		{
			name: "direct user grant carries its condition",
			args: args{user: "hank", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"notes.body"}},
			want: []resourceDecision{{conditions: []string{"owner = subject"}}},
		},
		{
			name: "unconditional wildcard settles a conditionally granted field",
			args: args{user: "carol", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"docs.title", "docs.other"}},
			want: []resourceDecision{{granted: true}, {granted: true}},
		},
		{
			name: "conditional grants stay inside their scope",
			args: args{user: "dana", scope: tenant2Scope, perm: "Update", resources: []accesstypes.Resource{"loans.phone"}},
			want: []resourceDecision{{}},
		},
		{
			name: "uncovered resource stays denied",
			args: args{user: "erin", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"employees"}},
			want: []resourceDecision{{}},
		},
		{
			name: "unknown permission fails closed",
			args: args{user: "dana", scope: tenant1Scope, perm: "Fly", resources: []accesstypes.Resource{"loans.phone"}},
			want: []resourceDecision{{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideUserResources(tt.args.user, tt.args.scope, tt.args.perm, tt.args.resources...)
			// The compiled trees ride beside the texts, always parallel;
			// their content is the condition package's own contract, so this
			// pin asserts the pairing and compares the texts.
			for i := range got {
				if len(got[i].exprs) != len(got[i].conditions) {
					t.Errorf("decision %d carries %d compiled trees for %d conditions", i, len(got[i].exprs), len(got[i].conditions))
				}
				got[i].exprs = nil
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(resourceDecision{})); diff != "" {
				t.Errorf("snapshot.decideUserResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_snapshot_decideUserResources_oneRoleTwoConditions pins that one role
// holding two conditional grants on one resource (one stored row per
// condition) decides exactly as the same two conditions arriving through two
// roles: the endpoint's any-of set covers both, and each field carries only
// the conditions of the grants that named it.
func Test_snapshot_decideUserResources_oneRoleTwoConditions(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("Dispatcher"), Perm: "Update", Resource: "loans", Condition: "state = 'new'"},
			{Scope: tenant1Policy, Subject: roleSubject("Dispatcher"), Perm: "Update", Resource: "loans", Condition: "state IN ('new', 'approved')"},
			{Scope: tenant1Policy, Subject: roleSubject("Dispatcher"), Perm: "Update", Resource: "loans", Field: "notes", Condition: "state = 'new'"},
			{Scope: tenant1Policy, Subject: roleSubject("Dispatcher"), Perm: "Update", Resource: "loans", Field: "phone", Condition: "state IN ('new', 'approved')"},
			{Scope: tenant1Policy, Subject: roleSubject("Dispatcher"), Perm: "Update", Resource: "loans", Field: "owner", Condition: "state = 'new'"},
			{Scope: tenant1Policy, Subject: roleSubject("Dispatcher"), Perm: "Update", Resource: "loans", Field: "owner", Condition: "state IN ('new', 'approved')"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: userSubject("dana"), Role: "Dispatcher"},
		},
	})

	tests := []struct {
		name      string
		resources []accesstypes.Resource
		want      []resourceDecision
	}{
		{
			name:      "the endpoint carries both conditions",
			resources: []accesstypes.Resource{"loans"},
			want:      []resourceDecision{{conditions: []string{"state = 'new'", "state IN ('new', 'approved')"}}},
		},
		{
			name:      "each field carries only its own grant's condition",
			resources: []accesstypes.Resource{"loans.notes", "loans.phone"},
			want:      []resourceDecision{{conditions: []string{"state = 'new'"}}, {conditions: []string{"state IN ('new', 'approved')"}}},
		},
		{
			name:      "a field both grants name carries both",
			resources: []accesstypes.Resource{"loans.owner"},
			want:      []resourceDecision{{conditions: []string{"state = 'new'", "state IN ('new', 'approved')"}}},
		},
		{
			name:      "a field neither grant names stays denied",
			resources: []accesstypes.Resource{"loans.term"},
			want:      []resourceDecision{{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideUserResources("dana", tenant1Scope, "Update", tt.resources...)
			for i := range got {
				if len(got[i].exprs) != len(got[i].conditions) {
					t.Errorf("decision %d carries %d compiled trees for %d conditions", i, len(got[i].exprs), len(got[i].conditions))
				}
				got[i].exprs = nil
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(resourceDecision{})); diff != "" {
				t.Errorf("snapshot.decideUserResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// rbacScopeWide, rbacMissing and rbacAllowed are the pre-ABAC unconditional
// evaluation rules, kept verbatim as the oracle for the decision path: with
// no conditions in the store, decide must grant exactly where they grant.
// They are functions of a grant map, like every engine primitive, so the same
// oracle judges users and roles.
func rbacScopeWide(s *snapshot, grants grantMap, perm accesstypes.Permission) bool {
	if grants == nil {
		return false
	}
	permID, ok := s.perms[perm]
	if !ok {
		return false
	}
	resID, ok := s.resources[""]
	if !ok {
		return false
	}
	fs := grants[packGrantKey(permID, resID)]

	return fs != nil && fs.endpoint
}

func rbacMissing(s *snapshot, grants grantMap, perm accesstypes.Permission, resources []accesstypes.Resource) []accesstypes.Resource {
	missing := make([]accesstypes.Resource, 0)
	permID, permKnown := s.perms[perm]
	for _, resource := range resources {
		if !permKnown || !rbacAllowed(s, grants, permID, resource) {
			missing = append(missing, resource)
		}
	}

	return missing
}

func rbacAllowed(s *snapshot, grants grantMap, permID uint16, resource accesstypes.Resource) bool {
	if grants == nil {
		return false
	}

	base, field := splitResourceField(string(resource))
	resID, ok := s.resources[base]
	if !ok {
		return false
	}

	fs := grants[packGrantKey(permID, resID)]
	if fs == nil {
		return false
	}

	switch field {
	case "":
		return fs.endpoint
	case "*":
		return fs.all
	default:
		if fs.all {
			// Implication: an all-fields grant covers fields unknown to the
			// snapshot, including ones generated after the grant was written.
			return true
		}
		i, ok := s.fields[resID][field]

		return ok && fs.bit(i)
	}
}

// Test_snapshot_zeroConditionsMatchesRBAC pins the "no conditions ⇒ pure
// RBAC" invariant: over a full sweep of a condition-free store, the decision
// path grants exactly where the pre-ABAC unconditional rules (the rbac*
// oracle above) grant — for users and roles alike, scope-wide and per
// resource — and nothing is ever Conditional.
func Test_snapshot_zeroConditionsMatchesRBAC(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees", Field: "name"},
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "documents", Field: "*"},
			{Scope: tenant1Policy, Subject: roleSubject("Auditor"), Perm: "List", Resource: "widgets"},
			{Scope: tenant1Policy, Subject: roleSubject("Auditor"), Perm: "List"},
			{Scope: tenant1Policy, Subject: roleSubject("Chief"), Perm: "Read", Resource: "budgets"},
			{Scope: tenant2Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees", Field: "*"},
			{Scope: tenant1Policy, Subject: userSubject("dana"), Perm: "List", Resource: "widgets"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: userSubject("erin"), Role: "Editor"},
			{Scope: tenant1Policy, Member: userSubject("erin"), Role: "Auditor"},
			{Scope: tenant1Policy, Member: roleSubject("Editor"), Role: "Chief"},
			{Scope: tenant2Policy, Member: userSubject("dana"), Role: "Editor"},
		},
	})

	users := []accesstypes.User{"erin", "dana", "stranger"}
	roles := []accesstypes.Role{"Editor", "Auditor", "Chief", "Ghost"}
	scopes := []accesstypes.Scope{tenant1Scope, tenant2Scope, accesstypes.GlobalScope()}
	perms := []accesstypes.Permission{"Read", "List", "Fly"}
	resources := []accesstypes.Resource{
		"employees", "employees.name", "employees.salary", "employees.*",
		"documents", "documents.title", "documents.*",
		"widgets", "budgets", "spaceships", "spaceships.crew",
	}

	for _, scope := range scopes {
		for _, perm := range perms {
			for _, user := range users {
				assertMatchesRBACOracle(t, snap, string(user), snap.userGrants(scope, user), scope, perm, resources,
					snap.checkUser(user, scope, perm), snap.decideUserResources(user, scope, perm, resources...))
			}
			for _, role := range roles {
				assertMatchesRBACOracle(t, snap, string(role), snap.roleGrants(scope, role), scope, perm, resources,
					snap.checkRole(role, scope, perm), snap.decideRoleResources(role, scope, perm, resources...))
			}
		}
	}
}

// assertMatchesRBACOracle compares one subject's scope-wide and per-resource
// decisions with the rbac oracle over the same grant map: granted exactly
// where the oracle grants, and never Conditional. Shared by the fixed sweep
// and its randomized companion (snapshot_property_test.go).
func assertMatchesRBACOracle(t *testing.T, snap *snapshot, subject string, grants grantMap, scope accesstypes.Scope, perm accesstypes.Permission, resources []accesstypes.Resource, scopeWide resourceDecision, decisions []resourceDecision) {
	t.Helper()
	if want := rbacScopeWide(snap, grants, perm); scopeWide.granted != want || len(scopeWide.conditions) != 0 {
		t.Errorf("scope-wide decision for %s in %s on %s = granted %v with conditions %v, want granted %v unconditional", subject, scope, perm, scopeWide.granted, scopeWide.conditions, want)
	}
	missing := rbacMissing(snap, grants, perm, resources)
	for i, resource := range resources {
		wantGranted := !slices.Contains(missing, resource)
		if decisions[i].granted != wantGranted {
			t.Errorf("decide(%s, %s, %s, %s).granted = %v, want %v", subject, scope, perm, resource, decisions[i].granted, wantGranted)
		}
		if len(decisions[i].conditions) != 0 {
			t.Errorf("decide(%s, %s, %s, %s) = conditional %v, want unconditional from a condition-free store", subject, scope, perm, resource, decisions[i].conditions)
		}
	}
}

// Test_newSnapshot_skipsGrantsTheReleaseCannotUse pins the load-time rule for
// a grant the running release cannot use: it is left out of the snapshot and
// reported, so the permission it would grant is denied while the role's other
// grants hold and the load completes. Three things make a grant unusable: a
// condition text that does not parse, a row-referencing condition on a
// scope-wide grant (a grant attached to no resource has no row for it to see),
// and, when the collection is known, a permission, resource or field the
// release does not declare. A scope-wide grant's permission is not checked
// against the collection, which lists only the permissions resources require.
// Schema validation of conditions (binding names against the collection) is not
// the snapshot's job: the role file's validation does it for the release's grants.
func Test_newSnapshot_skipsGrantsTheReleaseCannotUse(t *testing.T) {
	t.Parallel()

	chief := roleSubject("Chief")
	tests := []struct {
		name       string
		grant      policy.Grant
		collection PermissionCollection
		wantSkip   *SkippedGrant // nil: the grant is kept
		wantReason string
		// check is the resource whose decision proves the grant kept or
		// skipped; the grant's own dotted name when empty.
		check accesstypes.Resource
	}{
		{
			name:       "a condition that does not parse",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Widgets", Field: "price", Condition: "state = "},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Read", Resource: "Widgets.price", Condition: "state = "},
			wantReason: "does not parse",
		},
		{
			name:       "a row-referencing condition on a scope-wide grant",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Approve", Resource: "", Condition: "state = 'new'"},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Approve", Resource: "", Condition: "state = 'new'"},
			wantReason: "row-referencing condition on a scope-wide grant",
		},
		{
			name:  "a row-free condition on a scope-wide grant is kept",
			grant: policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Approve", Resource: "", Condition: "now < '2027-03-01T00:00:00Z'"},
		},
		{
			name:  "a conditional field grant is kept",
			grant: policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Widgets", Field: "price", Condition: "price < 100"},
		},
		{
			name:  "an unconditional field grant is kept",
			grant: policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Widgets", Field: "name"},
		},
		{
			name:       "a permission the release does not declare",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Fly", Resource: "Widgets"},
			collection: grammarCollection{},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Fly", Resource: "Widgets"},
			wantReason: "this release has no permission Fly",
		},
		{
			name:       "a resource the release does not declare",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Gadgets"},
			collection: grammarCollection{},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Read", Resource: "Gadgets"},
			wantReason: "this release declares no Read on Gadgets",
		},
		{
			name:       "a field the release does not declare",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Widgets", Field: "weight"},
			collection: grammarCollection{},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Read", Resource: "Widgets.weight"},
			wantReason: "this release declares no Read on Widgets.weight",
		},
		{
			name:       "a field the release declares for another permission only",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Update", Resource: "Widgets", Field: "name"},
			collection: grammarCollection{},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Update", Resource: "Widgets.name"},
			wantReason: "this release declares no Update on Widgets.name",
		},
		{
			name:       "all fields of a declared resource are kept",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Update", Resource: "Widgets", Field: "*"},
			collection: grammarCollection{},
			check:      "Widgets.price",
		},
		{
			name:       "all fields of an undeclared resource",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Gadgets", Field: "*"},
			collection: grammarCollection{},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Read", Resource: "Gadgets"},
			wantReason: "this release declares no Read on Gadgets",
		},
		{
			name:       "a scope-wide grant is not checked against the collection",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Fly", Resource: ""},
			collection: grammarCollection{},
		},
		{
			name:  "names are not checked without a collection",
			grant: policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Gadgets"},
		},
		{
			name:       "an undeclared name and a bad condition: one report, the name first",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Gadgets", Condition: "state = "},
			collection: grammarCollection{},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "role Chief", Permission: "Read", Resource: "Gadgets", Condition: "state = "},
			wantReason: "this release declares no Read on Gadgets",
		},
		{
			name:       "a user's own grant is reported as the user's",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: userSubject("cleo"), Perm: "Read", Resource: "Widgets", Field: "price", Condition: "state = "},
			wantSkip:   &SkippedGrant{Scope: tenant1Policy, Subject: "user cleo", Permission: "Read", Resource: "Widgets.price", Condition: "state = "},
			wantReason: "does not parse",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The sibling grant stays with the role whatever happens to the one
			// under test, and cleo holds the role.
			records := &policy.Records{
				Roles: []policy.Role{{Scope: tenant1Policy, Name: "Chief"}},
				Grants: []policy.Grant{
					{Scope: tenant1Policy, Subject: chief, Perm: "Read", Resource: "Widgets"},
					tt.grant,
				},
				Memberships: []policy.Membership{{Scope: tenant1Policy, Member: userSubject("cleo"), Role: "Chief"}},
			}
			snap, skipped, err := newSnapshot(records, nil, tt.collection, time.Now())
			if err != nil {
				t.Fatalf("newSnapshot() error = %v, want the load to complete", err)
			}
			if got := snap.decideUserResources("cleo", tenant1Scope, "Read", "Widgets"); !got[0].granted {
				t.Errorf("the sibling grant is not held: decide(Read, Widgets) = %+v", got[0])
			}

			check := tt.check
			if check == "" {
				check = grantResourceName(&tt.grant)
			}
			var decision resourceDecision
			if tt.grant.Resource == "" {
				decision = snap.checkUser("cleo", tenant1Scope, tt.grant.Perm)
			} else {
				decision = snap.decideUserResources("cleo", tenant1Scope, tt.grant.Perm, check)[0]
			}

			if tt.wantSkip == nil {
				if len(skipped) != 0 {
					t.Fatalf("skipped = %v, want the grant kept", skipped)
				}
				if !decision.granted && len(decision.conditions) == 0 {
					t.Errorf("the kept grant decides nothing: %+v", decision)
				}

				return
			}
			if len(skipped) != 1 {
				t.Fatalf("skipped %d grants, want 1: %v", len(skipped), skipped)
			}
			got, ok := skipped[0].(*SkippedGrant)
			if !ok {
				t.Fatalf("finding = %T, want *SkippedGrant", skipped[0])
			}
			if got.Scope != tt.wantSkip.Scope || got.Subject != tt.wantSkip.Subject || got.Permission != tt.wantSkip.Permission || got.Resource != tt.wantSkip.Resource || got.Condition != tt.wantSkip.Condition {
				t.Errorf("SkippedGrant = %+v, want %+v", got, tt.wantSkip)
			}
			if got.Reason == nil || !strings.Contains(got.Reason.Error(), tt.wantReason) {
				t.Errorf("SkippedGrant.Reason = %v, want one containing %q", got.Reason, tt.wantReason)
			}
			if !strings.Contains(got.Error(), tt.wantReason) || !strings.Contains(got.Error(), tt.wantSkip.Subject) {
				t.Errorf("SkippedGrant.Error() = %q, want the subject and the reason in it", got.Error())
			}
			if decision.granted || len(decision.conditions) != 0 {
				t.Errorf("the skipped grant still decides: %+v, want denied", decision)
			}
		})
	}
}

// Test_newDecisions pins the translation to the public Decisions, including
// fact folding and the shared-group emission: each distinct covering set's
// any-of combination folds once — TRUE settles its members Granted, FALSE
// settles them Denied, anything left is Conditional — and resources sharing
// a covering condition set get ONE ConditionGroup listing every member
// (first-appearance input order, deduplicated), the same group value
// reachable from each member's Decision, its payload the folded any-of
// expression. The group key is the covering set (grant identity), never the
// folded text.
func Test_newDecisions(t *testing.T) {
	t.Parallel()

	instant := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)

	// dec builds an engine decision covered by the given condition texts,
	// compiled exactly as the snapshot load compiles them.
	dec := func(texts ...string) resourceDecision {
		exprs := make([]condition.Expr, len(texts))
		for i, text := range texts {
			expr, err := condition.Parse(text)
			if err != nil {
				t.Fatalf("condition.Parse(%q) error = %v", text, err)
			}
			exprs[i] = expr
		}

		return resourceDecision{conditions: texts, exprs: exprs}
	}
	// payload builds the Condition a group is expected to carry.
	payload := func(source string) accesstypes.Condition {
		c, err := accesstypes.NewCondition(source)
		if err != nil {
			t.Fatalf("accesstypes.NewCondition(%q) error = %v", source, err)
		}

		return c
	}

	sharedGroup := accesstypes.ConditionGroup{
		Resources: []accesstypes.Resource{"loans.phone", "loans.term"},
		Condition: payload("state = 'new'"),
	}

	tests := []struct {
		name      string
		resources []accesstypes.Resource
		decisions []resourceDecision
		facts     condition.Facts
		want      accesstypes.Decisions
		wantErr   bool
	}{
		{
			name:      "granted",
			resources: []accesstypes.Resource{"employees.name"},
			decisions: []resourceDecision{{granted: true}},
			want:      accesstypes.Decisions{"employees.name": accesstypes.Granted()},
		},
		{
			name:      "denied",
			resources: []accesstypes.Resource{"employees.name"},
			decisions: []resourceDecision{{}},
			want:      accesstypes.Decisions{"employees.name": accesstypes.Denied()},
		},
		{
			name:      "lone conditional carries one group naming the resource",
			resources: []accesstypes.Resource{"employees.name"},
			decisions: []resourceDecision{dec("owner = subject")},
			want: accesstypes.Decisions{
				"employees.name": accesstypes.Conditional(accesstypes.ConditionGroup{
					Resources: []accesstypes.Resource{"employees.name"},
					Condition: payload("owner = subject"),
				}),
			},
		},
		{
			name:      "identical covering sets share one group in each member's decision",
			resources: []accesstypes.Resource{"loans.phone", "loans.term"},
			decisions: []resourceDecision{
				dec("state = 'new'"),
				dec("state = 'new'"),
			},
			want: accesstypes.Decisions{
				"loans.phone": accesstypes.Conditional(sharedGroup),
				"loans.term":  accesstypes.Conditional(sharedGroup),
			},
		},
		{
			name:      "distinct covering sets get distinct groups with any-of payloads",
			resources: []accesstypes.Resource{"loans.phone", "loans.notes"},
			decisions: []resourceDecision{
				dec("crew IN subject.crews", "state = 'open'"),
				dec("crew IN subject.crews"),
			},
			want: accesstypes.Decisions{
				"loans.phone": accesstypes.Conditional(accesstypes.ConditionGroup{
					Resources: []accesstypes.Resource{"loans.phone"},
					Condition: payload("crew IN subject.crews OR state = 'open'"),
				}),
				"loans.notes": accesstypes.Conditional(accesstypes.ConditionGroup{
					Resources: []accesstypes.Resource{"loans.notes"},
					Condition: payload("crew IN subject.crews"),
				}),
			},
		},
		{
			name:      "grouping never absorbs granted or denied members",
			resources: []accesstypes.Resource{"loans", "loans.phone", "loans.term", "secrets"},
			decisions: []resourceDecision{
				{granted: true},
				dec("state = 'new'"),
				dec("state = 'new'"),
				{},
			},
			want: accesstypes.Decisions{
				"loans":       accesstypes.Granted(),
				"loans.phone": accesstypes.Conditional(sharedGroup),
				"loans.term":  accesstypes.Conditional(sharedGroup),
				"secrets":     accesstypes.Denied(),
			},
		},
		{
			name:      "duplicate checked resource appears once in its group",
			resources: []accesstypes.Resource{"loans.phone", "loans.phone"},
			decisions: []resourceDecision{
				dec("state = 'new'"),
				dec("state = 'new'"),
			},
			want: accesstypes.Decisions{
				"loans.phone": accesstypes.Conditional(accesstypes.ConditionGroup{
					Resources: []accesstypes.Resource{"loans.phone"},
					Condition: payload("state = 'new'"),
				}),
			},
		},
		{
			name:      "environment window folding TRUE settles the member granted",
			resources: []accesstypes.Resource{"loans.phone"},
			decisions: []resourceDecision{dec("now < '2027-06-01T00:00:00Z'")},
			facts:     condition.NewFacts().WithNow(instant),
			want:      accesstypes.Decisions{"loans.phone": accesstypes.Granted()},
		},
		{
			name:      "environment window folding FALSE settles the member denied",
			resources: []accesstypes.Resource{"loans.phone"},
			decisions: []resourceDecision{dec("now < '2027-01-01T00:00:00Z'")},
			facts:     condition.NewFacts().WithNow(instant),
			want:      accesstypes.Decisions{"loans.phone": accesstypes.Denied()},
		},
		{
			name:      "folded window leaves only the data condition in the payload",
			resources: []accesstypes.Resource{"loans.phone"},
			decisions: []resourceDecision{dec("crew IN subject.crews AND now < '2027-06-01T00:00:00Z'")},
			facts:     condition.NewFacts().WithNow(instant),
			want: accesstypes.Decisions{
				"loans.phone": accesstypes.Conditional(accesstypes.ConditionGroup{
					Resources: []accesstypes.Resource{"loans.phone"},
					Condition: payload("crew IN subject.crews"),
				}),
			},
		},
		{
			name:      "condition referencing an absent environment attribute is a check error",
			resources: []accesstypes.Resource{"loans.phone"},
			decisions: []resourceDecision{dec("now < '2027-06-01T00:00:00Z'")},
			wantErr:   true,
		},
		{
			name:      "empty batch",
			resources: []accesstypes.Resource{},
			decisions: []resourceDecision{},
			want:      accesstypes.Decisions{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := newDecisions(tt.resources, tt.decisions, tt.facts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newDecisions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("newDecisions() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_snapshot_scopeWideChecks pins the resource-less check path: a
// scope-wide grant compiles under the empty resource name — unreachable from
// data, since every write boundary validates resource names non-empty — and
// is answered by the base-name check methods. Resource grants never satisfy a
// scope-wide check and vice versa.
func Test_snapshot_scopeWideChecks(t *testing.T) {
	t.Parallel()

	globalScope := accesstypes.GlobalScope()
	records := &policy.Records{
		Grants: []policy.Grant{
			{Scope: globalScope.PolicyScope(), Subject: roleSubject("Admin"), Perm: "Export", Resource: ""},
			{Scope: globalScope.PolicyScope(), Subject: roleSubject("Admin"), Perm: "Read", Resource: "employees"},
			{Scope: tenant1Policy, Subject: roleSubject("Chief"), Perm: "Approve", Resource: ""},
		},
		Memberships: []policy.Membership{
			{Scope: globalScope.PolicyScope(), Member: userSubject("alice"), Role: "Admin"},
			{Scope: tenant1Policy, Member: userSubject("carol"), Role: "Chief"},
		},
	}
	snap, _, err := newSnapshot(records, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	tests := []struct {
		name  string
		check func() bool
		want  bool
	}{
		{name: "user holds scope-wide perm through role", check: func() bool { return snap.checkUser("alice", globalScope, "Export").granted }, want: true},
		{name: "role holds its scope-wide perm", check: func() bool { return snap.checkRole("Admin", globalScope, "Export").granted }, want: true},
		{name: "resource grant does not satisfy a scope-wide check", check: func() bool { return snap.checkUser("alice", globalScope, "Read").granted }},
		{name: "resource grant does not satisfy a scope-wide role check", check: func() bool { return snap.checkRole("Admin", globalScope, "Read").granted }},
		{name: "scope-wide grant is partitioned by scope", check: func() bool { return snap.checkUser("alice", tenant1Scope, "Export").granted }},
		{name: "role's scope-wide grant is partitioned by scope", check: func() bool { return snap.checkRole("Admin", tenant1Scope, "Export").granted }},
		{name: "tenant scope-wide grant works in its scope", check: func() bool { return snap.checkUser("carol", tenant1Scope, "Approve").granted }, want: true},
		{name: "tenant role holds its scope-wide grant", check: func() bool { return snap.checkRole("Chief", tenant1Scope, "Approve").granted }, want: true},
		{name: "a tenant named global is not the global scope", check: func() bool { return snap.checkUser("alice", accesstypes.DomainScope("global"), "Export").granted }},
		{name: "unknown perm fails closed", check: func() bool { return snap.checkUser("alice", globalScope, "Fly").granted }},
		{name: "unknown user fails closed", check: func() bool { return snap.checkUser("stranger", globalScope, "Export").granted }},
		{name: "unknown role fails closed", check: func() bool { return snap.checkRole("Ghost", globalScope, "Export").granted }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.check(); got != tt.want {
				t.Errorf("scope-wide check = %v, want %v", got, tt.want)
			}
		})
	}

	// The scope-wide grant does not leak into the resource batch path either:
	// checking the empty-string resource through decideUserResources is not a
	// public spelling, and real resources stay ungranted.
	if decisions := snap.decideUserResources("alice", globalScope, "Export", "employees"); decisions[0].granted {
		t.Errorf("decideUserResources() = %v, want employees denied: scope-wide grants must not satisfy resource checks", decisions)
	}
	if decisions := snap.decideRoleResources("Admin", globalScope, "Export", "employees"); decisions[0].granted {
		t.Errorf("decideRoleResources() = %v, want employees denied: scope-wide grants must not satisfy resource checks", decisions)
	}
}

// Test_snapshot_decideRoleResources pins the role twin of the resource
// batch: a role answers from its own and transitively inherited grants, with
// no member involved; inheritance is one-way, a cycle compiles and denies
// safely, and an unknown role or wrong scope fails closed.
func Test_snapshot_decideRoleResources(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Chief"), Perm: "Read", Resource: "budgets"},
		},
		Memberships: []policy.Membership{
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: roleSubject("Editor"), Role: "Chief"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: roleSubject("Loop1"), Role: "Loop2"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: roleSubject("Loop2"), Role: "Loop1"},
		},
	})

	type args struct {
		role      accesstypes.Role
		scope     accesstypes.Scope
		perm      accesstypes.Permission
		resources []accesstypes.Resource
	}
	tests := []struct {
		name        string
		args        args
		wantMissing []accesstypes.Resource
	}{
		{
			name:        "own grant",
			args:        args{role: "Editor", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "inherited grant",
			args:        args{role: "Editor", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"budgets"}},
			wantMissing: []accesstypes.Resource{},
		},
		{
			name:        "inheritance is one-way",
			args:        args{role: "Chief", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
		{
			name:        "inheritance cycle compiles and denies safely",
			args:        args{role: "Loop1", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
		{
			name:        "unknown role",
			args:        args{role: "Ghost", scope: accesstypes.DomainScope("tenant1"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
		{
			name:        "wrong domain",
			args:        args{role: "Editor", scope: accesstypes.DomainScope("tenant2"), perm: "Read", resources: []accesstypes.Resource{"employees"}},
			wantMissing: []accesstypes.Resource{"employees"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			decisions := snap.decideRoleResources(tt.args.role, tt.args.scope, tt.args.perm, tt.args.resources...)
			gotMissing := missingFromDecisions(t, tt.args.resources, decisions)
			if diff := cmp.Diff(tt.wantMissing, gotMissing); diff != "" {
				t.Errorf("snapshot.decideRoleResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_snapshot_decideRoleResources_conditions pins that a role's decisions
// carry conditional coverage exactly as a member's do: a role sees its own
// and inherited grants only (a member's direct grant never reaches it), an
// unconditional cover from a parent settles granted, and a subject term rides
// through unbound — a role has no identity for it; the resource layer binds
// the session's.
func Test_snapshot_decideRoleResources_conditions(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("DraftEditor"), Perm: "Update", Resource: "loans", Field: "*", Condition: "state = 'new'"},
			{Scope: tenant1Policy, Subject: roleSubject("Auditor"), Perm: "Read", Resource: "reports", Condition: "region = subject"},
			{Scope: tenant1Policy, Subject: roleSubject("Chief"), Perm: "Read", Resource: "reports"},
			{Scope: tenant1Policy, Subject: userSubject("hank"), Perm: "Read", Resource: "reports"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: roleSubject("Lead"), Role: "Auditor"},
			{Scope: tenant1Policy, Member: roleSubject("Lead"), Role: "Chief"},
			{Scope: tenant1Policy, Member: userSubject("hank"), Role: "Auditor"},
		},
	})

	type args struct {
		role      accesstypes.Role
		scope     accesstypes.Scope
		perm      accesstypes.Permission
		resources []accesstypes.Resource
	}
	tests := []struct {
		name string
		args args
		want []resourceDecision
	}{
		{
			name: "own conditional grant carries its condition with the subject unbound; a member's direct grant never reaches the role",
			args: args{role: "Auditor", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"reports"}},
			want: []resourceDecision{{conditions: []string{"region = subject"}}},
		},
		{
			name: "an inherited unconditional cover settles as granted",
			args: args{role: "Lead", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"reports"}},
			want: []resourceDecision{{granted: true}},
		},
		{
			name: "conditional wildcard covers unknown fields by implication",
			args: args{role: "DraftEditor", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"loans.term"}},
			want: []resourceDecision{{conditions: []string{"state = 'new'"}}},
		},
		{
			name: "conditional wildcard does not cover the endpoint",
			args: args{role: "DraftEditor", scope: tenant1Scope, perm: "Update", resources: []accesstypes.Resource{"loans"}},
			want: []resourceDecision{{}},
		},
		{
			name: "conditional grants stay inside their scope",
			args: args{role: "Auditor", scope: tenant2Scope, perm: "Read", resources: []accesstypes.Resource{"reports"}},
			want: []resourceDecision{{}},
		},
		{
			name: "unknown role fails closed",
			args: args{role: "Ghost", scope: tenant1Scope, perm: "Read", resources: []accesstypes.Resource{"reports"}},
			want: []resourceDecision{{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snap.decideRoleResources(tt.args.role, tt.args.scope, tt.args.perm, tt.args.resources...)
			for i := range got {
				if len(got[i].exprs) != len(got[i].conditions) {
					t.Errorf("decision %d carries %d compiled trees for %d conditions", i, len(got[i].exprs), len(got[i].conditions))
				}
				got[i].exprs = nil
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(resourceDecision{})); diff != "" {
				t.Errorf("snapshot.decideRoleResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Shared scopes for the package's test fixtures: where a request is
// (tenant1Scope, tenant2Scope) and where policy is held (the Policy forms).
var (
	tenant1Scope      = accesstypes.DomainScope("tenant1")
	tenant2Scope      = accesstypes.DomainScope("tenant2")
	tenant1Policy     = accesstypes.DomainPolicyScope("tenant1")
	tenant2Policy     = accesstypes.DomainPolicyScope("tenant2")
	globalPolicy      = accesstypes.GlobalPolicyScope()
	everyDomainPolicy = accesstypes.EveryDomainPolicyScope()
)

// engineFakeStore returns a fakeStore seeded with the engine lifecycle
// fixture: Editor holds Read on employees in tenant1, erin is an Editor.
func engineFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	ctx := context.Background()
	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, tenant1Policy, "Editor", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Editor", "Read", "employees", "", ""),
		store.InsertUserRole(ctx, tenant1Policy, "erin", "Editor"),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}

	return store
}

// grantWidgets simulates another instance's policy write: it lands in the
// store without notifying this instance's engine.
func grantWidgets(t *testing.T, store *fakeStore) {
	t.Helper()
	if err := store.InsertGrant(context.Background(), tenant1Policy, "Editor", "List", "widgets", "", ""); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
}

// testEngine builds a snapshotEngine whose background loop is effectively
// inert (1h heartbeat) so tests drive reloads deterministically, and whose
// goroutines stop at test end.
func testEngine(t *testing.T, store Store, opts *clientOptions) *snapshotEngine {
	t.Helper()
	if opts == nil {
		opts = defaultClientOptions()
	}
	opts.heartbeatInterval = time.Hour
	e := newSnapshotEngine(store, nil, opts)
	t.Cleanup(func() {
		if err := e.close(); err != nil {
			t.Errorf("snapshotEngine.close() error = %v", err)
		}
	})

	return e
}

// settle waits until the background loop's in-flight reload (if any) has
// finished, so subsequent fixture mutations cannot race it.
func settle(e *snapshotEngine) {
	e.tryReload(context.Background())
}

func Test_snapshotEngine_checkUserResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		storeDown bool
		wantErr   bool
	}{
		{
			name: "served from snapshot",
		},
		{
			name:      "first load failure returns an error",
			storeDown: true,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := engineFakeStore(t)
			if tt.storeDown {
				store.setFail(errors.New("store down"))
			}
			e := testEngine(t, store, nil)

			got, err := e.checkUserResources(context.Background(), "erin", tenant1Scope, "Read", "employees")
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(got) != 1 || !got[0].granted {
				t.Errorf("checkUserResources() = %v, want employees granted", got)
			}
		})
	}
}

func Test_snapshotEngine_waitReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		storeDown bool
		timeout   time.Duration
		wantErr   bool
	}{
		{
			name:    "ready after first load",
			timeout: 5 * time.Second,
		},
		{
			name:      "times out while the store is down",
			storeDown: true,
			timeout:   50 * time.Millisecond,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := engineFakeStore(t)
			if tt.storeDown {
				store.setFail(errors.New("store down"))
			}
			e := testEngine(t, store, nil)

			readyCtx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()
			if err := e.waitReady(readyCtx); (err != nil) != tt.wantErr {
				t.Fatalf("waitReady() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && e.peek() == nil {
				t.Fatal("peek() = nil after waitReady")
			}
		})
	}
}

func Test_snapshotEngine_invalidateReloadsOnNextCheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := engineFakeStore(t)
	e := testEngine(t, store, nil)

	if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets"); err != nil || got[0].granted {
		t.Fatalf("checkUser() = (%v, %v), want widgets denied before the write", got, err)
	}
	settle(e)

	grantWidgets(t, store)

	if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets"); err != nil || got[0].granted {
		t.Fatalf("checkUser() = (%v, %v), want stale answer before invalidate", got, err)
	}

	e.invalidate()

	if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets"); err != nil || !got[0].granted {
		t.Fatalf("checkUser() = (%v, %v), want widgets granted after invalidate", got, err)
	}
}

func Test_snapshotEngine_reloadFailureServesLastGoodSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := engineFakeStore(t)
	var reloadErrs atomic.Int64
	opts := defaultClientOptions()
	opts.onReloadError = func(error) { reloadErrs.Add(1) }
	e := testEngine(t, store, opts)

	if _, err := e.checkUserResources(ctx, "erin", tenant1Scope, "Read", "employees"); err != nil {
		t.Fatalf("checkUser() error = %v", err)
	}
	settle(e)

	// Break the store, then force a reload attempt.
	store.setFail(errors.New("store down"))
	e.invalidate()

	got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "Read", "employees")
	if err != nil {
		t.Fatalf("checkUser() error = %v, want stale snapshot served", err)
	}
	if !got[0].granted {
		t.Fatalf("checkUser() = %v, want granted from stale snapshot", got)
	}
}

func Test_snapshotEngine_closeIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := testEngine(t, engineFakeStore(t), nil)

	if _, err := e.checkUserResources(ctx, "erin", tenant1Scope, "Read", "employees"); err != nil {
		t.Fatalf("checkUser() error = %v", err)
	}
	if err := e.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	if err := e.close(); err != nil {
		t.Fatalf("second close() error = %v", err)
	}
	if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "Read", "employees"); err != nil || !got[0].granted {
		t.Fatalf("checkUser() after close = (%v, %v), want served from last snapshot", got, err)
	}
}

// fakeSignal is an in-process ChangeSignal for tests.
type fakeSignal struct {
	mu           sync.Mutex
	announces    atomic.Int64
	onChange     func()
	watchStarted chan struct{}
	startedOnce  sync.Once
}

func newFakeSignal() *fakeSignal {
	return &fakeSignal{watchStarted: make(chan struct{})}
}

func (f *fakeSignal) Announce(_ context.Context) error {
	f.announces.Add(1)

	return nil
}

func (f *fakeSignal) Watch(ctx context.Context, onChange func()) error {
	f.mu.Lock()
	f.onChange = onChange
	f.mu.Unlock()
	f.startedOnce.Do(func() { close(f.watchStarted) })
	<-ctx.Done()

	return nil
}

func (f *fakeSignal) trigger() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onChange != nil {
		f.onChange()
	}
}

func Test_snapshotEngine_changeSignal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := engineFakeStore(t)

	sig := newFakeSignal()
	opts := defaultClientOptions()
	opts.signal = sig
	e := testEngine(t, store, opts)

	if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets"); err != nil || got[0].granted {
		t.Fatalf("checkUser() = (%v, %v), want widgets denied before the change", got, err)
	}
	settle(e)

	select {
	case <-sig.watchStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("ChangeSignal.Watch was never started")
	}

	// A hint (not a local invalidate) must reload through the background loop.
	grantWidgets(t, store)
	sig.trigger()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets")
		if err != nil {
			t.Fatalf("checkUser() error = %v", err)
		}
		if got[0].granted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hint did not propagate a policy change within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A local policy write announces to other instances.
	e.policyChanged()
	deadline = time.Now().Add(5 * time.Second)
	for sig.announces.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("policyChanged did not announce within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// convoyStore instruments ReadPolicy to reproduce the write-storm race: while
// armed, every store read lands another local policy write (onLoad), so the
// write counter is always one ahead of the read in flight. The first armed
// read additionally parks on gate so the test can queue checks behind the
// reload mutex before letting anything finish.
type convoyStore struct {
	*fakeStore
	onLoad  func() // lands a write during every armed read; set before the engine starts
	loads   atomic.Int64
	armed   atomic.Bool
	started chan struct{} // closed when the first armed read is in flight
	gate    chan struct{} // first armed read blocks until this closes
	gateSeq sync.Once
}

func (c *convoyStore) ReadPolicy(ctx context.Context) (*policy.Records, error) {
	c.loads.Add(1)
	if c.armed.Load() {
		c.onLoad()
		c.gateSeq.Do(func() {
			close(c.started)
			<-c.gate
		})
	}

	return c.fakeStore.ReadPolicy(ctx)
}

// Test_snapshotEngine_syncReloadDedup pins the sync-reload dedup semantics:
// a check queued behind an in-flight reload is satisfied by any snapshot
// covering the writes that preceded the check's own entry. Regression for the
// convoy where the wake-up recheck chased the live write counter, sending
// every queued check into its own serial reload under sustained writes
// (loads grew with check count, not write count).
func Test_snapshotEngine_syncReloadDedup(t *testing.T) {
	t.Parallel()

	const checks = 50

	tests := []struct {
		name string
		// writeDuringRead lands another local write during every store read,
		// keeping the write counter permanently ahead of the read in flight —
		// the moving-target condition that caused the convoy.
		writeDuringRead bool
		// wantMaxLoads bounds store reads after the checks are queued; slack
		// covers stragglers that enter after a mid-read write. The convoy
		// regression produced one read per queued check (checks+1).
		wantMaxLoads int64
	}{
		{
			name:         "one write, queued checks share one reload",
			wantMaxLoads: 1,
		},
		{
			name:            "write storm, reloads scale with writes not checks",
			writeDuringRead: true,
			wantMaxLoads:    3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &convoyStore{
				fakeStore: engineFakeStore(t),
				started:   make(chan struct{}),
				gate:      make(chan struct{}),
			}
			e := testEngine(t, store, nil)
			store.onLoad = func() {}
			if tt.writeDuringRead {
				store.onLoad = e.invalidate
			}

			if err := e.waitReady(context.Background()); err != nil {
				t.Fatalf("waitReady() error = %v", err)
			}
			settle(e)
			baseline := store.loads.Load()

			store.armed.Store(true)
			e.invalidate() // send checks into the sync-reload path

			errs := make(chan error, checks)
			var wg sync.WaitGroup
			for range checks {
				wg.Go(func() {
					_, err := e.checkUserResources(context.Background(), "erin", tenant1Scope, "Read", "employees")
					errs <- err
				})
			}

			// Wait for the first sync reload to hold the store read open,
			// then give the remaining checks time to record their entry
			// generation and queue on the reload mutex before any reload
			// completes.
			<-store.started
			time.Sleep(50 * time.Millisecond)
			close(store.gate)

			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatalf("checkUser() error = %v", err)
				}
			}

			if extra := store.loads.Load() - baseline; extra > tt.wantMaxLoads {
				t.Errorf("store loads with checks queued = %d, want at most %d", extra, tt.wantMaxLoads)
			}
		})
	}
}

// Test_snapshot_userHasGrants pins the visibility question concealed tenancy
// asks: any grant in the scope — through a role or direct, resource-attached
// or scope-wide — is a foothold; role membership resolving to no grants, an
// unknown scope, and grants held only in another scope are not.
func Test_snapshot_userHasGrants(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Subject: userSubject("dana"), Perm: "List", Resource: "widgets"},
			{Scope: accesstypes.DomainPolicyScope("tenant2"), Subject: roleSubject("Chief"), Perm: "Read"},
		},
		Memberships: []policy.Membership{
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: userSubject("erin"), Role: "Editor"},
			{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: userSubject("gale"), Role: "Idler"},
			{Scope: accesstypes.DomainPolicyScope("tenant2"), Member: userSubject("hana"), Role: "Chief"},
		},
	})

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		want  bool
	}{
		{name: "grant through a role is a foothold", user: "erin", scope: accesstypes.DomainScope("tenant1"), want: true},
		{name: "direct grant is a foothold", user: "dana", scope: accesstypes.DomainScope("tenant1"), want: true},
		{name: "scope-wide grant is a foothold", user: "hana", scope: accesstypes.DomainScope("tenant2"), want: true},
		{name: "membership in a grantless role is not", user: "gale", scope: accesstypes.DomainScope("tenant1"), want: false},
		{name: "grants in another scope do not carry over", user: "erin", scope: accesstypes.DomainScope("tenant2"), want: false},
		{name: "unknown scope has no footholds", user: "erin", scope: accesstypes.DomainScope("tenant9"), want: false},
		{name: "unknown user has no footholds", user: "nobody", scope: accesstypes.DomainScope("tenant1"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := snap.userHasGrants(tt.scope, tt.user); got != tt.want {
				t.Errorf("userHasGrants(%s, %s) = %v, want %v", tt.scope, tt.user, got, tt.want)
			}
		})
	}
}

// Test_snapshot_userDigest pins the digest enumeration: structural (nothing
// folds), fail-closed by absence (denied targets never appear), granted
// beating conditional, all-fields grants enumerating the known field
// vocabulary, and scope-wide grants staying outside the digest.
func Test_snapshot_userDigest(t *testing.T) {
	t.Parallel()

	tenant1 := accesstypes.DomainScope("tenant1")
	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			// Editor: unconditional Read on the employees endpoint and its name
			// field; conditional Update on both.
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees", Field: "name"},
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Editor"), Perm: "Update", Resource: "employees", Condition: "status = 'open'"},
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Editor"), Perm: "Update", Resource: "employees", Field: "name", Condition: "status = 'open'"},
			// Chief: unconditional Update on the field Editor holds only
			// conditionally, and an all-fields List on widgets.
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Chief"), Perm: "Update", Resource: "employees", Field: "name"},
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Chief"), Perm: "List", Resource: "widgets", Field: "*"},
			// Auditor is assigned to nobody: its grant only puts "label" into
			// the widgets field vocabulary the all-fields enumeration lists.
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Auditor"), Perm: "Read", Resource: "widgets", Field: "label"},
			// Pinger holds only a scope-wide grant: no resource to enumerate.
			{Scope: tenant1.PolicyScope(), Subject: roleSubject("Pinger"), Perm: "Ping"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1.PolicyScope(), Member: userSubject("erin"), Role: "Editor"},
			{Scope: tenant1.PolicyScope(), Member: userSubject("casey"), Role: "Editor"},
			{Scope: tenant1.PolicyScope(), Member: userSubject("casey"), Role: "Chief"},
			{Scope: tenant1.PolicyScope(), Member: userSubject("wren"), Role: "Chief"},
			{Scope: tenant1.PolicyScope(), Member: userSubject("gale"), Role: "Pinger"},
		},
	})

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		want  accesstypes.PermissionDigest
	}{
		{
			name:  "endpoint and field entries with the conditional tri-state",
			user:  "erin",
			scope: tenant1,
			want: accesstypes.PermissionDigest{
				"employees":      {"Read": accesstypes.DigestGranted, "Update": accesstypes.DigestConditional},
				"employees.name": {"Read": accesstypes.DigestGranted, "Update": accesstypes.DigestConditional},
			},
		},
		{
			name:  "an unconditional cover from any role settles the entry granted",
			user:  "casey",
			scope: tenant1,
			want: accesstypes.PermissionDigest{
				"employees":      {"Read": accesstypes.DigestGranted, "Update": accesstypes.DigestConditional},
				"employees.name": {"Read": accesstypes.DigestGranted, "Update": accesstypes.DigestGranted},
				"widgets.label":  {"List": accesstypes.DigestGranted},
			},
		},
		{
			name:  "an all-fields grant lists the known vocabulary, never the endpoint",
			user:  "wren",
			scope: tenant1,
			want: accesstypes.PermissionDigest{
				"employees.name": {"Update": accesstypes.DigestGranted},
				"widgets.label":  {"List": accesstypes.DigestGranted},
			},
		},
		{
			name:  "a scope-wide grant attaches to no resource and is not enumerated",
			user:  "gale",
			scope: tenant1,
			want:  accesstypes.PermissionDigest{},
		},
		{
			name:  "grants in another scope do not carry over",
			user:  "erin",
			scope: accesstypes.DomainScope("tenant2"),
			want:  accesstypes.PermissionDigest{},
		},
		{
			name:  "an unknown user holds nothing",
			user:  "nobody",
			scope: tenant1,
			want:  accesstypes.PermissionDigest{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := snap.userDigest(tt.scope, tt.user)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("userDigest(%s, %s) mismatch (-want +got):\n%s", tt.scope, tt.user, diff)
			}
		})
	}
}

// Test_snapshot_roleHasGrants pins the foothold question for a session
// operating as a role: any grant the role holds in the scope — own or
// inherited, resource-attached or scope-wide — is a foothold; a role that
// exists but resolves to no grants, a role known only in another scope, and
// an unknown role have none.
func Test_snapshot_roleHasGrants(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: tenant2Policy, Subject: roleSubject("Chief"), Perm: "Read"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: roleSubject("Lead"), Role: "Editor"},
			{Scope: tenant1Policy, Member: userSubject("gale"), Role: "Idler"},
		},
	})

	tests := []struct {
		name  string
		role  accesstypes.Role
		scope accesstypes.Scope
		want  bool
	}{
		{name: "own grant is a foothold", role: "Editor", scope: tenant1Scope, want: true},
		{name: "inherited grant is a foothold", role: "Lead", scope: tenant1Scope, want: true},
		{name: "scope-wide grant is a foothold", role: "Chief", scope: tenant2Scope, want: true},
		{name: "a role with members but no grants is not", role: "Idler", scope: tenant1Scope, want: false},
		{name: "grants in another scope do not carry over", role: "Editor", scope: tenant2Scope, want: false},
		{name: "unknown scope has no footholds", role: "Editor", scope: accesstypes.DomainScope("tenant9"), want: false},
		{name: "unknown role has no footholds", role: "Ghost", scope: tenant1Scope, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := snap.roleHasGrants(tt.scope, tt.role); got != tt.want {
				t.Errorf("roleHasGrants(%s, %s) = %v, want %v", tt.scope, tt.role, got, tt.want)
			}
		})
	}
}

// Test_snapshot_roleDigest pins the digest for a session operating as a role:
// the role's own and inherited grants enumerate exactly as a member's would —
// granted beating conditional, all-fields grants listing the known
// vocabulary, scope-wide grants outside the digest — and a member's direct
// grants never appear.
func Test_snapshot_roleDigest(t *testing.T) {
	t.Parallel()

	snap := compileSnapshot(t, &policy.Records{
		Grants: []policy.Grant{
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Read", Resource: "employees"},
			{Scope: tenant1Policy, Subject: roleSubject("Editor"), Perm: "Update", Resource: "employees", Field: "name", Condition: "status = 'open'"},
			{Scope: tenant1Policy, Subject: roleSubject("Chief"), Perm: "Update", Resource: "employees", Field: "name"},
			{Scope: tenant1Policy, Subject: roleSubject("Chief"), Perm: "List", Resource: "widgets", Field: "*"},
			{Scope: tenant1Policy, Subject: roleSubject("Auditor"), Perm: "Read", Resource: "widgets", Field: "label"},
			{Scope: tenant1Policy, Subject: roleSubject("Pinger"), Perm: "Ping"},
			{Scope: tenant1Policy, Subject: userSubject("erin"), Perm: "Delete", Resource: "employees"},
		},
		Memberships: []policy.Membership{
			{Scope: tenant1Policy, Member: roleSubject("Lead"), Role: "Editor"},
			{Scope: tenant1Policy, Member: roleSubject("Lead"), Role: "Chief"},
			{Scope: tenant1Policy, Member: userSubject("erin"), Role: "Editor"},
		},
	})

	tests := []struct {
		name  string
		role  accesstypes.Role
		scope accesstypes.Scope
		want  accesstypes.PermissionDigest
	}{
		{
			name:  "own grants enumerate with the conditional tri-state; a member's direct grant never appears",
			role:  "Editor",
			scope: tenant1Scope,
			want: accesstypes.PermissionDigest{
				"employees":      {"Read": accesstypes.DigestGranted},
				"employees.name": {"Update": accesstypes.DigestConditional},
			},
		},
		{
			name:  "an inherited unconditional cover settles the entry granted and all-fields lists the known vocabulary",
			role:  "Lead",
			scope: tenant1Scope,
			want: accesstypes.PermissionDigest{
				"employees":      {"Read": accesstypes.DigestGranted},
				"employees.name": {"Update": accesstypes.DigestGranted},
				"widgets.label":  {"List": accesstypes.DigestGranted},
			},
		},
		{
			name:  "a scope-wide grant attaches to no resource and is not enumerated",
			role:  "Pinger",
			scope: tenant1Scope,
			want:  accesstypes.PermissionDigest{},
		},
		{
			name:  "grants in another scope do not carry over",
			role:  "Editor",
			scope: tenant2Scope,
			want:  accesstypes.PermissionDigest{},
		},
		{
			name:  "an unknown role holds nothing",
			role:  "Ghost",
			scope: tenant1Scope,
			want:  accesstypes.PermissionDigest{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := snap.roleDigest(tt.scope, tt.role)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("roleDigest(%s, %s) mismatch (-want +got):\n%s", tt.scope, tt.role, diff)
			}
		})
	}
}

// Test_snapshotEngine_reportsSkippedGrants: a grant the release cannot use
// does not stop the load. The engine becomes ready, the other grants hold, the
// skipped grant is denied, and the reload-error hook receives one
// *SkippedGrant per compiled snapshot: not again on a reload that finds the
// store unchanged, again when the store changed and the snapshot recompiled.
func Test_snapshotEngine_reportsSkippedGrants(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := engineFakeStore(t)
	if err := store.InsertGrant(ctx, tenant1Policy, "Editor", "Read", "widgets", "", "state = "); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}

	var mu sync.Mutex
	var reports []error
	opts := defaultClientOptions()
	opts.onReloadError = func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, err)
	}
	e := testEngine(t, store, opts)

	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := e.waitReady(readyCtx); err != nil {
		t.Fatalf("waitReady() error = %v, want ready despite the bad grant", err)
	}
	settle(e)

	if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "Read", "employees", "widgets"); err != nil || !got[0].granted || got[1].granted || len(got[1].conditions) != 0 {
		t.Fatalf("checkUserResources() = (%+v, %v), want employees granted and widgets denied", got, err)
	}

	skippedReports := func() []*SkippedGrant {
		mu.Lock()
		defer mu.Unlock()
		var out []*SkippedGrant
		for _, err := range reports {
			var sg *SkippedGrant
			if errors.As(err, &sg) {
				out = append(out, sg)
			}
		}

		return out
	}

	got := skippedReports()
	if len(got) != 1 {
		t.Fatalf("reports = %v, want one SkippedGrant", reports)
	}
	want := SkippedGrant{Scope: tenant1Policy, Subject: "role Editor", Permission: "Read", Resource: "widgets", Condition: "state = "}
	if got[0].Scope != want.Scope || got[0].Subject != want.Subject || got[0].Permission != want.Permission || got[0].Resource != want.Resource || got[0].Condition != want.Condition || got[0].Reason == nil {
		t.Errorf("SkippedGrant = %+v, want %+v with a reason", got[0], want)
	}

	// An unchanged store does not recompile, so nothing is reported again.
	settle(e)
	if got := skippedReports(); len(got) != 1 {
		t.Errorf("after a reload of an unchanged store: %d reports, want still 1", len(got))
	}

	// A changed store recompiles and reports the grant once more.
	grantWidgets(t, store)
	e.invalidate()
	if _, err := e.checkUserResources(ctx, "erin", tenant1Scope, "Read", "employees"); err != nil {
		t.Fatalf("checkUserResources() error = %v", err)
	}
	if got := skippedReports(); len(got) != 2 {
		t.Errorf("after a recompile: %d reports, want 2", len(got))
	}
}

// Test_logReloadError pins the default hook: a skipped grant reaches the
// standard log, prefixed so the line is found by its package.
func Test_logReloadError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	defaultClientOptions().onReloadError(&SkippedGrant{
		Scope:      tenant1Policy,
		Subject:    "role Chief",
		Permission: "Read",
		Resource:   "Widgets.price",
		Condition:  "state = ",
		Reason:     errors.New("the condition does not parse"),
	})

	for _, want := range []string{"access: skipped a grant this release cannot use, so it is denied", "role Chief", "Read on Widgets.price", `under condition "state = "`, "the condition does not parse"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log = %q, want it to contain %q", buf.String(), want)
		}
	}
}
