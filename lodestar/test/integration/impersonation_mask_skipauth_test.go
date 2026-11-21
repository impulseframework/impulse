//go:build skipAuth

package integration

// impersonation_mask_skipauth_test: impersonation is granted with a mask, proven on the
// served stack over Spanner. The console declares three kinds of impersonation, each
// with the grant that allows it and the widest mask that grant gives (app/impersonate.go);
// these suites prove what each mask refuses, that a request gets the grant's mask or a
// narrower one and never a wider one, and that the generated credential methods, each
// one the generated collection classes credential, are refused under every mask that
// refuses them while the person they are refused to runs them unmasked. The actions
// classed access are impersonation_access_skipauth_test's, and the rule that nobody is
// the subject of what they do impersonation_subject_skipauth_test's.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
)

// methodRoute is the console's route of a generated method, from its action's name: the
// name in kebab case, under a sector's prefix for a method the collection registers for
// a tenant and under the console's own for one of the whole application.
func methodRoute(action accesstypes.Resource) string {
	var route strings.Builder
	for i, r := range string(action) {
		if unicode.IsUpper(r) && i > 0 {
			route.WriteByte('-')
		}
		route.WriteRune(unicode.ToLower(r))
	}
	if router.Collection().HasPermission(accesstypes.DomainPermissionScope, accesstypes.Execute, action) {
		return sectorPath(anvil, route.String())
	}

	return consoleAPI + "/" + route.String()
}

// credentialBody is the body a generated credential method takes, by its act: the
// method's name without the auth's and without the tenant's ("SetCrewSectorUserEmail"
// is the act SetUserEmail). A credential method whose act is not here fails the suite:
// when the generated set gains one, add its body, so the new method is proven refused
// with the others.
func credentialBody(t *testing.T, action accesstypes.Resource, userID accesstypes.User) []byte {
	t.Helper()

	act := string(action)
	for _, part := range []string{"Crew", "Members", "Sector"} {
		act = strings.Replace(act, part, "", 1)
	}
	switch act {
	case "RequirePasswordChange", "RemovePassword", "SendUserPasswordResetLink":
		return fmt.Appendf(nil, `{"userId":%q}`, userID)
	case "UnlinkSessionIdentity":
		return []byte(`{"identityId":"c0000000-0000-4000-8000-00000000c4ed"}`)
	case "RenameUser":
		return fmt.Appendf(nil, `{"userId":%q,"username":"renamed-under-a-mask"}`, userID)
	case "SetUserEmail":
		return fmt.Appendf(nil, `{"userId":%q,"email":"under-a-mask@example.com"}`, userID)
	default:
		t.Fatalf("the generated collection classes %s credential and this suite has no body for its act %s: add one to credentialBody, so the method is proven refused under a mask with the others", action, act)

		return nil
	}
}

// anvilsOwn makes a person Anvil holds alone, whom a sector's own methods reach (their
// grants stop at a person who holds a role anywhere else): an account nobody has signed
// in to (seedCrewAccount), which the governor gives the Cadet role in Anvil. Nobody
// makes an account for someone else through the application, where a person comes in by
// an invitation. It answers the account's id.
func anvilsOwn(ctx context.Context, t *testing.T, s *served) accesstypes.User {
	t.Helper()

	address := "gus@example.com"
	id := s.seedCrewAccount(ctx, t, "gus", &address).String()
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	body := fmt.Appendf(nil, `{"userId":%q,"role":"Cadet","domain":%q}`, id, anvil)
	if status, answer := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), body); status != http.StatusNoContent {
		t.Fatalf("the governor giving the new person the Cadet role in Anvil: status %d, want 204: %s", status, answer)
	}

	return accesstypes.User(id)
}

// dryRun posts a method as the browser with X-Dry-Run: the whole call runs, every
// refusal answers as the real call would, and the transaction is rolled back.
func dryRun(ctx context.Context, b *browser, path string, body []byte) (status int, answer string) {
	b.t.Helper()

	resp := b.doWith(ctx, http.MethodPost, path, body, map[string]string{"X-Dry-Run": "true"})
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}

	return resp.StatusCode, string(raw)
}

// minted is the mint route's answer: the session it started and what the session may do.
type minted struct {
	SessionID string `json:"sessionId"`
	Mask      string `json:"mask"`
}

// mint starts an impersonated session as the browser and fails the test when the route
// refuses.
func mint(ctx context.Context, t *testing.T, b *browser, body string) minted {
	t.Helper()

	status, answer := b.impersonate(ctx, body)
	if status != http.StatusOK {
		t.Fatalf("POST impersonate %s: status %d, want 200: %s", body, status, answer)
	}
	var out minted
	if err := json.Unmarshal(answer, &out); err != nil {
		t.Fatal(err)
	}

	return out
}

// digestOf reads the browser's permission digest in a sector, or the whole
// application's when sector is empty.
func digestOf(ctx context.Context, t *testing.T, b *browser, sector string) accesstypes.PermissionDigest {
	t.Helper()

	path := consoleAPI + "/permission-digest"
	if sector != "" {
		path += "?domain=" + sector
	}
	status, body := b.do(ctx, http.MethodGet, path, nil)
	assertStatus(t, status, http.StatusOK, body)
	var digest accesstypes.PermissionDigest
	if err := json.Unmarshal(body, &digest); err != nil {
		t.Fatal(err)
	}

	return digest
}

// TestImpersonationMask_credentialMethods proves on the served stack, over Spanner, that
// every generated credential method is refused a session whose mask refuses it, with the
// denial logged as the mask's, and is not refused the same person unmasked. The methods
// are the generated collection's own list of the actions classed credential, so one
// added to the generated set is asked here without this suite naming it. The governor
// runs each as a dry run in a session of his own, on a person Anvil holds alone, and is
// let through every one his roles give him; the ones the shipped role file gives nobody
// (the members' methods of a sector) are refused him by policy, which says so, and never
// by a mask. The marshal, given the grant to act as a person, then starts three sessions
// as the governor, and each is refused every one: a session whose mask keeps no Execute,
// by the masked check, which the log names with the mask; a read-only view, by the
// read-only guard in front of every route, which records the blocked write with the
// mask; and a session that acts as the governor, whose mask keeps every kind and refuses
// the class credential, by the masked check, which the log names with the class. Each
// session still does what its mask keeps.
//
// The test runs on its own, never in parallel: the console exporter writes through the
// standard logger, which the test redirects to read each denial.
//
// Demonstrates: impersonation.grant-mask, impersonation.act-as-user, impersonation.mask, @class.
func TestImpersonationMask_credentialMethods(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)

	credential := router.Collection().ActionsOfClass(accesstypes.ClassCredential)
	if len(credential) == 0 {
		t.Fatal("the generated collection classes no action credential: the generated identity methods that change how a person signs in carry the class")
	}

	giveMarshalStandIn(ctx, t, s)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	// The person the methods are asked to act on: one Anvil holds alone, whom a sector's
	// own methods reach (their grants stop at a person who holds a role anywhere else).
	target := anvilsOwn(ctx, t, s)
	// What the governor's roles give him, in the whole application and in Anvil.
	holds := func(action accesstypes.Resource) bool {
		t.Helper()
		sector := ""
		if router.Collection().HasPermission(accesstypes.DomainPermissionScope, accesstypes.Execute, action) {
			sector = anvil
		}
		_, ok := digestOf(ctx, t, governor, sector)[action][accesstypes.Execute]

		return ok
	}
	// The credential methods the shipped role file gives no role, the governor
	// included: the members' methods of a sector. Every other one he holds.
	nobodyHolds := []accesstypes.Resource{"RenameMembersSectorUser", "SetMembersSectorUserEmail", "UnlinkMembersSectorSessionIdentity"}
	var held int

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
			name:    "a session whose mask keeps no Execute",
			browser: start("no Execute", actAs(t, "governor", "no Execute", `,"mask":["List","Read","Create","Update","Delete"]`), "Create, Delete, List, Read and Update only, except "+actMaskRefusalWords),
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
			name:    "a session whose mask refuses the class credential",
			browser: start("no credential", actAs(t, "governor", "no credential", ""), actMaskWords),
			logged: func(action accesstypes.Resource) []string {
				return []string{"impersonation: Execute denied by mask (" + actMaskText + ") for " + string(account(t, "governor")) + " on " + string(action) + ": the mask refuses the class credential"}
			},
		},
	}

	var output requestLogOutput
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	for _, action := range credential {
		route, payload := methodRoute(action), credentialBody(t, action, target)

		// The governor in a session of his own. A method his roles give him lets him
		// through: the answer is the method's own (it ran, or it found nothing to run
		// on), never a refusal. One the role file gives nobody is refused by policy,
		// which names the grant he lacks. Either way no mask is named in the log.
		holdsIt := holds(action)
		if holdsIt == slices.Contains(nobodyHolds, action) {
			t.Errorf("%s: the governor's digest says he holds it = %t, want %t: the comparison with the same person unmasked stands on every credential method the role file gives him", action, holdsIt, !holdsIt)
		}
		output.reset()
		switch status, answer := dryRun(ctx, governor, route, payload); {
		case holdsIt && status == http.StatusForbidden:
			t.Errorf("%s as the governor, unmasked: status 403, want the method let through: %s", action, answer)
		case holdsIt:
			held++
		case status != http.StatusForbidden || !strings.Contains(answer, "You may not do this here."):
			t.Errorf("%s as the governor, who does not hold it, unmasked: status %d, want 403 from policy, in its own words: %s", action, status, answer)
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

	// The comparison stands on the methods the governor holds: every credential method
	// but the ones the role file gives nobody.
	if want := len(credential) - len(nobodyHolds); held != want {
		t.Errorf("the governor was let through %d credential methods unmasked, want %d: the suite compares a masked session with the same person unmasked", held, want)
	}

	// Each session still does what its mask keeps: the two that keep no Execute read,
	// and the one that acts as the governor runs a method that is no credential action.
	for _, session := range sessions[:2] {
		if status, body := session.browser.do(ctx, http.MethodGet, sectorPath(anvil, "missions"), nil); status != http.StatusOK {
			t.Errorf("GET missions in %s: status %d, want 200: the mask keeps reading: %s", session.name, status, body)
		}
	}
	if status, body := sessions[2].browser.do(ctx, http.MethodPost, consoleAPI+"/issue-bulletin", []byte(`{"announcement":"All hands: drill at 0600"}`)); status != http.StatusOK {
		t.Errorf("issue-bulletin in %s: status %d, want 200: the mask refuses the two classes alone: %s", sessions[2].name, status, body)
	}
}

// TestImpersonationMask_grantsAndCeilings proves on the served stack that each kind of
// impersonation is a grant of its own with a ceiling. The marshal holds the grant to view
// as a person and not the grant to act as one: she is refused a session that acts, and a
// view with more than reading only, each with a sentence and nothing started. A view
// that names no mask gets the grant's, reading only; one that asks for less gets the
// narrower mask. The governor, who holds the grant to act, acts as the marshal: a write
// of hers lands, and a credential method of her sector, which she runs herself on a
// person her sector holds alone, is refused the session. The watch desk says each
// session's mask in words.
//
// Demonstrates: impersonation.grant-mask, impersonation.act-as-user, impersonation.view-as, impersonation.active-list.
func TestImpersonationMask_grantsAndCeilings(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	desk := newBrowser(t, s, consoleAPI)
	desk.signIn(ctx, "governor")
	// The watch desk is the list of the crew auth's impersonation records: what it says
	// of each session, by the session's id.
	listed := func() map[string]map[string]any {
		t.Helper()
		status, body := desk.do(ctx, http.MethodGet, consoleAPI+"/crew-session-impersonations?limit=200", nil)
		assertStatus(t, status, http.StatusOK, body)
		bySession := make(map[string]map[string]any)
		for _, row := range decodeRows(t, body) {
			bySession[cell[string](t, row, "sessionId")] = row
		}

		return bySession
	}
	impersonation := func(b *browser) map[string]any {
		t.Helper()
		record, _ := b.session(ctx)["impersonation"].(map[string]any)

		return record
	}

	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	// The requests the route refuses, each with a sentence, and nothing started by any.
	refused := []struct {
		name, body string
		wantStatus int
		says       string
	}{
		{
			name:       "the grant to view is not the grant to act",
			body:       actAs(t, "cadet", "no grant", ""),
			wantStatus: http.StatusForbidden,
			says:       "takes Execute on ActAsUser, which you do not hold",
		},
		{
			name:       "a view with more than the grant gives is refused, not trimmed",
			body:       fmt.Sprintf(`{"kind":"view","principal":%q,"reason":"more","mask":["List","Read","Execute"]}`, account(t, "cadet")),
			wantStatus: http.StatusForbidden,
			says:       "gives reading only, and the request asks for Execute as well",
		},
		{
			name:       "a view with one kind the grant gives and one it does not",
			body:       fmt.Sprintf(`{"kind":"view","principal":%q,"reason":"more","mask":["Read","Update"]}`, account(t, "cadet")),
			wantStatus: http.StatusForbidden,
			says:       "the request asks for Update as well",
		},
		{
			name:       "a kind the console does not offer",
			body:       fmt.Sprintf(`{"kind":"user","principal":%q}`, account(t, "cadet")),
			wantStatus: http.StatusBadRequest,
			says:       "is not a kind of impersonation this application offers: it offers view, act, role",
		},
		{
			name:       "a mask that names a kind spelled as no permission kind is",
			body:       fmt.Sprintf(`{"kind":"view","principal":%q,"mask":["list","read"]}`, account(t, "cadet")),
			wantStatus: http.StatusBadRequest,
			says:       "which is no permission kind: the kinds are Create, Delete, Execute, List, Read and Update, spelled so",
		},
		{
			name:       "a mask that refuses a class no action carries",
			body:       fmt.Sprintf(`{"kind":"view","principal":%q,"refuseClasses":["nonesuch"]}`, account(t, "cadet")),
			wantStatus: http.StatusBadRequest,
			says:       "nonesuch",
		},
		{
			name:       "a mask that refuses an action the application does not have",
			body:       fmt.Sprintf(`{"kind":"view","principal":%q,"refuseActions":["ScuttleShip"]}`, account(t, "cadet")),
			wantStatus: http.StatusBadRequest,
			says:       "ScuttleShip",
		},
	}
	for _, tt := range refused {
		status, body := marshal.impersonate(ctx, tt.body)
		if status != tt.wantStatus || !strings.Contains(string(body), tt.says) {
			t.Errorf("%s: status %d, want %d saying %q: %s", tt.name, status, tt.wantStatus, tt.says, body)
		}
		if info := marshal.session(ctx); info["username"] != "marshal" || info["impersonation"] != nil {
			t.Fatalf("%s: the session after the refusal = %v, want the marshal's own: a refused request starts nothing", tt.name, info)
		}
	}
	if entries := listed(); len(entries) != 0 {
		t.Errorf("the watch desk after the refused requests = %v, want no session: a refused request starts nothing", entries)
	}

	// The ceiling when the request names no mask: the grant's, reading only.
	view := mint(ctx, t, marshal, viewAs(t, "cadet", "the grant's mask"))
	if view.Mask != "reading only" {
		t.Errorf("a view that names no mask = %q, want the grant's: reading only", view.Mask)
	}
	record := impersonation(marshal)
	if kinds, _ := record["mask"].([]any); !slices.Equal(kinds, []any{"List", "Read"}) || record["maskWords"] != "reading only" || record["refusedClasses"] != nil {
		t.Errorf("the view's record = %v, want the kinds List and Read, in words reading only, nothing refused inside them", record)
	}

	// The narrower one when the request names one: List alone, on a browser of its own.
	narrower := newBrowser(t, s, consoleAPI)
	narrower.signIn(ctx, "marshal")
	listing := mint(ctx, t, narrower, fmt.Sprintf(`{"kind":"view","principal":%q,"reason":"narrower","mask":["List"]}`, account(t, "cadet")))
	if listing.Mask != "List only" {
		t.Errorf("a view that asks for List alone = %q, want the narrower mask: List only", listing.Mask)
	}
	status, body := narrower.do(ctx, http.MethodGet, sectorPath(anvil, "missions?limit=200"), nil)
	assertStatus(t, status, http.StatusOK, body)
	rows := decodeRows(t, body)
	if len(rows) == 0 {
		t.Fatal("the cadet's board under List alone is empty, want the missions the cadet lists")
	}
	mission := sectorPath(anvil, "missions/"+cell[string](t, rows[0], "id"))
	if status, body := narrower.do(ctx, http.MethodGet, mission, nil); status != http.StatusForbidden {
		t.Errorf("reading a mission under List alone: status %d, want 403: the session asked for less than the grant gives, and holds less: %s", status, body)
	}
	if status, body := marshal.do(ctx, http.MethodGet, mission, nil); status != http.StatusOK {
		t.Errorf("reading the same mission under the grant's own mask: status %d, want 200: %s", status, body)
	}

	// A person Anvil holds alone, whose address the marshal's own grant reaches: she
	// sets it herself, in a session of her own.
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	setAddress := sectorPath(anvil, "set-crew-sector-user-email")
	address := fmt.Appendf(nil, `{"userId":%q,"email":"gus.new@example.com"}`, anvilsOwn(ctx, t, s))
	if status, answer := dryRun(ctx, own, setAddress, address); status != http.StatusOK {
		t.Fatalf("the marshal setting the address of a person her sector holds alone, as a dry run: status %d, want 200: %s", status, answer)
	}

	// The governor holds the grant to act and acts as the marshal: the session does
	// what she may do, except the credential actions, the actions that change who may
	// do what, and the writes of the records that decide what a person may do.
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "governor")
	act := mint(ctx, t, acting, actAs(t, "marshal", "stand in", ""))
	if act.Mask != actMaskWords {
		t.Errorf("a session that acts as a person = %q, want the grant's mask: %s", act.Mask, actMaskWords)
	}
	if info := acting.session(ctx); info["username"] != "marshal" {
		t.Fatalf("the session after the governor acts as the marshal = %v, want the marshal's", info)
	}
	record = impersonation(acting)
	if classes, _ := record["refusedClasses"].([]any); record["actor"] != "governor" || record["mask"] != nil || !slices.Equal(classes, []any{"access", "credential"}) || record["maskWords"] != actMaskWords {
		t.Errorf("the acting session's record = %v, want the actor governor, every kind kept, the classes access and credential refused, and the mask in words", record)
	}
	// The record keeps what the kind keeps out of the class access: the three writes of
	// HangarPasses, each by the class, the permission and the record.
	var kept []string
	excepted, _ := record["excepted"].([]any)
	for _, entry := range excepted {
		exception, _ := entry.(map[string]any)
		kept = append(kept, fmt.Sprintf("%v:%v:%v", exception["class"], exception["permission"], exception["resource"]))
	}
	if want := []string{"access:Create:HangarPasses", "access:Delete:HangarPasses", "access:Update:HangarPasses"}; !slices.Equal(kept, want) {
		t.Errorf("the acting session's record keeps %v out of the classes it refuses, want %v", kept, want)
	}
	// The record names no write: the class access is on the writes of the records that
	// decide what a person may do, so the kind refuses them without naming one.
	if named, _ := record["refused"].([]any); len(named) != 0 {
		t.Errorf("the acting session's record names the refused writes %v, want none: the kind refuses the writes by their class", record["refused"])
	}
	if status, body := acting.do(ctx, http.MethodPost, consoleAPI+"/issue-bulletin", []byte(`{"announcement":"Relief watch: drill moved to 0700"}`)); status != http.StatusOK {
		t.Errorf("issuing a bulletin acting as the marshal: status %d, want 200: the session acts: %s", status, body)
	}
	if status, body := acting.do(ctx, http.MethodPost, setAddress, address); status != http.StatusForbidden {
		t.Errorf("setting a person's address acting as the marshal: status %d, want 403: the session's mask refuses the credential actions: %s", status, body)
	}
	if status, body := own.do(ctx, http.MethodPost, setAddress, address); status != http.StatusOK {
		t.Errorf("the marshal setting the address herself: status %d, want 200, the answer that says whether the confirmation went: %s", status, body)
	}

	// A third kind: the governor acts as a role, which the declaration leaves unmasked.
	asRole := newBrowser(t, s, consoleAPI)
	asRole.signIn(ctx, "governor")
	role := mint(ctx, t, asRole, `{"kind":"role","principal":"Dispatcher","reason":"as a role"}`)
	if role.Mask != "everything" {
		t.Errorf("a session that acts as a role = %q, want the grant's mask: everything", role.Mask)
	}

	// The watch desk says each session's mask in words: it is the generated list of the
	// impersonation records, which reads the stored text as words.
	words := map[string]string{view.SessionID: "reading only", listing.SessionID: "List only", act.SessionID: actMaskWords, role.SessionID: "everything"}
	entries := listed()
	for id, want := range words {
		if got, ok := entries[id]; !ok || got["mask"] != want {
			t.Errorf("the watch desk's record of the session %s = %v, want it listed with the mask %q", id, got, want)
		}
	}
	status, body = desk.do(ctx, http.MethodGet, consoleAPI+"/crew-session-impersonations/"+act.SessionID, nil)
	assertStatus(t, status, http.StatusOK, body)
	if got := decodeRow(t, body)["mask"]; got != words[act.SessionID] {
		t.Errorf("the generated read of the acting session's record says the mask %v, want %q", got, words[act.SessionID])
	}
}

// TestImpersonationMask_namedActionAndOwnClass proves on the served stack the two
// refusals a request adds inside the kinds its session keeps. A mask that refuses one
// action by name refuses that action alone: the session is refused ScrapShip, which the
// marshal runs herself, and its permission digest is the marshal's own without that
// action and without the credential actions the kind refuses, every neighbor kept as
// she holds it. And an action the application marks with a class of its own, ScrapShip
// with irreversible, is refused under a mask that names the class, while a session of
// the same kind that does not name it is let through.
//
// Demonstrates: impersonation.mask, impersonation.grant-mask, @class.
func TestImpersonationMask_namedActionAndOwnClass(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	collection := router.Collection()
	if got := collection.ActionsOfClass(router.ClassIrreversible); !slices.Equal(got, []accesstypes.Resource{"ScrapShip"}) {
		t.Fatalf("the actions classed irreversible = %v, want ScrapShip alone", got)
	}
	scrap := sectorPath(anvil, "scrap-ship")
	mule := fmt.Appendf(nil, `{"refitId":%q}`, refitMuleID)

	// The marshal's own session: she scraps a ship (as a dry run, so the ship stays),
	// and her digest carries the action.
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	if status, answer := dryRun(ctx, own, scrap, mule); status != http.StatusOK {
		t.Fatalf("the marshal scrapping a ship, as a dry run: status %d, want 200: %s", status, answer)
	}
	hers := digestOf(ctx, t, own, anvil)
	for _, action := range []accesstypes.Resource{"ScrapShip", "AssignCrewSectorRole", "RequireCrewSectorPasswordChange"} {
		if _, ok := hers[action][accesstypes.Execute]; !ok {
			t.Fatalf("the marshal's digest carries no Execute on %s: the suite reads ScrapShip, a neighbor of it and a credential method off it", action)
		}
	}
	for record, perm := range map[accesstypes.Resource]accesstypes.Permission{"SquadronMemberships": accesstypes.Create, "HangarPasses": accesstypes.Delete, "Squadrons": accesstypes.Update, "Squadrons.wingId": accesstypes.Update} {
		if _, ok := hers[record][perm]; !ok {
			t.Fatalf("the marshal's digest carries no %s on %s: the suite reads off it the writes the act kind refuses, and the writes of HangarPasses it keeps", perm, record)
		}
	}

	acting := func(name, more, wantMask string) *browser {
		t.Helper()
		b := newBrowser(t, s, consoleAPI)
		b.signIn(ctx, "governor")
		if got := mint(ctx, t, b, actAs(t, "marshal", name, more)); got.Mask != wantMask {
			t.Fatalf("%s: the session's mask = %q, want %q", name, got.Mask, wantMask)
		}

		return b
	}
	// What the kind refuses of its own on a record: the three writes of the records that
	// decide what a person may do, and a squadron's change, each on the record and on
	// every field of it. The writes of HangarPasses are classed access with them, and
	// the kind keeps them out of the class: the digest carries them as the marshal
	// holds them.
	writes := []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete}
	kindRefusesWrites := map[accesstypes.Resource][]accesstypes.Permission{
		"ClientContacts": writes, "PilotCertifications": writes, "Pilots": writes, "SquadronMemberships": writes,
		"Squadrons": {accesstypes.Update},
	}
	// wantDigest is the marshal's own digest without the actions a mask refuses and
	// without the writes the kind refuses on a record, every other entry as she holds
	// it. An entry left with no permission is not carried.
	wantDigest := func(refused ...accesstypes.Resource) accesstypes.PermissionDigest {
		want := make(accesstypes.PermissionDigest, len(hers))
		for res, perms := range hers {
			if slices.Contains(refused, res) {
				continue
			}
			record, _, _ := strings.Cut(string(res), ".")
			kept := maps.Clone(perms)
			for _, perm := range kindRefusesWrites[accesstypes.Resource(record)] {
				delete(kept, perm)
			}
			if len(kept) > 0 {
				want[res] = kept
			}
		}

		return want
	}
	assertDigest := func(name string, got, want accesstypes.PermissionDigest) {
		t.Helper()
		for res, perms := range want {
			if fmt.Sprint(got[res]) != fmt.Sprint(perms) {
				t.Errorf("%s: digest[%s] = %v, want %v: the mask refuses what it names and keeps its neighbors", name, res, got[res], perms)
			}
		}
		for res := range got {
			if _, ok := want[res]; !ok {
				t.Errorf("%s: the digest carries %s, which the mask refuses", name, res)
			}
		}
	}
	// What the kind refuses of its own: the actions classed credential and access.
	kindRefuses := slices.Concat(collection.ActionsOfClass(accesstypes.ClassCredential), collection.ActionsOfClass(accesstypes.ClassAccess))

	// One named action.
	named := acting("one named action", `,"refuseActions":["ScrapShip"]`,
		"everything except access actions (still allowed: writing HangarPasses), credential actions and the action ScrapShip")
	if status, body := named.do(ctx, http.MethodPost, scrap, mule); status != http.StatusForbidden {
		t.Errorf("scrap-ship under a mask that refuses the action ScrapShip: status %d, want 403: %s", status, body)
	}
	assertDigest("one named action", digestOf(ctx, t, named, anvil), wantDigest(append([]accesstypes.Resource{"ScrapShip"}, kindRefuses...)...))

	// A class of the application's own.
	classed := acting("a class of the application's own", `,"refuseClasses":["irreversible"]`,
		"everything except access actions (still allowed: writing HangarPasses), credential actions and irreversible actions")
	if status, body := classed.do(ctx, http.MethodPost, scrap, mule); status != http.StatusForbidden {
		t.Errorf("scrap-ship under a mask that refuses the class irreversible: status %d, want 403: %s", status, body)
	}
	assertDigest("a class of the application's own", digestOf(ctx, t, classed, anvil), wantDigest(append([]accesstypes.Resource{"ScrapShip"}, kindRefuses...)...))

	// The same kind without either refusal is let through: what refused the two
	// sessions above is what their requests named.
	plain := acting("neither", "", actMaskWords)
	if status, answer := dryRun(ctx, plain, scrap, mule); status != http.StatusOK {
		t.Errorf("scrap-ship acting as the marshal with neither refusal, as a dry run: status %d, want 200: %s", status, answer)
	}
	assertDigest("neither", digestOf(ctx, t, plain, anvil), wantDigest(kindRefuses...))
}
