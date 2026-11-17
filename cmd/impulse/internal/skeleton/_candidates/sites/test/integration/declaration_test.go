package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	consoleapp "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/apps/console/app"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestDeclaration_whoMayEnterATenant proves the Tenant record's declaration end to end
// on both sites' served stacks, which share the auth and the roster. A tenant has one
// directory and a directory belongs to one tenant, and nobody types one in: the staff
// sign in by password, so the console's create is refused with how a directory is set,
// a request that names a method and a connection is refused, and the directory is set
// in code through the generated step a sign-in takes (setDirectory), which holds it to
// the rules: a second directory for a tenant, and the directory on another tenant, are
// each refused with what to do. A tenant's sign-in settings restrict its own door
// alone, and a directory closes its tenant to nobody until the tenant is directory
// only: north's leaves the member's password session where it was. Turning directory
// only on is refused unless the person is signed in through the tenant's directory
// themselves, and until the tenant has one, on the method and on the warning it
// answers first, which says what it would do; the staff sign in by password, so here
// nobody can, and the suite sets the flag in the table as a seed does. How a tenant
// admits its directory's people is the tenant's own to say as well: opened to them with
// a role, behind a warning, and made invite only again. North admitting its people through its
// directory only refuses the member's password session at reach time on the console
// and on the portal alike, at once, since both read the shared roster; north stays in
// the member's list marked closed, with the reason, and its list of its people says
// who it keeps out; a fresh password sign-in is refused naming the rule, since every
// tenant of the member is directory only; its directory is not removed while it is
// directory only; turning it off is plain; the administrator, a super administrator,
// keeps every tenant. Every edit leaves a change event.
func TestDeclaration_whoMayEnterATenant(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, s.console, adminUser)
	member := signedIn(ctx, t, s, s.console, memberUser)
	portalMember := signedIn(ctx, t, s, s.portal, memberUser)

	// Nobody types a directory in. The administrator is signed in with a password: the
	// session read reports no directory, the create that names none is refused with how
	// a directory is set, and one that types a method and a connection is refused as
	// naming fields the record does not take. Neither writes.
	if status, body := admin.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK || strings.Contains(string(body), `"directory"`) {
		t.Errorf("the administrator's password session: status %d: %s, want no directory reported", status, body)
	}
	fromSession := `[{"op":"add","path":"/tenants/` + north + `/tenant-directories","value":{}}]`
	status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(fromSession))
	if status != http.StatusConflict || !strings.Contains(string(body), "You are signed in with a password. A directory is set by signing in through it") {
		t.Errorf("setting north's directory from a password session: status %d, want 409 saying how a directory is set: %s", status, body)
	}
	typed := `[{"op":"add","path":"/tenants/` + north + `/tenant-directories","value":{"method":"google","connection":"north.example"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(typed)); status != http.StatusBadRequest {
		t.Errorf("typing a directory in: status %d, want 400: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 0 {
		t.Fatalf("north's directory after the refused calls = %+v, want none", rows)
	}

	// The Workspace becomes north's directory as a sign-in through it sets it. A
	// directory closes its tenant to nobody until the tenant is directory only: the
	// member, who came by password, keeps north as before.
	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "north.example"}
	if err := setDirectory(ctx, t, s, north, workspace); err != nil {
		t.Fatalf("setting north's directory: %v", err)
	}
	if domains := chart(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart with north's directory set = %v, want [%s]: north is not directory only", domains, north)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 || rows[0].Connection != "north.example" {
		t.Errorf("north's directory = %+v, want the one set", rows)
	}

	// A tenant has one directory, and a directory belongs to one tenant: the step
	// refuses north a second one, the same or another, refuses south the one north has,
	// and refuses a personal account as anybody's, each as a conflict that says what to
	// do.
	rules := []struct {
		name      string
		tenant    string
		directory signin.Directory
		want      string
	}{
		{"north's own directory again", north, workspace, "is already this tenant's directory"},
		{"a second directory for north", north, signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "other.example"}, "A tenant has one directory: remove it first to set another."},
		{"north's directory on south", south, workspace, "already has a tenant here, and a directory belongs to one tenant"},
		{"any Google account", south, signin.Directory{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection}, "A personal Google account is not an organization's directory"},
		{"a personal Microsoft account", south, signin.Directory{Method: sessioninfo.MethodAzure, Connection: signin.MicrosoftPersonalAccountsConnection}, "A personal Microsoft account is not an organization's directory"},
	}
	for _, rule := range rules {
		err := setDirectory(ctx, t, s, rule.tenant, rule.directory)
		if err == nil || !strings.Contains(httpio.Message(err), rule.want) {
			t.Errorf("%s: setting it answered %v, want a refusal saying %q", rule.name, err, rule.want)
		}
	}
	if rows := directories(ctx, t, admin, south); len(rows) != 0 {
		t.Errorf("south's directory after the refusals = %+v, want none", rows)
	}

	// Directory only. Turning it on is refused unless the person is signed in through
	// the tenant's directory themselves, on the warning a page asks first and on the method,
	// in the same words, which say what to do. The staff sign in by password, so here
	// nobody can turn it on until a directory method joins the staff auth. A tenant with
	// no directory has nothing to be only of. Nothing is written.
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
	// The global record shows the flag and never writes it: a patch of the record that
	// carries it is refused, the application's administrator's included.
	patchRecord := []byte(`[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":true}}]`)
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", patchRecord); status != http.StatusBadRequest {
		t.Errorf("patching the Tenants record's directoryOnly as the administrator: status %d, want 400, the record does not take the flag: %s", status, body)
	}
	notThroughIt := "Directory only is turned on by a person who is signed in through this tenant's directory, Google Workspace north.example: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through Google Workspace north.example and try again."
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
	// The member administers north and nothing else, so north's switch is theirs to read
	// and to turn, under the tenant's own grant: they read it off, and the rule answers
	// them as it answers the administrator, since they too signed in with a password.
	// South, where they hold nothing, does not exist for them. A caller the grant
	// refuses is the generated authorization suite's case.
	if status, got := state(member, north); status != http.StatusOK || got != `{"directoryOnly":false}` {
		t.Errorf("north's switch as the member, its administrator, reads it: status %d %s, want 200 off", status, got)
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
		return admin.doWith(ctx, http.MethodPost, "/api/tenants/"+north+"/open-tenant-to-directory", []byte(`{"role":"`+role+`"}`), headers)
	}
	inviteOnly := func(headers map[string]string) (int, []byte) {
		return admin.doWith(ctx, http.MethodPost, "/api/tenants/"+north+"/make-tenant-invite-only", []byte(`{}`), headers)
	}
	// The role is picked from the roles the caller may give in the tenant, and no other
	// (StaffTenantDirectoryRoles): the tenant's administrator gives every role of the
	// tenant, so the list offers each.
	if status, body := admin.do(ctx, http.MethodGet, "/api/tenants/"+north+"/staff-tenant-directory-roles", nil); status != http.StatusOK || !strings.Contains(string(body), `"name":"TenantAdministrator"`) {
		t.Errorf("listing the roles the tenant may be opened with: status %d: %s, want 200 with TenantAdministrator among them", status, body)
	}
	opens := "Anyone who signs in through " + "north.example" + " will be able to enter this tenant without an invitation and gets the role TenantAdministrator."
	if status, body := open("TenantAdministrator", map[string]string{"X-Warning": "true"}); status != http.StatusOK || !strings.Contains(string(body), opens) {
		t.Errorf("the warning of opening north to its directory: status %d: %s, want 200 saying %q", status, body, opens)
	}
	if status, body := open("Nobody", nil); status != http.StatusBadRequest || !strings.Contains(string(body), "is not a role of this tenant") {
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
	if domains := chart(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart after the refusals = %v, want [%s]: nothing was written", domains, north)
	}

	// North is made directory only in the table, as a seed makes it: the member loses
	// north on both sites at once, where it stays in their list marked closed, with the
	// reason, and a fresh password sign-in is refused naming the rule. The
	// administrator keeps every tenant.
	setDirectoryOnly(ctx, t, s, north, true)
	byPassword := map[string]string{north: "North admits only people who sign in through its own directory. You signed in with a password."}
	for _, site := range []struct {
		name    string
		browser *browser
	}{{"console", member}, {"portal", portalMember}} {
		if domains := chart(ctx, t, site.browser); len(domains) != 0 {
			t.Errorf("the member's chart on the %s once north is directory only = %v, want none", site.name, domains)
		}
		if closed := closedDomains(ctx, t, site.browser, "/api/user-domains"); !maps.Equal(closed, byPassword) {
			t.Errorf("the tenants the member's list on the %s marks closed = %v, want %v", site.name, closed, byPassword)
		}
		if status, body := site.browser.do(ctx, http.MethodGet, "/api/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
			t.Errorf("GET north announcements on the %s as the member once north is directory only: status %d, want 404: %s", site.name, status, body)
		}
	}
	if domains := chart(ctx, t, admin); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the administrator's chart = %v, want both: a super administrator reaches every tenant", domains)
	}
	// North's list of its people is made from who holds a role there, not from who can
	// sign in: the member stays in it, and while north is directory only their row says
	// they cannot enter; the administrator's, a super administrator, says nothing. The
	// warning a page asks before turning it on has nothing to turn on: it says so, and
	// names neither the directory nor the people.
	keptOut := "Has not signed in through this tenant's directory, so cannot enter while this tenant is directory only"
	people := tenantRows(ctx, t, admin, north, "staff-tenant-users")
	if got, _ := people[s.memberID]["cannotEnter"].(string); got != keptOut {
		t.Errorf("north's people: the member's row says %q, want %q", got, keptOut)
	}
	if row, listed := people[s.adminID]; !listed || row["cannotEnter"] != nil {
		t.Errorf("north's people: the administrator's row is listed = %v saying %v, want listed saying nothing", listed, row["cannotEnter"])
	}
	onAlready := "This tenant is directory only already: only people who sign in through its directory enter it. There is nothing to turn on."
	status, body = warns(admin, north)
	if status != http.StatusConflict || !strings.Contains(string(body), onAlready) || strings.Contains(string(body), memberUser) || strings.Contains(string(body), "north.example") {
		t.Errorf("asking what directory only would do to north while it is on: status %d: %s, want 409 saying %q and naming nobody", status, body, onAlready)
	}
	fresh := newBrowser(t, s, s.console)
	if status, body := fresh.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the refused login: status %d: %s", status, body)
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

	// A tenant that is directory only keeps its directory: nobody could enter it
	// without one, so the removal is refused, saying to turn directory only off first.
	current := directories(ctx, t, admin, north)
	if len(current) != 1 {
		t.Fatalf("north's directory = %+v, want the one set", current)
	}
	keep := `[{"op":"remove","path":"/tenants/` + north + `/tenant-directories/` + current[0].ID + `"}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(keep)); status != http.StatusConflict || !strings.Contains(string(body), "Turn directory only off first, then remove the directory") {
		t.Errorf("removing the directory of a tenant that is directory only: status %d, want 409 saying what to do first: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 {
		t.Errorf("north's directory after the refused removal = %+v, want it kept", rows)
	}

	// Turning it off is plain: the administrator does it on the console, by password.
	// The member signs in again, on the portal too with nothing closed, and the
	// directory is removed.
	if status, body := turn(admin, north, false); status != http.StatusOK {
		t.Fatalf("turning north's directory only off, signed in with a password: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, memberUser, adminPassword); status != http.StatusOK {
		t.Errorf("the member's password sign-in once north is not directory only: status %d, want 200: %s", status, body)
	}
	if domains := chart(ctx, t, portalMember); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's chart on the portal once north is not directory only = %v, want [%s]", domains, north)
	}
	if closed := closedDomains(ctx, t, portalMember, "/api/user-domains"); len(closed) != 0 {
		t.Errorf("the tenants the member's list on the portal marks closed once north is not directory only = %v, want none", closed)
	}

	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(keep)); status != http.StatusOK {
		t.Errorf("removing north's directory once it is not directory only: status %d: %s", status, body)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Tenants", "TenantDirectories")
	if events["Tenants"] != 1 || events["TenantDirectories"] < 2 {
		t.Errorf("change events = %v, want the one flag write the console made on Tenants, the refused ones leaving none, and the setting and the removal on TenantDirectories", events)
	}
}

// setDirectoryOnly writes a tenant's directory-only flag in the table, as a seed does,
// and reloads the roster as the method that turns it does after its commit. The
// tenant's method holds turning the flag on to a person who is signed in through the
// tenant's directory, which nobody on a site whose staff sign in by password is.
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
// write that sets a tenant's directory from a sign-in takes (consoleapp.SetTenantDirectory),
// and reloads the roster as the record's write paths do after their commit. It answers
// the step's refusal: the rules of a tenant's directory are held there, whoever calls.
func setDirectory(ctx context.Context, t *testing.T, s *served, tenant string, directory signin.Directory) error {
	t.Helper()

	id, err := impulse.UUIDFromString(tenant)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString(%s) error = %v", tenant, err)
	}
	err = resource.NewSpannerClient(s.db.Client).ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return consoleapp.SetTenantDirectory(ctx, txn, id, directory, "test")
	})
	if err != nil {
		return errors.Wrap(err, "resource.SpannerClient.ExecuteFunc()")
	}
	if err := s.tenants.Reload(ctx); err != nil {
		t.Fatalf("resource.TenantRoster.Reload() error = %v", err)
	}

	return nil
}

// signedIn returns a browser signed in on the site as the development login, through the
// password and the second factor every session carries (browser.signIn).
func signedIn(ctx context.Context, t *testing.T, s *served, site *httptest.Server, user string) *browser {
	t.Helper()

	b := newBrowser(t, s, site)
	b.signIn(ctx, user)

	return b
}

// chart reads the session's tenant list.
func chart(ctx context.Context, t *testing.T, b *browser) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/user-domains", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/user-domains: status %d: %s", status, body)
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

	status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/tenant-directories", nil)
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
