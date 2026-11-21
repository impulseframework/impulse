//go:build skipAuth

package integration

// impersonation_access_skipauth_test: the class access, proven on the served stack over
// Spanner. The generated identity methods that give or take away what someone may do
// carry the class, and so do the settings that say who may sign in (the reset link at
// the sign-in page, a sector's directory-only switch), the action of the route that
// creates a sector for the person who asks, and the application's own action that rates
// a pilot, which writes the clearance a person's permissions are read against. The
// console's "act as a person" refuses the class beside the class credential
// (app/impersonate.go), and a session whose mask refuses the class is refused every one
// of them. An account disabled, enabled or deleted is not in the class
// (impersonation_subject_skipauth_test proves what a session that acts as a person does
// with those).

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
)

// accessBody is the body an action classed access takes. A generated method's is found
// by its act: the method's name without the auth's and without the tenant's
// ("AssignCrewSectorRole" is the act AssignRole). An action whose act is not here fails
// the suite: when the collection gains one, add its body, so the new action is proven
// refused with the others. The group and the invitation named exist nowhere: a body is
// decoded before a permission is asked, so each is a body the action takes, and a
// refusal by the mask never comes to reading it. The pilot is one who flies for the
// sector, since the action's target is located in the sector's own view.
func accessBody(t *testing.T, action accesstypes.Resource, userID accesstypes.User) []byte {
	t.Helper()

	const group, invitation = "0190b000-0000-7000-8000-00000000a001", "0190b000-0000-7000-8000-00000000a002"
	// The application's own action that carries the class: rating a pilot.
	if action == "SetSectorPilotClearance" {
		return fmt.Appendf(nil, `{"pilotId":%q,"clearance":3}`, pilotVelaID)
	}
	// The methods whose object is the tenant itself, which the act's name does not tell
	// apart from a method of the same words for a person or a sign-in method.
	switch action {
	case "InviteCrewSector":
		return []byte(`{"email":"dusk-under-a-mask@example.com","sectorName":"Dusk","sectorRegion":"Rim","sectorEstablished":"2026-01-01"}`)
	case "WithdrawCrewInvitedSector":
		return fmt.Appendf(nil, `{"invitationId":%q}`, invitation)
	case "OpenSectorToDirectory":
		return []byte(`{"role":"Cadet"}`)
	case "MakeSectorInviteOnly", "MakeSectorDirectoryOnly", "EndSectorDirectoryOnly":
		return []byte(`{}`)
	case "CreateSector":
		return []byte(chartEmber)
	}

	act := string(action)
	for _, part := range []string{"Crew", "Members", "Sector"} {
		act = strings.Replace(act, part, "", 1)
	}
	switch act {
	case "AssignRole", "RemoveRole":
		return fmt.Appendf(nil, `{"userId":%q,"role":"Cadet"}`, userID)
	case "TakeRole":
		return fmt.Appendf(nil, `{"domain":%q,"role":"Cadet","length":"1 hour","reason":"taken under a mask"}`, anvil)
	case "CreateGroup":
		return []byte(`{"name":"Made under a mask","description":"A group nobody should have made"}`)
	case "DeleteGroup":
		return fmt.Appendf(nil, `{"groupId":%q}`, group)
	case "AddGroupMember", "RemoveGroupMember":
		return fmt.Appendf(nil, `{"groupId":%q,"userId":%q}`, group, userID)
	case "GiveGroupRole", "TakeGroupRole":
		return fmt.Appendf(nil, `{"groupId":%q,"role":"Cadet"}`, group)
	case "InviteUser":
		return []byte(`{"email":"invited-under-a-mask@example.com","roles":["Cadet"]}`)
	case "WithdrawInvitation":
		return fmt.Appendf(nil, `{"invitationId":%q}`, invitation)
	case "OpenSignUp", "MakeInviteOnly":
		return []byte(`{"name":"password"}`)
	case "SetSelfServiceReset":
		return []byte(`{"on":true}`)
	case "CreateUser":
		return []byte(`{"username":"made-under-a-mask","email":"Made <made-under-a-mask@example.com>","roles":["Cadet"]}`)
	default:
		t.Fatalf("the generated collection classes %s access and this suite has no body for its act %s: add one to accessBody, so the method is proven refused under a mask with the others", action, act)

		return nil
	}
}

// TestImpersonationMask_accessMethods proves on the served stack, over Spanner, that
// every action that carries the class access is refused a session whose mask refuses
// it, with the denial logged as the mask's, and is not refused by a mask to the same
// person in a session of his own. The actions are the generated collection's own list
// of the class, so one added to it is asked here without this suite naming it; the ones
// the library marks are looked for by name, an invitation that carries roles, a role
// taken for a set time, a role given to a group, a tenant's starting role for its
// directory, the two settings that say who may sign in (a sector's directory-only
// switch, turned on and off, and the reset link at the sign-in page) and the creation
// of a sector of one's own among them, and so is the application's own, which rates a
// pilot. The marshal, given the grant to act as a person, starts three sessions as the
// governor, and each is refused every one: a session whose mask keeps no Execute, a
// read-only view, and a session that acts as the governor, whose mask keeps every kind
// and refuses the class, which the log names.
//
// The test runs on its own, never in parallel: the console exporter writes through the
// standard logger, which the test redirects to read each denial.
//
// Demonstrates: impersonation.grant-mask, impersonation.act-as-user, impersonation.mask, @class.
func TestImpersonationMask_accessMethods(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)

	access := router.Collection().ActionsOfClass(accesstypes.ClassAccess)
	for _, want := range []accesstypes.Resource{
		"AssignCrewRole", "RemoveCrewRole", "AssignCrewSectorRole", "RemoveCrewSectorRole", "TakeCrewRole",
		"AddCrewGroupMember", "RemoveCrewGroupMember", "GiveCrewGroupRole", "TakeCrewGroupRole", "GiveCrewSectorGroupRole",
		"CreateCrewGroup", "DeleteCrewGroup", "InviteCrewUser", "InviteCrewSectorUser", "InviteCrewSector",
		"CreateCrewUser", "OpenCrewSignUp", "MakeCrewInviteOnly", "OpenSectorToDirectory", "MakeSectorInviteOnly",
		"MakeSectorDirectoryOnly", "EndSectorDirectoryOnly", "SetCrewSelfServiceReset", "CreateSector",
		"SetSectorPilotClearance",
	} {
		if !slices.Contains(access, want) {
			t.Errorf("the generated collection does not class %s access: it gives or takes away what someone may do", want)
		}
	}
	// An account disabled, enabled or deleted is the work a person who stands in for an
	// administrator is there to do, and carries no class.
	for _, kept := range []accesstypes.Resource{"DisableCrewUser", "EnableCrewUser", "DeleteCrewUser", "DisableCrewSectorUser", "EnableCrewSectorUser", "DeleteCrewSectorUser"} {
		if slices.Contains(access, kept) {
			t.Errorf("the generated collection classes %s access: an account disabled, enabled or deleted carries no class", kept)
		}
	}

	giveMarshalStandIn(ctx, t, s)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	status, body := governor.do(ctx, http.MethodPost, sectorPath(anvil, "create-crew-sector-user"), []byte(`{"username":"gus","email":"Gus <gus@example.com>","roles":["Cadet"]}`))
	assertStatus(t, status, http.StatusOK, body)
	target := accesstypes.User(cell[string](t, decodeRow(t, body), "id"))

	type masked struct {
		name    string
		browser *browser
		// says is what the refusal's answer says, and logged what the log carries for
		// the refusal of one action.
		says   string
		logged func(action accesstypes.Resource) []string
	}
	start := func(name, body, wantMask string) *browser {
		t.Helper()
		b := newBrowser(t, s, consoleAPI)
		b.signIn(ctx, "marshal")
		if got := mint(ctx, t, b, body); got.Mask != wantMask {
			t.Fatalf("%s: the session's mask = %q, want %q", name, got.Mask, wantMask)
		}

		return b
	}
	sessions := []masked{
		{
			name: "a session whose mask keeps no Execute",
			browser: start("no Execute", actAs(t, "governor", "no Execute", `,"mask":["List","Read","Create","Update","Delete"]`),
				"Create, Delete, List, Read and Update only, except "+actMaskRefusalWords),
			logged: func(action accesstypes.Resource) []string {
				return []string{"impersonation: Execute denied by mask (Create,Delete,List,Read,Update;" + actMaskRefusalText + ") for " + string(account(t, "governor")), string(action)}
			},
		},
		{
			name:    "a read-only view",
			browser: start("read-only", viewAs(t, "governor", "read-only"), "reading only"),
			says:    "this session is read-only",
			logged: func(action accesstypes.Resource) []string {
				return []string{"impersonation: POST " + methodRoute(action) + " refused for", "impersonation.event=WriteBlocked", "impersonation.mask=List,Read"}
			},
		},
		{
			name:    "a session whose mask refuses the class access",
			browser: start("no access", actAs(t, "governor", "no access", ""), actMaskWords),
			logged: func(action accesstypes.Resource) []string {
				return []string{
					"impersonation: Execute denied by mask (" + actMaskText + ") for " + string(account(t, "governor")) + " on " + string(action) + ": the mask refuses the class access",
				}
			},
		},
	}

	var output requestLogOutput
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	// The actions the governor was not refused in a session of his own: each answered
	// as the method answers, whatever it found to do.
	letThrough := make(map[accesstypes.Resource]bool)
	for _, action := range access {
		route, payload := methodRoute(action), accessBody(t, action, target)

		// The governor in a session of his own, as a dry run: whatever the method
		// answers, no mask is named in the log.
		output.reset()
		if status, _ := dryRun(ctx, governor, route, payload); status != http.StatusForbidden {
			letThrough[action] = true
		}
		if logged := output.String(); strings.Contains(logged, "denied by mask") || strings.Contains(logged, "WriteBlocked") {
			t.Errorf("%s as the governor, unmasked: the log names a mask: %s", action, logged)
		}

		for _, session := range sessions {
			output.reset()
			status, answer := session.browser.do(ctx, http.MethodPost, route, payload)
			if status != http.StatusForbidden || !strings.Contains(string(answer), session.says) {
				t.Errorf("%s in %s: status %d, want 403 saying %q: %s", action, session.name, status, session.says, answer)
			}
			logged := output.String()
			for _, want := range session.logged(action) {
				if !strings.Contains(logged, want) {
					t.Errorf("%s in %s: the log does not carry %q, the denial as the mask's:\n%s", action, session.name, want, logged)
				}
			}
		}
	}

	// The comparison with the same person unmasked stands on actions he runs: the ones
	// headquarters holds for the whole crew.
	for _, want := range []accesstypes.Resource{"AssignCrewRole", "CreateCrewGroup", "GiveCrewGroupRole", "InviteCrewUser"} {
		if !letThrough[want] {
			t.Errorf("%s was refused the governor in a session of his own: the suite compares a masked session with the same person unmasked", want)
		}
	}

	// The session that acts as the governor still does what its mask keeps: an action
	// that is neither a credential action nor one that changes who may do what.
	if status, body := sessions[2].browser.do(ctx, http.MethodPost, consoleAPI+"/issue-bulletin", []byte(`{"announcement":"All hands: drill at 0600"}`)); status != http.StatusOK {
		t.Errorf("issue-bulletin in %s: status %d, want 200: the mask refuses the two classes alone: %s", sessions[2].name, status, body)
	}
}

// crewAccountNamed reads the id of the crew account under a username from the served
// stack's database, as the permission store names the person: the way to a person who
// signed up during the test, whom the cast does not carry.
func crewAccountNamed(ctx context.Context, t *testing.T, s *served, username accesstypes.User) accesstypes.User {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT Id FROM CrewSessionUsers WHERE Username = @username",
		Params: map[string]any{"username": string(username)},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the crew account of %s: %v", username, err)
	}
	var id string
	if err := row.Columns(&id); err != nil {
		t.Fatalf("reading the id of the crew account of %s: %v", username, err)
	}

	return accesstypes.User(id)
}

// TestImpersonationMask_creatingASector proves on the served stack, over Spanner, who
// decides the creation of a sector in a session that acts as someone else. The route
// takes no grant: a signed-in person who holds no sector creates one and becomes its
// marshal, so in a session that acts as a person the new marshal would be the person
// acted as. The route is the action CreateSector, classed access, and the mask decides.
//
// The governor opens sign-up and the pioneer signs up, holding nothing: her own page
// is told a sector can be created. The marshal, given the grant to act as a person,
// acts as the pioneer under the act kind, whose mask refuses the class: the creation is
// refused in words that name the mask, the denial is logged as the mask's, no sector
// and no membership is written, and the page that offers the creation is told none is
// offered. The marshal then acts as the Bulletin Officer under the role kind, whose
// mask refuses nothing: the mask does not answer, and the rule that nobody is the
// subject of what they do refuses her, as before. Last, the pioneer in the session of
// her own, which has no mask, creates the sector the stand-in was refused.
//
// The test runs on its own, never in parallel: the console exporter writes through the
// standard logger, which the test redirects to read each denial.
//
// Demonstrates: impersonation.grant-mask, impersonation.act-as-user, impersonation.act-as-role, impersonation.mask, impersonation.never-the-subject, tenancy.self-service, @class.
func TestImpersonationMask_creatingASector(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)

	if access := router.Collection().ActionsOfClass(accesstypes.ClassAccess); !slices.Contains(access, "CreateSector") {
		t.Errorf("the generated collection does not class CreateSector access (%v): no mask could refuse the creation of a sector", access)
	}

	giveMarshalStandIn(ctx, t, s)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	openCrewSignUp(ctx, t, governor, crew.SignUpRegistration)

	// The pioneer signs up and holds nothing, so the route's own rules admit her: one
	// sector per person, while sign-up is open.
	pioneer := newBrowser(t, s, consoleAPI)
	if status, body := pioneer.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the sign-up: status %d: %s", status, body)
	}
	held := consoleLogin + "?pending=mfa&returnUrl=" + url.QueryEscape(consolePage)
	if status, location := pioneer.loginConsoleSignUp(ctx, string(pioneerUser)); status != http.StatusFound || location != held {
		t.Fatalf("console sign-up: status %d to %q, want %d to %q, the login page with the second factor's wait", status, location, http.StatusFound, held)
	}
	pioneer.secondFactor(ctx, string(pioneerUser))
	status, answer := pioneer.do(ctx, http.MethodGet, createSectorRoute, nil)
	assertStatus(t, status, http.StatusOK, answer)
	assertOffered(true)(t, "the pioneer in a session of her own, asking whether a sector can be created", answer)
	pioneerID := crewAccountNamed(ctx, t, s, pioneerUser)

	var output requestLogOutput
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})
	written := func() [2]int64 {
		t.Helper()

		return [2]int64{tableRows(ctx, t, s, "Sectors"), tableRows(ctx, t, s, "CrewUserRoles")}
	}
	before := written()

	// The act kind: the session acts as the pioneer, who holds no sector, so "one
	// sector per person" is not what answers. The mask refuses the class, and the
	// route's action carries it.
	standIn := newBrowser(t, s, consoleAPI)
	standIn.signIn(ctx, "marshal")
	if got := mint(ctx, t, standIn, fmt.Sprintf(`{"kind":"act","principal":%q,"reason":"helping the pioneer settle in"}`, pioneerID)); got.Mask != actMaskWords {
		t.Fatalf("the mask of the session that acts as the pioneer = %q, want %q", got.Mask, actMaskWords)
	}
	if reached := standIn.domains(ctx); len(reached) != 0 {
		t.Fatalf("user-domains acting as the pioneer = %v, want none: the route's own refusal of a session that holds a sector would answer first", reached)
	}
	output.reset()
	status, answer = standIn.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber))
	if got, want := refusalMessage(t, answer), "You are acting as someone else, and this session cannot create a sector."; status != http.StatusForbidden || got != want {
		t.Errorf("acting as the pioneer, creating a sector: status %d saying %q, want 403 saying %q", status, got, want)
	}
	if want, logged := "impersonation: Execute denied by mask ("+actMaskText+") for "+string(pioneerID)+" on CreateSector: the mask refuses the class access", output.String(); !strings.Contains(logged, want) {
		t.Errorf("acting as the pioneer, creating a sector: the log does not carry %q, the denial as the mask's:\n%s", want, logged)
	}
	if got := written(); got != before {
		t.Errorf("sectors and the crew's memberships after the call the mask refused = %v, want the %v there were: nothing is written", got, before)
	}
	status, answer = standIn.do(ctx, http.MethodGet, createSectorRoute, nil)
	assertStatus(t, status, http.StatusOK, answer)
	assertOffered(false)(t, "acting as the pioneer, asking whether a sector can be created", answer)

	// The role kind: its mask refuses nothing, so the mask does not answer. The creator
	// would be the marshal herself, the real person behind the session, and the rule
	// that nobody is the subject of what they do refuses her.
	asRole := newBrowser(t, s, consoleAPI)
	asRole.signIn(ctx, "marshal")
	mint(ctx, t, asRole, `{"kind":"role","principal":"BulletinOfficer","reason":"covering the bulletin desk"}`)
	if reached := asRole.domains(ctx); len(reached) != 0 {
		t.Fatalf("user-domains acting as the Bulletin Officer = %v, want none: the route's own refusal of a session that holds a sector would answer first", reached)
	}
	output.reset()
	status, answer = asRole.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber))
	if want := actingAsSomeoneElse + "create a sector of your own."; status != http.StatusForbidden || !strings.Contains(string(answer), want) {
		t.Errorf("acting as the Bulletin Officer, creating a sector: status %d, want 403 saying %q, the rule's own words: %s", status, want, answer)
	}
	if logged := output.String(); strings.Contains(logged, "denied by mask") {
		t.Errorf("acting as the Bulletin Officer, creating a sector: the log names a mask, and the kind's mask refuses nothing:\n%s", logged)
	}
	if got := written(); got != before {
		t.Errorf("sectors and the crew's memberships after the call the rule refused = %v, want the %v there were: nothing is written", got, before)
	}
	status, answer = asRole.do(ctx, http.MethodGet, createSectorRoute, nil)
	assertStatus(t, status, http.StatusOK, answer)
	assertOffered(false)(t, "acting as the Bulletin Officer, asking whether a sector can be created", answer)

	// The pioneer herself, in a session with no mask: the creation the stand-in was
	// refused is hers to make, and no mask is named.
	output.reset()
	status, answer = pioneer.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber))
	if status != http.StatusOK {
		t.Fatalf("the pioneer creating a sector in a session of her own: status %d, want 200: %s", status, answer)
	}
	ember := createdKey(t, "the pioneer creating a sector in a session of her own", answer)
	if logged := output.String(); strings.Contains(logged, "denied by mask") {
		t.Errorf("the pioneer creating a sector in a session of her own: the log names a mask:\n%s", logged)
	}
	if got, want := pioneer.domains(ctx), []string{ember}; !slices.Equal(got, want) {
		t.Errorf("the pioneer's sectors after the creation = %v, want %v", got, want)
	}
	if got := tableRows(ctx, t, s, "Sectors"); got != before[0]+1 {
		t.Errorf("sectors after the pioneer's own creation = %d, want %d: the one she made", got, before[0]+1)
	}
}
