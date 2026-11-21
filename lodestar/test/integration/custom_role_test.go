package integration

// custom_role_test: a custom role is the store's, created in one sector through the user
// manager beside the release's default roles. The manager writes a role no grant the
// release does not declare: the call is refused as a bad request and nothing of it is
// written. A grant that is in the store all the same, as one a release that declared the
// resource wrote before a rollback, is left out when the policy compiles and reported by
// the deploy's role check (access.Client.CheckPolicy) as a skipped grant; the role's other
// grants hold, so a check answers the declared grants granted and the undeclared one
// denied. A grant whose names the release declares and whose condition reads an attribute
// it does not is in the store the same way, and gives its holder no row.

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
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
	// whose collection lists the resource beside what this release declares.
	earlier := engineOfAReleaseThatDeclared(t, db, map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {undeclared}})
	if err := earlier.UserManager().AddRoleGrant(ctx, held, role, accesstypes.List, undeclared, ""); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrant() by the engine of a release that declared %s, error = %v", undeclared, err)
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

// TestCustomRole_storedConditionOnAnUndeclaredAttributeGrantsNothing proves what a grant
// in the store gives when its condition reads an attribute the release does not declare,
// as one a release that declared the attribute wrote before a rollback: nothing. The
// release's engine refuses to write such a grant, so the engine of a release that
// declared the attribute writes it. The load holds a stored grant to its names, which
// this one's are, so no report names it and the engine answers that the condition is
// still to be judged, never that the permission is held. The condition is judged where
// the rows are read, and there the read is refused: the person is served no list and no
// row of the resource (500), the role's other grant serves as it did, and a role whose
// same grant reads an attribute the release declares is served the rows its condition
// admits.
func TestCustomRole_storedConditionOnAnUndeclaredAttributeGrantsNothing(t *testing.T) {
	t.Parallel()

	w := newIdentityWorld(t)
	ctx := t.Context()
	manager := w.engine.UserManager()

	const (
		missions  accesstypes.Resource = "Missions"
		title     accesstypes.Resource = "Missions.title"
		squadrons accesstypes.Resource = "Squadrons"
		// completed reads the state of a mission, an attribute the release declares, and
		// chartered an attribute it does not.
		completed = "state = 'completed'"
		chartered = "charter = 'open'"

		declaredRole   accesstypes.Role = "CompletedReader"
		undeclaredRole accesstypes.Role = "CharterReader"
	)
	held := accesstypes.DomainPolicyScope(anvil)
	scope := accesstypes.DomainScope(anvil)
	// reads is the grant under test: the list of a sector's missions and one mission
	// read, each showing the title, under the condition.
	reads := func(condition string) []access.GrantRow {
		return []access.GrantRow{
			{Permission: accesstypes.List, Resource: missions, Condition: condition},
			{Permission: accesstypes.List, Resource: title, Condition: condition},
			{Permission: accesstypes.Read, Resource: missions, Condition: condition},
			{Permission: accesstypes.Read, Resource: title, Condition: condition},
		}
	}
	// usernames are the people a request is made as, and people the same two as the
	// permission store names them.
	usernames := map[accesstypes.Role]accesstypes.User{declaredRole: "completed-reader", undeclaredRole: "charter-reader"}
	people := make(map[accesstypes.Role]accesstypes.User)
	for role, username := range usernames {
		if err := manager.AddRole(ctx, held, role, "Reads some of a sector's missions, and its squadrons."); err != nil {
			t.Fatalf("access.UserManager.AddRole(%s) error = %v", role, err)
		}
		// The role's other grant, under no condition: the list of the sector's squadrons.
		if err := manager.AddRoleGrants(ctx, held, role,
			access.GrantRow{Permission: accesstypes.List, Resource: squadrons},
			access.GrantRow{Permission: accesstypes.List, Resource: "Squadrons.name"},
		); err != nil {
			t.Fatalf("access.UserManager.AddRoleGrants(%s) error = %v", role, err)
		}
		people[role] = makeCrewAccount(ctx, t, w.db, username)
		if err := manager.AddUserRoles(ctx, held, people[role], role); err != nil {
			t.Fatalf("access.UserManager.AddUserRoles(%s, %s) error = %v", username, role, err)
		}
	}
	if err := manager.AddRoleGrants(ctx, held, declaredRole, reads(completed)...); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() with a condition on an attribute the release declares, error = %v", err)
	}
	// The release's engine refuses the condition and writes nothing of the call; the
	// engine of a release that declared the attribute writes it.
	err := manager.AddRoleGrants(ctx, held, undeclaredRole, reads(chartered)...)
	const refused = `condition references "charter", which is not an attribute of Missions`
	if !httpio.HasBadRequest(err) || !strings.Contains(httpio.Message(err), refused) {
		t.Fatalf("access.UserManager.AddRoleGrants() with a condition on an attribute the release does not declare, error = %v, want a bad request saying %q", err, refused)
	}
	earlier := engineOfAReleaseWithAttributes(t, w.db, map[accesstypes.Resource][]string{missions: {"charter"}})
	if err := earlier.UserManager().AddRoleGrants(ctx, held, undeclaredRole, reads(chartered)...); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() by the engine of a release that declared the attribute, error = %v", err)
	}

	// The store writes signal a reload and the swap is asynchronous: once the grants
	// written last show, every answer below reads a policy that holds both roles whole.
	// The condition on the declared attribute shows as a grant still to be judged.
	deadline := time.Now().Add(15 * time.Second)
	for _, role := range []accesstypes.Role{declaredRole, undeclaredRole} {
		checker := w.engine.ForUser(people[role])
		if d := decisionFor(ctx, t, checker, scope, accesstypes.List, squadrons, deadline); !d.IsGranted() {
			t.Fatalf("List %s for the holder of %s = %s, want granted before the cases run", squadrons, role, d)
		}
		for {
			decisions, err := checker.Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), scope, accesstypes.Read, title)
			if err != nil {
				t.Fatalf("Check(Read, %s) for the holder of %s, error = %v", title, role, err)
			}
			if decisions[title].IsConditional() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Read %s for the holder of %s = %s, want it to stand on its condition before the cases run", title, role, decisions[title])
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	t.Run("the deploy check names no skipped grant: the grant's names are declared", func(t *testing.T) {
		t.Parallel()

		warnings, err := w.engine.CheckPolicy(ctx)
		if err != nil {
			t.Fatalf("access.Client.CheckPolicy() error = %v", err)
		}
		for _, warning := range warnings {
			if sg, ok := warning.(*access.SkippedGrant); ok {
				t.Errorf("CheckPolicy() lists the skipped grant %+v, want none", sg)
			}
		}
	})

	t.Run("the engine never answers that the permission is held", func(t *testing.T) {
		t.Parallel()

		checker := w.engine.ForUser(people[undeclaredRole])
		for _, permission := range []accesstypes.Permission{accesstypes.List, accesstypes.Read} {
			decisions, err := checker.Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), scope, permission, missions, title)
			if err != nil {
				t.Fatalf("Check(%s) error = %v", permission, err)
			}
			for _, res := range []accesstypes.Resource{missions, title} {
				if d := decisions[res]; !d.IsConditional() {
					t.Errorf("%s %s for the holder of %s = %s, want it to stand on the stored condition", permission, res, undeclaredRole, d)
				}
			}
		}
	})

	missionsList := sectorPath(anvil, "missions?limit=200")
	completedMission := sectorPath(anvil, "missions/"+missionPodID)
	failedMission := sectorPath(anvil, "missions/"+missionTowID)
	tests := []struct {
		name       string
		role       accesstypes.Role
		target     string
		wantStatus int
		// wantIDs are rows a served list must hold and withoutIDs rows it must not.
		wantIDs    []string
		withoutIDs []string
	}{
		{name: "a declared attribute: the list holds the rows the condition admits", role: declaredRole, target: missionsList, wantStatus: http.StatusOK, wantIDs: []string{missionPodID}, withoutIDs: []string{missionTowID, missionBullionID}},
		{name: "a declared attribute: a row the condition admits is read", role: declaredRole, target: completedMission, wantStatus: http.StatusOK},
		{name: "a declared attribute: a row the condition refuses is not found", role: declaredRole, target: failedMission, wantStatus: http.StatusNotFound},
		{name: "an undeclared attribute: no list is served", role: undeclaredRole, target: missionsList, wantStatus: http.StatusInternalServerError},
		{name: "an undeclared attribute: no row is read", role: undeclaredRole, target: completedMission, wantStatus: http.StatusInternalServerError},
		{name: "an undeclared attribute: nor a row the other role's condition refuses", role: undeclaredRole, target: failedMission, wantStatus: http.StatusInternalServerError},
		{name: "a declared attribute: the role's other grant serves", role: declaredRole, target: sectorPath(anvil, "squadrons"), wantStatus: http.StatusOK, wantIDs: []string{squadronHammerID}},
		{name: "an undeclared attribute: the role's other grant serves", role: undeclaredRole, target: sectorPath(anvil, "squadrons"), wantStatus: http.StatusOK, wantIDs: []string{squadronHammerID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, w.h, usernames[tt.role], http.MethodGet, tt.target, "")
			assertStatus(t, status, tt.wantStatus, body)
			if status != http.StatusOK {
				// A refused read carries nothing of a mission: neither its id nor a title.
				for _, leaked := range []string{missionPodID, missionTowID, `"title"`} {
					if strings.Contains(string(body), leaked) {
						t.Errorf("the refused read's body holds %s: %s", leaked, body)
					}
				}

				return
			}
			if tt.wantIDs == nil {
				if id := cell[string](t, decodeRow(t, body), "id"); id != missionPodID {
					t.Errorf("the row read has the id %s, want %s", id, missionPodID)
				}

				return
			}
			ids := idsOf(t, decodeRows(t, body))
			for _, id := range tt.wantIDs {
				if !slices.Contains(ids, id) {
					t.Errorf("the list = %v, want it to hold %s", ids, id)
				}
			}
			for _, id := range tt.withoutIDs {
				if slices.Contains(ids, id) {
					t.Errorf("the list = %v, want it without %s", ids, id)
				}
			}
		})
	}
}
