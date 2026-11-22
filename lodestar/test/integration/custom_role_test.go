package integration

// custom_role_test: a custom role is the store's, created in one sector through the user
// manager beside the release's default roles. The manager writes a role no grant the
// release does not declare: the call is refused as a bad request and nothing of it is
// written. A grant that is in the store all the same, as one a release that declared the
// resource wrote before a rollback, is left out when the policy compiles and reported by
// the deploy's role check (access.Client.CheckPolicy) as a skipped grant; the role's other
// grants hold, so a check answers the declared grants granted and the undeclared one
// denied. A grant whose names the release declares and whose condition reads an attribute
// it does not is in the store the same way, and is left out and reported the same way. A
// custom role is a role of the auth whose engine made it, and is refused a grant that
// reads another auth's set of people.

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
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
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
// as one a release that declared the attribute wrote before a rollback: nothing, and it
// costs its holder nothing else. The release's engine refuses to write such a grant, so
// the engine of a release that declared the attribute writes it. The load holds a role's
// stored grant to the rule the write is held to, so the grant is left out when the policy
// compiles and the deploy's role check (access.Client.CheckPolicy) reports it as a skipped
// grant, saying which attribute. Its holder is refused the list and the rows of the
// resource as a person with no grant of them is (403), and the role's other grant serves
// as it did. A person who also holds a role whose same grant reads an attribute the
// release declares is served the rows that condition admits: the grant that cannot be
// judged is not in the way of the one that can.
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

		// The people a request is made as: one holds each role alone, and one holds both.
		completedReader accesstypes.User = "completed-reader"
		charterReader   accesstypes.User = "charter-reader"
		bothReader      accesstypes.User = "both-reader"
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
	for _, role := range []accesstypes.Role{declaredRole, undeclaredRole} {
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
	}
	// people are the same three as the permission store names them.
	people := make(map[accesstypes.User]accesstypes.User)
	for _, username := range []accesstypes.User{completedReader, charterReader, bothReader} {
		people[username] = makeCrewAccount(ctx, t, w.db, username)
	}
	if err := manager.AddUserRoles(ctx, held, people[completedReader], declaredRole); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s) error = %v", completedReader, err)
	}
	if err := manager.AddUserRoles(ctx, held, people[charterReader], undeclaredRole); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s) error = %v", charterReader, err)
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
	// The person who holds both roles is given them last, after every grant is in the
	// store. The store writes signal a reload and the swap is asynchronous: once that
	// person's read stands on a condition, every answer below reads a policy loaded with
	// both roles' grants in the store.
	if err := manager.AddUserRoles(ctx, held, people[bothReader], declaredRole, undeclaredRole); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s) error = %v", bothReader, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		decisions, err := w.engine.ForUser(people[bothReader]).Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), scope, accesstypes.Read, title)
		if err != nil {
			t.Fatalf("Check(Read, %s) for %s, error = %v", title, bothReader, err)
		}
		if decisions[title].IsConditional() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Read %s for %s = %s, want it to stand on a condition before the cases run", title, bothReader, decisions[title])
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, username := range []accesstypes.User{completedReader, charterReader, bothReader} {
		if d := decisionFor(ctx, t, w.engine.ForUser(people[username]), scope, accesstypes.List, squadrons, deadline); !d.IsGranted() {
			t.Fatalf("List %s for %s = %s, want granted before the cases run", squadrons, username, d)
		}
	}

	t.Run("the deploy check lists the stored grants as skipped, saying which attribute", func(t *testing.T) {
		t.Parallel()

		warnings, err := w.engine.CheckPolicy(ctx)
		if err != nil {
			t.Fatalf("access.Client.CheckPolicy() error = %v", err)
		}
		var skipped []string
		for _, warning := range warnings {
			sg, ok := warning.(*access.SkippedGrant)
			if !ok {
				continue
			}
			if sg.Scope != held || sg.Subject != "role "+string(undeclaredRole) || sg.Condition != chartered || !strings.Contains(sg.Error(), refused) {
				t.Errorf("CheckPolicy() lists the skipped grant %q, want none but the grants of %s under %q, each saying %q", sg.Error(), undeclaredRole, chartered, refused)

				continue
			}
			skipped = append(skipped, string(sg.Permission)+" "+string(sg.Resource))
		}
		slices.Sort(skipped)
		if want := []string{"List Missions", "List Missions.title", "Read Missions", "Read Missions.title"}; !slices.Equal(skipped, want) {
			t.Errorf("CheckPolicy() lists the skipped grants %v, want %v", skipped, want)
		}
	})

	t.Run("the engine answers what each person holds", func(t *testing.T) {
		t.Parallel()

		for _, permission := range []accesstypes.Permission{accesstypes.List, accesstypes.Read} {
			for username, wantConditional := range map[accesstypes.User]bool{completedReader: true, charterReader: false, bothReader: true} {
				decisions, err := w.engine.ForUser(people[username]).Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), scope, permission, missions, title)
				if err != nil {
					t.Fatalf("Check(%s) for %s, error = %v", permission, username, err)
				}
				for _, res := range []accesstypes.Resource{missions, title} {
					d := decisions[res]
					if wantConditional && !d.IsConditional() {
						t.Errorf("%s %s for %s = %s, want it to stand on the condition the release can judge", permission, res, username, d)
					}
					if !wantConditional && !d.IsDenied() {
						t.Errorf("%s %s for %s = %s, want it denied: the stored grant is skipped", permission, res, username, d)
					}
				}
			}
		}
	})

	missionsList := sectorPath(anvil, "missions?limit=200")
	completedMission := sectorPath(anvil, "missions/"+missionPodID)
	failedMission := sectorPath(anvil, "missions/"+missionTowID)
	tests := []struct {
		name       string
		as         accesstypes.User
		target     string
		wantStatus int
		// wantIDs are rows a served list must hold and withoutIDs rows it must not.
		wantIDs    []string
		withoutIDs []string
	}{
		{name: "a declared attribute: the list holds the rows the condition admits", as: completedReader, target: missionsList, wantStatus: http.StatusOK, wantIDs: []string{missionPodID}, withoutIDs: []string{missionTowID, missionBullionID}},
		{name: "a declared attribute: a row the condition admits is read", as: completedReader, target: completedMission, wantStatus: http.StatusOK},
		{name: "a declared attribute: a row the condition refuses is not found", as: completedReader, target: failedMission, wantStatus: http.StatusNotFound},
		{name: "an undeclared attribute: no list is served", as: charterReader, target: missionsList, wantStatus: http.StatusForbidden},
		{name: "an undeclared attribute: no row is read", as: charterReader, target: completedMission, wantStatus: http.StatusForbidden},
		{name: "an undeclared attribute: nor a row the other role's condition refuses", as: charterReader, target: failedMission, wantStatus: http.StatusForbidden},
		{name: "both roles: the list holds the rows the declared attribute's condition admits", as: bothReader, target: missionsList, wantStatus: http.StatusOK, wantIDs: []string{missionPodID}, withoutIDs: []string{missionTowID, missionBullionID}},
		{name: "both roles: a row that condition admits is read", as: bothReader, target: completedMission, wantStatus: http.StatusOK},
		{name: "both roles: a row that condition refuses is not found", as: bothReader, target: failedMission, wantStatus: http.StatusNotFound},
		{name: "a declared attribute: the role's other grant serves", as: completedReader, target: sectorPath(anvil, "squadrons"), wantStatus: http.StatusOK, wantIDs: []string{squadronHammerID}},
		{name: "an undeclared attribute: the role's other grant serves", as: charterReader, target: sectorPath(anvil, "squadrons"), wantStatus: http.StatusOK, wantIDs: []string{squadronHammerID}},
		{name: "both roles: the roles' other grant serves", as: bothReader, target: sectorPath(anvil, "squadrons"), wantStatus: http.StatusOK, wantIDs: []string{squadronHammerID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, w.h, tt.as, http.MethodGet, tt.target, "")
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

// TestCustomRole_grantThroughAnotherAuthsSetIsRefused: a custom role made through the
// members' engine is a role of the members auth, held to that auth's sets as the members'
// role file is. A grant whose condition reads the set of a crew person's groups is
// refused as a bad request in the role file's sentence, and nothing of it is written:
// the set is looked up by the ids of the crew's people, so it says nothing about a
// member who holds the role.
func TestCustomRole_grantThroughAnotherAuthsSetIsRefused(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := membersEngine.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	manager := membersEngine.UserManager()

	const (
		role     accesstypes.Role     = "GroupReader"
		resource accesstypes.Resource = "CrewSectorGroups"
		reads                         = "group IN subject.crewGroups"
		refused                       = "role GroupReader: Read grant on CrewSectorGroups: the condition reads subject.crewGroups, which describes the people of crew (auth: crew in the set's @subjectSet), and this is a role of members. " +
			"The set is looked up by the ids of crew's people, so it says nothing about the people who hold this role: read a set that describes the people of members, or give the grant to a role of crew"
	)
	held := accesstypes.DomainPolicyScope(anvil)
	if err := manager.AddRole(ctx, held, role, "Reads the groups of a sector."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	err = manager.AddRoleGrant(ctx, held, role, accesstypes.Read, resource, reads)
	if !httpio.HasBadRequest(err) || httpio.Message(err) != refused {
		t.Fatalf("access.UserManager.AddRoleGrant() through the crew's group set, error = %v, want a bad request saying %q", err, refused)
	}
	if grants, err := manager.RoleGrants(ctx, held, role); err != nil || len(grants) != 0 {
		t.Fatalf("access.UserManager.RoleGrants() after the refused call = %v, %v; want no grant written", grants, err)
	}
}
