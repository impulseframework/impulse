package access

// These tests pin the release's policy as it enters the Client: the role
// file's parse rules, what New refuses and what a Client built with
// WithDefaultRoles answers, and Client.CheckPolicy — the deploy's read-only
// look at the role file's warnings and at what the store holds that this
// release cannot use as written — which the engine reports through the
// reload-error hook as well.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// grammarRoleFile is a role file over grammarCollection: Steward executes the
// global method, Reader reads Widgets and its name field.
var grammarRoleFile = RoleFile(`{
  "roles": {
    "global": [
      {"name": "Steward", "description": "Runs the global method.", "permissions": {"Execute": [{"resource": "DoThing"}]}}
    ],
    "domain": [
      {"name": "Reader", "description": "Reads widgets and their names.", "permissions": {"Read": [{"resource": "Widgets", "fields": ["name"]}]}}
    ]
  }
}`)

// probeRoleFile is a role file over probeCollection whose one domain role,
// Paymaster, holds a conditional Delete on Missions without a Read or List
// on it: the shape GrantWarning flags.
var probeRoleFile = RoleFile(`{
  "roles": {
    "domain": [
      {"name": "Paymaster", "description": "Settles the open missions.", "permissions": {"Delete": [{"resource": "Missions", "condition": "state = 'open'"}]}}
    ]
  }
}`)

// paymasterWarning is the warning probeRoleFile raises.
var paymasterWarning = GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"}

// newPolicyClient builds a Client over store with the options, failing the
// test when New refuses, and closes it at test end.
func newPolicyClient(t *testing.T, store Store, opts ...Option) *Client {
	t.Helper()
	client, err := New(store, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	return client
}

// TestRoleFile_Parse pins the file's parse rules: the RoleConfig shape, an
// empty file and malformed JSON refused, a key the shape does not declare
// refused at any depth, so a misspelled key cannot silently drop roles — and
// the shape rules a tool checks with no permission collection: every role
// name in the grammar, every role described, every grant well-formed with a
// condition that parses.
func TestRoleFile_Parse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    RoleFile
		want    *RoleConfig
		wantErr string
	}{
		{
			name: "a valid file parses into its configuration",
			file: grammarRoleFile,
			want: &RoleConfig{Roles: ScopedRoles{
				Global: []*Role{{Name: "Steward", Description: "Runs the global method.", Permissions: map[accesstypes.Permission][]Grant{
					"Execute": {{Resource: "DoThing"}},
				}}},
				Domain: []*Role{{Name: "Reader", Description: "Reads widgets and their names.", Permissions: map[accesstypes.Permission][]Grant{
					"Read": {{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}}},
				}}},
			}},
		},
		{
			name: "a condition parses with its grant",
			file: probeRoleFile,
			want: &RoleConfig{Roles: ScopedRoles{
				Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
					"Delete": {{Resource: "Missions", Condition: "state = 'open'"}},
				}}},
			}},
		},
		{name: "an empty file is refused", file: RoleFile(""), wantErr: "empty"},
		{name: "a whitespace-only file is refused", file: RoleFile(" \n\t\n"), wantErr: "empty"},
		{name: "malformed JSON is refused", file: RoleFile(`{"roles": {"domain": [`), wantErr: "does not parse"},
		{name: "an unknown top-level key is refused", file: RoleFile(`{"roles": {}, "extras": []}`), wantErr: "unknown field"},
		{name: "an unknown scope key is refused", file: RoleFile(`{"roles": {"tenant": []}}`), wantErr: "unknown field"},
		{name: "an unknown role key is refused", file: RoleFile(`{"roles": {"domain": [{"name": "Reader", "perms": {}}]}}`), wantErr: "unknown field"},
		{name: "an unknown grant key is refused", file: RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": "Reads widgets and their names.", "permissions": {"Read": [{"resource": "Widgets", "field": "name"}]}}]}}`), wantErr: "unknown field"},
		{
			name:    "a role name with an underscore is refused",
			file:    RoleFile(`{"roles": {"global": [{"name": "Administrator_Global", "description": "Administers everything.", "permissions": {}}]}}`),
			wantErr: "role Administrator_Global: a role name is letters and digits starting with a capital letter, such as Administrator or TenantAdministrator; the scope is never part of the name",
		},
		{
			name:    "a role name starting lowercase is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "reader", "description": "Reads widgets.", "permissions": {}}]}}`),
			wantErr: "role reader: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a role name with a space is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Tenant Administrator", "description": "Administers the tenant.", "permissions": {}}]}}`),
			wantErr: "role Tenant Administrator: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a role name with a dot is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader.Domain", "description": "Reads widgets.", "permissions": {}}]}}`),
			wantErr: "role Reader.Domain: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a role without a description is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader", "permissions": {}}]}}`),
			wantErr: "role Reader: a role carries a description, one or two plain sentences saying what the role is for",
		},
		{
			name:    "a whitespace description is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": " \n\t", "permissions": {}}]}}`),
			wantErr: "role Reader: a role carries a description",
		},
		{
			name:    "a grant naming no resource is refused",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": "Reads widgets.", "permissions": {"Read": [{"fields": ["name"]}]}}]}}`),
			wantErr: "role Reader: a Read grant names no resource",
		},
		{
			name:    "a dotted resource with fields is refused with no collection",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": "Reads widgets.", "permissions": {"Read": [{"resource": "Widgets.name", "fields": ["name"]}]}}]}}`),
			wantErr: "role Reader: grant on Widgets.name: a dotted field resource takes no Fields or Condition",
		},
		{
			name:    "a resource granted twice unconditionally is refused with no collection",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": "Reads widgets.", "permissions": {"Read": [{"resource": "Widgets"}, {"resource": "Widgets", "fields": ["name"]}]}}]}}`),
			wantErr: "role Reader: two unconditional Read grants on Widgets",
		},
		{
			name:    "a condition that does not parse is refused with no collection",
			file:    RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": "Reads widgets.", "permissions": {"Read": [{"resource": "Widgets", "condition": "owner = = subject"}]}}]}}`),
			wantErr: "role Reader: Read grant on Widgets",
		},
		{
			name: "a described role in the grammar with a condition that parses loads with no collection",
			file: RoleFile(`{"roles": {"domain": [{"name": "Tier2Reader", "description": "Reads the widgets it owns.", "permissions": {"Read": [{"resource": "Nowhere", "condition": "owner = subject"}]}}]}}`),
			want: &RoleConfig{Roles: ScopedRoles{
				Domain: []*Role{{Name: "Tier2Reader", Description: "Reads the widgets it owns.", Permissions: map[accesstypes.Permission][]Grant{
					"Read": {{Resource: "Nowhere", Condition: "owner = subject"}},
				}}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.file.Parse()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RoleFile.Parse() error = %v, want one containing %q", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("RoleFile.Parse() = %v beside an error, want nil", got)
				}

				return
			}
			if err != nil {
				t.Fatalf("RoleFile.Parse() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RoleFile.Parse() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNew_withDefaultRoles_refusals pins what New refuses with
// WithDefaultRoles: a nil collection, a file that does not parse, a file
// whose shape is wrong — a role name outside the grammar, a role with no
// description — and a file naming what the collection does not declare: a
// release whose default roles are wrong does not start.
func TestNew_withDefaultRoles_refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		collection PermissionCollection
		file       RoleFile
		wantErr    string
	}{
		{name: "a nil collection", collection: nil, file: grammarRoleFile, wantErr: "collection must not be nil"},
		{name: "a file that does not parse", collection: grammarCollection{}, file: RoleFile(`{"roles": `), wantErr: "does not parse"},
		{name: "an empty file", collection: grammarCollection{}, file: RoleFile(""), wantErr: "empty"},
		{
			name:       "a file naming a role outside the grammar",
			collection: grammarCollection{},
			file:       RoleFile(`{"roles": {"domain": [{"name": "reader", "description": "Reads widgets.", "permissions": {"Read": [{"resource": "Widgets"}]}}]}}`),
			wantErr:    "role reader: a role name is letters and digits starting with a capital letter",
		},
		{
			name:       "a file whose role has no description",
			collection: grammarCollection{},
			file:       RoleFile(`{"roles": {"domain": [{"name": "Reader", "permissions": {"Read": [{"resource": "Widgets"}]}}]}}`),
			wantErr:    "role Reader: a role carries a description",
		},
		{
			name:       "a file naming a resource the collection does not declare",
			collection: grammarCollection{},
			file:       RoleFile(`{"roles": {"domain": [{"name": "Reader", "description": "Reads widgets and their names.", "permissions": {"Read": [{"resource": "Nowhere"}]}}]}}`),
			wantErr:    "does not validate",
		},
		{
			name:       "a file granting a domain resource to a global role",
			collection: grammarCollection{},
			file:       RoleFile(`{"roles": {"global": [{"name": "Reader", "description": "Reads widgets and their names.", "permissions": {"Read": [{"resource": "Widgets"}]}}]}}`),
			wantErr:    "does not validate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), WithDefaultRoles(tt.collection, tt.file))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New() error = %v, want one containing %q", err, tt.wantErr)
			}
			if client != nil {
				t.Errorf("New() = %v beside an error, want nil", client)
			}
		})
	}
}

// TestClient_WithDefaultRoles pins a Client built over a valid role file: the
// manager sees the defaults without a store row, and the checks answer from
// them — a domain default held in every domain grants in any tenant scope, a
// global default held globally grants in the global scope, and neither
// reaches the other's partition.
func TestClient_WithDefaultRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	client := newPolicyClient(t, newFakeStore(), WithDefaultRoles(grammarCollection{}, grammarRoleFile))
	if err := client.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	manager := client.UserManager()
	for scope, role := range map[accesstypes.PolicyScope]accesstypes.Role{
		everyDomainPolicy: "Reader",
		globalPolicy:      "Steward",
	} {
		got, exists, err := manager.ResolveRole(ctx, scope, string(role))
		if err != nil {
			t.Fatalf("ResolveRole(%s, %s) error = %v", scope, role, err)
		}
		if !exists || got != role {
			t.Errorf("ResolveRole(%s, %s) = (%q, %v), want the default seen", scope, role, got, exists)
		}
	}
	if err := manager.AddRoleUsers(ctx, everyDomainPolicy, "Reader", "dana"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}
	if err := manager.AddRoleUsers(ctx, globalPolicy, "Steward", "gus"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	tests := []struct {
		name      string
		user      accesstypes.User
		scope     accesstypes.Scope
		perm      accesstypes.Permission
		resources []accesstypes.Resource
		// wantGranted lists the resources granted; the rest are denied.
		wantGranted []accesstypes.Resource
	}{
		{
			name: "an every-domain membership of a domain default grants in one tenant scope",
			user: "dana", scope: tenant1Scope, perm: "Read",
			resources:   []accesstypes.Resource{"Widgets", "Widgets.name", "Widgets.price"},
			wantGranted: []accesstypes.Resource{"Widgets", "Widgets.name"},
		},
		{
			name: "and in a tenant scope the store holds no row in",
			user: "dana", scope: neverSeenScope, perm: "Read",
			resources:   []accesstypes.Resource{"Widgets", "Widgets.name"},
			wantGranted: []accesstypes.Resource{"Widgets", "Widgets.name"},
		},
		{
			name: "a domain default grants nothing in the global scope",
			user: "dana", scope: accesstypes.GlobalScope(), perm: "Read",
			resources: []accesstypes.Resource{"Widgets"},
		},
		{
			name: "a global membership of a global default grants in the global scope",
			user: "gus", scope: accesstypes.GlobalScope(), perm: "Execute",
			resources:   []accesstypes.Resource{"DoThing"},
			wantGranted: []accesstypes.Resource{"DoThing"},
		},
		{
			name: "a global default grants nothing in a tenant scope",
			user: "gus", scope: tenant1Scope, perm: "Execute",
			resources: []accesstypes.Resource{"DoThing"},
		},
		{
			name: "a user holding no default is denied",
			user: "nobody", scope: tenant1Scope, perm: "Read",
			resources: []accesstypes.Resource{"Widgets"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decisions, err := client.CheckUserResources(t.Context(), accesstypes.NewEnvironment(), tt.user, tt.scope, tt.perm, tt.resources...)
			if err != nil {
				t.Fatalf("CheckUserResources() error = %v", err)
			}
			granted := make([]accesstypes.Resource, 0, len(tt.resources))
			for _, res := range tt.resources {
				if decisions[res].IsGranted() {
					granted = append(granted, res)
				}
			}
			if diff := cmp.Diff(tt.wantGranted, granted, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("CheckUserResources() granted (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_CheckPolicy pins what the deploy check reports and in what
// order: the role file's own warnings first, then what the store holds that
// this release cannot use as written — a grant it skips, a custom role a
// default of its kind shadows, memberships naming a role nothing defines —
// and that the check writes nothing.
func TestClient_CheckPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seed func(ctx context.Context, store *fakeStore) error
		want []Warning
		// wantText is one substring per warning, in the same order.
		wantText []string
	}{
		{
			name:     "an empty store reports the role file's warnings alone",
			want:     []Warning{paymasterWarning},
			wantText: []string{"without Read or List on Missions"},
		},
		{
			name: "a custom role a default of its kind shadows",
			seed: func(ctx context.Context, store *fakeStore) error {
				return store.InsertRole(ctx, tenant1Policy, "Paymaster", aDescription)
			},
			want:     []Warning{paymasterWarning, &ShadowedRole{Scope: tenant1Policy, Role: "Paymaster"}},
			wantText: []string{"without Read or List", "custom role Paymaster in scope tenant1"},
		},
		{
			name: "a custom role of a default's name in the other kind of scope is not shadowed",
			seed: func(ctx context.Context, store *fakeStore) error {
				return store.InsertRole(ctx, globalPolicy, "Paymaster", aDescription)
			},
			want:     []Warning{paymasterWarning},
			wantText: []string{"without Read or List"},
		},
		{
			name: "memberships naming a role nothing defines, the members sorted",
			seed: func(ctx context.Context, store *fakeStore) error {
				if err := store.InsertUserRole(ctx, tenant1Policy, "zed", "Ghost"); err != nil {
					return err
				}

				return store.InsertUserRole(ctx, tenant1Policy, "amy", "Ghost")
			},
			want:     []Warning{paymasterWarning, &OrphanedMembership{Scope: tenant1Policy, Role: "Ghost", Users: []accesstypes.User{"amy", "zed"}}},
			wantText: []string{"without Read or List", "role Ghost is held by 2 user(s) in scope tenant1"},
		},
		{
			name: "a membership of a default role resolves",
			seed: func(ctx context.Context, store *fakeStore) error {
				return store.InsertUserRole(ctx, everyDomainPolicy, "dana", "Paymaster")
			},
			want:     []Warning{paymasterWarning},
			wantText: []string{"without Read or List"},
		},
		{
			name: "a membership in one domain of a custom role held in every domain resolves",
			seed: func(ctx context.Context, store *fakeStore) error {
				if err := store.InsertRole(ctx, everyDomainPolicy, "Scout", aDescription); err != nil {
					return err
				}

				return store.InsertUserRole(ctx, tenant1Policy, "dana", "Scout")
			},
			want:     []Warning{paymasterWarning},
			wantText: []string{"without Read or List"},
		},
		{
			name: "a grant naming a resource the release does not declare",
			seed: func(ctx context.Context, store *fakeStore) error {
				if err := store.InsertRole(ctx, tenant1Policy, "Rogue", aDescription); err != nil {
					return err
				}

				return store.InsertGrant(ctx, tenant1Policy, "Rogue", "Read", "Nowhere", "", "")
			},
			want:     []Warning{paymasterWarning, &SkippedGrant{Scope: tenant1Policy, Subject: "role Rogue", Permission: "Read", Resource: "Nowhere"}},
			wantText: []string{"without Read or List", "role Rogue in scope tenant1, Read on Nowhere: "},
		},
		{
			name: "every kind at once, in report order",
			seed: func(ctx context.Context, store *fakeStore) error {
				for _, err := range []error{
					store.InsertUserRole(ctx, tenant1Policy, "zed", "Ghost"),
					store.InsertRole(ctx, tenant1Policy, "Paymaster", aDescription),
					store.InsertRole(ctx, tenant1Policy, "Rogue", aDescription),
					store.InsertGrant(ctx, tenant1Policy, "Rogue", "Read", "Nowhere", "", ""),
				} {
					if err != nil {
						return err
					}
				}

				return nil
			},
			want: []Warning{
				paymasterWarning,
				&SkippedGrant{Scope: tenant1Policy, Subject: "role Rogue", Permission: "Read", Resource: "Nowhere"},
				&ShadowedRole{Scope: tenant1Policy, Role: "Paymaster"},
				&OrphanedMembership{Scope: tenant1Policy, Role: "Ghost", Users: []accesstypes.User{"zed"}},
			},
			wantText: []string{"without Read or List", "skipped a grant", "skipped the grants of custom role Paymaster", "role Ghost is held by 1 user(s)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			store := newFakeStore()
			if tt.seed != nil {
				if err := tt.seed(ctx, store); err != nil {
					t.Fatalf("seeding fake store: %v", err)
				}
			}
			client := newPolicyClient(t, store, WithDefaultRoles(probeCollection{}, probeRoleFile))
			before, err := store.ReadPolicy(ctx)
			if err != nil {
				t.Fatalf("ReadPolicy() before error = %v", err)
			}
			roles, memberships, grants := len(store.roles), len(store.memberships), len(store.grants)

			got, err := client.CheckPolicy(ctx)
			if err != nil {
				t.Fatalf("CheckPolicy() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateComparable(accesstypes.PolicyScope{}), cmpopts.IgnoreFields(SkippedGrant{}, "Reason")); diff != "" {
				t.Errorf("CheckPolicy() (-want +got):\n%s", diff)
			}
			if len(got) != len(tt.wantText) {
				t.Fatalf("CheckPolicy() returned %d warnings, want %d", len(got), len(tt.wantText))
			}
			for i, w := range got {
				if !strings.Contains(w.String(), tt.wantText[i]) {
					t.Errorf("warning %d = %q, want one containing %q", i, w.String(), tt.wantText[i])
				}
			}

			// The check writes nothing: the store's rows and its policy
			// content are as they were.
			after, err := store.ReadPolicy(ctx)
			if err != nil {
				t.Fatalf("ReadPolicy() after error = %v", err)
			}
			if before.Hash() != after.Hash() {
				t.Errorf("CheckPolicy() changed the store's policy content")
			}
			if len(store.roles) != roles || len(store.memberships) != memberships || len(store.grants) != grants {
				t.Errorf("CheckPolicy() changed the store's row counts: roles %d→%d, memberships %d→%d, grants %d→%d",
					roles, len(store.roles), memberships, len(store.memberships), grants, len(store.grants))
			}
		})
	}
}

// TestClient_CheckPolicy_findingsAreErrors pins the findings' error side: each
// is retrievable with errors.As, carries its fields, and a skipped grant
// unwraps to its reason.
func TestClient_CheckPolicy_findingsAreErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, tenant1Policy, "Paymaster", aDescription),
		store.InsertUserRole(ctx, tenant1Policy, "zed", "Ghost"),
		store.InsertRole(ctx, tenant1Policy, "Rogue", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Rogue", "Read", "Nowhere", "", ""),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}
	client := newPolicyClient(t, store, WithDefaultRoles(probeCollection{}, probeRoleFile))

	warnings, err := client.CheckPolicy(ctx)
	if err != nil {
		t.Fatalf("CheckPolicy() error = %v", err)
	}

	var skipped, shadowed, orphaned int
	for _, w := range warnings {
		finding, isError := w.(error)
		if !isError {
			// The role file's own warning is not an error: nothing in the
			// store raised it.
			if _, ok := w.(GrantWarning); !ok {
				t.Errorf("warning %T is neither a finding nor a GrantWarning", w)
			}

			continue
		}
		var sg *SkippedGrant
		var sr *ShadowedRole
		var om *OrphanedMembership
		switch {
		case errors.As(finding, &sg):
			skipped++
			checkRogueSkippedGrant(t, sg)
		case errors.As(finding, &sr):
			shadowed++
			checkPaymasterShadowed(t, sr)
		case errors.As(finding, &om):
			orphaned++
			checkGhostOrphaned(t, om)
		default:
			t.Errorf("finding %T is none of the three kinds", w)
		}
		if finding.Error() != w.String() {
			t.Errorf("finding %T prints %q as an error and %q as a warning", w, finding.Error(), w.String())
		}
	}
	if skipped != 1 || shadowed != 1 || orphaned != 1 {
		t.Errorf("CheckPolicy() found %d skipped, %d shadowed, %d orphaned, want one of each", skipped, shadowed, orphaned)
	}
}

// checkRogueSkippedGrant asserts the skipped grant is Rogue's Read on
// Nowhere, with the undeclared resource as its reason, carried in its line.
func checkRogueSkippedGrant(t *testing.T, sg *SkippedGrant) {
	t.Helper()
	if sg.Resource != "Nowhere" || sg.Subject != "role Rogue" || sg.Permission != "Read" {
		t.Errorf("SkippedGrant = %+v, want role Rogue's Read on Nowhere", sg)
	}
	if sg.Reason == nil || !strings.Contains(sg.Reason.Error(), "declares no Read on Nowhere") {
		t.Fatalf("SkippedGrant.Reason = %v, want the undeclared resource named", sg.Reason)
	}
	if !strings.Contains(sg.String(), "declares no Read on Nowhere") || strings.Contains(sg.String(), "source=") {
		t.Errorf("SkippedGrant.String() = %q, want the reason as plain text without source positions", sg.String())
	}
}

// checkPaymasterShadowed asserts the shadowed role is Paymaster in tenant1.
func checkPaymasterShadowed(t *testing.T, sr *ShadowedRole) {
	t.Helper()
	if sr.Role != "Paymaster" || sr.Scope != tenant1Policy {
		t.Errorf("ShadowedRole = %+v, want Paymaster in tenant1", sr)
	}
}

// checkGhostOrphaned asserts the orphaned membership is Ghost in tenant1,
// held by zed alone.
func checkGhostOrphaned(t *testing.T, om *OrphanedMembership) {
	t.Helper()
	if om.Role != "Ghost" || om.Scope != tenant1Policy || len(om.Users) != 1 || om.Users[0] != "zed" {
		t.Errorf("OrphanedMembership = %+v, want Ghost in tenant1 held by zed", om)
	}
}

// TestClient_CheckPolicy_storeError pins that a store that cannot be read is
// the check's error, not an empty report.
func TestClient_CheckPolicy_storeError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	client := newPolicyClient(t, store, WithDefaultRoles(probeCollection{}, probeRoleFile))
	store.setFail(errors.New("store down"))

	warnings, err := client.CheckPolicy(ctx)
	if err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("CheckPolicy() error = %v, want the store's", err)
	}
	if warnings != nil {
		t.Errorf("CheckPolicy() = %v beside an error, want nil", warnings)
	}
}

// reloadErrors collects what the reload-error hook receives; the engine
// calls it from its own goroutine.
type reloadErrors struct {
	mu   sync.Mutex
	errs []error
}

func (c *reloadErrors) collect(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errs = append(c.errs, err)
}

func (c *reloadErrors) list() []error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]error(nil), c.errs...)
}

// TestClient_CheckPolicy_findingsReachReloadHook pins that the findings
// CheckPolicy lists are the ones the engine reports through the reload-error
// hook when it compiles the snapshot, each retrievable with errors.As and
// printing the same line.
func TestClient_CheckPolicy_findingsReachReloadHook(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, tenant1Policy, "Paymaster", aDescription),
		store.InsertUserRole(ctx, tenant1Policy, "zed", "Ghost"),
		store.InsertRole(ctx, tenant1Policy, "Rogue", aDescription),
		store.InsertGrant(ctx, tenant1Policy, "Rogue", "Read", "Nowhere", "", ""),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}
	hook := &reloadErrors{}
	client := newPolicyClient(t, store, WithDefaultRoles(probeCollection{}, probeRoleFile), WithReloadErrorHandler(hook.collect))

	warnings, err := client.CheckPolicy(ctx)
	if err != nil {
		t.Fatalf("CheckPolicy() error = %v", err)
	}
	if err := client.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	// The first load reports its findings after it signals ready; settling
	// waits for that reload to finish.
	settle(client.snapEngine)

	reported := hook.list()
	var shadowed *ShadowedRole
	var orphaned *OrphanedMembership
	var skipped *SkippedGrant
	for _, err := range reported {
		if !errors.As(err, &shadowed) && !errors.As(err, &orphaned) && !errors.As(err, &skipped) {
			t.Errorf("the hook received %v, which is none of the three findings", err)
		}
	}
	if shadowed == nil || orphaned == nil || skipped == nil {
		t.Fatalf("the hook received %v, want a ShadowedRole, an OrphanedMembership and a SkippedGrant", reported)
	}

	// Each finding CheckPolicy lists reached the hook, printing the same line.
	lines := make(map[string]bool, len(reported))
	for _, err := range reported {
		lines[err.Error()] = true
	}
	for _, w := range warnings {
		if _, isError := w.(error); !isError {
			continue
		}
		if !lines[w.String()] {
			t.Errorf("CheckPolicy() listed %q, which the hook did not receive", w.String())
		}
	}
	if len(reported) != 3 {
		t.Errorf("the hook received %d errors, want the three findings once each", len(reported))
	}
}
