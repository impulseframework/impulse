//go:build skipAuth

package integration

// impersonation_subject_skipauth_test: nobody is the subject of what they do while they
// act as someone else, proven on the served stack over Spanner. The rule is the identity
// library's (identity.RefuseImpersonatorAsSubject): every act of the generated user
// management that changes something of a person is refused when the person is the one
// who started the impersonated session, whatever the session's mask keeps. An action of
// Lodestar's own that names a person, SetSectorPilotClearance, asks the same rule.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// actingAsSomeoneElse opens the sentence the rule refuses with, whatever was asked.
const actingAsSomeoneElse = "you are acting as someone else, so you cannot "

// TestImpersonation_nobodyIsTheSubject proves the rule on the served stack. The marshal
// holds the grant to act as a role and acts as Headquarters, the role that manages the
// crew's accounts, so the session holds everything headquarters may do. Each of those
// things is refused on the marshal's own account, with the sentence that says she is
// acting as someone else: a role given and taken away, a place in a group, the unlock
// of her own name, an enable, a password, a reset link, an address, a name, a rename,
// the end of her own sessions, her remembered browsers, and an invitation to the address
// her account has confirmed. The same act on another person goes through, and her own
// row is still listed and read. It holds with no mask and under a mask that keeps
// Execute; under the mask of a session that acts as a person, which refuses the classes
// credential and access, the classed acts are the mask's to refuse and the enable and
// the unlock, which carry no class, are still the rule's.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-role, impersonation.mask.
func TestImpersonation_nobodyIsTheSubject(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	marshal, cadet := account(t, "marshal"), account(t, "cadet")
	const (
		ownAddress   = "marshal-own-account@example.com"
		otherAddress = "nobody-here-yet@example.com"
	)

	// The marshal confirms an address on her own account, in a session of her own: an
	// invitation names a person by the address their account has confirmed.
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	if status, body := own.do(ctx, http.MethodPost, consoleAPI+"/user/email", []byte(`{"email":"`+ownAddress+`"}`)); status != http.StatusOK {
		t.Fatalf("the marshal adding an address: status %d: %s", status, body)
	}
	_, link := lastMailTo(t, s, ownAddress, "verify")
	if status, body := own.do(ctx, http.MethodPost, consoleAPI+"/user/email/verify", []byte(`{"token":"`+link+`"}`)); status != http.StatusOK {
		t.Fatalf("the marshal confirming her address: status %d: %s", status, body)
	}

	// Two groups, made by the governor: one nobody is in, for a place given, and one
	// the marshal and the cadet are in, for a place taken away (a place that is not
	// there is not found before any rule is asked).
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	group := func(name string, members ...accesstypes.User) string {
		t.Helper()
		status, body := governor.do(ctx, http.MethodPost, crewMethod("create-crew-group"), fmt.Appendf(nil, `{"name":%q,"description":"Stands in between watches"}`, name))
		assertStatus(t, status, http.StatusOK, body)
		id := cell[string](t, decodeRow(t, body), "id")
		for _, member := range members {
			status, body := governor.do(ctx, http.MethodPost, crewMethod("add-crew-group-member"), fmt.Appendf(nil, `{"groupId":%q,"userId":%q}`, id, member))
			assertStatus(t, status, http.StatusNoContent, body)
		}

		return id
	}
	empty, staffed := group("Relief watch"), group("Night watch", marshal, cadet)

	// A wrong password, from a browser of nobody's, gives a name a row of failed sign-ins
	// to unlock (a row that is not there is not found before any rule is asked). The
	// person's next sign-in clears it, so the unlock of the marshal's own name is
	// preceded by one each time.
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	failSignIn := func(name string) {
		t.Helper()
		if status, body := stranger.login(ctx, name, "not the password"); status != http.StatusUnauthorized {
			t.Fatalf("a wrong password for %s: status %d, want 401: %s", name, status, body)
		}
	}
	failSignIn("cadet")

	// who is a person an act is done to: the id of their account, the name their failed
	// sign-ins are counted under, and the address an invitation to them is mailed to.
	type who struct {
		id       accesstypes.User
		username string
		address  string
	}
	herself := who{id: marshal, username: "marshal", address: ownAddress}
	another := who{id: cadet, username: "cadet", address: otherAddress}
	acts := []struct {
		name  string
		route string
		body  func(p who) string
		// says is the end of the sentence the rule refuses the act with, and classed
		// whether the act carries a class the mask of a session that acts as a person
		// refuses.
		says    string
		classed bool
		// prepare readies the person for the act, where the act needs something of
		// theirs to be there.
		prepare func(p who)
	}{
		{
			name: "a role given", route: "assign-crew-role", says: "give yourself a role", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"role":"StandIn"}`, p.id)
			},
		},
		{
			name: "a role taken away", route: "remove-crew-role", says: "take a role from yourself", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"role":"StandIn"}`, p.id)
			},
		},
		{
			name: "a place in a group", route: "add-crew-group-member", says: "put yourself in a group", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, empty, p.id)
			},
		},
		{
			name: "a place in a group taken away", route: "remove-crew-group-member", says: "take yourself out of a group", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, staffed, p.id)
			},
		},
		{
			name: "an enable", route: "enable-crew-user", says: "enable your own account",
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q}`, p.id)
			},
		},
		{
			name: "an invitation to the address of their own account", route: "invite-crew-user", says: "invite yourself", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"email":%q,"roles":["StandIn"]}`, p.address)
			},
		},
		{
			name: "a password", route: "set-crew-temporary-password", says: "set your own password", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"password":"a temporary passphrase 7"}`, p.id)
			},
		},
		{
			name: "a reset link", route: "send-crew-user-password-reset-link", says: "send yourself a password reset link", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q}`, p.id)
			},
		},
		{
			name: "an address", route: "set-crew-user-email", says: "change your own email address", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"email":"moved-by-hand@example.com"}`, p.id)
			},
		},
		{
			name: "a rename", route: "rename-crew-user", says: "rename your own account", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"username":%q}`, p.id, strings.ToLower(p.username)+"-renamed")
			},
		},
		{
			name: "an unlock", route: "unlock-crew-sign-in", says: "unlock your own sign-in",
			body: func(p who) string {
				return fmt.Sprintf(`{"name":%q}`, p.username)
			},
			prepare: func(p who) {
				failSignIn(p.username)
			},
		},
		{
			name: "a name", route: "set-crew-user-name", says: "change your own name",
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"name":"Somebody Else"}`, p.id)
			},
		},
		{
			name: "the end of their sessions", route: "end-crew-user-sessions", says: "end your own sessions",
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q}`, p.id)
			},
		},
		{
			name: "their remembered browsers", route: "revoke-crew-user-trusted-devices", says: "revoke your own trusted devices",
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q}`, p.id)
			},
		},
	}

	asHeadquarters := func(more string) *browser {
		t.Helper()
		b := newBrowser(t, s, consoleAPI)
		b.signIn(ctx, "marshal")
		mint(ctx, t, b, `{"kind":"role","principal":"Headquarters","reason":"support"`+more+`}`)

		return b
	}
	sessions := []struct {
		name    string
		browser *browser
	}{
		{name: "acting as the Headquarters role with no mask", browser: asHeadquarters("")},
		{name: "acting as the Headquarters role under a mask that keeps Execute", browser: asHeadquarters(`,"refuseClasses":["irreversible"]`)},
	}
	for _, session := range sessions {
		for _, act := range acts {
			// On her own account: refused, with the sentence.
			if act.prepare != nil {
				act.prepare(herself)
			}
			status, answer := session.browser.do(ctx, http.MethodPost, crewMethod(act.route), []byte(act.body(herself)))
			if want := actingAsSomeoneElse + act.says + ":"; status != http.StatusForbidden || !strings.Contains(string(answer), want) {
				t.Errorf("%s, %s on her own account: status %d, want 403 saying %q: %s", session.name, act.name, status, want, answer)
			}
			// On another person: the whole call runs, as a dry run, and would commit.
			if status, answer := dryRun(ctx, session.browser, crewMethod(act.route), []byte(act.body(another))); status/100 != 2 {
				t.Errorf("%s, %s on another person: status %d, want the act to go through: %s", session.name, act.name, status, answer)
			}
		}

		// The rule is for changes: her own row is still read, and still listed.
		if status, body := session.browser.do(ctx, http.MethodGet, crewMethod("crew-session-users/"+string(marshal)), nil); status != http.StatusOK {
			t.Errorf("%s, reading her own row: status %d, want 200: %s", session.name, status, body)
		}
		status, body := session.browser.do(ctx, http.MethodGet, crewMethod("crew-session-users?limit=200"), nil)
		assertStatus(t, status, http.StatusOK, body)
		if !slices.ContainsFunc(decodeRows(t, body), func(row map[string]any) bool {
			return row["id"] == string(marshal)
		}) {
			t.Errorf("%s: the list of the crew does not carry her own row", session.name)
		}
	}

	// Nothing of her account changed: the StandIn role she was refused is not hers, so
	// her own digest carries no grant to act as a person.
	if _, holds := digestOf(ctx, t, own, "")["ActAsUser"]; holds {
		t.Error("the marshal's own digest carries ActAsUser after every role given to her from inside the session was refused")
	}

	// Under the mask of a session that acts as a person, which refuses the classes
	// credential and access: the governor gives the marshal the grant, and she acts as
	// the governor. A classed act on her own account is refused, by the mask; the
	// enable, the unlock and the other acts no class marks reach the rule, which refuses
	// them in its own words.
	giveMarshalStandIn(ctx, t, s)
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "marshal")
	if got, want := mint(ctx, t, acting, actAs(t, "governor", "support", "")).Mask, actMaskWords; got != want {
		t.Fatalf("the mask of a session that acts as a person = %q, want %q", got, want)
	}
	for _, act := range acts {
		if act.prepare != nil {
			act.prepare(herself)
		}
		status, answer := acting.do(ctx, http.MethodPost, crewMethod(act.route), []byte(act.body(herself)))
		if status != http.StatusForbidden {
			t.Errorf("acting as the governor, %s on her own account: status %d, want 403: %s", act.name, status, answer)

			continue
		}
		if says := strings.Contains(string(answer), actingAsSomeoneElse+act.says+":"); says == act.classed {
			t.Errorf("acting as the governor, %s on her own account: the answer carries the rule's sentence = %t, want %t (a classed act is the mask's to refuse, any other the rule's): %s",
				act.name, says, !act.classed, answer)
		}
	}

	// A read-only view writes nothing at all, her own account included.
	viewing := newBrowser(t, s, consoleAPI)
	viewing.signIn(ctx, "marshal")
	mint(ctx, t, viewing, viewAs(t, "governor", "support"))
	for _, act := range acts {
		if act.prepare != nil {
			act.prepare(herself)
		}
		if status, answer := viewing.do(ctx, http.MethodPost, crewMethod(act.route), []byte(act.body(herself))); status != http.StatusForbidden {
			t.Errorf("viewing as the governor, %s on her own account: status %d, want 403: %s", act.name, status, answer)
		}
	}
}

// TestImpersonation_accountSwitchesNeverTheirOwn proves on the served stack what a
// session that acts as a person does with an account's switches. Disabling, enabling
// and deleting an account carry no class, so the mask of the kind, which refuses the
// classes credential and access, keeps them: the marshal, acting as the governor,
// disables, enables and deletes a cadet's account as the governor would. On her own
// account each of the three is refused, by the rule that nobody is the subject of what
// they do in such a session and in the rule's words, never by the mask, and her account
// is as it was.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-user, impersonation.mask, @class.
func TestImpersonation_accountSwitchesNeverTheirOwn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	marshal := account(t, "marshal")
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	status, body := governor.do(ctx, http.MethodPost, sectorPath(anvil, "create-crew-sector-user"), []byte(`{"username":"gus","email":"Gus <gus@example.com>","roles":["Cadet"]}`))
	assertStatus(t, status, http.StatusOK, body)
	gus := cell[string](t, decodeRow(t, body), "id")

	giveMarshalStandIn(ctx, t, s)
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "marshal")
	if got, want := mint(ctx, t, acting, actAs(t, "governor", "covering the accounts desk", "")).Mask, actMaskWords; got != want {
		t.Fatalf("the mask of a session that acts as a person = %q, want %q", got, want)
	}
	// disabled reads whether an account is disabled, as the governor in a session of his
	// own, and found whether the account is there at all.
	disabled := func(id string) (isDisabled, found bool) {
		t.Helper()
		status, body := governor.do(ctx, http.MethodGet, crewMethod("crew-session-users/"+id), nil)
		if status == http.StatusNotFound {
			return false, false
		}
		assertStatus(t, status, http.StatusOK, body)

		return cell[bool](t, decodeRow(t, body), "disabled"), true
	}

	// On another person's account, the three are the stand-in's to do.
	switches := []struct {
		route string
		// wantDisabled and wantFound are the account after the act.
		wantDisabled, wantFound bool
	}{
		{route: "disable-crew-user", wantDisabled: true, wantFound: true},
		{route: "enable-crew-user", wantDisabled: false, wantFound: true},
		{route: "delete-crew-user", wantDisabled: false, wantFound: false},
	}
	for _, act := range switches {
		status, answer := acting.do(ctx, http.MethodPost, crewMethod(act.route), fmt.Appendf(nil, `{"userId":%q}`, gus))
		if status != http.StatusNoContent {
			t.Fatalf("acting as the governor, %s on a cadet's account: status %d, want 204: the act carries no class, so the mask of the kind keeps it: %s", act.route, status, answer)
		}
		if isDisabled, found := disabled(gus); isDisabled != act.wantDisabled || found != act.wantFound {
			t.Errorf("after %s the cadet's account is disabled = %t, found = %t, want %t and %t", act.route, isDisabled, found, act.wantDisabled, act.wantFound)
		}
	}

	// On her own account each is refused, in the rule's words. Nobody disables or
	// deletes themselves in any session, which is the rule's first reach; the enable is
	// refused for the person who acts as someone else.
	own := []struct {
		route string
		says  string
	}{
		{route: "disable-crew-user", says: "you cannot disable your own account"},
		{route: "enable-crew-user", says: actingAsSomeoneElse + "enable your own account:"},
		{route: "delete-crew-user", says: "you cannot delete your own account"},
	}
	for _, act := range own {
		status, answer := acting.do(ctx, http.MethodPost, crewMethod(act.route), fmt.Appendf(nil, `{"userId":%q}`, marshal))
		if status != http.StatusForbidden || !strings.Contains(string(answer), act.says) {
			t.Errorf("acting as the governor, %s on her own account: status %d, want 403 saying %q, the rule's refusal: %s", act.route, status, act.says, answer)
		}
	}
	if isDisabled, found := disabled(string(marshal)); isDisabled || !found {
		t.Errorf("after the refused acts the marshal's account is disabled = %t, found = %t, want it as it was", isDisabled, found)
	}
}

// TestImpersonation_applicationsOwnAction proves that an action of the application's own
// that names a person asks the library's rule. SetSectorPilotClearance rates a pilot,
// and a pilot's clearance says which missions they may claim. The Wing Commander is a
// pilot of Anvil. Given the grant to act as a role, he acts as the Sector Marshal, whose
// grant rates every pilot of the sector, and is refused his own record, with the
// action's own words; he rates another pilot; and in a session of his own the rule says
// nothing, and his own grant is what answers.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-role.
func TestImpersonation_applicationsOwnAction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	grant := fmt.Appendf(nil, `{"userId":%q,"role":"CommandStaff"}`, account(t, "wingco"))
	if status, answer := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), grant); status != http.StatusNoContent {
		t.Fatalf("the governor giving the Wing Commander the CommandStaff role: status %d, want 204: %s", status, answer)
	}

	wingco := newBrowser(t, s, consoleAPI)
	wingco.signIn(ctx, "wingco")
	mint(ctx, t, wingco, `{"kind":"role","principal":"SectorMarshal","reason":"covering the marshal's desk"}`)
	clearance := func(pilot string) int64 {
		t.Helper()

		return readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilot}, "Clearance")
	}
	// rate asks for a clearance the pilot does not hold, so a call that went through
	// shows in the registry.
	rate := func(pilot string) (status int, answer string, asked int64) {
		t.Helper()
		asked = 1
		if clearance(pilot) == asked {
			asked = 2
		}
		code, body := wingco.do(ctx, http.MethodPost, sectorPath(anvil, "set-sector-pilot-clearance"), fmt.Appendf(nil, `{"pilotId":%q,"clearance":%d}`, pilot, asked))

		return code, string(body), asked
	}

	before := clearance(pilotWildeID)
	status, answer, _ := rate(pilotWildeID)
	if want := actingAsSomeoneElse + "set your own clearance:"; status != http.StatusForbidden || !strings.Contains(answer, want) {
		t.Errorf("acting as the Sector Marshal, rating his own record: status %d, want 403 saying %q: %s", status, want, answer)
	}
	if got := clearance(pilotWildeID); got != before {
		t.Errorf("the Wing Commander's clearance = %d after the refused call, want %d as before", got, before)
	}
	status, answer, asked := rate(pilotVelaID)
	if status != http.StatusNoContent {
		t.Errorf("acting as the Sector Marshal, rating another pilot: status %d, want 204: %s", status, answer)
	}
	if got := clearance(pilotVelaID); got != asked {
		t.Errorf("the other pilot's clearance = %d, want %d", got, asked)
	}

	// In a session of his own, his own record is his grant's to answer for.
	self := newBrowser(t, s, consoleAPI)
	self.signIn(ctx, "wingco")
	if _, answer := dryRun(ctx, self, sectorPath(anvil, "set-sector-pilot-clearance"), fmt.Appendf(nil, `{"pilotId":%q,"clearance":2}`, pilotWildeID)); strings.Contains(answer, actingAsSomeoneElse) {
		t.Errorf("rating his own record in a session of his own: the rule refused it: %s", answer)
	}
}

// TestImpersonation_noSectorOfTheirOwn proves the rule on the route that creates a sector
// for the person who asks. The route makes its caller the new sector's marshal, and in a
// session that acts as a role the caller is the real person behind it, who would hold
// the seat after the session had ended. The governor opens sign-up, so the route admits
// a person who holds no sector. The marshal acts as the Bulletin Officer, a role that
// holds a grant in no sector, so no other refusal of the route answers for this one: she
// is refused in the route's own words with nothing written, the page that offers the
// creation is told that none is offered, and in a session of her own the rule says
// nothing.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-role.
func TestImpersonation_noSectorOfTheirOwn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	openCrewSignUp(ctx, t, governor, crew.SignUpRegistration)

	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "marshal")
	mint(ctx, t, acting, `{"kind":"role","principal":"BulletinOfficer","reason":"covering the bulletin desk"}`)
	// The session holds a grant in no sector, so "one sector per person" is not what
	// answers below.
	if held := acting.domains(ctx); len(held) != 0 {
		t.Fatalf("user-domains acting as the Bulletin Officer = %v, want none: the route's own refusal of a session that holds a sector would answer first", held)
	}

	sectors, memberships := tableRows(ctx, t, s, "Sectors"), tableRows(ctx, t, s, "CrewUserRoles")
	status, answer := acting.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber))
	if want := actingAsSomeoneElse + "create a sector of your own:"; status != http.StatusForbidden || !strings.Contains(string(answer), want) {
		t.Errorf("acting as the Bulletin Officer, creating a sector of her own: status %d, want 403 saying %q: %s", status, want, answer)
	}
	if got := tableRows(ctx, t, s, "Sectors"); got != sectors {
		t.Errorf("sectors after the refused call = %d, want the %d there were: nothing is written", got, sectors)
	}
	if got := tableRows(ctx, t, s, "CrewUserRoles"); got != memberships {
		t.Errorf("the crew's memberships after the refused call = %d, want the %d there were: nobody is given a role", got, memberships)
	}
	status, answer = acting.do(ctx, http.MethodGet, createSectorRoute, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s acting as the Bulletin Officer: status %d, want 200: %s", createSectorRoute, status, answer)
	}
	assertOffered(false)(t, "acting as the Bulletin Officer, asking whether a sector can be created", answer)

	// In a session of her own the route's own rules answer: she holds Anvil, and one
	// person creates one sector.
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	if status, answer := own.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber)); status != http.StatusForbidden || strings.Contains(string(answer), actingAsSomeoneElse) {
		t.Errorf("creating a sector in a session of her own: status %d, want 403 from the route's own rule, one sector per person: %s", status, answer)
	}
}
