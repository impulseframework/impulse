//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/app"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestDeclaration_whoMayEnterATenant proves the Tenant record's declaration end to end
// on the served stack, over both auths. A tenant has one directory and a directory
// belongs to one tenant: the development seed makes the members auth's directory
// north's. A tenant's sign-in settings restrict its own door alone, so the portal's
// member, who came through that directory and is seated in north, reaches north with
// the directory north's and with it nobody's, by a standing session and by a fresh
// sign-in alike: a directory closes its tenant to nobody until the tenant is directory
// only. Nobody types a directory in: the console's staff sign in by
// password, so the console's create is refused with how a directory is set, a request
// that names a method and a connection is refused, and the directory is set in code
// through the generated step a sign-in takes (setDirectory), which holds it to the
// rules: a second directory for a tenant, and the directory on another tenant, are each
// refused with what to do. Turning directory only on is refused unless the person is
// signed in through the tenant's directory themselves, and until the tenant has one, on
// the method and on the warning it answers first, which says what it would do; the
// console's staff sign in by password, so here nobody can, and the suite sets the flag
// in the table as a seed does. How a tenant admits its directory's people is the
// tenant's own to say as well: opened to them with a role, behind a warning, and made
// invite only again. A tenant whose people enter through its directory only admits a
// sign-in through that directory and, of its own administrators alone, a password with
// the code sent to their email: the staff member, north's administrator by a role held
// in north itself, keeps north with their password and code, by the session they have
// and by a fresh sign-in, which is the tenant's way back in when its directory has a
// problem, and the directory session keeps the tenant; the support login, one of the
// application's own team, who holds the same role in every tenant, is refused at reach
// time, which is what the switch is for: the tenant stays in their list marked closed,
// with the reason, and its list of its people says who it keeps out; its directory is
// not removed while it is directory only; turning it off is plain; the administrator, a
// super administrator, keeps every tenant. A write decides the next request, since the write
// paths reload the roster after their commit, and every edit leaves a change event. The
// portal's simulated sign-in sets a process variable, so the test does not run in
// parallel.
func TestDeclaration_whoMayEnterATenant(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)

	admin := newBrowser(t, s, consoleAPI)
	admin.signIn(ctx, adminUser)

	// The member, seated in north, reaches it: they came through the seeded directory,
	// north's own.
	client := newBrowser(t, s, portalAPI)
	if status, body := client.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET portal session before login: status %d: %s", status, body)
	}
	if status, location := client.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("the member's portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}
	if domains := chart(ctx, t, client, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart with the directory north's = %v, want [%s]", domains, north)
	}
	seeded := directories(ctx, t, admin, north)
	if len(seeded) != 1 || seeded[0].Method != string(sessioninfo.MethodAzure) {
		t.Fatalf("north's directory = %+v, want the seeded azure directory alone", seeded)
	}
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: seeded[0].Connection}
	if rows := directories(ctx, t, admin, south); len(rows) != 0 {
		t.Fatalf("south's directory = %+v, want none: a directory belongs to one tenant", rows)
	}

	// A tenant has one directory, and a directory belongs to one tenant: the step that
	// sets a directory from a sign-in refuses north a second one, the same or another,
	// and refuses south the one north has, each as a conflict that says what to do.
	rules := []struct {
		name      string
		tenant    string
		directory signin.Directory
		want      string
	}{
		{"north's own directory again", north, entra, "is already this tenant's directory"},
		{"a second directory for north", north, signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "north.example"}, "A tenant has one directory: remove it first to set another."},
		{"north's directory on south", south, entra, "already has a tenant here, and a directory belongs to one tenant"},
		{"a personal Microsoft account", south, signin.Directory{Method: sessioninfo.MethodAzure, Connection: signin.MicrosoftPersonalAccountsConnection}, "A personal Microsoft account is not an organization's directory"},
		{"any Google account", south, signin.Directory{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection}, "A personal Google account is not an organization's directory"},
	}
	for _, rule := range rules {
		err := setDirectory(ctx, t, s, rule.tenant, rule.directory)
		if err == nil || httpio.Message(err) == "" || !strings.Contains(httpio.Message(err), rule.want) {
			t.Errorf("%s: setting it answered %v, want a refusal saying %q", rule.name, err, rule.want)
		}
	}
	if rows := directories(ctx, t, admin, south); len(rows) != 0 {
		t.Errorf("south's directory after the refusals = %+v, want none", rows)
	}

	// The administrator removes north's directory on the console. A tenant's sign-in
	// settings restrict its own door alone, and north, which is not directory only,
	// admits everyone it seats however they signed in: the member's next request still
	// stands in north, and a fresh sign-in through the directory, now no tenant's, is
	// admitted and stands there too.
	remove := `[{"op":"remove","path":"/tenants/` + north + `/tenant-directories/` + seeded[0].ID + `"}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(remove)); status != http.StatusOK {
		t.Fatalf("removing north's directory: status %d: %s", status, body)
	}
	if domains := chart(ctx, t, client, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's chart once north's directory is removed = %v, want [%s]: north is not directory only", domains, north)
	}
	if status, body := client.do(ctx, http.MethodGet, portalAPI+"/tenants/"+north+"/announcements", nil); status != http.StatusOK {
		t.Errorf("GET north announcements as the member once the directory is removed: status %d, want 200: %s", status, body)
	}
	again := newBrowser(t, s, portalAPI)
	if status, body := again.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET portal session before the second login: status %d: %s", status, body)
	}
	if status, location := again.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Errorf("a directory sign-in with the directory no tenant's: status %d to %q, want %d to %q: a tenant that is not directory only admits it", status, location, http.StatusFound, portalPage)
	}
	if domains := chart(ctx, t, again, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the chart of the member's fresh sign-in with the directory no tenant's = %v, want [%s]", domains, north)
	}

	// Nobody types a directory in. The console's administrator is signed in with a
	// password: the session read reports no directory, the create that names none is
	// refused with how a directory is set, and one that types a method and a connection
	// is refused as naming fields the record does not take. Neither writes.
	if status, body := admin.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK || strings.Contains(string(body), `"directory"`) {
		t.Errorf("the administrator's password session: status %d: %s, want no directory reported", status, body)
	}
	fromSession := `[{"op":"add","path":"/tenants/` + north + `/tenant-directories","value":{}}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(fromSession)); status != http.StatusConflict || !strings.Contains(string(body), "You are signed in with a password. A directory is set by signing in through it") {
		t.Errorf("setting north's directory from a password session: status %d, want 409 saying how a directory is set: %s", status, body)
	}
	typed := `[{"op":"add","path":"/tenants/` + north + `/tenant-directories","value":{"method":"` + string(entra.Method) + `","connection":"` + entra.Connection + `"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(typed)); status != http.StatusBadRequest {
		t.Errorf("typing a directory in: status %d, want 400: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 0 {
		t.Fatalf("north's directory after the refused calls = %+v, want none", rows)
	}

	// Set again, as a sign-in through it sets it, the member's next request stands in
	// north as before.
	if err := setDirectory(ctx, t, s, north, entra); err != nil {
		t.Fatalf("setting north's directory again: %v", err)
	}
	if domains := chart(ctx, t, client, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's chart once the directory is north's again = %v, want [%s]", domains, north)
	}

	// Directory only. The staff member, signed in by password, is seated in north alone.
	member := newBrowser(t, s, consoleAPI)
	member.signIn(ctx, memberUser)
	if domains := chart(ctx, t, member, consoleAPI); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the staff member's chart before = %v, want [%s]", domains, north)
	}
	// The switch is the tenant's own: two methods and a read under the tenant, each
	// method to Execute on it there, which the tenant's administrator holds, and the
	// read under the grant of either.
	turn := func(b *browser, tenant string, on bool) (int, []byte) {
		method := "end-tenant-directory-only"
		if on {
			method = "make-tenant-directory-only"
		}

		return b.do(ctx, http.MethodPost, consoleAPI+"/tenants/"+tenant+"/"+method, []byte(`{}`))
	}
	// state reads the switch as it stands. What the read says turning it on takes of
	// the reader (its conditions) is left out here: the library's own tests hold it.
	state := func(b *browser, tenant string) (int, string) {
		status, body := b.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+tenant+"/sign-in-settings", nil)
		var read struct {
			DirectoryOnly *bool `json:"directoryOnly"`
		}
		if status != http.StatusOK || json.Unmarshal(body, &read) != nil || read.DirectoryOnly == nil {
			return status, strings.TrimSpace(string(body))
		}
		if *read.DirectoryOnly {
			return status, `{"directoryOnly":true}`
		}

		return status, `{"directoryOnly":false}`
	}
	// warns asks the method that turns the switch on for its warning: what the call
	// would do, in the server's words, with nothing saved.
	warns := func(b *browser, tenant string) (int, []byte) {
		return b.doWith(ctx, http.MethodPost, consoleAPI+"/tenants/"+tenant+"/make-tenant-directory-only", []byte(`{}`), map[string]string{"X-Warning": "true"})
	}
	// The global record shows the flag and never writes it: a patch of the record that
	// carries it is refused, the application's administrator's included.
	patchRecord := []byte(`[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":true}}]`)
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", patchRecord); status != http.StatusBadRequest {
		t.Errorf("patching the Tenants record's directoryOnly as the administrator: status %d, want 400, the record does not take the flag: %s", status, body)
	}

	// Turning it on is refused unless the person is signed in through the tenant's
	// directory themselves, on the warning a page asks first and on the method, in the same
	// words, which say what to do. The console's staff sign in by password, so here
	// nobody can turn it on until a directory method joins the staff auth. A tenant with
	// no directory has nothing to be only of. Nothing is written.
	// The sentence says the directory in words a person knows: the registration lists its
	// tenant by its id alone, which names nothing, so the directory is said by the label
	// of the button the portal's members sign in to it with, and never by the tenant's id.
	const itsDirectory = "your organization's Microsoft directory"
	notThroughIt := "Directory only is turned on by a person who is signed in through this tenant's directory, " + itsDirectory + ": that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through " + itsDirectory + " and try again."
	if status, body := warns(admin, north); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("asking what directory only would do to north, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(admin, north, true); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("turning north's directory only on, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	noDirectory := "This tenant has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this tenant's directory, then turn directory only on."
	if status, body := warns(admin, south); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("asking what directory only would do to south, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	if status, body := turn(admin, south, true); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("turning directory only on for south, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	// The staff member administers north and nothing else, so north's switch is theirs
	// to read and to turn, under the tenant's own grant: they read it off, and the rule
	// answers them as it answers the administrator, since they too signed in with a
	// password. South, where they hold nothing, does not exist for them. A caller the
	// grant refuses is the generated authorization suite's case.
	if status, got := state(member, north); status != http.StatusOK || got != `{"directoryOnly":false}` {
		t.Errorf("north's switch as the staff member, its administrator, reads it: status %d %s, want 200 off", status, got)
	}
	if status, body := warns(member, north); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("asking what directory only would do as the staff member, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(member, north, true); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("turning north's directory only on as the staff member, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(member, south, true); status != http.StatusNotFound {
		t.Errorf("turning south's directory only on as the staff member, who holds nothing in south: status %d, want 404: %s", status, body)
	}
	if status, got := state(member, south); status != http.StatusNotFound {
		t.Errorf("reading south's switch as the staff member, who holds nothing in south: status %d, want 404: %s", status, got)
	}
	// The administrator reads the switch in the tenant.
	if status, got := state(admin, north); status != http.StatusOK || got != `{"directoryOnly":false}` {
		t.Errorf("north's switch as the administrator reads it: status %d %s, want 200 off", status, got)
	}

	// How north admits its directory's people is the tenant's own to say too, with two
	// methods the tenant's administrator holds. Opening it names the role each of them
	// is given at their first sign-in through the directory: asked for its warning
	// first, the method says who will enter and with what role, and saves nothing; a
	// role that is not one of the tenant's is refused. Making it invite only again is
	// plain, and declares no warning.
	open := func(role string, headers map[string]string) (int, []byte) {
		return admin.doWith(ctx, http.MethodPost, consoleAPI+"/tenants/"+north+"/open-tenant-to-directory", []byte(`{"role":"`+role+`"}`), headers)
	}
	inviteOnly := func(headers map[string]string) (int, []byte) {
		return admin.doWith(ctx, http.MethodPost, consoleAPI+"/tenants/"+north+"/make-tenant-invite-only", []byte(`{}`), headers)
	}
	// The role is picked from the roles the caller may give in the tenant, and no other
	// (StaffTenantDirectoryRoles): the tenant's administrator gives every role of the
	// tenant, so the list offers each.
	if status, body := admin.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+north+"/staff-tenant-directory-roles", nil); status != http.StatusOK || !strings.Contains(string(body), `"name":"TenantAdministrator"`) {
		t.Errorf("listing the roles the tenant may be opened with: status %d: %s, want 200 with TenantAdministrator among them", status, body)
	}
	opens := "Anyone who signs in through " + itsDirectory + " will be able to enter this tenant without an invitation and gets the role Tenant administrator."
	if status, body := open("TenantAdministrator", map[string]string{"X-Warning": "true"}); status != http.StatusOK || !strings.Contains(string(body), opens) || strings.Contains(string(body), entra.Connection) {
		t.Errorf("the warning of opening north to its directory: status %d: %s, want 200 saying %q and never the tenant's id", status, body, opens)
	}
	if status, body := open("Nobody", nil); status != http.StatusBadRequest || !strings.Contains(string(body), "Nobody is not a role of this tenant. Choose one of the roles the picker lists.") {
		t.Errorf("opening north with a role it does not have: status %d, want 400 saying so: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 || rows[0].DefaultSeat != nil {
		t.Fatalf("north's directory after the warning and the refusal = %+v, want the one row naming no role: nothing was saved", rows)
	}
	if status, body := open("TenantAdministrator", nil); status != http.StatusOK {
		t.Fatalf("opening north to its directory's people: status %d: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 || rows[0].DefaultSeat == nil || *rows[0].DefaultSeat != "TenantAdministrator" {
		t.Errorf("north's directory once it is opened = %+v, want the one row naming TenantAdministrator", rows)
	}
	if status, body := inviteOnly(map[string]string{"X-Warning": "true"}); status != http.StatusBadRequest || !strings.Contains(string(body), "declares no warning") {
		t.Errorf("asking the method that makes north invite only for a warning: status %d, want 400 saying it declares none: %s", status, body)
	}
	if status, body := inviteOnly(nil); status != http.StatusOK {
		t.Fatalf("making north invite only: status %d: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 || rows[0].DefaultSeat != nil {
		t.Errorf("north's directory once it is invite only = %+v, want the one row naming no role", rows)
	}
	if domains := chart(ctx, t, member, consoleAPI); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the staff member's chart after the refusals = %v, want [%s]: nothing was written", domains, north)
	}

	// The support login is one of the application's own team: it holds the tenant's
	// administrator role in every tenant, as the first administrator does, and no Super
	// Administrator role, and signs in by password as the staff member does.
	supportID := teamLogin(ctx, t, s, supportUser)
	support := newBrowser(t, s, consoleAPI)
	support.signIn(ctx, supportUser)
	if domains := chart(ctx, t, support, consoleAPI); !slices.Equal(domains, []string{north, south}) {
		t.Fatalf("the support login's chart before = %v, want both tenants: the role is held in every one", domains)
	}

	// North is made directory only in the table, as a seed makes it. The support login
	// loses north at once: it answers as a tenant that does not exist, and stays in
	// their list marked closed, with the reason, while south, which is not directory
	// only, still admits them. The staff member keeps north: they hold the
	// administrator's role in north itself and signed in with a password and the code,
	// and a fresh password sign-in of theirs is held for the code, reaches nothing
	// before it and enters north after it. The administrator, a super administrator,
	// keeps every tenant, and the portal's member, who came through the directory,
	// keeps north.
	setDirectoryOnly(ctx, t, s, north, true)
	if domains := chart(ctx, t, support, consoleAPI); !slices.Equal(domains, []string{south}) {
		t.Errorf("the support login's chart once north is directory only = %v, want [%s]: a role held in every tenant is no way into a directory-only one", domains, south)
	}
	byPassword := map[string]string{north: "North admits only people who sign in through its own directory. You signed in with a password."}
	if closed := closedDomains(ctx, t, support, consoleAPI+"/user-domains"); !maps.Equal(closed, byPassword) {
		t.Errorf("the tenants the support login's list marks closed = %v, want %v", closed, byPassword)
	}
	if status, body := support.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
		t.Errorf("GET north announcements as the support login once north is directory only: status %d, want 404: %s", status, body)
	}
	if domains := chart(ctx, t, member, consoleAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the staff member's chart once north is directory only = %v, want [%s]: north admits its own administrator by password and code", domains, north)
	}
	if closed := closedDomains(ctx, t, member, consoleAPI+"/user-domains"); len(closed) != 0 {
		t.Errorf("the tenants the staff member's list marks closed = %v, want none", closed)
	}
	if status, body := member.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+north+"/announcements", nil); status != http.StatusOK {
		t.Errorf("GET north announcements as the staff member once north is directory only: status %d, want 200: %s", status, body)
	}
	if domains := chart(ctx, t, admin, consoleAPI); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the administrator's chart = %v, want both: a super administrator reaches every tenant", domains)
	}
	if domains := chart(ctx, t, client, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the portal member's chart once north is directory only = %v, want [%s]: the directory session is admitted", domains, north)
	}
	if closed := closedDomains(ctx, t, client, portalAPI+"/user-domains"); len(closed) != 0 {
		t.Errorf("the tenants the portal member's list marks closed = %v, want none", closed)
	}
	fresh := newBrowser(t, s, consoleAPI)
	if status, body := fresh.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET console session before the staff member's login: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, memberUser, adminPassword); status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("the staff member's password sign-in with every tenant of theirs directory only: status %d: %s, want 200 holding the sign-in for the code, since they administer north", status, body)
	}
	if status, body := fresh.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+north+"/announcements", nil); status != http.StatusUnauthorized {
		t.Errorf("GET north announcements with the staff member's password and without the code: status %d, want 401: %s", status, body)
	}
	fresh.secondFactor(ctx, memberUser)
	if domains := chart(ctx, t, fresh, consoleAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the staff member's chart after a fresh sign-in with the password and the code = %v, want [%s]", domains, north)
	}

	// North's list of its people keeps everyone who holds a role there, whether or not
	// they can sign in: the support login stays in it, and while north is directory
	// only their row says they cannot enter; the staff member's, north's own
	// administrator who has a password, and the administrator's, a super administrator,
	// say nothing. The warning a page asks before turning it on has nothing to turn on:
	// it says so, and names neither the directory nor the people.
	keptOut := "Has not signed in through this tenant's directory, so cannot enter while this tenant is directory only"
	people := tenantRows(ctx, t, admin, north, "staff-tenant-users")
	if got, _ := people[supportID]["cannotEnter"].(string); got != keptOut {
		t.Errorf("north's people: the support login's row says %q, want %q", got, keptOut)
	}
	if got := people[supportID]["outsideDirectory"]; got != true {
		t.Errorf("north's people: the support login's row marks them outside the directory = %v, want true: the mark their sentence is said from", got)
	}
	for _, id := range []string{s.adminID, s.memberID} {
		if row, listed := people[id]; !listed || row["cannotEnter"] != nil {
			t.Errorf("north's people: the row of %s is listed = %v saying %v, want listed saying nothing", id, listed, row["cannotEnter"])
		}
	}
	onAlready := "This tenant is directory only already: only people who sign in through its directory enter it. There is nothing to turn on."
	status, body := warns(admin, north)
	if status != http.StatusConflict || !strings.Contains(string(body), onAlready) || strings.Contains(string(body), memberUser) || strings.Contains(string(body), entra.Connection) {
		t.Errorf("asking what directory only would do to north while it is on: status %d: %s, want 409 saying %q and naming nobody", status, body, onAlready)
	}

	// A tenant that is directory only keeps its directory: nobody could enter it
	// without one, so the removal is refused, saying to turn directory only off first.
	current := directories(ctx, t, admin, north)
	if len(current) != 1 {
		t.Fatalf("north's directory = %+v, want the one set again", current)
	}
	keep := `[{"op":"remove","path":"/tenants/` + north + `/tenant-directories/` + current[0].ID + `"}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(keep)); status != http.StatusConflict || !strings.Contains(string(body), "Turn directory only off first, then remove the directory") {
		t.Errorf("removing the directory of a tenant that is directory only: status %d, want 409 saying what to do first: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 {
		t.Errorf("north's directory after the refused removal = %+v, want it kept", rows)
	}

	// Turning it off is plain: the administrator does it on the console, by password.
	// The support login enters north again at its next request, and nothing in their
	// list is closed.
	if status, body := turn(admin, north, false); status != http.StatusOK {
		t.Fatalf("turning north's directory only off, signed in with a password: status %d: %s", status, body)
	}
	if domains := chart(ctx, t, support, consoleAPI); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the support login's chart once north is not directory only = %v, want both tenants", domains)
	}
	if closed := closedDomains(ctx, t, support, consoleAPI+"/user-domains"); len(closed) != 0 {
		t.Errorf("the tenants the support login's list marks closed once north is not directory only = %v, want none", closed)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Tenants", "TenantDirectories")
	if events["Tenants"] != 1 || events["TenantDirectories"] < 2 {
		t.Errorf("change events = %v, want the one flag write the console made on Tenants, the refused ones leaving none, and the removal and the setting on TenantDirectories", events)
	}
}

// supportUser is the sign-in name of a login of the application's own team, which the
// suite creates beside the development ones.
const supportUser = "support"

// teamLogin creates a password login of the application's own team and answers the id of
// its account: it holds the tenant's administrator role in every tenant, as the first
// administrator does, and no Super Administrator role. A tenant's directory-only switch
// keeps such a person out with everyone else.
func teamLogin(ctx context.Context, t *testing.T, s *served, username string) string {
	t.Helper()

	password := adminPassword
	id, err := s.staffAuth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: username, Password: &password})
	if err != nil {
		t.Fatalf("CreateSessionUser(%s) error = %v", username, err)
	}
	user := accesstypes.User(id.String())
	if err := s.access.UserManager().AddUserRoles(ctx, accesstypes.EveryDomainPolicyScope(), user, "TenantAdministrator"); err != nil {
		t.Fatalf("AddUserRoles(%s, every tenant) error = %v", username, err)
	}
	waitForDomains(ctx, t, s.access, user, []accesstypes.Domain{north, south})

	return id.String()
}

// setDirectoryOnly writes a tenant's directory-only flag in the table, as a seed does,
// and reloads the roster as the method that turns it does after its commit. The
// tenant's method holds turning the flag on to a person who is signed in through the
// tenant's directory, which nobody on a console whose staff sign in by password is.
func setDirectoryOnly(ctx context.Context, t *testing.T, s *served, tenant string, on bool) {
	t.Helper()

	if _, err := s.db.Apply(ctx, []*cloudspanner.Mutation{cloudspanner.Update("Tenants", []string{"Id", "DirectoryOnly"}, []any{tenant, on})}); err != nil {
		t.Fatalf("writing %s's directory-only flag: %v", tenant, err)
	}
	if err := s.tenants.Reload(ctx); err != nil {
		t.Fatalf("resource.TenantRoster.Reload() error = %v", err)
	}
}

// setDirectory makes directory the tenant's, in code, through the generated step every
// write that sets a tenant's directory from a sign-in takes (app.SetTenantDirectory),
// and reloads the roster as the record's write paths do after their commit. It answers
// the step's refusal: the rules of a tenant's directory are held there, whoever calls.
func setDirectory(ctx context.Context, t *testing.T, s *served, tenant string, directory signin.Directory) error {
	t.Helper()

	id, err := impulse.UUIDFromString(tenant)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString(%s) error = %v", tenant, err)
	}
	err = resource.NewSpannerClient(s.db.Client).ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return app.SetTenantDirectory(ctx, txn, id, directory, "test")
	})
	if err != nil {
		return errors.Wrap(err, "resource.SpannerClient.ExecuteFunc()")
	}
	if err := s.tenants.Reload(ctx); err != nil {
		t.Fatalf("resource.TenantRoster.Reload() error = %v", err)
	}

	return nil
}

// chart reads the session's tenant list under the outlet's prefix.
func chart(ctx context.Context, t *testing.T, b *browser, prefix string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, prefix+"/user-domains", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/user-domains: status %d: %s", prefix, status, body)
	}
	var domains []string
	if err := unmarshalDomains(body, &domains); err != nil {
		t.Fatalf("user-domains is not a list: %v: %s", err, body)
	}

	return domains
}

// tenantDirectory is a row of the directories record as the console lists it.
type tenantDirectory struct {
	ID          string  `json:"id"`
	Method      string  `json:"method"`
	Connection  string  `json:"connection"`
	DefaultSeat *string `json:"defaultSeat"`
}

// directories lists the tenant's rows of the directories record on the console: one at most.
func directories(ctx context.Context, t *testing.T, b *browser, tenant string) []tenantDirectory {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+tenant+"/tenant-directories", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's directories: status %d: %s", tenant, status, body)
	}
	var rows []tenantDirectory
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the directories are not a list: %v: %s", err, body)
	}

	return rows
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
