//go:build skipAuth

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/grpc/codes"
)

// TestTenantDelete_aDeletedSectorTakesItsRolesAndInvitations proves a sector that is
// deleted leaves nothing of itself behind in either auth, on the served stack: the roles
// people held in it are removed, the custom role made in it goes with its grants, and the
// invitations into it that still stood are withdrawn, in the transaction that deletes
// the row, so nothing is left for a later sector to pick up. The generated delete of the
// Sector record clears the crew's tables and the members' through each auth's generated
// identity client (ClearTenant). A person who held roles in the sector alone is deleted
// with it, by the rule on an account nothing admits, and a person who holds a role
// elsewhere stays, in the crew and among the members alike. The sector is made the way
// every sector is, by an invitation: its founder, the first marshal, holds roles there
// alone and goes with it.
//
// The sector's directory goes with it in the same request, though the sector is
// directory only by then: nothing is turned off or removed first, the directory's
// removal leaves its change event, another sector's directory stands, and the directory
// is free for another sector, which the schema refused while Ember had it. The marshal,
// seated in Ember and in Anvil, signs in afterwards as before and reaches Anvil.
// Lodestar's own tables keyed to a sector carry no delete rule, so Ember holds none of
// their rows here; the skeletons' sample table carries the rule and their suites prove
// a tenant's rows go with it.
//
// No role the release ships deletes a sector, so the suite makes one that does: a custom
// role of the whole application holding Delete on Sectors, given to the governor.
//
// Demonstrates: tenancy.deleted-tenant.
func TestTenantDelete_aDeletedSectorTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	const (
		closer          accesstypes.Role = "SectorCloser"
		watch           accesstypes.Role = "EmberWatch"
		hand            accesstypes.User = "emberhand"
		drifter         accesstypes.User = "ember-drifter"
		commuter        accesstypes.User = "ember-commuter"
		sectorsResource                  = accesstypes.Resource("Sectors")
	)
	crewManager, membersManager := s.access.UserManager(), s.members.UserManager()
	global := accesstypes.GlobalPolicyScope()
	if err := crewManager.AddRole(ctx, global, closer, "Closes a sector the fleet has left."); err != nil {
		t.Fatalf("access.UserManager.AddRole(%s) error = %v", closer, err)
	}
	if err := crewManager.AddRoleGrants(ctx, global, closer, access.GrantRow{Permission: accesstypes.Delete, Resource: sectorsResource}); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants(%s) error = %v", closer, err)
	}
	// The permission store names a person by the id of their account: the governor's and
	// the marshal's are the cast's fixed ids.
	governorUser, marshalUser := account(t, "governor"), account(t, "marshal")
	if err := crewManager.AddUserRoles(ctx, global, governorUser, closer); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(governor, %s) error = %v", closer, err)
	}
	if d := decisionFor(ctx, t, s.access.ForUser(governorUser), accesstypes.GlobalScope(), accesstypes.Delete, sectorsResource, time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Delete on Sectors for the governor holding %s = %s, want granted before the sector is made", closer, d)
	}

	// Headquarters invites Ember's founder, whose first sign-in makes the sector and
	// seats them as its marshal.
	ember, _ := foundSector(ctx, t, s, hq, "Ember", "Near frontier", "2226-10-20", "ember-founder")
	founderID := crewAccountID(ctx, t, hq, "ember-founder")
	founderUser := accesstypes.User(founderID)
	emberScope := accesstypes.DomainPolicyScope(accesstypes.Domain(ember))
	anvilScope := accesstypes.DomainPolicyScope(anvil)

	// The crew in Ember: the marshal of Anvil is given a role there; a person is created
	// there who holds a role nowhere else; an invitation into Ember stands; and a custom
	// role is made in Ember.
	status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"userId":"`+string(marshalUser)+`","role":"Pilot","domain":"`+ember+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = hq.do(ctx, http.MethodPost, sectorPath(ember, "create-crew-sector-user"), []byte(`{"username":"`+string(hand)+`","email":"Hand <hand@example.com>","roles":["Cadet"]}`))
	assertStatus(t, status, http.StatusOK, body)
	handID := cell[string](t, decodeRow(t, body), "id")
	handUser := accesstypes.User(handID)
	status, body = hq.do(ctx, http.MethodPost, sectorPath(ember, "invite-crew-sector-user"), []byte(`{"email":"invited@example.com","roles":["Cadet"]}`))
	assertStatus(t, status, http.StatusOK, body)
	var invitation invited
	if err := json.Unmarshal(body, &invitation); err != nil || invitation.ID == "" {
		t.Fatalf("invite-crew-sector-user answered %s (%v), want the invitation's id", body, err)
	}
	if err := crewManager.AddRole(ctx, emberScope, watch, "Stands watch over Ember."); err != nil {
		t.Fatalf("access.UserManager.AddRole(%s) error = %v", watch, err)
	}

	// The members in Ember: the drifter holds the portal's role there and nowhere else,
	// the commuter holds it there and in Anvil, and an invitation into Ember stands.
	drifterID, commuterID, memberLink := newID(t), newID(t), newID(t)
	drifterUser, commuterUser := accesstypes.User(drifterID), accesstypes.User(commuterID)
	hash := sha256.Sum256([]byte(memberLink))
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("MembersSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{drifterID, string(drifter), spanner.CommitTimestamp}),
		spanner.Insert("MembersSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{commuterID, string(commuter), spanner.CommitTimestamp}),
		spanner.InsertMap("MembersSessionMailLinks", map[string]any{
			"Id": memberLink, "TokenHash": hash[:], "Kind": "invitation", "Email": "member@example.com", "TenantId": ember,
			"RoleNames": []string{"ClientPortal"}, "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(time.Hour),
		}),
	}); err != nil {
		t.Fatalf("writing the members: %v", err)
	}
	if err := membersManager.AddUserRoles(ctx, emberScope, drifterUser, "ClientPortal"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Ember) error = %v", drifter, err)
	}
	if err := membersManager.AddUserRoles(ctx, emberScope, commuterUser, "ClientPortal"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Ember) error = %v", commuter, err)
	}
	if err := membersManager.AddUserRoles(ctx, anvilScope, commuterUser, "ClientPortal"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Anvil) error = %v", commuter, err)
	}

	// Before the delete, Ember holds what the suite gave it.
	if got := marshal.domains(ctx); !slices.Contains(got, ember) {
		t.Fatalf("the marshal's sectors once given a role in Ember = %v, want Ember among them", got)
	}
	if !crewAccountExists(t, s.db, handID) {
		t.Fatal("the person created in Ember has no account")
	}
	for _, held := range []struct {
		name    string
		manager access.UserManager
		user    accesstypes.User
	}{
		{name: "Ember's founder, its first marshal", manager: crewManager, user: founderUser},
		{name: "the marshal", manager: crewManager, user: marshalUser},
		{name: "the person created in Ember", manager: crewManager, user: handUser},
		{name: "the drifter, a member", manager: membersManager, user: drifterUser},
		{name: "the commuter, a member", manager: membersManager, user: commuterUser},
	} {
		if roles := rolesHeldIn(ctx, t, held.manager, held.user, emberScope); len(roles) == 0 {
			t.Fatalf("%s holds no role in Ember before the delete, want one", held.name)
		}
	}

	if !hasRole(ctx, t, crewManager, emberScope, watch) {
		t.Fatalf("the custom role %s made in Ember does not exist before the delete", watch)
	}
	if readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{invitation.ID}, "ConsumedAt").Valid {
		t.Fatal("the crew's invitation into Ember does not stand before the delete")
	}
	if readColumn[spanner.NullTime](ctx, t, s.db, "MembersSessionMailLinks", spanner.Key{memberLink}, "ConsumedAt").Valid {
		t.Fatal("the members' invitation into Ember does not stand before the delete")
	}

	// Ember is directory only by now, with an organization's directory, written here as
	// rows: what its marshal would have declared on the sector's Sign-in page. While
	// Ember has the directory no other sector takes it, which the schema holds.
	const entra = "6f2c1b7e-0000-4000-8000-0000000000e1"
	directoryRow := func(sector string) []*spanner.Mutation {
		return []*spanner.Mutation{spanner.Insert("SectorDirectories", []string{"Id", "SectorId", "Method", "Connection"}, []any{newID(t), sector, "azure", entra})}
	}
	if _, err := s.db.Apply(ctx, append(directoryRow(ember), spanner.Update("Sectors", []string{"Id", "DirectoryOnly"}, []any{ember, true}))); err != nil {
		t.Fatalf("making Ember directory only with a directory: %v", err)
	}
	if _, err := s.db.Apply(ctx, directoryRow(anvil)); spanner.ErrCode(err) != codes.AlreadyExists {
		t.Fatalf("giving Anvil the directory Ember has: err = %v, want the unique index to refuse it", err)
	}
	directoriesBefore := sectorDirectoryRows(ctx, t, s, "")
	eventsBefore := changeEvents(ctx, t, s, "SectorDirectories")["SectorDirectories"]

	// One request, with nothing before it: directory only is not turned off and the
	// directory is not removed first.
	status, body = hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"/sectors/`+ember+`"}]`))
	assertStatus(t, status, http.StatusOK, body)

	// The directory went with the sector, its removal is in the audit, and every other
	// sector's directory stands.
	if n := sectorDirectoryRows(ctx, t, s, ember); n != 0 {
		t.Errorf("Ember's directory after the delete: %d row(s), want none: the directory goes with its sector", n)
	}
	if n := sectorDirectoryRows(ctx, t, s, ""); n != directoriesBefore-1 {
		t.Errorf("SectorDirectories holds %d row(s) after the delete, want %d: Ember's alone is gone", n, directoriesBefore-1)
	}
	if after := changeEvents(ctx, t, s, "SectorDirectories")["SectorDirectories"]; after != eventsBefore+1 {
		t.Errorf("change events of SectorDirectories: %d before the delete and %d after, want one more: the directory's removal is in the audit", eventsBefore, after)
	}
	// The directory is free for another sector.
	if _, err := s.db.Apply(ctx, directoryRow(anvil)); err != nil {
		t.Errorf("giving Anvil the directory Ember had: %v, want it free once Ember is deleted", err)
	}
	// The marshal signs in as before and reaches Anvil.
	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, "marshal")
	if got := again.domains(ctx); !slices.Equal(got, []string{anvil}) {
		t.Errorf("the marshal's sectors at a sign-in after Ember is deleted = %v, want Anvil alone", got)
	}

	// Gone at once, for the governor and for the marshal alike.
	for name, b := range map[string]*browser{"the governor": hq, "the marshal": marshal} {
		if status, body := b.do(ctx, http.MethodGet, sectorPath(ember, "missions"), nil); status != http.StatusNotFound {
			t.Errorf("GET Ember's missions as %s after the sector is deleted: status %d, want 404: %s", name, status, body)
		}
	}
	if got := marshal.domains(ctx); !slices.Equal(got, []string{anvil}) {
		t.Errorf("the marshal's sectors after Ember is deleted = %v, want Anvil alone", got)
	}

	// Neither auth's permission store holds a role in the deleted sector: not the
	// founder's, not the marshal's, not the person's who was created there, and not
	// the members'.
	for _, gone := range []struct {
		name    string
		manager access.UserManager
		user    accesstypes.User
	}{
		{name: "the crew's store, for Ember's founder", manager: crewManager, user: founderUser},
		{name: "the crew's store, for the marshal", manager: crewManager, user: marshalUser},
		{name: "the crew's store, for the person created in Ember", manager: crewManager, user: handUser},
		{name: "the members' store, for the drifter", manager: membersManager, user: drifterUser},
		{name: "the members' store, for the commuter", manager: membersManager, user: commuterUser},
	} {
		if roles := rolesHeldIn(ctx, t, gone.manager, gone.user, emberScope); len(roles) != 0 {
			t.Errorf("%s holds %v in the deleted sector, want none", gone.name, roles)
		}
	}
	// The custom role made in Ember went with it.
	if hasRole(ctx, t, crewManager, emberScope, watch) {
		t.Errorf("the custom role %s made in Ember still exists, want it deleted with the sector", watch)
	}

	// A person who holds a role elsewhere stays, with that role; a person who held
	// roles in Ember alone is deleted with it.
	if got := crewHolds(t, s.db, "marshal"); !slices.Contains(got, "domain/"+anvil+" SectorMarshal") {
		t.Errorf("the marshal holds %v after the delete, want Anvil's marshal role kept", got)
	}
	if crewAccountExists(t, s.db, handID) {
		t.Error("the account of the crew member who held roles in Ember alone still exists, want it deleted with the sector")
	}
	if crewAccountExists(t, s.db, founderID) {
		t.Error("the account of Ember's founder, who held roles in Ember alone, still exists, want it deleted with the sector")
	}
	if got := rolesHeldIn(ctx, t, membersManager, commuterUser, anvilScope); !slices.Equal(got, []accesstypes.Role{"ClientPortal"}) {
		t.Errorf("the commuter holds %v in Anvil after the delete, want ClientPortal kept", got)
	}
	if _, err := s.db.Single().ReadRow(ctx, "MembersSessionUsers", spanner.Key{commuterID}, []string{"Id"}); err != nil {
		t.Errorf("the commuter's account after the delete: %v, want it kept: the commuter holds a role in Anvil", err)
	}
	if _, err := s.db.Single().ReadRow(ctx, "MembersSessionUsers", spanner.Key{drifterID}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the account of the member who held roles in Ember alone: err = %v, want not found: it is deleted with the sector", err)
	}

	// The invitations into Ember are withdrawn, the crew's and the members': each link
	// is consumed.
	if !readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{invitation.ID}, "ConsumedAt").Valid {
		t.Error("the crew's invitation into the deleted sector still stands, want it withdrawn")
	}
	if !readColumn[spanner.NullTime](ctx, t, s.db, "MembersSessionMailLinks", spanner.Key{memberLink}, "ConsumedAt").Valid {
		t.Error("the members' invitation into the deleted sector still stands, want it withdrawn")
	}
}

// sectorDirectoryRows counts the rows of the directories record, a sector's own or, for
// an empty sector, every one, as committed.
func sectorDirectoryRows(ctx context.Context, t *testing.T, s *served, sector string) int64 {
	t.Helper()

	statement := spanner.Statement{SQL: "SELECT COUNT(*) FROM SectorDirectories"}
	if sector != "" {
		statement = spanner.Statement{SQL: "SELECT COUNT(*) FROM SectorDirectories WHERE SectorId = @sector", Params: map[string]any{"sector": sector}}
	}
	iter := s.db.Single().Query(ctx, statement)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the directories: %v", err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of the directories: %v", err)
	}

	return n
}

// rolesHeldIn reads the roles the user holds in the one scope, as the auth's permission
// store holds them.
func rolesHeldIn(ctx context.Context, t *testing.T, manager access.UserManager, user accesstypes.User, scope accesstypes.PolicyScope) []accesstypes.Role {
	t.Helper()

	held, err := manager.UserRoles(ctx, user, scope)
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(%s) error = %v", user, err)
	}

	return held[scope]
}

// hasRole reports whether the role exists in the scope, as the auth's permission store
// lists the scope's roles.
func hasRole(ctx context.Context, t *testing.T, manager access.UserManager, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	t.Helper()

	roles, err := manager.Roles(ctx, scope)
	if err != nil {
		t.Fatalf("access.UserManager.Roles() error = %v", err)
	}

	return slices.ContainsFunc(roles, func(held access.RoleInfo) bool {
		return held.Name == role
	})
}

// newID answers a new id's text.
func newID(t *testing.T) string {
	t.Helper()

	id, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}

	return id.String()
}
