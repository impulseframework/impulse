package integration

// custom_role_test: a custom role is the store's, created in one sector through the user
// manager beside the release's default roles. The manager writes a role no grant the
// release does not declare: the call is refused as a bad request and nothing of it is
// written. A grant that is in the store all the same, as one a release that declared the
// resource wrote before a rollback, is left out when the policy compiles and reported by
// the deploy's role check (access.Client.CheckPolicy) as a skipped grant; the role's other
// grants hold, so a check answers the declared grants granted and the undeclared one
// denied.

import (
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/spannerstore"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

func TestCustomRole_undeclaredGrantIsSkipped(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	client := newAccessClient(t, db, crew.Roles())
	manager := client.UserManager()

	const (
		role       accesstypes.Role     = "SectorAuditor"
		login      accesstypes.User     = "auditor"
		declared   accesstypes.Resource = "Missions"
		undeclared accesstypes.Resource = "Ledgers"
	)
	held := accesstypes.DomainPolicyScope(anvil)
	if err := manager.AddRole(ctx, held, role, "Audits a sector's missions."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	// The manager holds a grant to what the release declares: the call that names the
	// undeclared resource is refused, and the two rows beside it are not written.
	err = manager.AddRoleGrants(ctx, held, role,
		access.GrantRow{Permission: accesstypes.List, Resource: declared},
		access.GrantRow{Permission: accesstypes.Read, Resource: declared},
		access.GrantRow{Permission: accesstypes.List, Resource: undeclared},
	)
	const refused = "List on Ledgers cannot be given to a role: this release declares no List on Ledgers. " +
		"A role is given a permission the application declares on a resource or a field; check the permission and the name"
	if !httpio.HasBadRequest(err) || httpio.Message(err) != refused {
		t.Fatalf("access.UserManager.AddRoleGrants() with a resource the release does not declare, error = %v, want a bad request saying %q", err, refused)
	}
	if grants, err := manager.RoleGrants(ctx, held, role); err != nil || len(grants) != 0 {
		t.Fatalf("access.UserManager.RoleGrants() after the refused call = %v, %v; want no grant written", grants, err)
	}
	if err := manager.AddRoleGrants(ctx, held, role,
		access.GrantRow{Permission: accesstypes.List, Resource: declared},
		access.GrantRow{Permission: accesstypes.Read, Resource: declared},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	// The grant on the undeclared resource is put in the store as a release that
	// declared the resource would have left it there: by an engine over the same store
	// that was told nothing of what this release declares, and so checks no name.
	store, err := spannerstore.New(db.Client, spannerstore.WithPrefix(crew.TablePrefix))
	if err != nil {
		t.Fatalf("spannerstore.New() error = %v", err)
	}
	earlier, err := access.New(store)
	if err != nil {
		t.Fatalf("access.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := earlier.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	if err := earlier.UserManager().AddRoleGrant(ctx, held, role, accesstypes.List, undeclared, ""); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrant() by an engine with no collection, error = %v", err)
	}
	if err := manager.AddUserRoles(ctx, held, login, role); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles() error = %v", err)
	}

	t.Run("the deploy check lists the undeclared grant as skipped", func(t *testing.T) {
		t.Parallel()

		warnings, err := client.CheckPolicy(ctx)
		if err != nil {
			t.Fatalf("access.Client.CheckPolicy() error = %v", err)
		}
		var skipped []*access.SkippedGrant
		for _, w := range warnings {
			if sg, ok := w.(*access.SkippedGrant); ok {
				skipped = append(skipped, sg)
			}
		}
		if len(skipped) != 1 {
			t.Fatalf("CheckPolicy() listed %d skipped grant(s), want exactly the undeclared one: %v", len(skipped), skipped)
		}
		sg := skipped[0]
		if sg.Scope != held || sg.Subject != "role "+string(role) || sg.Permission != accesstypes.List || sg.Resource != undeclared || sg.Condition != "" {
			t.Errorf("SkippedGrant = %+v, want %s's unconditional List on %s in %s", sg, role, undeclared, held)
		}
	})

	// The store writes signal a reload and the swap is asynchronous: once the declared
	// grant shows, every answer below reads a policy that holds the whole role.
	checker := client.ForUser(login)
	scope := accesstypes.DomainScope(anvil)
	if d := decisionFor(ctx, t, checker, scope, accesstypes.List, declared, time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("List %s for a login holding %s = %s, want granted before the cases run", declared, role, d)
	}

	tests := []struct {
		name        string
		permission  accesstypes.Permission
		resource    accesstypes.Resource
		wantGranted bool
	}{
		{name: "the declared grant is granted", permission: accesstypes.List, resource: declared, wantGranted: true},
		{name: "the undeclared grant is denied", permission: accesstypes.List, resource: undeclared, wantGranted: false},
		{name: "the role's other grant holds", permission: accesstypes.Read, resource: declared, wantGranted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decisions, err := checker.Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), scope, tt.permission, tt.resource)
			if err != nil {
				t.Fatalf("Check(%s, %s) error = %v", tt.permission, tt.resource, err)
			}
			if got := decisions[tt.resource]; got.IsGranted() != tt.wantGranted {
				t.Errorf("%s %s for a login holding %s = %s, want granted = %t", tt.permission, tt.resource, role, got, tt.wantGranted)
			}
		})
	}
}
