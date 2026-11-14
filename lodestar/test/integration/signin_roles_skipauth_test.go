//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// This suite holds the roles a sign-in gives to the transaction of the step that causes
// them, on the served stack: an invitation is spent and its roles given together, or
// neither; an account a sector's directory provisions is made and given the directory's
// default seat together, or neither. The session library runs the application's hooks
// inside its own transaction and hands it to them, and the hooks are the crew's
// generated identity client's (crewidentity.Client.InvitationAccepted, DirectorySeated),
// which write through the framework's membership rule (identity.Manager.AssignRole).
// The members' roles are their directory's, so no sign-in gives them one here.
//
// The failure is a real one: a role that existed when the invitation was sent, or when
// the directory's default seat was set, and was deleted before the person signed in.
// The management refuses a membership of a role that does not exist, in the
// transaction, and the whole transaction goes with it.

// countRowsOf counts the rows of the table whose column holds the value, read straight
// from the database.
func countRowsOf(ctx context.Context, t *testing.T, s *served, table, column, value string) int64 {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM " + table + " WHERE " + column + " = @value",
		Params: map[string]any{"value": value},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting %s by %s: %v", table, column, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of %s: %v", table, err)
	}

	return n
}

// assertNoCrewAccount fails unless the database holds nothing of the person: no user
// row, no identity link, no membership and no session.
func assertNoCrewAccount(ctx context.Context, t *testing.T, s *served, username, when string) {
	t.Helper()

	for _, held := range []struct{ table, column, value string }{
		{"CrewSessionUsers", "Username", username},
		{"CrewSessionIdentities", "Subject", "skipauth-" + username},
		{"CrewUserRoles", "User", username},
		{"CrewSessions", "Username", username},
	} {
		if n := countRowsOf(ctx, t, s, held.table, held.column, held.value); n != 0 {
			t.Errorf("%s: %s holds %d row(s) of %s, want none", when, held.table, n, username)
		}
	}
}

// invitationStands reports whether the invitation is still live: not consumed.
func invitationStands(ctx context.Context, t *testing.T, s *served, id string) bool {
	t.Helper()

	return !readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{id}, "ConsumedAt").Valid
}

// TestSignInRoles_invitationSpentWithItsRolesOrNotAtAll invites three people into Anvil
// as Stevedore, a custom role of the sector, and deletes the role before each accepts.
// The stowaway is a stranger who follows the link and signs in through the fleet's
// directory: the sign-in fails, the invitation stands, and there is no account, no
// link and no membership, not even Cinder's default seat, which the same transaction
// had buffered. The cadet, signed in, accepts by hand: refused, the invitation
// standing and her browser still carrying it. The ensign creates a login with a
// password at the link: the sign-in fails and the invitation stands; the login the
// sign-up made in its own step before the sign-in stays, holding nothing. Once the role
// exists again each of them accepts the very same invitation and holds the role.
//
// Demonstrates: auth.invitation, tenancy.default-seat.
func TestSignInRoles_invitationSpentWithItsRolesOrNotAtAll(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const role accesstypes.Role = "Stevedore"
	const (
		stowaway = "stowaway@example.com"
		ensign   = "ensign"
		ensignAt = "ensign@example.com"
	)
	held := accesstypes.DomainPolicyScope(anvil)
	manager := s.access.UserManager()
	addRole := func() {
		t.Helper()
		if err := manager.AddRole(ctx, held, role, "Loads the holds."); err != nil {
			t.Fatalf("access.UserManager.AddRole() error = %v", err)
		}
	}
	deleteRole := func() {
		t.Helper()
		if _, err := manager.DeleteRole(ctx, held, role); err != nil {
			t.Fatalf("access.UserManager.DeleteRole() error = %v", err)
		}
	}
	invite := func(hq *browser, email string) (id, token string) {
		t.Helper()
		answer := inviteCrew(ctx, t, hq, `{"email":"`+email+`","roles":["`+string(role)+`"],"domain":"`+anvil+`"}`)
		_, token = lastMailTo(t, s, email, "invitation")

		return answer.ID, token
	}
	open := func(b *browser, token string) {
		t.Helper()
		if status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
			t.Fatalf("opening the invitation: status %d: %s", status, body)
		}
	}
	fresh := func() *browser {
		t.Helper()
		b := newBrowser(t, s, consoleAPI)
		if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session on a fresh browser: status %d: %s", status, body)
		}

		return b
	}

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, string(policyCadet))
	cadetAt := factorAddress(string(policyCadet))

	// The three invitations are sent while the role exists, and then it is deleted.
	addRole()
	stowawayInvitation, stowawayToken := invite(hq, stowaway)
	cadetInvitation, cadetToken := invite(hq, cadetAt)
	ensignInvitation, ensignToken := invite(hq, ensignAt)
	deleteRole()

	// The stowaway, a stranger, through the fleet's directory. Cinder's row names a
	// default seat, so the transaction that would create her buffers that seat first
	// and the invitation's role after it, which fails: nothing of it is committed.
	stranger := fresh()
	open(stranger, stowawayToken)
	status, location := stranger.loginConsoleGoogle(ctx, stowaway)
	if want := consoleLogin + "?code=" + string(sessioninfo.RefusedByApplication); status != http.StatusFound || location != want {
		t.Fatalf("the stowaway's sign-in with the role gone: status %d to %q, want %d to %q", status, location, http.StatusFound, want)
	}
	if sess := stranger.session(ctx); sess["authenticated"] != false {
		t.Errorf("the stowaway's session after the failed sign-in = %v, want none", sess)
	}
	if !invitationStands(ctx, t, s, stowawayInvitation) {
		t.Error("the stowaway's invitation was consumed by a sign-in that gave no role, want it standing")
	}
	assertNoCrewAccount(ctx, t, s, stowaway, "after the stowaway's failed sign-in")

	// The cadet, signed in, accepts by hand: the acceptance is refused with the
	// management's own answer, the invitation stands and she holds no new seat.
	open(cadet, cadetToken)
	status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/accept", nil)
	if status != http.StatusNotFound {
		t.Fatalf("the cadet accepts with the role gone: status %d, want 404: %s", status, body)
	}
	var refused struct {
		Message string `json:"message"`
	}
	if want := `role "Stevedore" is not a valid role. Please check that the role exists.`; json.Unmarshal(body, &refused) != nil || refused.Message != want {
		t.Errorf("the cadet's refusal = %s, want the message %q", body, want)
	}
	if !invitationStands(ctx, t, s, cadetInvitation) {
		t.Error("the cadet's invitation was consumed by an acceptance that gave no role, want it standing")
	}
	if roles := rolesHeldInSector(ctx, t, hq, anvil, string(policyCadet)); slices.Contains(roles, string(role)) {
		t.Errorf("the cadet's roles at Anvil after the refused acceptance = %v, want no %s", roles, role)
	}

	// The ensign creates a login with a password at the link. The sign-up makes the
	// login in a step of its own and then signs it in, and the sign-in accepts the
	// invitation: that fails, the invitation stands, and the login stays, holding
	// nothing and signed in nowhere.
	newcomer := fresh()
	open(newcomer, ensignToken)
	status, body = newcomer.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": ensign, "password": signUpPassword}))
	if status != http.StatusNotFound {
		t.Fatalf("the ensign's sign-up with the role gone: status %d, want 404: %s", status, body)
	}
	if sess := newcomer.session(ctx); sess["authenticated"] != false {
		t.Errorf("the ensign's session after the failed sign-up = %v, want none", sess)
	}
	if !invitationStands(ctx, t, s, ensignInvitation) {
		t.Error("the ensign's invitation was consumed by a sign-up that gave no role, want it standing")
	}
	if n := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", ensign); n != 1 {
		t.Errorf("CrewSessionUsers holds %d row(s) of the ensign after the failed sign-up, want the 1 the sign-up made before the sign-in", n)
	}
	if n := countRowsOf(ctx, t, s, "CrewUserRoles", "User", ensign); n != 0 {
		t.Errorf("CrewUserRoles holds %d row(s) of the ensign after the failed sign-up, want none", n)
	}

	// The role exists again, and every invitation is the one that was sent: nothing
	// was spent. The stowaway's next sign-in creates her, seated by the invitation in
	// Anvil and by Cinder's directory row in Cinder.
	addRole()
	if status, location := stranger.loginConsoleGoogle(ctx, stowaway); status != http.StatusFound || location != consolePage {
		t.Fatalf("the stowaway's sign-in once the role exists: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	if invitationStands(ctx, t, s, stowawayInvitation) {
		t.Error("the stowaway's invitation still stands after the sign-in that accepted it")
	}
	if roles := rolesHeldInSector(ctx, t, hq, anvil, stowaway); !equalStrings(roles, []string{string(role)}) {
		t.Errorf("the stowaway's roles at Anvil = %v, want %s by the invitation", roles, role)
	}
	if roles := rolesHeldInSector(ctx, t, hq, cinder, stowaway); !equalStrings(roles, []string{"SectorCrew"}) {
		t.Errorf("the stowaway's roles at Cinder = %v, want SectorCrew by the directory's default seat", roles)
	}
	if n := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", stowaway); n != 1 {
		t.Errorf("CrewSessionUsers holds %d row(s) of the stowaway, want 1", n)
	}

	// The cadet's browser still carries her invitation: she accepts without opening it
	// again.
	if status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/accept", nil); status != http.StatusOK {
		t.Fatalf("the cadet accepts once the role exists: status %d: %s", status, body)
	}
	if invitationStands(ctx, t, s, cadetInvitation) {
		t.Error("the cadet's invitation still stands after she accepted it")
	}
	if roles := rolesHeldInSector(ctx, t, hq, anvil, string(policyCadet)); !slices.Contains(roles, string(role)) {
		t.Errorf("the cadet's roles at Anvil = %v, want %s among them", roles, role)
	}

	// The ensign's login stands, so she signs in with the password she chose, her
	// browser still carrying the invitation, and the sign-in accepts it.
	if status, body := newcomer.login(ctx, ensign, signUpPassword); status != http.StatusOK {
		t.Fatalf("the ensign's password sign-in once the role exists: status %d: %s", status, body)
	}
	newcomer.secondFactor(ctx, ensign)
	if sess := newcomer.session(ctx); sess["authenticated"] != true || sess["username"] != ensign {
		t.Fatalf("the ensign's session = %v, want her signed in", sess)
	}
	if invitationStands(ctx, t, s, ensignInvitation) {
		t.Error("the ensign's invitation still stands after the sign-in that accepted it")
	}
	if roles := rolesHeldInSector(ctx, t, hq, anvil, ensign); !equalStrings(roles, []string{string(role)}) {
		t.Errorf("the ensign's roles at Anvil = %v, want %s by the invitation", roles, role)
	}
}

// TestSignInRoles_directorySeatGivenWithTheAccountOrNotAtAll names a custom role of
// Cinder, Lamplighter, as the default seat of Cinder's directory row and deletes the
// role. The castaway, a stranger from the fleet's directory, signs in: the directory
// row admits her and the transaction that creates her cannot seat her, so it fails and
// there is no account, no link and no membership. Once the role exists again her next
// sign-in creates her seated.
//
// Demonstrates: tenancy.default-seat.
func TestSignInRoles_directorySeatGivenWithTheAccountOrNotAtAll(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const role accesstypes.Role = "Lamplighter"
	const castaway = "castaway"
	held := accesstypes.DomainPolicyScope(cinder)
	manager := s.access.UserManager()
	if err := manager.AddRole(ctx, held, role, "Keeps the beacons lit."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	rows := directories(ctx, t, hq, cinder)
	if len(rows) != 1 {
		t.Fatalf("Cinder's directories = %+v, want the seeded fleet directory alone", rows)
	}
	nameSeat := `[{"op":"patch","path":"/sectors/` + cinder + `/sector-directories/` + rows[0].ID + `","value":{"defaultSeat":"` + string(role) + `"}}]`
	if status, body := hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(nameSeat)); status != http.StatusOK {
		t.Fatalf("naming Cinder's default seat: status %d: %s", status, body)
	}
	if _, err := manager.DeleteRole(ctx, held, role); err != nil {
		t.Fatalf("access.UserManager.DeleteRole() error = %v", err)
	}

	// The seat cannot be given: the sign-in fails, and no account is left holding
	// nothing.
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the castaway's login: status %d: %s", status, body)
	}
	status, location := stranger.loginConsoleGoogle(ctx, castaway)
	if want := consoleLogin + "?code=" + string(sessioninfo.RefusedByApplication); status != http.StatusFound || location != want {
		t.Fatalf("the castaway's sign-in with the seat's role gone: status %d to %q, want %d to %q", status, location, http.StatusFound, want)
	}
	if sess := stranger.session(ctx); sess["authenticated"] != false {
		t.Errorf("the castaway's session after the failed sign-in = %v, want none", sess)
	}
	assertNoCrewAccount(ctx, t, s, castaway, "after the castaway's failed sign-in")
	if row := crewAccount(ctx, t, hq, castaway); row != nil {
		t.Errorf("the castaway's account after the failed sign-in = %v, want none created", row)
	}

	// The role exists again: the same person's next sign-in is a stranger's still, and
	// creates her seated.
	if err := manager.AddRole(ctx, held, role, "Keeps the beacons lit."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if status, location := stranger.loginConsoleGoogle(ctx, castaway); status != http.StatusFound || location != consolePage {
		t.Fatalf("the castaway's sign-in once the role exists: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	if roles := rolesHeldInSector(ctx, t, hq, cinder, castaway); !equalStrings(roles, []string{string(role)}) {
		t.Errorf("the castaway's roles at Cinder = %v, want %s by the directory's default seat", roles, role)
	}
	if n := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", castaway); n != 1 {
		t.Errorf("CrewSessionUsers holds %d row(s) of the castaway, want 1", n)
	}
}

// TestSignInRoles_seatAndInvitationNamingOneRoleGiveItOnce is the sign-in of a stranger
// two things would give the same role in the same sector: Cinder's directory row names
// SectorCrew as its default seat, and she is invited into Cinder as SectorCrew and
// SectorMarshal. Both are written in the transaction that creates her, by two hooks,
// and neither can read a row the other buffered; a membership buffered twice is refused
// at the commit, and the sign-in would fail with internal_error every time. The session
// library leaves the seat to the invitation that names it, so the role is given once:
// she signs in, the invitation is consumed, and she holds the two roles the invitation
// names, SectorCrew among them once.
func TestSignInRoles_seatAndInvitationNamingOneRoleGiveItOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const twice = "twice@example.com"

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	invitation := inviteCrew(ctx, t, hq, `{"email":"`+twice+`","roles":["SectorCrew","SectorMarshal"],"domain":"`+cinder+`"}`)
	_, token := lastMailTo(t, s, twice, "invitation")

	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the login: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
		t.Fatalf("opening the invitation: status %d: %s", status, body)
	}
	if status, location := stranger.loginConsoleGoogle(ctx, twice); status != http.StatusFound || location != consolePage {
		t.Fatalf("the sign-in the seat and the invitation both give SectorCrew at Cinder: status %d to %q, want %d to %q",
			status, location, http.StatusFound, consolePage)
	}
	if invitationStands(ctx, t, s, invitation.ID) {
		t.Error("the invitation stands after the sign-in that accepted it")
	}
	if roles := rolesHeldInSector(ctx, t, hq, cinder, twice); !equalStrings(roles, []string{"SectorCrew", "SectorMarshal"}) {
		t.Errorf("her roles at Cinder = %v, want SectorCrew and SectorMarshal, each once", roles)
	}
	if n := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", twice); n != 1 {
		t.Errorf("CrewSessionUsers holds %d row(s) of her, want 1", n)
	}
}
