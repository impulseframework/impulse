//go:build skipAuth

package integration

// impersonation_access_skipauth_test: the class access, proven on the served stack over
// Spanner. The generated identity methods that give or take away what someone may do
// carry the class, the console's "act as a person" refuses it beside the class
// credential (app/impersonate.go), and a session whose mask refuses the class is refused
// every one of them.

import (
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
)

// accessBody is the body a generated method classed access takes, by its act: the
// method's name without the auth's and without the tenant's ("AssignCrewSectorRole" is
// the act AssignRole). A method whose act is not here fails the suite: when the
// generated set gains one, add its body, so the new method is proven refused with the
// others. The group and the invitation named exist nowhere: a body is decoded before a
// permission is asked, so each is a body the method takes, and a refusal by the mask
// never comes to reading it.
func accessBody(t *testing.T, action accesstypes.Resource, userID accesstypes.User) []byte {
	t.Helper()

	const group, invitation = "0190b000-0000-7000-8000-00000000a001", "0190b000-0000-7000-8000-00000000a002"
	// The methods whose object is the tenant itself, which the act's name does not tell
	// apart from a method of the same words for a person or a sign-in method.
	switch action {
	case "InviteCrewSector":
		return []byte(`{"email":"dusk-under-a-mask@example.com","sectorName":"Dusk","sectorRegion":"Rim","sectorEstablished":"2026-01-01"}`)
	case "WithdrawCrewInvitedSector":
		return fmt.Appendf(nil, `{"invitationId":%q}`, invitation)
	case "OpenSectorToDirectory":
		return []byte(`{"role":"Cadet"}`)
	case "MakeSectorInviteOnly":
		return []byte(`{}`)
	}

	act := string(action)
	for _, part := range []string{"Crew", "Members", "Sector"} {
		act = strings.Replace(act, part, "", 1)
	}
	switch act {
	case "AssignRole", "RemoveRole":
		return fmt.Appendf(nil, `{"userId":%q,"role":"Cadet"}`, userID)
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
	case "CreateUser":
		return []byte(`{"username":"made-under-a-mask","email":"Made <made-under-a-mask@example.com>","roles":["Cadet"]}`)
	case "DeleteUser", "DisableUser", "EnableUser":
		return fmt.Appendf(nil, `{"userId":%q}`, userID)
	default:
		t.Fatalf("the generated collection classes %s access and this suite has no body for its act %s: add one to accessBody, so the method is proven refused under a mask with the others", action, act)

		return nil
	}
}

// TestImpersonationMask_accessMethods proves on the served stack, over Spanner, that
// every action the library marks with the class access is refused a session whose mask
// refuses it, with the denial logged as the mask's, and is not refused by a mask to the
// same person in a session of his own. The actions are the generated collection's own
// list of the class, so one added to the generated set is asked here without this suite
// naming it; the ones the item names are looked for by name, an invitation that carries
// roles, a role given to a group and a tenant's starting role for its directory among
// them. The marshal, given the grant to act as a person, starts three sessions as the
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
		"AssignCrewRole", "RemoveCrewRole", "AssignCrewSectorRole", "RemoveCrewSectorRole",
		"AddCrewGroupMember", "RemoveCrewGroupMember", "GiveCrewGroupRole", "TakeCrewGroupRole", "GiveCrewSectorGroupRole",
		"CreateCrewGroup", "DeleteCrewGroup", "InviteCrewUser", "InviteCrewSectorUser", "InviteCrewSector",
		"CreateCrewUser", "DisableCrewUser", "EnableCrewUser", "DeleteCrewUser",
		"OpenCrewSignUp", "MakeCrewInviteOnly", "OpenSectorToDirectory", "MakeSectorInviteOnly",
	} {
		if !slices.Contains(access, want) {
			t.Errorf("the generated collection does not class %s access: it gives or takes away what someone may do", want)
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
				"Create, Delete, List, Read and Update only, except access actions and credential actions"),
			logged: func(action accesstypes.Resource) []string {
				return []string{"impersonation: Execute denied by mask (Create,Delete,List,Read,Update;-class:access;-class:credential) for " + string(account(t, "governor")), string(action)}
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
			browser: start("no access", actAs(t, "governor", "no access", ""), "everything except access actions and credential actions"),
			logged: func(action accesstypes.Resource) []string {
				return []string{
					"impersonation: Execute denied by mask (*;-class:access;-class:credential) for " + string(account(t, "governor")) + " on " + string(action) + ": the mask refuses the class access",
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
	for _, want := range []accesstypes.Resource{"AssignCrewRole", "CreateCrewGroup", "GiveCrewGroupRole", "InviteCrewUser", "DisableCrewUser", "EnableCrewUser"} {
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
