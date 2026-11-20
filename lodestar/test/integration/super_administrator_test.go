package integration

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestSuperAdministrator_rolesAndTheLastHolder proves the Super Administrator role on
// the crew's generated methods, over the real engine and the shipped role file, in which
// no role is named SuperAdministrator: the framework defines it, and the governor holds
// it. Only a holder makes another: headquarters, which the role file lets give roles, is
// refused the role, a holder's removal and a holder's account, whether to delete it, to
// disable it or to enable it again, and every act on how a holder signs in: their
// password set, removed or required changed, their username, their email address, where
// the code of their second factor is mailed, and their directory identity. The
// governor, a holder, is refused none of them, and once the
// governor has given the role up is refused like anyone. The role is held for the
// whole service alone, so a sector's membership of it is refused. A person who holds
// nothing but the role gives and takes any role of the crew, in headquarters, in one
// sector and in every sector, where the role file grants them nothing; a person without
// it is refused the same. And there is always one who can sign in: the last holder whose
// account is not disabled keeps the role and the account, whether they ask or another
// holder does.
//
// Demonstrates: auth.super-administrator.
func TestSuperAdministrator_rolesAndTheLastHolder(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	h := w.h
	sfx := identitySuffix()
	sol, hana, ivan := accesstypes.User("sol-"+sfx), accesstypes.User("hana-"+sfx), accesstypes.User("ivan-"+sfx)
	governor := string(account(t, "governor"))

	// post runs a generated method as a person.
	post := func(as accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, h, as, http.MethodPost, consoleAPI+"/"+method, body)
	}
	// refused fails the test unless the method answered status with a body naming the rule.
	refused := func(what string, status int, body []byte, want int, rule string) {
		t.Helper()
		if status != want || !strings.Contains(string(body), rule) {
			t.Errorf("%s: status %d, want %d naming %q: %s", what, status, want, rule, body)
		}
	}
	create := func(as, user accesstypes.User, role string) (int, []byte, string) {
		status, body := post(as, "create-crew-user", `{"username":"`+string(user)+`","roles":["`+role+`"]}`)
		if status != http.StatusOK {
			return status, body, ""
		}
		id := cell[string](t, decodeRow(t, body), "id")
		knownAs(t, user, impulse.Must(impulse.UUIDFromString(id)))

		return status, body, id
	}
	role := func(userID, name, place string) string {
		return `{"userId":"` + userID + `","role":"` + name + `"` + place + `}`
	}
	const (
		inAnvil    = `,"domain":"` + anvil + `"`
		everywhere = `,"everyDomain":true`
		lastHolder = "This person is the last super administrator who can sign in"
		notAHolder = "you do not hold the Super Administrator role"
	)

	// A holder makes another: the governor creates sol holding the role and nothing
	// else. Headquarters gives roles by the role file and is no holder.
	status, body, solID := create("governor", sol, "SuperAdministrator")
	assertStatus(t, status, http.StatusOK, body)
	status, body, hanaID := create("governor", hana, "Headquarters")
	assertStatus(t, status, http.StatusOK, body)
	status, body, ivanID := create("governor", ivan, "CrewCommon")
	assertStatus(t, status, http.StatusOK, body)

	status, body = post(hana, "assign-crew-role", role(ivanID, "SuperAdministrator", ""))
	refused("headquarters giving the role", status, body, http.StatusForbidden, "Only a super administrator gives the Super Administrator role, and "+notAHolder)
	status, body, _ = create(hana, accesstypes.User("jo-"+sfx), "SuperAdministrator")
	refused("headquarters creating a person who holds the role", status, body, http.StatusForbidden, notAHolder)
	status, body = post(hana, "remove-crew-role", role(solID, "SuperAdministrator", ""))
	refused("headquarters taking the role from a holder", status, body, http.StatusForbidden, "Only a super administrator takes the Super Administrator role away, and "+notAHolder)
	status, body = post(hana, "delete-crew-user", `{"userId":"`+solID+`"}`)
	refused("headquarters deleting a holder's account", status, body, http.StatusForbidden, "Only a super administrator deletes a super administrator's account")
	status, body = post(hana, "disable-crew-user", `{"userId":"`+solID+`"}`)
	refused("headquarters disabling a holder's account", status, body, http.StatusForbidden, "Only a super administrator disables a super administrator's account, and "+notAHolder)
	if disabled := readColumn[bool](t.Context(), t, w.db, "CrewSessionUsers", spanner.Key{solID}, "Disabled"); disabled {
		t.Error("sol's account after headquarters was refused disabling it: disabled, want it left as it was")
	}
	status, body = post(hana, "assign-crew-role", role(hanaID, "SuperAdministrator", ""))
	refused("headquarters giving herself the role", status, body, http.StatusForbidden, notAHolder)

	// How a holder signs in is a holder's to change. Sol's two identities are written as
	// a directory sign-in writes one; headquarters, whose role file lets her run each of
	// these methods on the crew, is refused every one of them on sol.
	identity, spare := impulse.Must(impulse.NewUUID()).String(), impulse.Must(impulse.NewUUID()).String()
	for id, subject := range map[string]string{identity: "sol-" + sfx, spare: "sol-spare-" + sfx} {
		if _, err := w.db.Apply(t.Context(), []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": id, "UserId": solID, "Method": "google", "Connection": "google", "Subject": subject,
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		})}); err != nil {
			t.Fatalf("writing sol's identity: %v", err)
		}
	}
	signIn := []struct{ method, body, says string }{
		{"set-crew-temporary-password", `{"userId":"` + solID + `","password":"first-day-pass"}`, "sets a super administrator's password"},
		{"require-crew-password-change", `{"userId":"` + solID + `"}`, "requires a super administrator to change their password"},
		{"set-crew-user-email", `{"userId":"` + solID + `","email":"sol-` + sfx + `@example.com"}`, "changes a super administrator's email address"},
		{"remove-crew-password", `{"userId":"` + solID + `"}`, "removes a super administrator's password"},
		{"unlink-crew-session-identity", `{"identityId":"` + identity + `"}`, "unlinks a super administrator's sign-in identity"},
		{"rename-crew-user", `{"userId":"` + solID + `","username":"sol-renamed-` + sfx + `"}`, "renames a super administrator"},
	}
	for _, act := range signIn {
		status, body = post(hana, act.method, act.body)
		refused("headquarters running "+act.method+" on a holder", status, body, http.StatusForbidden, "Only a super administrator "+act.says+", and "+notAHolder)
	}
	// The governor holds the role, and is refused neither of two: a new address is asked
	// for sol (the method answers whether its link was mailed), and the spare of her two
	// identities is unlinked, as the role file allows.
	for method, act := range map[string]struct {
		body string
		want int
	}{
		"set-crew-user-email":          {`{"userId":"` + solID + `","email":"sol-` + sfx + `@example.com"}`, http.StatusOK},
		"unlink-crew-session-identity": {`{"identityId":"` + spare + `"}`, http.StatusNoContent},
	} {
		if status, answer := post("governor", method, act.body); status != act.want {
			t.Errorf("a super administrator running %s on another: status %d, want %d: %s", method, status, act.want, answer)
		}
	}

	// The role is held for the whole service alone.
	for _, place := range []string{inAnvil, everywhere} {
		status, body = post("governor", "assign-crew-role", role(ivanID, "SuperAdministrator", place))
		refused("the governor giving the role"+place, status, body, http.StatusBadRequest, "the SuperAdministrator role is held for the whole application alone")
	}

	// Sol holds the role and nothing else: the role file grants her nothing, and she
	// gives and takes any role of the crew, anywhere. Ivan, who holds no such role, is
	// refused the same request.
	for _, given := range []string{role(ivanID, "Cadet", inAnvil), role(ivanID, "Pilot", everywhere), role(ivanID, "BulletinOfficer", "")} {
		status, body = post(ivan, "assign-crew-role", given)
		if status != http.StatusForbidden {
			t.Errorf("ivan giving %s: status %d, want 403: %s", given, status, body)
		}
		status, body = post(sol, "assign-crew-role", given)
		if status != http.StatusNoContent {
			t.Errorf("a super administrator who holds nothing else giving %s: status %d, want 204: %s", given, status, body)
		}
	}
	for _, taken := range []string{role(ivanID, "Cadet", inAnvil), role(ivanID, "Pilot", everywhere), role(ivanID, "BulletinOfficer", "")} {
		status, body = post(sol, "remove-crew-role", taken)
		if status != http.StatusNoContent {
			t.Errorf("a super administrator who holds nothing else taking %s: status %d, want 204: %s", taken, status, body)
		}
	}
	// What the role does not give stays with the role file: sol lists no crew.
	if status, body := doRequestAs(t, h, sol, http.MethodGet, consoleAPI+"/crew-session-users?limit=1", ""); status != http.StatusForbidden {
		t.Errorf("a super administrator the role file grants nothing listing the crew: status %d, want 403: %s", status, body)
	}

	// Two holders can sign in, so either may go: the governor disables sol's account,
	// once sol holds headquarters too, whose grants the role file gives on the methods
	// that disable and delete an account. Now sol holds the role and cannot sign in, and
	// the governor is the last who can: the role and the account stay, whether the
	// governor asks or the other holder does.
	status, body = post("governor", "assign-crew-role", role(solID, "Headquarters", ""))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("governor", "disable-crew-user", `{"userId":"`+solID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	// Enabling a holder's account makes a super administrator who can sign in, so it is
	// a holder's to do: headquarters, whom the role file lets enable accounts, is refused.
	status, body = post(hana, "enable-crew-user", `{"userId":"`+solID+`"}`)
	refused("headquarters enabling a holder's account", status, body, http.StatusForbidden, "Only a super administrator enables a super administrator's account, and "+notAHolder)
	if disabled := readColumn[bool](t.Context(), t, w.db, "CrewSessionUsers", spanner.Key{solID}, "Disabled"); !disabled {
		t.Error("sol's account after headquarters was refused enabling it: enabled, want it left disabled")
	}
	status, body = post("governor", "remove-crew-role", role(governor, "SuperAdministrator", ""))
	refused("the last holder giving the role up", status, body, http.StatusForbidden, "You are the last super administrator who can sign in: give another person the Super Administrator role before you remove your own")
	status, body = post(sol, "remove-crew-role", role(governor, "SuperAdministrator", ""))
	refused("another holder taking the last holder's role", status, body, http.StatusForbidden, lastHolder)
	status, body = post(sol, "disable-crew-user", `{"userId":"`+governor+`"}`)
	refused("another holder disabling the last holder's account", status, body, http.StatusForbidden, lastHolder)
	status, body = post(sol, "delete-crew-user", `{"userId":"`+governor+`"}`)
	refused("another holder deleting the last holder's account", status, body, http.StatusForbidden, lastHolder)
	if got := readColumn[string](t.Context(), t, w.db, "CrewUserRoles", crewMembershipKey("SuperAdministrator", accesstypes.User(governor)), "UserId"); got != governor {
		t.Errorf("the governor's Super Administrator membership after the refusals: user = %q, want it kept", got)
	}

	// Once sol can sign in again the governor is no longer the last, and gives the
	// role up; without it, the governor cannot take it back.
	status, body = post("governor", "enable-crew-user", `{"userId":"`+solID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("governor", "remove-crew-role", role(governor, "SuperAdministrator", ""))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("governor", "assign-crew-role", role(governor, "SuperAdministrator", ""))
	refused("a person who gave the role up taking it back", status, body, http.StatusForbidden, notAHolder)

	// The rule on a holder's sign-in follows the role, not the person: the governor, who
	// ran these methods on sol a moment ago and still holds headquarters, is refused
	// them now, and sol, the holder, renames herself.
	for _, act := range signIn {
		status, body = post("governor", act.method, act.body)
		refused("the governor, who gave the role up, running "+act.method+" on a holder", status, body, http.StatusForbidden, "Only a super administrator "+act.says+", and "+notAHolder)
	}
	rename := signIn[len(signIn)-1]
	if status, body = post(sol, rename.method, rename.body); status != http.StatusNoContent {
		t.Errorf("a super administrator renaming herself: status %d, want 204: %s", status, body)
	}
}

// TestSuperAdministrator_twoHoldersLeaveAtOnce proves the rule on the last holder
// against two requests made at the same moment, each in a transaction of its own as every
// generated route runs: the crew has two holders who can sign in, and each gives the role
// up at once. The rule reads the holders inside its transaction, so the database judges
// the two one after the other: one goes, the other finds itself the last and is refused,
// and the crew keeps a super administrator.
//
// Demonstrates: auth.super-administrator.
func TestSuperAdministrator_twoHoldersLeaveAtOnce(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	sol := accesstypes.User("sol-" + identitySuffix())
	governor := string(account(t, "governor"))

	status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/create-crew-user", `{"username":"`+string(sol)+`","roles":["SuperAdministrator"]}`)
	assertStatus(t, status, http.StatusOK, body)
	solID := cell[string](t, decodeRow(t, body), "id")
	knownAs(t, sol, impulse.Must(impulse.UUIDFromString(solID)))

	leaving := map[accesstypes.User]string{"governor": governor, sol: solID}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		left    []string
		refused []string
	)
	for who, id := range leaving {
		wg.Go(func() {
			status, body := doRequestAs(t, w.h, who, http.MethodPost, consoleAPI+"/remove-crew-role", `{"userId":"`+id+`","role":"SuperAdministrator"}`)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case status == http.StatusNoContent:
				left = append(left, id)
			case status == http.StatusForbidden && strings.Contains(string(body), "You are the last super administrator who can sign in"):
				refused = append(refused, id)
			default:
				t.Errorf("%s giving the role up: status %d, want 204 or the refusal of the last holder: %s", who, status, body)
			}
		})
	}
	wg.Wait()
	if len(left) != 1 || len(refused) != 1 {
		t.Fatalf("of two holders giving the role up at once, %d left and %d were refused, want one of each", len(left), len(refused))
	}
	if got := readColumn[string](t.Context(), t, w.db, "CrewUserRoles", crewMembershipKey("SuperAdministrator", accesstypes.User(refused[0])), "UserId"); got != refused[0] {
		t.Errorf("the membership of the holder who was refused: user = %q, want it kept", got)
	}
}
