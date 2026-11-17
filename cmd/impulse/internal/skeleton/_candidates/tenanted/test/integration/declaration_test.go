//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestDeclaration_whoMayEnterATenant proves the Tenant record's declaration end to end
// on the served stack. A tenant has one directory and a directory belongs to one
// tenant, and nobody types one in: an administrator of the tenant who is signed in
// through a Workspace makes it the tenant's directory with a create that names no
// directory, which the server sets from the session; one signed in with a password is
// told by the session read that they came through no directory, and their call is
// refused, as is a request that types a method and a connection. A second directory
// for the tenant, and the directory on another tenant, are each refused with a sentence
// that says what to do. A tenant's sign-in settings restrict its own door alone: a
// tenant that is not directory only admits everyone it seats, however they signed in,
// with or without a directory, and a tenant that is directory only admits the people
// who sign in through its own directory and nobody else, at reach time and, when every
// tenant of the person is such, at sign-in; a tenant the person is seated in and cannot
// enter stays in their tenant list marked closed, with the reason, and the tenant's
// list of its people says who it keeps out. Turning directory only on is refused until
// the tenant has its directory, and unless the person is signed in through that
// directory themselves, on the method and on the warning it answers first, which says
// what it would do, the sentence and the people it names; turning it off is plain and
// warns of nothing; its directory is not removed while it is directory only.
//
// The directory's row carries a default role, given at a person's first sign-in through
// the directory: with none, a stranger is refused by invitation only and no account is
// created; with a role named, a stranger's next sign-in creates them seated with it,
// and a person whose account stood before is given it at the sign-in that links the
// account to the directory. The tenant is opened to its directory's people, with the
// role, and made invite only again by the tenant's own two methods, the first behind a
// warning that says who will enter and with what role; the record's own update never
// writes the role. A write through the API decides the next request,
// since the records' write paths reload the roster after their commit; the
// administrator, a super administrator, keeps every tenant; and every edit leaves a
// change event, a refused one none. Under the session library's skipAuth build tag Google is simulated:
// the callback signs in whoever APP_GOOGLE_USERNAME names, as a Workspace account whose
// connection is the simulated Workspace's domain. The test sets that variable, so it
// does not run in parallel.
func TestDeclaration_whoMayEnterATenant(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)

	// The administrator opens the Google method to sign-up, so a person with no account
	// gets one at their first sign-in through a Workspace no tenant has made its
	// directory. An account created ahead with no password would not do here: nothing
	// but its name would tie it to the sign-in, and a tenanted application links such an
	// account only through the directory of the one tenant it belongs to, or with its
	// own invitation (the veteran below).
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"google"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up for the Google method: status %d, want 204: %s", status, body)
	}

	// A person who signed in through Google, seated in south. South has no directory
	// and is not directory only, so it admits them however they signed in: a tenant's
	// sign-in settings restrict its own door, and south sets none.
	t.Setenv("APP_GOOGLE_USERNAME", "traveler")
	traveler := signedInThroughGoogle(ctx, t, s)
	seat(ctx, t, s, "traveler", south)
	if domains := userDomains(ctx, t, traveler); !slices.Equal(domains, []string{south}) {
		t.Fatalf("the traveler's chart before south has a directory = %v, want [%s]: a tenant that sets nothing admits everyone it seats", domains, south)
	}
	if status, body := traveler.do(ctx, http.MethodGet, "/api/tenants/"+south+"/announcements", nil); status != http.StatusOK {
		t.Fatalf("GET south announcements as the traveler before south has a directory: status %d, want 200: %s", status, body)
	}

	// The session read says which organization's directory a person is signed in
	// through, which is what the Sign-in page offers to make the tenant's: the
	// Workspace for the traveler, none for the administrator, who came by password.
	workspace := sessionDirectory(ctx, t, traveler)
	if workspace == nil || workspace.Method != string(sessioninfo.MethodGoogle) || workspace.Connection == "" {
		t.Fatalf("the traveler's session reports the directory %+v, want the Workspace the sign-in came through", workspace)
	}
	if directory := sessionDirectory(ctx, t, admin); directory != nil {
		t.Fatalf("the administrator's password session reports the directory %+v, want none", directory)
	}

	// A directory is never typed in. The administrator, signed in with a password, is
	// refused with what to do instead, and a request that names a method and a
	// connection is refused as naming fields the record does not take; neither writes.
	makeSouths := `[{"op":"add","path":"/tenants/` + south + `/tenant-directories","value":{}}]`
	status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(makeSouths))
	if status != http.StatusConflict || !strings.Contains(string(body), "You are signed in with a password. A directory is set by signing in through it") {
		t.Fatalf("setting south's directory from a password session: status %d, want 409 saying how a directory is set: %s", status, body)
	}
	typed := `[{"op":"add","path":"/tenants/` + south + `/tenant-directories","value":{"method":"google","connection":"typed.example"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(typed)); status != http.StatusBadRequest {
		t.Fatalf("typing a directory in: status %d, want 400: %s", status, body)
	}
	if rows := directories(ctx, t, admin, south); len(rows) != 0 {
		t.Fatalf("south's directory after the refused calls = %+v, want none", rows)
	}

	// The organizer is signed in through the same Workspace and administers both
	// tenants, which admit them as they admit anyone they seat: neither is directory
	// only. They set south's directory under the tenant's own Create grant.
	t.Setenv("APP_GOOGLE_USERNAME", "organizer")
	organizer := signedInThroughGoogle(ctx, t, s)
	seat(ctx, t, s, "organizer", north, south)
	if domains := userDomains(ctx, t, organizer); !slices.Equal(domains, []string{north, south}) {
		t.Fatalf("the organizer's chart = %v, want both tenants: neither is directory only", domains)
	}
	status, body = organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(makeSouths))
	if status != http.StatusOK {
		t.Fatalf("making the Workspace south's directory: status %d: %s", status, body)
	}
	var created struct {
		TenantDirectories []string `json:"tenantDirectories"`
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.TenantDirectories) != 1 {
		t.Fatalf("the answer = %s (err %v), want the new row's id", body, err)
	}
	// The row carries the directory the organizer's session came through, which the
	// request never named. The directory closes south to nobody: the traveler's chart
	// is as it was.
	if rows := directories(ctx, t, admin, south); len(rows) != 1 || rows[0].Method != workspace.Method || rows[0].Connection != workspace.Connection {
		t.Fatalf("south's directory = %+v, want the Workspace %+v the organizer is signed in through", rows, workspace)
	}
	if domains := userDomains(ctx, t, traveler); !slices.Equal(domains, []string{south}) {
		t.Errorf("the traveler's chart once the Workspace is south's directory = %v, want [%s]", domains, south)
	}
	if status, body := traveler.do(ctx, http.MethodGet, "/api/tenants/"+south+"/announcements", nil); status != http.StatusOK {
		t.Errorf("GET south announcements as the traveler once the Workspace is south's directory: status %d, want 200: %s", status, body)
	}

	// A tenant has one directory, and a directory belongs to one tenant: setting it
	// again on south, and setting it on north, are each refused with the rule and
	// what to do, and north is left with none.
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(makeSouths)); status != http.StatusConflict || !strings.Contains(string(body), "is already this tenant's directory") {
		t.Errorf("setting south's directory a second time: status %d, want 409 saying it is the tenant's already: %s", status, body)
	}
	makeNorths := `[{"op":"add","path":"/tenants/` + north + `/tenant-directories","value":{}}]`
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(makeNorths)); status != http.StatusConflict || !strings.Contains(string(body), "already has a tenant here, and a directory belongs to one tenant") {
		t.Errorf("setting south's directory on north: status %d, want 409 saying the organization has a tenant: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 0 {
		t.Errorf("north's directory after the refusal = %+v, want none", rows)
	}

	// Removed, the directory leaves south open as it was: the traveler's chart does
	// not change. Set again, it is a new row.
	row := "/tenants/" + south + "/tenant-directories/" + created.TenantDirectories[0]
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"`+row+`"}]`)); status != http.StatusOK {
		t.Fatalf("removing south's directory: status %d: %s", status, body)
	}
	if domains := userDomains(ctx, t, traveler); !slices.Equal(domains, []string{south}) {
		t.Errorf("the traveler's chart once south's directory is removed = %v, want [%s]", domains, south)
	}
	status, body = organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(makeSouths))
	if status != http.StatusOK {
		t.Fatalf("making the Workspace south's directory again: status %d: %s", status, body)
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.TenantDirectories) != 1 {
		t.Fatalf("the second answer = %s (err %v), want the new row's id", body, err)
	}
	row = "/tenants/" + south + "/tenant-directories/" + created.TenantDirectories[0]
	if domains := userDomains(ctx, t, traveler); !slices.Equal(domains, []string{south}) {
		t.Errorf("the traveler's chart once the directory is set again = %v, want [%s]", domains, south)
	}

	// The default role. The row names none, so a newcomer from the same Workspace, whom
	// nobody seated, is refused by invitation only: the callback sends the browser to
	// the login page with the code, no session opens, and no account is created; the
	// traveler, seated before, passes as before.
	t.Setenv("APP_GOOGLE_USERNAME", "newcomer")
	newcomer := newBrowser(t, s)
	status, location := newcomer.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login as the newcomer: status %d, want %d to the directory", status, http.StatusFound)
	}
	if status, location = newcomer.redirect(ctx, location); status != http.StatusFound || !strings.HasSuffix(location, "/login?code="+string(sessioninfo.RefusedInvitationOnly)) {
		t.Fatalf("the newcomer's callback with no default role on the row: %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedInvitationOnly)
	}
	if status, body := newcomer.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK || !strings.Contains(string(body), `"authenticated":false`) {
		t.Errorf("GET /api/user/session after the refusal: status %d: %s, want no session", status, body)
	}
	if accountExists(ctx, t, s, "newcomer") {
		t.Errorf("an account was created for the refused newcomer, want none")
	}
	// The role is the tenant's own to name, with the method that opens the tenant to
	// its directory's people: the record's update does not take it, and a role that is
	// not one of the tenant's is refused. Asked for its warning first, the method says
	// who will enter and with what role, and saves nothing.
	nameSeat := `[{"op":"patch","path":"` + row + `","value":{"defaultSeat":"TenantAdministrator"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(nameSeat)); status != http.StatusBadRequest {
		t.Errorf("patching the row's default role: status %d, want 400, the record's update does not take it: %s", status, body)
	}
	open := func(b *browser, role string, headers map[string]string) (int, []byte) {
		return b.doWith(ctx, http.MethodPost, "/api/tenants/"+south+"/open-tenant-to-directory", []byte(`{"role":"`+role+`"}`), headers)
	}
	if status, body := open(admin, "Nobody", nil); status != http.StatusBadRequest || !strings.Contains(string(body), "is not a role of this tenant") {
		t.Errorf("opening south with a role it does not have: status %d, want 400 saying so: %s", status, body)
	}
	opens := "Anyone who signs in through " + workspace.Connection + " will be able to enter this tenant without an invitation and gets the role TenantAdministrator."
	if status, body := open(admin, "TenantAdministrator", map[string]string{"X-Warning": "true"}); status != http.StatusOK || !strings.Contains(string(body), opens) || !strings.Contains(string(body), "Open to people who sign in through "+workspace.Connection) {
		t.Errorf("the warning of opening south to its directory: status %d: %s, want 200 saying %q under what the button does", status, body, opens)
	}
	if seats := defaultSeats(ctx, t, admin, south); !slices.Equal(seats, []string{""}) {
		t.Fatalf("south's default role after the refusals and the warning = %q, want none: nothing was saved", seats)
	}
	// TenantAdministrator named on the row, the newcomer's next sign-in creates them
	// seated with it in south, where their chart stands at once.
	if status, body := open(admin, "TenantAdministrator", nil); status != http.StatusOK {
		t.Fatalf("opening south to its directory's people: status %d: %s", status, body)
	}
	if seats := defaultSeats(ctx, t, admin, south); !slices.Equal(seats, []string{"TenantAdministrator"}) {
		t.Fatalf("south's default role once it is opened = %q, want the role named", seats)
	}
	newcomer = signedInThroughGoogle(ctx, t, s)
	if domains := userDomains(ctx, t, newcomer); !slices.Equal(domains, []string{south}) {
		t.Errorf("the newcomer's chart after the seated sign-in = %v, want [%s]: the row's default role", domains, south)
	}
	if status, body := newcomer.do(ctx, http.MethodGet, "/api/tenants/"+south+"/announcements", nil); status != http.StatusOK {
		t.Errorf("GET south announcements as the newcomer: status %d, want 200: %s", status, body)
	}
	// An account that stood before, created with no password under the name the
	// directory presents, has never signed in: nothing but the name ties it to whoever
	// signs in under it, and a directory asserts the names it likes. Holding nothing in
	// south, it is not south's own, so south's directory vouches for nothing: the
	// sign-in is refused as a stranger's would be where south named no default role,
	// and nothing is linked or given.
	createAccount(ctx, t, s, "veteran")
	veteranID := accesstypes.User(accountID(ctx, t, s, "veteran"))
	t.Setenv("APP_GOOGLE_USERNAME", "veteran")
	claimed := newBrowser(t, s)
	status, location = claimed.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login as the veteran: status %d, want %d to the directory", status, http.StatusFound)
	}
	if status, location = claimed.redirect(ctx, location); status != http.StatusFound || !strings.HasSuffix(location, "/login?code="+string(sessioninfo.RefusedInvitationOnly)) {
		t.Fatalf("the callback of a sign-in under the name of an account that has never signed in: %d to %q, want %d to the login page naming %s, as a stranger is refused", status, location, http.StatusFound, sessioninfo.RefusedInvitationOnly)
	}
	if status, body := claimed.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK || !strings.Contains(string(body), `"authenticated":false`) {
		t.Errorf("GET /api/user/session after the veteran's refusal: status %d: %s, want no session", status, body)
	}
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); len(held) != 0 {
		t.Fatalf("the veteran's roles in south after the refused sign-in = %v, want none", held)
	}
	// South's administrator gives the account a role in south. It is south's alone now,
	// so south's directory vouches for it: the first sign-in through the directory links
	// the account, which keeps the role it holds, and the default role adds nothing
	// beside it. The second sign-in finds the link.
	seat(ctx, t, s, "veteran", south)
	veteran := signedInThroughGoogle(ctx, t, s)
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); !slices.Equal(held, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the veteran's roles in south after their first sign-in through its directory = %v, want the role they were given and nothing more", held)
	}
	if domains := userDomains(ctx, t, veteran); !slices.Equal(domains, []string{south}) {
		t.Errorf("the veteran's chart after their first sign-in through the directory = %v, want [%s]", domains, south)
	}
	signedInThroughGoogle(ctx, t, s)
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); !slices.Equal(held, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the veteran's roles in south after their second sign-in = %v, want the one role and nothing more", held)
	}
	// Made invite only, the row names no default role again. The method is plain: it
	// declares no warning, so a request for one is refused and changes nothing.
	inviteOnly := func(headers map[string]string) (int, []byte) {
		return admin.doWith(ctx, http.MethodPost, "/api/tenants/"+south+"/make-tenant-invite-only", []byte(`{}`), headers)
	}
	if status, body := inviteOnly(map[string]string{"X-Warning": "true"}); status != http.StatusBadRequest || !strings.Contains(string(body), "declares no warning") {
		t.Errorf("asking the method that makes south invite only for a warning: status %d, want 400 saying it declares none: %s", status, body)
	}
	if status, body := inviteOnly(nil); status != http.StatusOK {
		t.Fatalf("making south invite only: status %d: %s", status, body)
	}
	if seats := defaultSeats(ctx, t, admin, south); !slices.Equal(seats, []string{""}) {
		t.Errorf("south's default role once it is invite only = %q, want the one row naming none", seats)
	}

	// South's directory is removed, and the roles it gave stand. The Workspace is free
	// for another tenant, and the organizer makes it north's.
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"`+row+`"}]`)); status != http.StatusOK {
		t.Fatalf("removing south's directory: status %d: %s", status, body)
	}
	newcomerID := accesstypes.User(accountID(ctx, t, s, "newcomer"))
	if held := rolesHeld(ctx, t, s, newcomerID, accesstypes.DomainPolicyScope(south)); !slices.Equal(held, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the newcomer's roles in south once its directory is removed = %v, want the role it gave to stand", held)
	}
	status, body = organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(makeNorths))
	if status != http.StatusOK {
		t.Fatalf("making the Workspace north's directory: status %d: %s", status, body)
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.TenantDirectories) != 1 {
		t.Fatalf("the answer = %s (err %v), want the new row's id", body, err)
	}
	northRow := "/tenants/" + north + "/tenant-directories/" + created.TenantDirectories[0]

	// Directory only. The member, signed in by password, is seated in north alone.
	member := signedIn(ctx, t, s, memberUser)
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart before = %v, want [%s]", domains, north)
	}
	// The switch is the tenant's own: two methods and a read under the tenant, each
	// method to Execute on it there, which the tenant's administrator holds, and the
	// read under the grant of either.
	turn := func(b *browser, tenant string, on bool) (int, []byte) {
		method := "end-tenant-directory-only"
		if on {
			method = "make-tenant-directory-only"
		}

		return b.do(ctx, http.MethodPost, "/api/tenants/"+tenant+"/"+method, []byte(`{}`))
	}
	state := func(b *browser, tenant string) (int, string) {
		status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/directory-only", nil)

		return status, strings.TrimSpace(string(body))
	}
	// warns asks the method that turns the switch on for its warning: what the call
	// would do, in the server's words, with nothing saved.
	warns := func(b *browser, tenant string) (int, []byte) {
		return b.doWith(ctx, http.MethodPost, "/api/tenants/"+tenant+"/make-tenant-directory-only", []byte(`{}`), map[string]string{"X-Warning": "true"})
	}
	const on, off = `{"directoryOnly":true}`, `{"directoryOnly":false}`
	// The tenant's administrator reads the switch in the tenant: they do not read the
	// global record.
	if status, got := state(organizer, north); status != http.StatusOK || got != off {
		t.Errorf("north's switch as its administrator reads it: status %d %s, want 200 %s", status, got, off)
	}

	// The global record shows the flag and never writes it: a patch of the record that
	// carries it is refused, the application's administrator's included.
	patchRecord := []byte(`[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":true}}]`)
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", patchRecord); status != http.StatusBadRequest {
		t.Errorf("patching the Tenants record's directoryOnly as the administrator: status %d, want 400, the record does not take the flag: %s", status, body)
	}

	// Turning it on is refused unless the person is signed in through the tenant's
	// directory themselves: the administrator holds the grant and signed in with a
	// password. The warning a page asks first and the method are refused in the same
	// words, which say what to do, and nothing is written.
	notThroughIt := "Directory only is turned on by a person who is signed in through this tenant's directory, Google Workspace " + workspace.Connection + ": that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through Google Workspace " + workspace.Connection + " and try again."
	if status, body := warns(admin, north); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("asking what directory only would do to north, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(admin, north, true); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("turning north's directory only on, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	// A tenant with no directory has nothing to be only of.
	noDirectory := "This tenant has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this tenant's directory, then turn directory only on."
	if status, body := warns(organizer, south); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("asking what directory only would do to south, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	if status, body := turn(organizer, south, true); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("turning directory only on for south, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	// The member administers north and nothing else, so north's switch is theirs to read
	// and to turn, under the tenant's own grant: they read it off, and the rule answers
	// them as it answers the administrator, since they too signed in with a password.
	// South, where they hold nothing, does not exist for them. A caller the grant
	// refuses is the generated authorization suite's case.
	if status, got := state(member, north); status != http.StatusOK || got != off {
		t.Errorf("north's switch as the member, its administrator, reads it: status %d %s, want 200 %s", status, got, off)
	}
	if status, body := warns(member, north); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("asking what directory only would do as the member, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(member, north, true); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("turning north's directory only on as the member, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(member, south, true); status != http.StatusNotFound {
		t.Errorf("turning south's directory only on as the member, who holds nothing in south: status %d, want 404: %s", status, body)
	}
	if status, got := state(member, south); status != http.StatusNotFound {
		t.Errorf("reading south's switch as the member, who holds nothing in south: status %d, want 404: %s", status, got)
	}
	// Turning it off declares no warning, so a request for one is refused and never
	// runs the call; the route the switch was turned by before is gone.
	if status, body := organizer.doWith(ctx, http.MethodPost, "/api/tenants/"+north+"/end-tenant-directory-only", []byte(`{}`), map[string]string{"X-Warning": "true"}); status != http.StatusBadRequest || !strings.Contains(string(body), "declares no warning") {
		t.Errorf("asking the method that turns directory only off for a warning: status %d, want 400 saying it declares none: %s", status, body)
	}
	if status, body := organizer.do(ctx, http.MethodPost, "/api/tenants/"+north+"/set-tenant-directory-only", []byte(`{"directoryOnly":true}`)); status != http.StatusNotFound {
		t.Errorf("POST the retired set-tenant-directory-only: status %d, want 404: %s", status, body)
	}
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart after the refusals = %v, want [%s]: nothing was written", domains, north)
	}

	// The organizer administers north and is signed in through north's directory. The
	// method's warning says what turning it on would do, in the server's words: who
	// will be able to enter, how many people seated in north sign in another way, and
	// who they are, since a tenant's administrator lists its people: the member alone,
	// by name and sign-in name. The administrator, a super administrator, is not among
	// them, nor is anyone who signed in through the directory.
	var would struct {
		Consequence string   `json:"consequence"`
		Names       []string `json:"names"`
		Confirm     string   `json:"confirm"`
	}
	status, body = warns(organizer, north)
	if status != http.StatusOK {
		t.Fatalf("asking what directory only would do to north, signed in through its directory: status %d: %s", status, body)
	}
	if err := json.Unmarshal(body, &would); err != nil {
		t.Fatalf("the warning is not an object: %v: %s", err, body)
	}
	if want := "Admit only people who sign in through " + workspace.Connection; would.Confirm != want {
		t.Errorf("the button = %q, want %q: what it does", would.Confirm, want)
	}
	if want := "Only people who sign in through " + workspace.Connection + " will be able to enter this tenant. 1 person seated here signs in another way and will lose access."; would.Consequence != want {
		t.Errorf("the sentence = %q, want %q", would.Consequence, want)
	}
	if len(would.Names) != 1 || (would.Names[0] != memberUser && !strings.HasSuffix(would.Names[0], " ("+memberUser+")")) {
		t.Errorf("the people named = %q, want the member alone, by their sign-in name", would.Names)
	}
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart after the warning = %v, want [%s]: asking for it writes nothing", domains, north)
	}

	// The organizer turns it on, with the tenant's method. The member loses north at once: it answers as a tenant
	// that does not exist, and stays in their tenant list marked closed, with the
	// reason. A fresh password sign-in is refused naming the rule, since every tenant
	// of the member is directory only. The administrator, a super administrator, keeps
	// north, and the organizer, through its directory, enters it.
	if status, body := turn(organizer, north, true); status != http.StatusOK {
		t.Fatalf("turning north's directory only on as its administrator, signed in through its directory: status %d: %s", status, body)
	}
	if status, got := state(organizer, north); status != http.StatusOK || got != on {
		t.Errorf("north's switch once it is turned on: status %d %s, want 200 %s", status, got, on)
	}
	if domains := userDomains(ctx, t, member); len(domains) != 0 {
		t.Errorf("the member's chart once north is directory only = %v, want none", domains)
	}
	byPassword := map[string]string{north: "North admits only people who sign in through its own directory. You signed in with a password."}
	if closed := closedDomains(ctx, t, member, "/api/user-domains"); !maps.Equal(closed, byPassword) {
		t.Errorf("the tenants the member's list marks closed = %v, want %v", closed, byPassword)
	}
	if status, body := member.do(ctx, http.MethodGet, "/api/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
		t.Errorf("GET north announcements as the member once north is directory only: status %d, want 404: %s", status, body)
	}
	if domains := userDomains(ctx, t, admin); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the administrator's chart = %v, want both: a super administrator reaches every tenant", domains)
	}
	if domains := userDomains(ctx, t, organizer); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the organizer's chart = %v, want both: north through its directory, south however they signed in", domains)
	}
	fresh := newBrowser(t, s)
	if status, body := fresh.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before the refused login: status %d: %s", status, body)
	}
	status, body = fresh.login(ctx, memberUser, adminPassword)
	if status != http.StatusUnauthorized {
		t.Fatalf("the member's password sign-in with every tenant directory only: status %d, want 401: %s", status, body)
	}
	var refusal struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &refusal); err != nil || refusal.Code != string(sessioninfo.RefusedDirectoryOnly) {
		t.Errorf("the refusal's code = %q (err %v), want %q: %s", refusal.Code, err, sessioninfo.RefusedDirectoryOnly, body)
	}

	// North's list of its people is made from who holds a role there, not from who can
	// sign in: the member stays in it, and while north is directory only their row says
	// they cannot enter. The organizer's row, who signed in through the directory, and
	// the administrator's, a super administrator, say nothing.
	people := tenantRows(ctx, t, organizer, north, "staff-tenant-users")
	keptOut := "Has not signed in through " + workspace.Connection + ", so cannot enter while this tenant is directory only"
	if got, _ := people[s.memberID]["cannotEnter"].(string); got != keptOut {
		t.Errorf("north's people: the member's row says %q, want %q", got, keptOut)
	}
	for _, id := range []string{s.adminID, accountID(ctx, t, s, "organizer")} {
		if row, listed := people[id]; !listed || row["cannotEnter"] != nil {
			t.Errorf("north's people: the row of %s is listed = %v saying %v, want listed saying nothing", id, listed, row["cannotEnter"])
		}
	}
	// Turned on already, the warning a page asks first has nothing to turn on: it says so,
	// and names neither the directory nor the people, to the administrator by password
	// and to the organizer through the directory alike.
	onAlready := "This tenant is directory only already: only people who sign in through its directory enter it. There is nothing to turn on."
	for name, asks := range map[string]*browser{"the administrator, by password": admin, "the organizer, through the directory": organizer} {
		if status, body := warns(asks, north); status != http.StatusConflict || !strings.Contains(string(body), onAlready) || strings.Contains(string(body), memberUser) || strings.Contains(string(body), workspace.Connection) {
			t.Errorf("asking what directory only would do to north while it is on, as %s: status %d: %s, want 409 saying %q and naming nobody", name, status, body, onAlready)
		}
	}

	// A tenant that is directory only keeps its directory: its people enter through it
	// alone, so removing it is refused, saying to turn directory only off first.
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"`+northRow+`"}]`)); status != http.StatusConflict || !strings.Contains(string(body), "Turn directory only off first, then remove the directory") {
		t.Errorf("removing the directory of a tenant that is directory only: status %d, want 409 saying what to do first: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 {
		t.Errorf("north's directory after the refused removal = %+v, want it kept", rows)
	}

	// Turning it off is plain: the administrator does it by password. The member signs
	// in again, nothing in their list is closed, and no row of north's people says
	// anything.
	if status, body := turn(admin, north, false); status != http.StatusOK {
		t.Fatalf("turning north's directory only off, signed in with a password: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, memberUser, adminPassword); status != http.StatusOK {
		t.Errorf("the member's password sign-in once north is not directory only: status %d, want 200: %s", status, body)
	}
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's chart once north is not directory only = %v, want [%s]", domains, north)
	}
	if closed := closedDomains(ctx, t, member, "/api/user-domains"); len(closed) != 0 {
		t.Errorf("the tenants the member's list marks closed once north is not directory only = %v, want none", closed)
	}
	if got := tenantRows(ctx, t, organizer, north, "staff-tenant-users")[s.memberID]["cannotEnter"]; got != nil {
		t.Errorf("north's people once it is not directory only: the member's row says %v, want nothing", got)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Tenants", "TenantDirectories")
	if events["Tenants"] != 2 || events["TenantDirectories"] < 7 {
		t.Errorf("change events = %v, want the two flag writes on Tenants, the refused ones leaving none, and the directories' seven writes on TenantDirectories", events)
	}
}

// directory is a directory as the session read and the directories record name one.
type directory struct {
	Method     string `json:"method"`
	Connection string `json:"connection"`
}

// sessionDirectory reads the organization's directory the browser's session came
// through, as the session read reports it; nil when it reports none.
func sessionDirectory(ctx context.Context, t *testing.T, b *browser) *directory {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/user/session: status %d: %s", status, body)
	}
	var read struct {
		Directory *directory `json:"directory"`
	}
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatalf("the session read is not an object: %v: %s", err, body)
	}

	return read.Directory
}

// directories lists the tenant's rows of the directories record: one at most.
func directories(ctx context.Context, t *testing.T, b *browser, tenant string) []directory {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/tenant-directories", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's directory: status %d: %s", tenant, status, body)
	}
	var rows []directory
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the directories are not a list: %v: %s", err, body)
	}

	return rows
}

// defaultSeats lists the tenant's declared directories' default seats, in the record's
// order, an empty string for a row naming none.
func defaultSeats(ctx context.Context, t *testing.T, b *browser, tenant string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/tenant-directories", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's directories: status %d: %s", tenant, status, body)
	}
	var rows []struct {
		DefaultSeat *string `json:"defaultSeat"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the directories are not a list: %v: %s", err, body)
	}
	seats := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.DefaultSeat == nil {
			seats = append(seats, "")
			continue
		}
		seats = append(seats, *row.DefaultSeat)
	}

	return seats
}

// signedInThroughGoogle returns a browser signed in through the staff auth's google
// registration, simulated: the login route sends the browser to the callback, which
// signs in whoever APP_GOOGLE_USERNAME names. The account's Workspace is not one the
// deployment administers, so the callback sends the browser to the login page's code
// step (pending=mfa) and the sign-in code opens the session, as a person enters it.
func signedInThroughGoogle(ctx context.Context, t *testing.T, s *served) *browser {
	t.Helper()

	b := newBrowser(t, s)
	status, location := b.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login: status %d, want %d to the directory", status, http.StatusFound)
	}
	if status, location = b.redirect(ctx, location); status != http.StatusFound || location != "/login?pending=mfa&returnUrl="+url.QueryEscape("/dashboard") {
		t.Fatalf("the callback answered %d to %q, want %d to the login page's code step, returning to /dashboard", status, location, http.StatusFound)
	}
	b.secondFactor(ctx, os.Getenv("APP_GOOGLE_USERNAME"), false)

	return b
}

// createAccount creates an account under the name the directory presents, holding no
// password and no role: a person an administrator created ahead of their first sign-in.
// Nothing but the name ties such an account to a sign-in, so it is linked only through
// the directory of the one tenant it belongs to, or with its own invitation.
func createAccount(ctx context.Context, t *testing.T, s *served, username string) {
	t.Helper()

	if _, err := s.auth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: username}); err != nil {
		t.Fatalf("CreateSessionUser(%s) error = %v", username, err)
	}
}

// seat gives the person the tenant's administrator role in each tenant named, held by
// the id of their account, and waits for the engine to answer them.
func seat(ctx context.Context, t *testing.T, s *served, username string, tenants ...accesstypes.Domain) {
	t.Helper()

	user := accesstypes.User(accountID(ctx, t, s, username))
	for _, tenant := range tenants {
		if err := s.access.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(tenant), user, "TenantAdministrator"); err != nil {
			t.Fatalf("AddUserRoles(%s, %s) error = %v", username, tenant, err)
		}
	}
	slices.Sort(tenants)
	waitForDomains(ctx, t, s.access, user, tenants)
}

// accountID finds the id of the account the staff auth holds under the username, for a
// person a suite did not create itself (one a directory sign-in created): the id is how
// the permission store and the generated management name the person.
func accountID(ctx context.Context, t *testing.T, s *served, username string) string {
	t.Helper()

	user, err := s.auth.Session().API().UserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("UserByUsername(%s) error = %v", username, err)
	}

	return user.ID.String()
}

// changeEvents counts the change events written for each table.
func changeEvents(ctx context.Context, t *testing.T, s *served, tables ...string) map[string]int64 {
	t.Helper()

	counts := map[string]int64{}
	for _, table := range tables {
		iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{
			SQL:    "SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = @table",
			Params: map[string]any{"table": table},
		})
		row, err := iter.Next()
		if err != nil {
			t.Fatalf("counting change events of %s: %v", table, err)
		}
		var n int64
		if err := row.Columns(&n); err != nil {
			t.Fatalf("reading the count of %s: %v", table, err)
		}
		iter.Stop()
		counts[table] = n
	}

	return counts
}
