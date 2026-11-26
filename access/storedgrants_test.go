package access

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// storedGrantsRelease is the permission collection of a release, as the generator
// writes one: a record of a domain's whose status a condition may read, and an action
// held globally.
func storedGrantsRelease(t *testing.T) PermissionCollection {
	t.Helper()

	return generated(t, &resource.CollectionData{
		Resources: []resource.CollectionResource{
			{
				Name:        "Documents",
				Scope:       accesstypes.DomainPermissionScope,
				Permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.Read, accesstypes.Update},
				Attributes:  []resource.AttributeData{{Name: "status", Column: "Status", Type: resource.AttributeTypeString}},
			},
			{Name: "DoThing", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}},
		},
	})
}

// TestClient_storedGrantIsHeldToTheWritesRule pins that a load of the policy holds a
// role's stored grant on a resource to the whole of the rule a write of the same grant
// is held to, past the names: the grant is of the role's own scope, and its condition
// reads what the release declares in a way the permission can be judged by. The rows
// are ones a store can hold that this release would not write: written by an earlier
// release, or straight to the store.
//
// A row the write would refuse is skipped: its holder is not given the permission, the
// grant alone is no foothold where the role is held, the deploy check lists it and the
// reload hook is told, in the write's own reason, and the role's listing
// (UserManager.RoleGrants) marks the row skipped with that reason. A row the write
// would make is carried, and listed as held. Each case writes the same grant through
// the user manager too, so the load and the write are seen to draw the line in one
// place.
func TestClient_storedGrantIsHeldToTheWritesRule(t *testing.T) {
	t.Parallel()

	const clerk accesstypes.Role = "Clerk"
	tests := []struct {
		name string
		// held is where the role is held, and asked where a check of it is made.
		held  accesstypes.PolicyScope
		asked accesstypes.Scope
		perm  accesstypes.Permission
		res   accesstypes.Resource
		cond  string
		// wantReason is why the load skips the row; empty when the row is carried.
		wantReason string
	}{
		{
			name: "a domain's role given a global action", held: tenant1Policy, asked: tenant1Scope,
			perm: accesstypes.Execute, res: "DoThing",
			wantReason: "role Clerk is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role",
		},
		{
			name: "a global role given a domain's record", held: globalPolicy, asked: accesstypes.GlobalScope(),
			perm: accesstypes.Read, res: "Documents",
			wantReason: "role Clerk is a global role but grants Read on Documents, a domain-scoped resource — a role's grants live at its declared scope; move the grant to a domain role",
		},
		{
			name: "Create under a condition that compares the proposed row with the stored one", held: tenant1Policy, asked: tenant1Scope,
			perm: accesstypes.Create, res: "Documents", cond: "new.status = status",
			wantReason: "condition compares the post-image against the row's own attributes (old-vs-new), which only update mutations can evaluate — both images exist only there",
		},
		{
			name: "a condition that reads an attribute the record does not declare", held: tenant1Policy, asked: tenant1Scope,
			perm: accesstypes.Read, res: "Documents", cond: "ghost = 'open'",
			wantReason: `condition references "ghost", which is not an attribute of Documents`,
		},
		{
			name: "Update under the same comparison is carried", held: tenant1Policy, asked: tenant1Scope,
			perm: accesstypes.Update, res: "Documents", cond: "new.status = status",
		},
		{
			name: "a condition that reads what the record declares is carried", held: tenant1Policy, asked: tenant1Scope,
			perm: accesstypes.Read, res: "Documents", cond: "status = 'open'",
		},
		{
			name: "a global role given a global action is carried", held: globalPolicy, asked: accesstypes.GlobalScope(),
			perm: accesstypes.Execute, res: "DoThing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			wantHeld := tt.wantReason == ""

			store := newFakeStore()
			for _, err := range []error{
				store.InsertRole(ctx, tt.held, clerk, aDescription),
				store.InsertGrant(ctx, tt.held, clerk, tt.perm, string(tt.res), "", tt.cond),
				store.InsertUserRole(ctx, tt.held, "dana", clerk, accesstypes.Period{}, ""),
			} {
				if err != nil {
					t.Fatalf("seeding fake store: %v", err)
				}
			}
			hook := &reloadErrors{}
			client := newPolicyClient(t, store, storedGrantsRelease(t), WithReloadErrorHandler(hook.collect))

			foothold, err := client.UserHasGrants(ctx, "dana", tt.asked)
			if err != nil {
				t.Fatalf("UserHasGrants() error = %v", err)
			}
			if foothold != wantHeld {
				t.Errorf("UserHasGrants() = %v, want %v", foothold, wantHeld)
			}
			decisions, err := client.CheckUserResources(ctx, accesstypes.NewEnvironment(), "dana", tt.asked, tt.perm, tt.res)
			if err != nil {
				t.Fatalf("CheckUserResources() error = %v", err)
			}
			if got := !decisions[tt.res].IsDenied(); got != wantHeld {
				t.Errorf("CheckUserResources(%s, %s) = %s, want held %v", tt.perm, tt.res, decisions[tt.res], wantHeld)
			}

			var want []string
			if !wantHeld {
				target := "on " + string(tt.res)
				if tt.cond != "" {
					target += ` under condition "` + tt.cond + `"`
				}
				want = []string{"skipped a grant this release cannot use, so it is denied: role Clerk in scope " + tt.held.String() + ", " + string(tt.perm) + " " + target + ": " + tt.wantReason}
			}
			warnings, err := client.CheckPolicy(ctx)
			if err != nil {
				t.Fatalf("CheckPolicy() error = %v", err)
			}
			var listed []string
			for _, warning := range warnings {
				listed = append(listed, warning.String())
			}
			if diff := cmp.Diff(want, listed); diff != "" {
				t.Errorf("CheckPolicy() mismatch (-want +got):\n%s", diff)
			}
			settle(client.snapEngine)
			var reported []string
			for _, err := range hook.list() {
				var skipped *SkippedGrant
				if !errors.As(err, &skipped) {
					t.Errorf("the hook received %v, want nothing but a skipped grant", err)

					continue
				}
				reported = append(reported, skipped.Error())
			}
			if diff := cmp.Diff(want, reported); diff != "" {
				t.Errorf("the hook's reports mismatch (-want +got):\n%s", diff)
			}

			// The role's listing holds the row either way, and says of a row the load
			// leaves out why the role does not hold it, in the load's reason.
			grants, err := client.UserManager().RoleGrants(ctx, tt.held, clerk)
			if err != nil {
				t.Fatalf("RoleGrants() error = %v", err)
			}
			wantGrants := map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant{
				tt.perm: {tt.res: {{Condition: tt.cond, Skipped: tt.wantReason}}},
			}
			if diff := cmp.Diff(wantGrants, grants); diff != "" {
				t.Errorf("RoleGrants() mismatch (-want +got):\n%s", diff)
			}

			// The write of the same grant, to a role of the same name held in the
			// same place in a store that holds nothing else.
			manager := newPolicyClient(t, newFakeStore(), storedGrantsRelease(t)).UserManager()
			if err := manager.AddRole(ctx, tt.held, clerk, aDescription); err != nil {
				t.Fatalf("AddRole() error = %v", err)
			}
			err = manager.AddRoleGrant(ctx, tt.held, clerk, tt.perm, tt.res, tt.cond)
			switch {
			case wantHeld && err != nil:
				t.Errorf("AddRoleGrant() error = %v, want the grant the load carries written", err)
			case !wantHeld && !httpio.HasBadRequest(err):
				t.Errorf("AddRoleGrant() error = %v, want the grant the load skips refused as a bad request", err)
			case !wantHeld && !strings.HasSuffix(httpio.Message(err), tt.wantReason):
				t.Errorf("AddRoleGrant() refusal = %q, want it to end in the load's reason %q", httpio.Message(err), tt.wantReason)
			}
		})
	}
}

// TestUserManager_RoleGrants_saysTheLoadsReason pins that a role's listing says of a
// stored row the load leaves out what the load reports for it, word for word, where the
// load reads a condition its own way: a condition that does not parse. The row is
// listed beside one the role holds on the same resource, the two in the order of their
// conditions, and only the row the load leaves out is marked.
func TestUserManager_RoleGrants_saysTheLoadsReason(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const clerk accesstypes.Role = "Clerk"
	const (
		unparsed = "status ="
		open     = "status = 'open'"
	)
	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, tenant1Policy, clerk, aDescription),
		store.InsertGrant(ctx, tenant1Policy, clerk, accesstypes.Read, "Documents", "", open),
		store.InsertGrant(ctx, tenant1Policy, clerk, accesstypes.Read, "Documents", "", unparsed),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}
	hook := &reloadErrors{}
	client := newPolicyClient(t, store, storedGrantsRelease(t), WithReloadErrorHandler(hook.collect))
	settle(client.snapEngine)

	var reasons []string
	for _, err := range hook.list() {
		var skipped *SkippedGrant
		if !errors.As(err, &skipped) {
			t.Fatalf("the hook received %v, want nothing but a skipped grant", err)
		}
		reasons = append(reasons, plainText(skipped.Reason))
	}
	if len(reasons) != 1 || !strings.HasPrefix(reasons[0], "the condition does not parse: ") {
		t.Fatalf("the load reported %q, want the one row whose condition does not parse", reasons)
	}

	got, err := client.UserManager().RoleGrants(ctx, tenant1Policy, clerk)
	if err != nil {
		t.Fatalf("RoleGrants() error = %v", err)
	}
	want := map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant{
		accesstypes.Read: {"Documents": {{Condition: unparsed, Skipped: reasons[0]}, {Condition: open}}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RoleGrants() mismatch (-want +got):\n%s", diff)
	}
}

// TestUserManager_RoleGrants_ofARoleHeldInEveryDomain pins that a custom role held in
// every domain lists its grants when it is asked for in one tenant: the rows are read
// where the role is held, and the one a load leaves out is marked with the load's
// reason, as it is when the role is asked for in every domain.
func TestUserManager_RoleGrants_ofARoleHeldInEveryDomain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const clerk accesstypes.Role = "Clerk"
	const open = "status = 'open'"
	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, everyDomainPolicy, clerk, aDescription),
		store.InsertGrant(ctx, everyDomainPolicy, clerk, accesstypes.Read, "Documents", "", open),
		store.InsertGrant(ctx, everyDomainPolicy, clerk, accesstypes.Execute, "DoThing", "", ""),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}
	hook := &reloadErrors{}
	client := newPolicyClient(t, store, storedGrantsRelease(t), WithReloadErrorHandler(hook.collect))
	settle(client.snapEngine)

	var skipped []*SkippedGrant
	for _, err := range hook.list() {
		var grant *SkippedGrant
		if !errors.As(err, &grant) {
			t.Fatalf("the hook received %v, want nothing but a skipped grant", err)
		}
		skipped = append(skipped, grant)
	}
	if len(skipped) != 1 || skipped[0].Scope != everyDomainPolicy || skipped[0].Permission != accesstypes.Execute || skipped[0].Resource != "DoThing" {
		t.Fatalf("the load reported %v, want the one DoThing row, in every domain", skipped)
	}
	reason := plainText(skipped[0].Reason)
	const wantReason = "role Clerk is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role"
	if reason != wantReason {
		t.Errorf("the load's reason = %q, want %q", reason, wantReason)
	}

	want := map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant{
		accesstypes.Read:    {"Documents": {{Condition: open}}},
		accesstypes.Execute: {"DoThing": {{Skipped: reason}}},
	}
	for _, scope := range []accesstypes.PolicyScope{tenant1Policy, everyDomainPolicy} {
		got, err := client.UserManager().RoleGrants(ctx, scope, clerk)
		if err != nil {
			t.Fatalf("RoleGrants(%s) error = %v", scope, err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("RoleGrants(%s) mismatch (-want +got):\n%s", scope, diff)
		}
	}
}

// Test_newSnapshot_holdsARolesStoredGrantsToTheWritesRule pins who the write's rule is
// asked of at a load. A grant given straight to a person names no role, so there is no
// role's scope to hold it to and it is held to its names alone; a grant for the whole
// of a scope names no resource, so it is held to its permission and to a condition
// a scope-wide check can settle; and a role the release declares was held to the rule when the
// role file was read, so its grants are not asked again. What a role holds on a
// resource in the store is asked the whole rule, and the reason says which part broke,
// the names before the place and the place before the condition.
func Test_newSnapshot_holdsARolesStoredGrantsToTheWritesRule(t *testing.T) {
	t.Parallel()

	clerk := roleSubject("Clerk")
	tests := []struct {
		name  string
		grant policy.Grant
		// wantReason is why the load skips the grant; empty when it is kept.
		wantReason string
	}{
		{
			name:  "a person's own grant on a resource of the other scope is kept",
			grant: policy.Grant{Scope: tenant1Policy, Subject: userSubject("dana"), Perm: "Execute", Resource: "DoThing"},
		},
		{
			name:       "a role's grant on a resource of the other scope",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: clerk, Perm: "Execute", Resource: "DoThing"},
			wantReason: "role Clerk is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role",
		},
		{
			name:       "a role held in every domain given a global action",
			grant:      policy.Grant{Scope: everyDomainPolicy, Subject: clerk, Perm: "Execute", Resource: "DoThing"},
			wantReason: "role Clerk is a domain role but grants Execute on DoThing, a global-scoped resource — a role's grants live at its declared scope; move the grant to a global role",
		},
		{
			name:  "a role held in every domain given a domain's record is kept",
			grant: policy.Grant{Scope: everyDomainPolicy, Subject: clerk, Perm: "Read", Resource: "Widgets", Field: "*"},
		},
		{
			name:       "a field's condition is held to its resource's attributes",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: clerk, Perm: "Read", Resource: "Widgets", Field: "price", Condition: "ghost = 'open'"},
			wantReason: `condition references "ghost", which is not an attribute of Widgets`,
		},
		{
			name:       "a condition that compares an attribute with a literal of another type",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: clerk, Perm: "Read", Resource: "Widgets", Condition: "price = 'cheap'"},
			wantReason: "price",
		},
		{
			name:       "a misplaced grant under a condition that does not parse: one report, the place first",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: clerk, Perm: "Execute", Resource: "DoThing", Condition: "owner = "},
			wantReason: "role Clerk is a domain role but grants Execute on DoThing",
		},
		{
			name:       "a condition that does not parse is said in the load's own words",
			grant:      policy.Grant{Scope: tenant1Policy, Subject: clerk, Perm: "Read", Resource: "Widgets", Condition: "owner = "},
			wantReason: "the condition does not parse",
		},
		{
			name:  "a grant for the whole of a scope is held to its permission",
			grant: policy.Grant{Scope: tenant1Policy, Subject: clerk, Perm: "Execute", Resource: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			records := &policy.Records{
				Roles:  []policy.Role{{Scope: tt.grant.Scope, Name: "Clerk"}},
				Grants: []policy.Grant{tt.grant},
			}
			_, findings, err := newSnapshot(records, nil, grammarCollection{}, time.Now())
			if err != nil {
				t.Fatalf("newSnapshot() error = %v, want the load to complete", err)
			}
			if tt.wantReason == "" {
				if len(findings) != 0 {
					t.Fatalf("newSnapshot() findings = %v, want the grant kept", findings)
				}

				return
			}
			if len(findings) != 1 {
				t.Fatalf("newSnapshot() findings = %v, want the one grant skipped", findings)
			}
			var skipped *SkippedGrant
			if !errors.As(findings[0], &skipped) {
				t.Fatalf("newSnapshot() finding = %v, want a skipped grant", findings[0])
			}
			if reason := plainText(skipped.Reason); !strings.Contains(reason, tt.wantReason) {
				t.Errorf("the skip's reason = %q, want containing %q", reason, tt.wantReason)
			}
		})
	}
}

// TestClient_oneBadStoredConditionLeavesTheOtherGrants pins what a person holds on a
// record when one of two roles carries a stored condition that reads an attribute the
// release does not declare: the other role's condition still decides. The bad row is
// skipped at the load and reported, so the decision carries the sound condition alone
// and nothing that is asked to judge it meets a name it cannot resolve. Before the
// load held a stored condition to what the release declares, the decision carried
// both, and every read of the record failed on the one that could not be judged.
func TestClient_oneBadStoredConditionLeavesTheOtherGrants(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, tenant1Policy, "Broken", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Broken", accesstypes.Read, "Documents", "", "ghost = 'open'"),
		store.InsertRole(ctx, tenant1Policy, "Sound", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Sound", accesstypes.Read, "Documents", "", "status = 'open'"),
		store.InsertUserRole(ctx, tenant1Policy, "dana", "Broken", accesstypes.Period{}, ""),
		store.InsertUserRole(ctx, tenant1Policy, "dana", "Sound", accesstypes.Period{}, ""),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}
	client := newPolicyClient(t, store, storedGrantsRelease(t))

	decisions, err := client.CheckUserResources(ctx, accesstypes.NewEnvironment(), "dana", tenant1Scope, accesstypes.Read, "Documents")
	if err != nil {
		t.Fatalf("CheckUserResources() error = %v", err)
	}
	decision := decisions["Documents"]
	if !decision.IsConditional() {
		t.Fatalf("CheckUserResources(Read, Documents) = %s, want it decided by the sound role's condition", decision)
	}
	var read []string
	for _, group := range decision.ConditionGroups() {
		read = append(read, condition.Bindings(group.Condition.Expr())...)
	}
	slices.Sort(read)
	if diff := cmp.Diff([]string{"status"}, slices.Compact(read)); diff != "" {
		t.Errorf("the attributes the decision reads mismatch (-want +got):\n%s", diff)
	}

	warnings, err := client.CheckPolicy(ctx)
	if err != nil {
		t.Fatalf("CheckPolicy() error = %v", err)
	}
	var listed []string
	for _, warning := range warnings {
		listed = append(listed, warning.String())
	}
	want := []string{`skipped a grant this release cannot use, so it is denied: role Broken in scope tenant1, Read on Documents under condition "ghost = 'open'": condition references "ghost", which is not an attribute of Documents`}
	if diff := cmp.Diff(want, listed); diff != "" {
		t.Errorf("CheckPolicy() mismatch (-want +got):\n%s", diff)
	}
}

// TestClient_oneUnsettledScopeWideConditionLeavesTheOtherGrants pins what a person holds
// for the whole of a tenant when one of two roles carries a stored scope-wide condition
// no scope-wide check could settle, an instant that is not RFC 3339: the other role's
// condition still decides. The bad row is skipped at the load and reported, so it gives
// nobody the permission, is no foothold for a person who holds that role alone, and
// fails no check. Before the load held a scope-wide condition to what a check can
// settle, the row was kept, and every check of the permission by a holder of the role
// was a server error, the sound role's holder's included.
func TestClient_oneUnsettledScopeWideConditionLeavesTheOtherGrants(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const exportLedger accesstypes.Permission = "ExportLedger"
	release := generated(t, &resource.CollectionData{
		Permissions: []accesstypes.Permission{exportLedger},
		Resources: []resource.CollectionResource{
			{Name: "Documents", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
		},
	})
	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, tenant1Policy, "Broken", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Broken", exportLedger, "", "", "now >= '2026'"),
		store.InsertRole(ctx, tenant1Policy, "Sound", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Sound", exportLedger, "", "", "now >= '2026-01-01T00:00:00Z'"),
		store.InsertUserRole(ctx, tenant1Policy, "dana", "Broken", accesstypes.Period{}, ""),
		store.InsertUserRole(ctx, tenant1Policy, "dana", "Sound", accesstypes.Period{}, ""),
		store.InsertUserRole(ctx, tenant1Policy, "erin", "Broken", accesstypes.Period{}, ""),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}
	client := newPolicyClient(t, store, release)
	env := accesstypes.EnvironmentAt(time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC))

	decision, err := client.CheckUser(ctx, env, "dana", tenant1Scope, exportLedger)
	if err != nil {
		t.Fatalf("CheckUser(dana) error = %v, want the sound role to decide", err)
	}
	if !decision.IsGranted() {
		t.Errorf("CheckUser(dana) = %s, want Granted by the sound role", decision)
	}
	decision, err = client.CheckUser(ctx, env, "erin", tenant1Scope, exportLedger)
	if err != nil {
		t.Fatalf("CheckUser(erin) error = %v, want a denial as for a person with no grant", err)
	}
	if !decision.IsDenied() {
		t.Errorf("CheckUser(erin) = %s, want Denied", decision)
	}
	foothold, err := client.UserHasGrants(ctx, "erin", tenant1Scope)
	if err != nil {
		t.Fatalf("UserHasGrants(erin) error = %v", err)
	}
	if foothold {
		t.Error("UserHasGrants(erin) = true, want the skipped row to be no foothold")
	}
	decision, err = client.CheckRole(ctx, env, "Broken", tenant1Scope, exportLedger)
	if err != nil {
		t.Fatalf("CheckRole(Broken) error = %v, want a denial", err)
	}
	if !decision.IsDenied() {
		t.Errorf("CheckRole(Broken) = %s, want Denied", decision)
	}

	warnings, err := client.CheckPolicy(ctx)
	if err != nil {
		t.Fatalf("CheckPolicy() error = %v", err)
	}
	var listed []string
	for _, warning := range warnings {
		listed = append(listed, warning.String())
	}
	want := []string{`skipped a grant this release cannot use, so it is denied: role Broken in scope tenant1, ExportLedger scope-wide under condition "now >= '2026'": now is the time of the request; "2026" is not an RFC 3339 timestamp`}
	if diff := cmp.Diff(want, listed); diff != "" {
		t.Errorf("CheckPolicy() mismatch (-want +got):\n%s", diff)
	}
}

// TestUserManager_SkippedStoredGrants pins that the engine says of the rows of the grants
// table a caller read what a load of the policy says of them, in the order given: nothing
// for a row its role holds, the load's own reason word for word for a row the load leaves
// out, and the shadowed role's reason for a row stored under the name of a role the
// release's role file defines. A row whose columns spell no scope a store writes is an
// error, and no rows answer nothing.
func TestUserManager_SkippedStoredGrants(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const clerk accesstypes.Role = "Clerk"
	const (
		unparsed = "status ="
		open     = "status = 'open'"
	)
	kind, axis, domain := policy.ScopeColumns(tenant1Policy)
	row := func(role accesstypes.Role, perm accesstypes.Permission, condition string) StoredGrant {
		return StoredGrant{Kind: kind, Axis: axis, Domain: domain, Role: role, Permission: perm, Resource: "Documents", Condition: condition}
	}

	t.Run("the load's reason for the row it leaves out, nothing for the rows the role holds", func(t *testing.T) {
		t.Parallel()

		store := newFakeStore()
		for _, err := range []error{
			store.InsertRole(ctx, tenant1Policy, clerk, aDescription),
			store.InsertGrant(ctx, tenant1Policy, clerk, accesstypes.Read, "Documents", "", open),
			store.InsertGrant(ctx, tenant1Policy, clerk, accesstypes.Read, "Documents", "", unparsed),
			store.InsertGrant(ctx, tenant1Policy, clerk, accesstypes.Update, "Documents", "", ""),
		} {
			if err != nil {
				t.Fatalf("seeding fake store: %v", err)
			}
		}
		hook := &reloadErrors{}
		client := newPolicyClient(t, store, storedGrantsRelease(t), WithReloadErrorHandler(hook.collect))
		settle(client.snapEngine)

		var reasons []string
		for _, err := range hook.list() {
			var skipped *SkippedGrant
			if !errors.As(err, &skipped) {
				t.Fatalf("the hook received %v, want nothing but a skipped grant", err)
			}
			reasons = append(reasons, plainText(skipped.Reason))
		}
		if len(reasons) != 1 || !strings.HasPrefix(reasons[0], "the condition does not parse: ") {
			t.Fatalf("the load reported %q, want the one row whose condition does not parse", reasons)
		}

		got, err := client.UserManager().SkippedStoredGrants([]StoredGrant{
			row(clerk, accesstypes.Read, open),
			row(clerk, accesstypes.Read, unparsed),
			row(clerk, accesstypes.Update, ""),
		})
		if err != nil {
			t.Fatalf("SkippedStoredGrants() error = %v", err)
		}
		if diff := cmp.Diff([]string{"", reasons[0], ""}, got); diff != "" {
			t.Errorf("SkippedStoredGrants() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a row under the name of a role the role file defines is left out as the shadowed role is", func(t *testing.T) {
		t.Parallel()

		const auditor accesstypes.Role = "Auditor"
		roleFile := RoleFile(`{
  "auth": "crew",
  "roles": {
    "domain": [
      {"name": "Auditor", "description": "Reads the documents.", "permissions": {"Read": [{"resource": "Documents"}]}}
    ]
  }
}`)
		client := newPolicyClient(t, newFakeStore(), storedGrantsRelease(t), WithDefaultRoles(roleFile, fixtureAuth))

		got, err := client.UserManager().SkippedStoredGrants([]StoredGrant{row(auditor, accesstypes.Read, "")})
		if err != nil {
			t.Fatalf("SkippedStoredGrants() error = %v", err)
		}
		if len(got) != 1 || got[0] == "" {
			t.Fatalf("SkippedStoredGrants() = %q, want the one row left out", got)
		}
		// The words are the load's report of the role, after its colon, so the two
		// cannot drift.
		if said := (&ShadowedRole{Scope: tenant1Policy, Role: auditor}).Error(); !strings.HasSuffix(said, ": "+got[0]) {
			t.Errorf("SkippedStoredGrants() = %q, want the words the load reports the role in after its colon: %q", got[0], said)
		}
	})

	t.Run("a row whose columns spell no scope a store writes is an error", func(t *testing.T) {
		t.Parallel()

		client := newPolicyClient(t, newFakeStore(), storedGrantsRelease(t))
		nowhere := row(clerk, accesstypes.Read, "")
		nowhere.Kind = "nowhere"

		got, err := client.UserManager().SkippedStoredGrants([]StoredGrant{row(clerk, accesstypes.Read, ""), nowhere})
		if err == nil {
			t.Fatalf("SkippedStoredGrants() = %q, want an error", got)
		}
		if got != nil {
			t.Errorf("SkippedStoredGrants() = %q beside its error, want no answer", got)
		}
	})

	t.Run("no rows answer nothing", func(t *testing.T) {
		t.Parallel()

		client := newPolicyClient(t, newFakeStore(), storedGrantsRelease(t))
		got, err := client.UserManager().SkippedStoredGrants(nil)
		if err != nil {
			t.Fatalf("SkippedStoredGrants() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("SkippedStoredGrants() = %q, want no strings", got)
		}
	})
}
