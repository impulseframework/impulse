package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"google.golang.org/grpc/codes"
)

// The generated identity of the crew auth (pkg/identity, the generator program's
// GenerateIdentity): the auth's own tables served as resources through the application's
// permission surface, and the methods that change them, each a route behind an Execute
// grant and a Go call the application makes in a transaction of its own. The suites
// below hold the three things the design rests on: one permission surface, one
// transaction, and an engine and a session library that need no word from the caller.
//
// Demonstrates: auth.generated-identity.

// identityWorld is a demo world of a test's own, for the tests that create users and
// memberships: the shared world's rows are pinned by other suites. It holds the App too,
// whose resource client and identity client the Go face of a method takes.
type identityWorld struct {
	db     *initiator.SpannerDB
	app    *app.App
	h      http.Handler
	engine *access.Client
	// members is the members' permission engine the App runs over.
	members *access.Client
}

// newIdentityWorld is the world with the cast's accounts in it, each under its fixed id
// as the bootstrap creates it, so a method that names a persona finds the account.
func newIdentityWorld(t *testing.T) *identityWorld {
	t.Helper()

	return buildIdentityWorld(t, true)
}

// buildIdentityWorld builds the world. The personas hold their roles by the ids
// users.json gives them either way; castAccounts says whether the accounts themselves
// are written, which a world leaves out when its lists of people are to hold the people
// the test makes and nobody else.
func buildIdentityWorld(t *testing.T, castAccounts bool) *identityWorld {
	t.Helper()

	return buildIdentityWorldOver(t, castAccounts, crew.Roles())
}

// buildIdentityWorldOver builds the world over the crew's role file as given: the one
// the release ships, or a suite's variant of it.
func buildIdentityWorldOver(t *testing.T, castAccounts bool, crewRoles access.RoleFile) *identityWorld {
	t.Helper()
	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	crewEngine, err := openEngine(db, crew.TablePrefix, crewRoles)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crewEngine.Close() })
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = membersEngine.Close() })
	if castAccounts {
		if err := seedCastAccounts(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
		t.Fatal(err)
	}
	a := app.New(&testConfigurer{db: db, access: crewEngine, membersAccess: membersEngine})

	return &identityWorld{db: db, app: a, h: withHandWrittenRoutes(a), engine: crewEngine, members: membersEngine}
}

// inTransaction runs the methods and writes of f in one transaction of the application's
// resource client, as an application composing them does.
func (w *identityWorld) inTransaction(ctx context.Context, f func(ctx context.Context, txn resource.ReadWriteTransaction) error) error {
	if err := w.app.ResourceClient().ExecuteFunc(ctx, f); err != nil {
		return fmt.Errorf("resource.Client.ExecuteFunc(): %w", err)
	}

	return nil
}

// seedCrewUser writes an account of the crew's for a person the test makes up
// (makeCrewAccount) and gives it the global roles as the bootstrap gives the cast
// theirs, held by the id of the account, which it answers. Nobody creates an account for
// someone else through the generated management: a person comes in by an invitation,
// which takes the session manager a served stack has, so a suite of this world that
// needs a person who is there already writes one.
func (w *identityWorld) seedCrewUser(ctx context.Context, t *testing.T, username string, roles ...accesstypes.Role) impulse.UUID {
	t.Helper()

	user := makeCrewAccount(ctx, t, w.db, accesstypes.User(username))
	if len(roles) > 0 {
		if err := w.engine.UserManager().AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), user, roles...); err != nil {
			t.Fatalf("AddUserRoles(%s, global): %v", username, err)
		}
	}

	return impulse.Must(impulse.UUIDFromString(string(user)))
}

// assignHeadquarters gives the account the Headquarters role, the global role these
// suites give, in the transaction, through the identity client's AssignRole: the Go call
// the role methods are made of, which looks nobody up. The served method
// (crewidentity.AssignCrewRole) reads the account first, since its request names a
// person, and refuses one that does not exist
// (TestGeneratedIdentityMembershipInTransaction holds both).
func (w *identityWorld) assignHeadquarters(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	const role = "Headquarters"
	if err := w.app.CrewIdentity().AssignRole(ctx, txn, accesstypes.GlobalPolicyScope(), userOf(userID), accesstypes.Role(role)); err != nil {
		return fmt.Errorf("crewidentity.Client.AssignRole(%s, %s): %w", userID, role, err)
	}

	return nil
}

// userOf is a person as the permission store names them: the id of their account, as text.
func userOf(id impulse.UUID) accesstypes.User {
	return accesstypes.User(id.String())
}

// identitySuffix keeps the usernames of one test apart from another's.
func identitySuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// crewMembershipKey is the key of a global membership in the crew's memberships table,
// as the permission store writes one: user is the id of the person's account, as text.
func crewMembershipKey(role string, user accesstypes.User) spanner.Key {
	return spanner.Key{"global", "", "", role, string(user)}
}

// TestGeneratedIdentityDigest holds the one permission surface: the crew auth's tables
// and methods are entries of Lodestar's collection, granted in its role file, and the
// digest the browser already reads names them beside the application's own, for the role
// that holds them and not for one that does not.
func TestGeneratedIdentityDigest(t *testing.T) {
	t.Parallel()
	_, h, _ := sharedWorld(t)

	tests := []struct {
		name   string
		user   accesstypes.User
		want   accesstypes.PermissionDigest
		absent []accesstypes.Resource
	}{
		{
			name: "headquarters holds the auth's grants in the one digest",
			user: "governor",
			want: accesstypes.PermissionDigest{
				"CrewSessionUsers": {"List": accesstypes.DigestGranted, "Read": accesstypes.DigestGranted},
				"CrewSessions":     {"List": accesstypes.DigestGranted, "Read": accesstypes.DigestGranted},
				"CrewUserRoles":    {"List": accesstypes.DigestGranted},
				// The acts on an account are Headquarters' on the accounts no sector manages
				// (managedByTenant = false), so the digest says they depend on the row.
				"DisableCrewUser": {"Execute": accesstypes.DigestConditional},
				"EnableCrewUser":  {"Execute": accesstypes.DigestConditional},
				"AssignCrewRole":  {"Execute": accesstypes.DigestGranted},
				"RemoveCrewRole":  {"Execute": accesstypes.DigestGranted},
				// The account actions, the lockout, the invitations and the members' own.
				"RenameCrewUser":                  {"Execute": accesstypes.DigestConditional},
				"DeleteCrewUser":                  {"Execute": accesstypes.DigestConditional},
				"RevokeCrewUserTrustedDevices":    {"Execute": accesstypes.DigestConditional},
				"UnlockCrewSignIn":                {"Execute": accesstypes.DigestGranted},
				"InviteCrewUser":                  {"Execute": accesstypes.DigestGranted},
				"WithdrawCrewInvitation":          {"Execute": accesstypes.DigestGranted},
				"CrewSessionTrustedDevices":       {"List": accesstypes.DigestGranted, "Read": accesstypes.DigestGranted},
				"RenameMembersUser":               {"Execute": accesstypes.DigestGranted},
				"DeleteMembersUser":               {"Execute": accesstypes.DigestGranted},
				"RevokeMembersUserTrustedDevices": {"Execute": accesstypes.DigestGranted},
				"UnlockMembersSignIn":             {"Execute": accesstypes.DigestGranted},
				"MembersSessionTrustedDevices":    {"List": accesstypes.DigestGranted, "Read": accesstypes.DigestGranted},
			},
			// The members' directory admits them and writes their roles: the generator
			// wrote no method that invites one, so the collection has no such entry to
			// grant.
			absent: []accesstypes.Resource{"InviteMembersUser", "WithdrawMembersInvitation"},
		},
		{
			name: "the cadet's digest names none of them",
			user: "cadet",
			absent: []accesstypes.Resource{
				"CrewSessionUsers", "CrewSessions", "CrewUserRoles", "DisableCrewUser", "EnableCrewUser", "AssignCrewRole", "RemoveCrewRole",
				"RenameCrewUser", "DeleteCrewUser", "RevokeCrewUserTrustedDevices", "UnlockCrewSignIn", "InviteCrewUser", "WithdrawCrewInvitation", "CrewSessionTrustedDevices",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, body := doRequestAs(t, h, tt.user, http.MethodGet, consoleAPI+"/permission-digest", "")
			assertStatus(t, status, http.StatusOK, body)
			var got accesstypes.PermissionDigest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode digest: %v: %s", err, body)
			}
			for res, want := range tt.want {
				if !maps.Equal(got[res], want) {
					t.Errorf("digest[%s] = %v, want %v", res, got[res], want)
				}
			}
			for _, res := range tt.absent {
				if _, ok := got[res]; ok {
					t.Errorf("digest carries %s = %v, want absent", res, got[res])
				}
			}
		})
	}
}

// TestGeneratedIdentityOneTransaction holds the Go face: two of the auth's methods and
// one of the application's own writes run in one transaction the application opened.
// All three land together, and when a later method fails at the commit (the disabling of
// a user that does not exist), the role given beside it is rolled back with it. The Go
// face needs no request: an account is deleted in a transaction the application opened
// with no session in its context, as a job deletes one.
func TestGeneratedIdentityOneTransaction(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	ctx := sessionContext(t, "governor")
	sfx := identitySuffix()

	alice, bob := w.seedCrewUser(ctx, t, "alice-"+sfx, "CrewCommon"), w.seedCrewUser(ctx, t, "bob-"+sfx, "CrewCommon")
	var clientID impulse.UUID
	clientName := "Identity Freight " + sfx
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := (&crewidentity.AssignCrewRole{UserID: alice, Role: "ClientBrowser"}).Execute(ctx, txn, w.app.CrewIdentity()); err != nil {
			return fmt.Errorf("AssignCrewRole(alice): %w", err)
		}
		if err := (&crewidentity.AssignCrewRole{UserID: bob, Role: "BulletinOfficer"}).Execute(ctx, txn, w.app.CrewIdentity()); err != nil {
			return fmt.Errorf("AssignCrewRole(bob): %w", err)
		}
		patch, err := resources.NewClientCreatePatch()
		if err != nil {
			return fmt.Errorf("resources.NewClientCreatePatch(): %w", err)
		}
		patch.SetName(clientName).SetContactName("Ada Byron").SetContactEmail("ada@example.com").SetTrusted(true)
		if err := patch.Buffer(ctx, txn, eventsource.UserEvent(ctx)); err != nil {
			return fmt.Errorf("resources.ClientCreatePatch.Buffer(): %w", err)
		}
		clientID = patch.ID()

		return nil
	}); err != nil {
		t.Fatalf("one transaction with two methods and an application write: %v", err)
	}
	// Each person holds the role the transaction gave: the memberships landed together,
	// held by the id of each account.
	for _, membership := range []struct {
		user accesstypes.User
		role string
	}{{userOf(alice), "ClientBrowser"}, {userOf(bob), "BulletinOfficer"}} {
		if got := readColumn[string](ctx, t, w.db, "CrewUserRoles", crewMembershipKey(membership.role, membership.user), "UserId"); got != string(membership.user) {
			t.Errorf("the membership of %s in %s: user = %q", membership.user, membership.role, got)
		}
	}
	if got := readColumn[string](ctx, t, w.db, "Clients", spanner.Key{clientID.String()}, "Name"); got != clientName {
		t.Errorf("the application's client = %q, want %q", got, clientName)
	}

	// The rollback: the first method succeeds, the second fails at the commit, nothing lands.
	missing, err := impulse.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	carol := w.seedCrewUser(ctx, t, "carol-"+sfx, "CrewCommon")
	err = w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := (&crewidentity.AssignCrewRole{UserID: carol, Role: "ClientBrowser"}).Execute(ctx, txn, w.app.CrewIdentity()); err != nil {
			return fmt.Errorf("AssignCrewRole(carol): %w", err)
		}
		if err := (&crewidentity.DisableCrewUser{UserID: missing}).Execute(ctx, txn, w.app.CrewIdentity()); err != nil {
			return fmt.Errorf("DisableCrewUser(): %w", err)
		}

		return nil
	})
	if err == nil {
		t.Fatal("a transaction disabling a user that does not exist committed; want a refusal")
	}
	if _, err := w.db.Single().ReadRow(ctx, "CrewUserRoles", crewMembershipKey("ClientBrowser", userOf(carol)), []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("carol's membership after the rollback: err = %v, want not found", err)
	}

	// Outside any request: the application deletes an account in a transaction of its own
	// with no session in the context, as a job does. The store names the request's person
	// as who made the delete only where there is one.
	dave := w.seedCrewUser(ctx, t, "dave-"+sfx)
	err = w.inTransaction(t.Context(), func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if _, err := (&crewidentity.DeleteCrewUser{UserID: dave}).Execute(ctx, txn, w.app.CrewIdentity()); err != nil {
			return fmt.Errorf("DeleteCrewUser(): %w", err)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("deleting an account outside any request: %v", err)
	}
	if _, err := w.db.Single().ReadRow(ctx, "CrewSessionUsers", spanner.Key{dave.String()}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the account after its delete outside a request: err = %v, want not found", err)
	}
}

// TestGeneratedIdentityHTTPFaces holds the HTTP face: the generated routes serve the list,
// the read and the methods, checked against the same grants as the application's own
// routes. Headquarters is served and the cadet refused, on every one.
func TestGeneratedIdentityHTTPFaces(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	h := w.h
	ctx := t.Context()
	username := "dana-" + identitySuffix()
	dana := w.seedCrewUser(ctx, t, username, "CrewCommon", "ClientBrowser")
	id := dana.String()

	// The person's first request is served with the roles: the client list is the
	// client browser's.
	status, body := doRequestAs(t, h, accesstypes.User(username), http.MethodGet, consoleAPI+"/clients?limit=1", "")
	assertStatus(t, status, http.StatusOK, body)

	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users/"+id, "")
	assertStatus(t, status, http.StatusOK, body)
	row := decodeRow(t, body)
	if got := cell[string](t, row, "username"); got != username {
		t.Errorf("username = %q, want %q", got, username)
	}
	if got := cell[bool](t, row, "disabled"); got {
		t.Errorf("disabled = true before the method ran")
	}

	status, body = doRequestAs(t, h, "cadet", http.MethodGet, consoleAPI+"/crew-session-users", "")
	if status != http.StatusForbidden {
		t.Errorf("the cadet's list: status %d, want 403: %s", status, body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users?limit=200", "")
	assertStatus(t, status, http.StatusOK, body)
	rows := decodeRows(t, body)
	if _, ok := rowsByID(t, rows, "id")[id]; !ok {
		t.Errorf("the list of %d rows lacks the person %s", len(rows), id)
	}

	status, body = doRequestAs(t, h, "cadet", http.MethodPost, consoleAPI+"/disable-crew-user", `{"userId":"`+id+`"}`)
	if status != http.StatusForbidden {
		t.Errorf("the cadet's disable-crew-user: status %d, want 403: %s", status, body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/disable-crew-user", `{"userId":"`+id+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users/"+id, "")
	assertStatus(t, status, http.StatusOK, body)
	if got := cell[bool](t, decodeRow(t, body), "disabled"); !got {
		t.Errorf("disabled = false after disable-crew-user")
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/enable-crew-user", `{"userId":"`+id+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users/"+id, "")
	assertStatus(t, status, http.StatusOK, body)
	if got := cell[bool](t, decodeRow(t, body), "disabled"); got {
		t.Errorf("disabled = true after enable-crew-user")
	}

	// Nothing writes the tables over HTTP but the methods: the resources carry no patch.
	status, body = doRequestAs(t, h, "governor", http.MethodPatch, consoleAPI+"/crew-session-users/"+id, `{"disabled":true}`)
	if status != http.StatusMethodNotAllowed && status != http.StatusNotFound {
		t.Errorf("a patch of the users table: status %d, want no such route: %s", status, body)
	}
}

// TestGeneratedIdentityDisableEndsSession holds the session half, on the served stack
// with the real session manager. A person signed in is disabled through the generated
// method; the sign-in path, untouched, refuses their next request and their next sign-in
// from the rows the method wrote.
func TestGeneratedIdentityDisableEndsSession(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	if status, body := marshal.do(ctx, http.MethodGet, consoleAPI+"/user-domains", nil); status != http.StatusOK {
		t.Fatalf("the marshal before the method ran: status %d: %s", status, body)
	}

	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-users?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	var marshalID string
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "username") == "marshal" {
			marshalID = cell[string](t, row, "id")
		}
	}
	if marshalID == "" {
		t.Fatalf("the list of the crew's users does not carry the marshal: %s", body)
	}
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-sessions?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	live := 0
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "username") == "marshal" && !cell[bool](t, row, "expired") {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("the marshal's live sessions before the method ran = %d, want 1: %s", live, body)
	}

	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/disable-crew-user", []byte(`{"userId":"`+marshalID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)

	if status, body := marshal.do(ctx, http.MethodGet, consoleAPI+"/user-domains", nil); status != http.StatusUnauthorized {
		t.Errorf("the marshal's next request after the method ran: status %d, want 401: %s", status, body)
	}
	again := newBrowser(t, s, consoleAPI)
	if status, body := again.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("priming the XSRF cookie: status %d: %s", status, body)
	}
	if status, body := again.login(ctx, "marshal", personaPassword); status != http.StatusUnauthorized {
		t.Errorf("the disabled marshal's sign-in: status %d, want 401: %s", status, body)
	}
}

// TestGeneratedIdentityMembershipInTransaction holds the grant half. A membership is
// written by the generated method inside the application's transaction, and the
// permission engine serves it at the very next check with no word from the caller: the method registers the telling on the transaction, which runs it
// once the commit lands. A membership in a transaction that fails never reaches the
// engine.
func TestGeneratedIdentityMembershipInTransaction(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	db, h, engine := w.db, w.h, w.engine
	ctx := sessionContext(t, "governor")
	sfx := identitySuffix()
	erin, frank := accesstypes.User("erin-"+sfx), accesstypes.User("frank-"+sfx)

	// The Go face: the membership lands in a transaction of the application's, the role
	// given to the account by its id.
	erinID, frankID := w.seedCrewUser(ctx, t, string(erin), "CrewCommon"), w.seedCrewUser(ctx, t, string(frank), "CrewCommon")
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return w.assignHeadquarters(ctx, txn, erinID)
	}); err != nil {
		t.Fatalf("a membership in a transaction of the application's: %v", err)
	}
	erinUser, frankUser := knownAs(t, erin, erinID), knownAs(t, frank, frankID)
	if got := readColumn[string](ctx, t, db, "CrewUserRoles", crewMembershipKey("Headquarters", erinUser), "UserId"); got != string(erinUser) {
		t.Errorf("the membership row's user = %q, want the id of erin's account", got)
	}
	status, body := doRequestAs(t, h, erin, http.MethodGet, consoleAPI+"/crew-session-users?limit=1", "")
	assertStatus(t, status, http.StatusOK, body)
	status, body = doRequestAs(t, h, frank, http.MethodGet, consoleAPI+"/crew-session-users?limit=1", "")
	if status != http.StatusForbidden {
		t.Errorf("frank, who holds no role that lists the crew's users: status %d, want 403: %s", status, body)
	}
	digest, err := engine.UserPermissionDigest(ctx, erinUser, accesstypes.GlobalScope())
	if err != nil {
		t.Fatalf("UserPermissionDigest(erin): %v", err)
	}
	if got := digest["CrewSessionUsers"]["List"]; got != accesstypes.DigestGranted {
		t.Errorf("erin's digest for List CrewSessionUsers = %v, want granted", got)
	}

	// The HTTP face: headquarters gives frank the role, and frank's next request is
	// served; the list of the memberships carries it; headquarters takes it back, and
	// frank's next request is refused.
	assign := `{"userId":"` + frankID.String() + `","role":"Headquarters"}`
	status, body = doRequestAs(t, h, "cadet", http.MethodPost, consoleAPI+"/assign-crew-role", assign)
	if status != http.StatusForbidden {
		t.Errorf("the cadet's assign-crew-role: status %d, want 403: %s", status, body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", assign)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, frank, http.MethodGet, consoleAPI+"/crew-session-users?limit=1", "")
	assertStatus(t, status, http.StatusOK, body)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-user-roles?limit=200", "")
	assertStatus(t, status, http.StatusOK, body)
	held := false
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "userId") == string(frankUser) && cell[string](t, row, "role") == "Headquarters" && cell[string](t, row, "kind") == "global" {
			held = true
		}
	}
	if !held {
		t.Errorf("the list of the crew's memberships lacks frank's: %s", body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", `{"userId":"`+frankID.String()+`","role":"NoSuchRole"}`)
	if status != http.StatusNotFound {
		t.Errorf("assigning a role that does not exist: status %d, want 404: %s", status, body)
	}
	// A sector's role does not exist in the global scope, and given in every sector it
	// is written where the permission store keeps a role held everywhere.
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", `{"userId":"`+frankID.String()+`","role":"Pilot"}`)
	if status != http.StatusNotFound {
		t.Errorf("assigning a sector's role in the global scope: status %d, want 404: %s", status, body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", `{"userId":"`+frankID.String()+`","role":"Pilot","everyDomain":true}`)
	assertStatus(t, status, http.StatusNoContent, body)
	every := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), frankUser, "Pilot")
	if got := readColumn[string](ctx, t, db, "CrewUserRoles", spanner.Key{every.Kind, every.Axis, every.Domain, string(every.Role), string(every.User)}, "UserId"); got != string(frankUser) {
		t.Errorf("the membership of a role given in every sector: user = %q, want the id of frank's account", got)
	}
	// A custom role is no entry of the role file: the rule reads it from the roles table
	// in the transaction that writes the membership.
	if err := engine.UserManager().AddRole(ctx, accesstypes.GlobalPolicyScope(), "Auditor", "Reads the books."); err != nil {
		t.Fatalf("AddRole(Auditor): %v", err)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", `{"userId":"`+frankID.String()+`","role":"Auditor"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	if got := readColumn[string](ctx, t, db, "CrewUserRoles", crewMembershipKey("Auditor", frankUser), "UserId"); got != string(frankUser) {
		t.Errorf("the custom role's membership row's user = %q", got)
	}
	// The custom roles are a list of the management: headquarters reads the role with
	// its description, and a role that holds no grant on the list is refused.
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-roles?limit=200", "")
	assertStatus(t, status, http.StatusOK, body)
	listed := false
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "role") == "Auditor" && cell[string](t, row, "kind") == "global" && cell[string](t, row, "description") == "Reads the books." {
			listed = true
		}
	}
	if !listed {
		t.Errorf("the list of the crew's custom roles lacks Auditor: %s", body)
	}
	if status, body := doRequestAs(t, h, "cadet", http.MethodGet, consoleAPI+"/crew-roles?limit=200", ""); status != http.StatusForbidden {
		t.Errorf("the cadet's list of the crew's custom roles: status %d, want 403: %s", status, body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", `{"userId":"`+frankID.String()+`","role":"Auditor","domain":"`+anvil+`","everyDomain":true}`)
	if status != http.StatusBadRequest {
		t.Errorf("a role asked for in one domain and in every domain: status %d, want 400: %s", status, body)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", assign)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, frank, http.MethodGet, consoleAPI+"/crew-session-users?limit=1", "")
	if status != http.StatusForbidden {
		t.Errorf("frank after remove-crew-role: status %d, want 403: %s", status, body)
	}

	// The served method names a person in its request, so it reads the account before
	// it gives the role: an account that does not exist is refused, and nothing lands.
	// An account that exists is given the role by the same call.
	hopeID, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	err = w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return (&crewidentity.AssignCrewRole{UserID: hopeID, Role: "Headquarters"}).Execute(ctx, txn, w.app.CrewIdentity())
	})
	if !httpio.HasNotFound(err) {
		t.Errorf("the method giving a role to an account that does not exist: err = %v, want not found", err)
	}
	if _, err := db.Single().ReadRow(ctx, "CrewUserRoles", crewMembershipKey("Headquarters", userOf(hopeID)), []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("a membership of the account that does not exist, after the refused transaction: err = %v, want not found", err)
	}
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return (&crewidentity.AssignCrewRole{UserID: frankID, Role: "BulletinOfficer"}).Execute(ctx, txn, w.app.CrewIdentity())
	}); err != nil {
		t.Errorf("the method giving a role to an account that exists: %v", err)
	}
	if got := readColumn[string](ctx, t, db, "CrewUserRoles", crewMembershipKey("BulletinOfficer", frankUser), "UserId"); got != string(frankUser) {
		t.Errorf("the membership the method wrote: user = %q, want the id of frank's account", got)
	}

	// The rollback: a membership buffered beside a write that fails never lands.
	gina := accesstypes.User("gina-" + sfx)
	ginaID := w.seedCrewUser(ctx, t, string(gina), "CrewCommon")
	err = w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := w.assignHeadquarters(ctx, txn, ginaID); err != nil {
			return err
		}

		return errors.New("the application's own write failed")
	})
	if err == nil {
		t.Fatal("a failing transaction committed")
	}
	if _, err := db.Single().ReadRow(ctx, "CrewUserRoles", crewMembershipKey("Headquarters", knownAs(t, gina, ginaID)), []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("gina's membership after the rollback: err = %v, want not found", err)
	}
	if status, body := doRequestAs(t, h, gina, http.MethodGet, consoleAPI+"/crew-session-users?limit=1", ""); status != http.StatusForbidden {
		t.Errorf("gina after the rollback: status %d, want 403: %s", status, body)
	}
}

// TestGeneratedIdentityRules holds the rules the user management keeps, on the generated
// methods' routes: nobody disables their own account; the service's last super
// administrator keeps the role; a person who removes a role from themselves in a sector
// keeps an administrator role nobody else would hold there (the marshal's, which a
// marshal of every sector does not stand in for: they are no marshal of the sector's
// own); and the removal of a person's last role anywhere leaves the account standing,
// holding nothing.
func TestGeneratedIdentityRules(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	h, ctx := w.h, t.Context()
	sfx := identitySuffix()
	hana, ivan := accesstypes.User("hana-"+sfx), accesstypes.User("ivan-"+sfx)
	// The sector's rule is held in a domain of its own, an id no seeded sector carries,
	// so the seeded sectors' marshals stand apart from it.
	rules, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	sector := rules.String()

	// The last administrator of the application is its last super administrator: the
	// governor is the one holder of the role who can sign in and may not give it up.
	// The methods name a person by the id of their account: user is that id, as text.
	superAdministrator := func(user accesstypes.User) string {
		return `{"userId":"` + string(user) + `","role":"SuperAdministrator"}`
	}
	governor := account(t, "governor")
	status, body := doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", superAdministrator(governor))
	if status != http.StatusForbidden || !strings.Contains(string(body), "You are the last super administrator who can sign in. Give another person the Super administrator role before you remove your own.") {
		t.Errorf("the governor giving up the last Super Administrator role: status %d, want 403 naming the last super administrator: %s", status, body)
	}
	if got := readColumn[string](ctx, t, w.db, "CrewUserRoles", crewMembershipKey("SuperAdministrator", governor), "UserId"); got != string(governor) {
		t.Errorf("the governor's Super Administrator membership after the refusal: user = %q, want it kept", got)
	}

	// The two people the rules are held over, each an account with the one global role:
	// a request made as the username runs as the account.
	hanaID, ivanID := w.seedCrewUser(ctx, t, string(hana), "Headquarters").String(), w.seedCrewUser(ctx, t, string(ivan), "CrewCommon").String()
	hanaUser, ivanUser := accesstypes.User(hanaID), accesstypes.User(ivanID)

	// Nobody disables themselves: hana holds the grant and is refused her own account;
	// her request for another person's goes through.
	status, body = doRequestAs(t, h, hana, http.MethodPost, consoleAPI+"/disable-crew-user", `{"userId":"`+hanaID+`"}`)
	if status != http.StatusForbidden || !strings.Contains(string(body), "your own account") {
		t.Errorf("hana disabling her own account: status %d, want 403 naming her own account: %s", status, body)
	}
	status, body = doRequestAs(t, h, hana, http.MethodPost, consoleAPI+"/disable-crew-user", `{"userId":"`+ivanID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, hana, http.MethodPost, consoleAPI+"/enable-crew-user", `{"userId":"`+ivanID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)

	// The last administrator of a sector. The governor is marshal of every sector, and
	// that makes nobody a marshal of the sector's own: hana is the sector's one marshal
	// and keeps the role, refused in the rule's words, while the governor holds it
	// everywhere. The governor gives up the role held everywhere (no administrator role
	// of one sector, so it goes), and hana gives up hers once ivan holds it in the
	// sector too.
	marshal := func(user accesstypes.User) string {
		return `{"userId":"` + string(user) + `","role":"SectorMarshal","domain":"` + sector + `"}`
	}
	membership := access.NewMembershipKey(accesstypes.DomainPolicyScope(accesstypes.Domain(sector)), hanaUser, "SectorMarshal")
	heldKey := spanner.Key{membership.Kind, membership.Axis, membership.Domain, string(membership.Role), string(membership.User)}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", marshal(hanaUser))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, hana, http.MethodPost, consoleAPI+"/remove-crew-role", marshal(hanaUser))
	if status != http.StatusForbidden || !strings.Contains(string(body), "You are the last administrator of this sector. Give another person the role before you remove your own.") {
		t.Errorf("hana giving up the sector's last marshal role while the governor is marshal of every sector: status %d, want 403 naming the rule: %s", status, body)
	}
	if got := readColumn[string](ctx, t, w.db, "CrewUserRoles", heldKey, "UserId"); got != string(hanaUser) {
		t.Errorf("hana's marshal membership after the refusal: user = %q, want it kept", got)
	}
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", `{"userId":"`+string(governor)+`","role":"SectorMarshal","everyDomain":true}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", marshal(ivanUser))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, hana, http.MethodPost, consoleAPI+"/remove-crew-role", marshal(hanaUser))
	assertStatus(t, status, http.StatusNoContent, body)
	if _, err := w.db.Single().ReadRow(ctx, "CrewUserRoles", heldKey, []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("hana's marshal membership once another marshal stands: err = %v, want not found", err)
	}

	// The last role anywhere: ivan holds two; after either removal the account stands,
	// and after the second it holds nothing.
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", marshal(ivanUser))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users/"+ivanID, "")
	assertStatus(t, status, http.StatusOK, body)
	status, body = doRequestAs(t, h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", `{"userId":"`+ivanID+`","role":"CrewCommon"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users/"+ivanID, "")
	if status != http.StatusOK {
		t.Errorf("ivan's account after his last role went: status %d, want 200, the account kept: %s", status, body)
	}
	if got := crewHeldBy(t, w.db, ivanID); len(got) != 0 {
		t.Errorf("ivan holds %v after his last role went, want nothing", got)
	}

	// Hana's own last role: she gives up headquarters, which the governor holds too,
	// and the account stays, holding nothing, as anyone's does.
	status, body = doRequestAs(t, h, hana, http.MethodPost, consoleAPI+"/remove-crew-role", `{"userId":"`+hanaID+`","role":"Headquarters"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, consoleAPI+"/crew-session-users/"+hanaID, "")
	if status != http.StatusOK {
		t.Errorf("hana's account after her last role went: status %d, want 200, the account kept: %s", status, body)
	}
	if got := crewHeldBy(t, w.db, hanaID); len(got) != 0 {
		t.Errorf("hana holds %v after her last role went, want nothing", got)
	}
}

// TestGeneratedIdentityAccounts holds the account actions on the served stack, with the
// real session manager: a rename that leaves the session and the roles where they are
// (both belong to the account, whose id a rename does not change), the reset of the
// second factor, and a delete; nobody deletes themselves. Nobody sets a password for
// someone else: the link a person chooses a new one through is TestPasswordResetLink's.
func TestGeneratedIdentityAccounts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)
	userDomains := consoleAPI + "/user-domains"

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	post := func(method, body string) (int, []byte) {
		return hq.do(ctx, http.MethodPost, consoleAPI+"/"+method, []byte(body))
	}

	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}

	// A rename: the marshal's session stands under the new name and the roles stay
	// where they were, the sector's among them. A role is held by the id of the
	// account, which a rename does not change, so the membership row is the same row
	// before and after, and nothing is held under either username. A name another user
	// holds is refused.
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	marshalID := crewAccountID(ctx, t, hq, "marshal")
	anvilScope := accesstypes.DomainPolicyScope(anvil)
	membership := access.NewMembershipKey(anvilScope, accesstypes.User(marshalID), "SectorMarshal")
	membershipKey := spanner.Key{membership.Kind, membership.Axis, membership.Domain, string(membership.Role), string(membership.User)}
	given := readColumn[time.Time](ctx, t, s.db, "CrewUserRoles", membershipKey, "CreatedAt")
	status, body := post("rename-crew-user", `{"userId":"`+marshalID+`","username":"cadet"}`)
	if status != http.StatusConflict {
		t.Errorf("a rename to a name another user holds: status %d, want 409: %s", status, body)
	}
	status, body = post("rename-crew-user", `{"userId":"`+marshalID+`","username":"marshal-of-anvil"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = marshal.do(ctx, http.MethodGet, userDomains, nil)
	assertStatus(t, status, http.StatusOK, body)
	if !strings.Contains(string(body), `"`+anvil+`"`) {
		t.Errorf("the renamed marshal's sectors = %s, want Anvil among them", body)
	}
	if got := readColumn[string](ctx, t, s.db, "CrewUserRoles", membershipKey, "UserId"); got != marshalID {
		t.Errorf("the marshal's membership after the rename: user = %q, want the id of the account %s", got, marshalID)
	}
	if again := readColumn[time.Time](ctx, t, s.db, "CrewUserRoles", membershipKey, "CreatedAt"); !again.Equal(given) {
		t.Errorf("the marshal's membership was given at %s before the rename and %s after: a rename writes no membership", given, again)
	}
	for _, name := range []accesstypes.User{"marshal", "marshal-of-anvil"} {
		named := access.NewMembershipKey(anvilScope, name, "SectorMarshal")
		if _, err := s.db.Single().ReadRow(ctx, "CrewUserRoles", spanner.Key{named.Kind, named.Axis, named.Domain, string(named.Role), string(named.User)}, []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
			t.Errorf("a membership under the username %s: err = %v, want not found", name, err)
		}
	}
	if status, body := stranger.login(ctx, "marshal", personaPassword); status != http.StatusUnauthorized {
		t.Errorf("a sign-in under the old name: status %d, want 401: %s", status, body)
	}

	// The second factor's reset: the browser the marshal asked to be remembered is
	// forgotten, and the next sign-in from it waits for the factor again.
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-trusted-devices?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	remembered := 0
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "userId") == marshalID && row["revokedAt"] == nil {
			remembered++
		}
	}
	if remembered != 1 {
		t.Fatalf("the marshal's remembered browsers = %d, want the one the sign-in asked for: %s", remembered, body)
	}
	status, body = marshal.login(ctx, "marshal-of-anvil", personaPassword)
	assertStatus(t, status, http.StatusOK, body)
	if heldForFactor(t, body) {
		t.Fatalf("the marshal's remembered browser was asked for the factor before the reset: %s", body)
	}
	status, body = post("revoke-crew-user-trusted-devices", `{"userId":"`+marshalID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = marshal.login(ctx, "marshal-of-anvil", personaPassword)
	assertStatus(t, status, http.StatusOK, body)
	if !heldForFactor(t, body) {
		t.Errorf("the marshal's browser after the reset was not asked for the factor: %s", body)
	}

	// A delete: nobody deletes themselves; the marshal goes with the sessions and the
	// roles, and a second delete finds nobody.
	governorID := crewAccountID(ctx, t, hq, "governor")
	status, body = post("delete-crew-user", `{"userId":"`+governorID+`"}`)
	if status != http.StatusForbidden || !strings.Contains(string(body), "your own account") {
		t.Errorf("the governor deleting their own account: status %d, want 403 naming their own account: %s", status, body)
	}
	status, body = post("delete-crew-user", `{"userId":"`+marshalID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := marshal.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusUnauthorized {
		t.Errorf("the deleted marshal's session: status %d, want 401: %s", status, body)
	}
	if _, err := s.db.Single().ReadRow(ctx, "CrewSessionUsers", spanner.Key{marshalID}, []string{"Username"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the marshal's row after the delete: err = %v, want not found", err)
	}
	if _, err := s.db.Single().ReadRow(ctx, "CrewUserRoles", membershipKey, []string{"UserId"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the marshal's membership after the delete: err = %v, want not found", err)
	}
	if status, body := stranger.login(ctx, "marshal-of-anvil", personaPassword); status != http.StatusUnauthorized {
		t.Errorf("a sign-in as the deleted marshal: status %d, want 401: %s", status, body)
	}
	status, body = post("delete-crew-user", `{"userId":"`+marshalID+`"}`)
	if status != http.StatusNotFound {
		t.Errorf("a second delete: status %d, want 404: %s", status, body)
	}
}

// TestGeneratedIdentityUnlock holds the cleared lockout on the served stack, with the
// real session manager and its limiter: a name locked by failed sign-ins is refused
// even its right password; headquarters clears it through the generated method, which
// deletes the name's row; and the person signs in once the instance has read the name
// again, which it does within the few seconds it trusts what it remembers.
func TestGeneratedIdentityUnlock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")

	// The pilot signs in once, so the browser is remembered and a later sign-in is the
	// password alone; then the name is locked by wrong passwords from elsewhere.
	pilot := newBrowser(t, s, consoleAPI)
	pilot.signIn(ctx, "pilot")
	if status, body := pilot.do(ctx, http.MethodDelete, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the pilot's logout: status %d: %s", status, body)
	}
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	const failuresToLock = 5 // the session library's default limit
	for range failuresToLock {
		if status, body := stranger.login(ctx, "pilot", "not the password"); status != http.StatusUnauthorized {
			t.Fatalf("a wrong password: status %d, want 401: %s", status, body)
		}
	}
	if status, body := pilot.login(ctx, "pilot", personaPassword); status != http.StatusTooManyRequests {
		t.Fatalf("the locked pilot's right password: status %d, want 429: %s", status, body)
	}

	// The list shows the lock, and the row's Execute answer offers the method.
	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-sign-in-attempts?limit=200&capabilities=Execute", nil)
	assertStatus(t, status, http.StatusOK, body)
	locked := false
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "name") == "pilot" && row["lockedUntil"] != nil {
			locked = true
		}
	}
	if !locked {
		t.Fatalf("the list of sign-in attempts does not show the pilot locked: %s", body)
	}

	// A cadet holds no grant on the method; headquarters clears the lock.
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	if status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/unlock-crew-sign-in", []byte(`{"name":"pilot"}`)); status != http.StatusForbidden {
		t.Errorf("the cadet's unlock: status %d, want 403: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/unlock-crew-sign-in", []byte(`{"name":"pilot"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if _, err := s.db.Single().ReadRow(ctx, "CrewSessionSignInAttempts", spanner.Key{"pilot"}, []string{"Failures"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the pilot's row after the unlock: err = %v, want not found", err)
	}
	if status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/unlock-crew-sign-in", []byte(`{"name":"pilot"}`)); status != http.StatusNotFound {
		t.Errorf("an unlock of a name with no count: status %d, want 404: %s", status, body)
	}

	// The pilot signs in with the right password once the instance reads the name
	// again: within the limiter's read window of five seconds, and never at once.
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, body = pilot.login(ctx, "pilot", personaPassword)
		if status == http.StatusOK {
			break
		}
		if status != http.StatusTooManyRequests {
			t.Fatalf("the unlocked pilot's sign-in: status %d, want 429 while the lock is remembered, then 200: %s", status, body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pilot is still refused 20s after the unlock: %s", body)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if heldForFactor(t, body) {
		t.Fatalf("the pilot's remembered browser was asked for the factor: %s", body)
	}
	if status, body := pilot.do(ctx, http.MethodGet, consoleAPI+"/user-domains", nil); status != http.StatusOK {
		t.Errorf("the unlocked pilot's first request: status %d, want 200: %s", status, body)
	}
}

// crewAccountID finds a crew user's id in the generated list of the auth's users.
func crewAccountID(ctx context.Context, t *testing.T, hq *browser, username string) string {
	t.Helper()

	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-users?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "username") == username {
			return cell[string](t, row, "id")
		}
	}
	t.Fatalf("the list of the crew's users does not carry %s: %s", username, body)

	return ""
}

// standingImpersonation finds the session of the one impersonation record that names
// the actor and has not ended.
func standingImpersonation(ctx context.Context, t *testing.T, hq *browser, actor string) string {
	t.Helper()

	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-impersonations?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	var sessions []string
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "actorUsername") == actor && row["endedAt"] == nil {
			sessions = append(sessions, cell[string](t, row, "sessionId"))
		}
	}
	if len(sessions) != 1 {
		t.Fatalf("standing impersonation records of %s = %v, want one: %s", actor, sessions, body)
	}

	return sessions[0]
}

// impersonationRecord reads one impersonation record through its generated read route.
func impersonationRecord(ctx context.Context, t *testing.T, hq *browser, sessionID string) map[string]any {
	t.Helper()

	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-impersonations/"+sessionID, nil)
	assertStatus(t, status, http.StatusOK, body)

	return decodeRow(t, body)
}

// TestGeneratedIdentityEndSessions holds the three ways a session is ended, on the
// served stack with the real session manager, which is told nothing: it refuses the
// sessions from the rows the generated methods wrote. All of a person's sessions are
// their own and the ones they hold as someone else, found by their name in the
// impersonation records, each record ending with its session; one session ends alone;
// and an impersonated session ends by its record, which a person's own session has not.
func TestGeneratedIdentityEndSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)
	userDomains := consoleAPI + "/user-domains"

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	viewing := newBrowser(t, s, consoleAPI)
	viewing.signIn(ctx, "marshal")
	if status, body := viewing.impersonate(ctx, viewAs(t, "cadet", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the cadet: status %d: %s", status, body)
	}
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	for name, b := range map[string]*browser{"the marshal's own session": own, "the marshal's session as the cadet": viewing, "the cadet's own session": cadet} {
		if status, body := b.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
			t.Fatalf("%s before anything ended: status %d: %s", name, status, body)
		}
	}
	heldAsCadet := standingImpersonation(ctx, t, hq, "marshal")

	// All of the marshal's sessions: both of their own, and the one they hold as the
	// cadet, which is the cadet's by its user and the marshal's by its record.
	marshalID := crewAccountID(ctx, t, hq, "marshal")
	status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/end-crew-user-sessions", []byte(`{"userId":"`+marshalID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	for name, b := range map[string]*browser{"the marshal's own session": own, "the marshal's session as the cadet": viewing} {
		if status, body := b.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusUnauthorized {
			t.Errorf("%s after end-crew-user-sessions: status %d, want 401: %s", name, status, body)
		}
	}
	if status, body := cadet.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
		t.Errorf("the cadet's own session after the marshal's ended: status %d, want 200: %s", status, body)
	}
	record := impersonationRecord(ctx, t, hq, heldAsCadet)
	if record["endedAt"] == nil || record["endReason"] != "Revoked" {
		t.Errorf("the record of the marshal's session as the cadet: endedAt = %v, endReason = %v, want it ended as Revoked", record["endedAt"], record["endReason"])
	}

	// The marshal was not disabled: they sign in again, and that one session is ended
	// by its id.
	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, "marshal")
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-sessions?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	var standing []string
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "username") == "marshal" && !cell[bool](t, row, "expired") {
			standing = append(standing, cell[string](t, row, "id"))
		}
	}
	if len(standing) != 1 {
		t.Fatalf("the marshal's standing sessions = %v, want the one just established: %s", standing, body)
	}
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/end-crew-session", []byte(`{"sessionId":"`+standing[0]+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := again.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusUnauthorized {
		t.Errorf("the marshal's session after end-crew-session: status %d, want 401: %s", status, body)
	}
	if status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/end-crew-session", []byte(`{"sessionId":"`+standing[0]+`"}`)); status != http.StatusForbidden {
		t.Errorf("the cadet's end-crew-session: status %d, want 403: %s", status, body)
	}

	// An impersonated session ends by its record: the governor working as a role in a
	// second browser. A session that was never impersonated has no record to name.
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "governor")
	if status, body := acting.impersonate(ctx, `{"kind":"role","principal":"Dispatcher","reason":"walkthrough"}`); status != http.StatusOK {
		t.Fatalf("the governor acting as Dispatcher: status %d: %s", status, body)
	}
	asDispatcher := standingImpersonation(ctx, t, hq, "governor")
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/end-crew-session-impersonation", []byte(`{"sessionId":"`+asDispatcher+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := acting.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusUnauthorized {
		t.Errorf("the governor's session as Dispatcher after end-crew-session-impersonation: status %d, want 401: %s", status, body)
	}
	record = impersonationRecord(ctx, t, hq, asDispatcher)
	if record["endedAt"] == nil || record["endReason"] != "Revoked" {
		t.Errorf("the record of the governor's session as Dispatcher: endedAt = %v, endReason = %v, want it ended as Revoked", record["endedAt"], record["endReason"])
	}
	if status, body := hq.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
		t.Errorf("the governor's own session after their impersonated one ended: status %d, want 200: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/end-crew-session-impersonation", []byte(`{"sessionId":"`+standing[0]+`"}`))
	if status != http.StatusNotFound {
		t.Errorf("end-crew-session-impersonation of a session that was never impersonated: status %d, want 404: %s", status, body)
	}
}

// TestGeneratedIdentityCredentials holds the three methods that act on how a person signs
// in, on the served stack with the real session manager: a required password change
// binds the sessions a person already holds and is refused for a person with no
// password; an identity is unlinked unless it is the last way in of a person with no
// password; and a revoked bearer token is refused at its very next request.
func TestGeneratedIdentityCredentials(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)
	userDomains := consoleAPI + "/user-domains"

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")

	// A password change required of the cadet, who holds a password and a session.
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	cadetID := crewAccountID(ctx, t, hq, "cadet")
	status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/require-crew-password-change", []byte(`{"userId":"`+cadetID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := cadet.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusForbidden || !strings.Contains(string(body), string(sessioninfo.RefusedPasswordChangeRequired)) {
		t.Errorf("the cadet's next request after require-crew-password-change: status %d, want 403 naming %s: %s", status, sessioninfo.RefusedPasswordChangeRequired, body)
	}

	// A person with no password cannot be required to change one.
	directoryOnly := s.seedCrewAccount(ctx, t, "directory-only", nil).String()
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/require-crew-password-change", []byte(`{"userId":"`+directoryOnly+`"}`))
	if status != http.StatusBadRequest {
		t.Errorf("require-crew-password-change of a person with no password: status %d, want 400: %s", status, body)
	}

	// Identities, written as the crew's open Google sign-up writes one for a personal
	// Google account, under the connection of any Google account, which that sign-in
	// takes, so each is a way in: two for the person with no password, one for the
	// marshal, who holds a password. No sector's directory can be that connection, so no
	// sector manages either person's account, and the fleet's unlink reaches each.
	marshalID := crewAccountID(ctx, t, hq, "marshal")
	link := func(userID, subject string) string {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": id.String(), "UserId": userID, "Method": "google", "Connection": signin.GoogleAnyAccountConnection, "Subject": subject,
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		})}); err != nil {
			t.Fatalf("writing an identity of %s: %v", userID, err)
		}

		return id.String()
	}
	first, second, marshals := link(directoryOnly, "subject-1"), link(directoryOnly, "subject-2"), link(marshalID, "subject-3")
	unlink := func(id string) (int, []byte) {
		return hq.do(ctx, http.MethodPost, consoleAPI+"/unlink-crew-session-identity", []byte(`{"identityId":"`+id+`"}`))
	}
	status, body = unlink(first)
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := unlink(second); status != http.StatusConflict {
		t.Errorf("unlinking the last identity of a person with no password: status %d, want 409: %s", status, body)
	}
	status, body = unlink(marshals)
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := unlink(first); status != http.StatusNotFound {
		t.Errorf("unlinking an identity that is gone: status %d, want 404: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-identities?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	left := rowsByID(t, decodeRows(t, body), "id")
	if _, ok := left[second]; !ok || len(left) != 1 {
		t.Errorf("the identities that remain = %v, want the one that could not be unlinked (%s)", left, second)
	}

	// The droid's bearer token: served, revoked, refused.
	if status, body := droid(ctx, t, s, droidsAPIKey, http.MethodGet, "/droids/features", nil); status != http.StatusOK {
		t.Fatalf("the droid's token before it is revoked: status %d: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-tokens?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	tokens := decodeRows(t, body)
	if len(tokens) != 1 || tokens[0]["revokedAt"] != nil {
		t.Fatalf("the crew's tokens = %v, want the droid's one live token", tokens)
	}
	if _, served := tokens[0]["tokenHash"]; served {
		t.Errorf("the token's row carries its hash: %v", tokens[0])
	}
	tokenID := cell[string](t, tokens[0], "id")
	if status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/revoke-crew-session-token", []byte(`{"tokenId":"`+tokenID+`"}`)); status != http.StatusForbidden {
		t.Errorf("the cadet's revoke-crew-session-token: status %d, want 403: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/revoke-crew-session-token", []byte(`{"tokenId":"`+tokenID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := droid(ctx, t, s, droidsAPIKey, http.MethodGet, "/droids/features", nil); status != http.StatusUnauthorized {
		t.Errorf("the droid's token after revoke-crew-session-token: status %d, want 401: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-tokens/"+tokenID, nil)
	assertStatus(t, status, http.StatusOK, body)
	if row := decodeRow(t, body); row["revokedAt"] == nil || row["revokedReason"] != "revoked" {
		t.Errorf("the token after revoke-crew-session-token: revokedAt = %v, revokedReason = %v", row["revokedAt"], row["revokedReason"])
	}
	if status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/revoke-crew-session-token", []byte(`{"tokenId":"`+tokenID+`"}`)); status != http.StatusNotFound {
		t.Errorf("revoking a token that is revoked already: status %d, want 404: %s", status, body)
	}
}
