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
// credential and access, the classed acts are the mask's to refuse and the unlock, which
// carries no class, is still the rule's.
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

	// A group for the place in a group, made by the governor.
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	status, body := governor.do(ctx, http.MethodPost, crewMethod("create-crew-group"), []byte(`{"name":"Relief watch","description":"Stands in between watches"}`))
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")

	// who is a person an act is done to: the id of their account, the name their failed
	// sign-ins are counted under, and the address an invitation to them is mailed to.
	type who struct {
		id       accesstypes.User
		username string
		address  string
	}
	herself := who{id: marshal, username: "Marshal", address: ownAddress}
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
	}{
		{
			name: "a role given", route: "assign-crew-role", says: "give yourself a role", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q,"role":"StandIn"}`, p.id) },
		},
		{
			name: "a role taken away", route: "remove-crew-role", says: "take a role from yourself", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q,"role":"StandIn"}`, p.id) },
		},
		{
			name: "a place in a group", route: "add-crew-group-member", says: "put yourself in a group", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, p.id) },
		},
		{
			name: "a place in a group taken away", route: "remove-crew-group-member", says: "take yourself out of a group", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, p.id) },
		},
		{
			name: "an enable", route: "enable-crew-user", says: "enable your own account", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q}`, p.id) },
		},
		{
			name: "an invitation to the address of their own account", route: "invite-crew-user", says: "invite yourself", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"email":%q,"roles":["StandIn"]}`, p.address) },
		},
		{
			name: "a password", route: "set-crew-temporary-password", says: "set your own password", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q,"password":"a temporary passphrase 7"}`, p.id) },
		},
		{
			name: "a reset link", route: "send-crew-user-password-reset-link", says: "send yourself a password reset link", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q}`, p.id) },
		},
		{
			name: "an address", route: "set-crew-user-email", says: "change your own email address", classed: true,
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q,"email":"moved-by-hand@example.com"}`, p.id) },
		},
		{
			name: "a rename", route: "rename-crew-user", says: "rename your own account", classed: true,
			body: func(p who) string {
				return fmt.Sprintf(`{"userId":%q,"username":%q}`, p.id, strings.ToLower(p.username)+"-renamed")
			},
		},
		{
			name: "an unlock", route: "unlock-crew-sign-in", says: "unlock your own sign-in",
			body: func(p who) string { return fmt.Sprintf(`{"name":%q}`, p.username) },
		},
		{
			name: "a name", route: "set-crew-user-name", says: "change your own name",
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q,"name":"Somebody Else"}`, p.id) },
		},
		{
			name: "the end of their sessions", route: "end-crew-user-sessions", says: "end your own sessions",
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q}`, p.id) },
		},
		{
			name: "their remembered browsers", route: "revoke-crew-user-trusted-devices", says: "revoke your own trusted devices",
			body: func(p who) string { return fmt.Sprintf(`{"userId":%q}`, p.id) },
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
		if !slices.ContainsFunc(decodeRows(t, body), func(row map[string]any) bool { return row["id"] == string(marshal) }) {
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
	// the governor. A classed act on her own account is refused, by the mask; the unlock
	// and the other acts no class marks reach the rule, which refuses them in its own
	// words.
	giveStandIn(ctx, t, s, "marshal")
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "marshal")
	if got, want := mint(ctx, t, acting, actAs(t, "governor", "support", "")).Mask, "everything except access actions and credential actions"; got != want {
		t.Fatalf("the mask of a session that acts as a person = %q, want %q", got, want)
	}
	for _, act := range acts {
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
		if status, answer := viewing.do(ctx, http.MethodPost, crewMethod(act.route), []byte(act.body(herself))); status != http.StatusForbidden {
			t.Errorf("viewing as the governor, %s on her own account: status %d, want 403: %s", act.name, status, answer)
		}
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
	rate := func(pilot string) (int, string) {
		status, answer := wingco.do(ctx, http.MethodPost, sectorPath(anvil, "set-sector-pilot-clearance"), fmt.Appendf(nil, `{"pilotId":%q,"clearance":5}`, pilot))

		return status, string(answer)
	}

	status, answer := rate(pilotWildeID)
	if want := actingAsSomeoneElse + "set your own clearance:"; status != http.StatusForbidden || !strings.Contains(answer, want) {
		t.Errorf("acting as the Sector Marshal, rating his own record: status %d, want 403 saying %q: %s", status, want, answer)
	}
	if got := readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilotWildeID}, "Clearance"); got == 5 {
		t.Error("the Wing Commander's clearance was raised by the refused call")
	}
	if status, answer := rate(pilotVelaID); status != http.StatusNoContent {
		t.Errorf("acting as the Sector Marshal, rating another pilot: status %d, want 204: %s", status, answer)
	}
	if got := readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilotVelaID}, "Clearance"); got != 5 {
		t.Errorf("the other pilot's clearance = %d, want 5", got)
	}

	// In a session of his own, his own record is his grant's to answer for.
	self := newBrowser(t, s, consoleAPI)
	self.signIn(ctx, "wingco")
	if _, answer := dryRun(ctx, self, sectorPath(anvil, "set-sector-pilot-clearance"), fmt.Appendf(nil, `{"pilotId":%q,"clearance":2}`, pilotWildeID)); strings.Contains(answer, actingAsSomeoneElse) {
		t.Errorf("rating his own record in a session of his own: the rule refused it: %s", answer)
	}
}
