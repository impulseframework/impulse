package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
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
// each refused with what to do. The directory admits nobody yet, since nobody signs in
// through one: north's leaves the member's password session where it was. North
// admitting its people through its directory only refuses the member's password session
// at reach time on the console and on the portal alike, at once, since the console's
// write reloads the shared roster; a fresh password sign-in is refused naming the rule,
// since every tenant of the member is directory only; its directory is not removed
// while it is directory only; the administrator, a global role, keeps every tenant.
// Every edit leaves a change event.
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

	// The Workspace becomes north's directory as a sign-in through it sets it. It
	// admits the people who come through it; the member, who came by password, keeps
	// north as before.
	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "north.example"}
	if err := setDirectory(ctx, t, s, north, workspace); err != nil {
		t.Fatalf("setting north's directory: %v", err)
	}
	if domains := chart(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart with north's directory set = %v, want [%s]: a password is admitted", domains, north)
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

	// North admits its people through its directory only: the member loses north on
	// both sites at once, and a fresh password sign-in is refused naming the rule. The
	// administrator keeps every tenant.
	closeNorth := `[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":true}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(closeNorth)); status != http.StatusOK {
		t.Fatalf("setting north directory only: status %d: %s", status, body)
	}
	for _, site := range []struct {
		name    string
		browser *browser
	}{{"console", member}, {"portal", portalMember}} {
		if domains := chart(ctx, t, site.browser); len(domains) != 0 {
			t.Errorf("the member's chart on the %s once north is directory only = %v, want none", site.name, domains)
		}
		if status, body := site.browser.do(ctx, http.MethodGet, "/api/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
			t.Errorf("GET north announcements on the %s as the member once north is directory only: status %d, want 404: %s", site.name, status, body)
		}
	}
	if domains := chart(ctx, t, admin); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the administrator's chart = %v, want both: a global role reaches every tenant", domains)
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

	// Back to passwords: the member signs in again, on the portal too, and the
	// directory is removed.
	openNorth := `[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":false}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(openNorth)); status != http.StatusOK {
		t.Fatalf("setting north back to passwords: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, memberUser, adminPassword); status != http.StatusOK {
		t.Errorf("the member's password sign-in once north admits passwords again: status %d, want 200: %s", status, body)
	}
	if domains := chart(ctx, t, portalMember); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's chart on the portal once north admits passwords again = %v, want [%s]", domains, north)
	}

	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(keep)); status != http.StatusOK {
		t.Errorf("removing north's directory once it admits passwords again: status %d: %s", status, body)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Tenants", "TenantDirectories")
	if events["Tenants"] < 2 || events["TenantDirectories"] < 2 {
		t.Errorf("change events = %v, want the two flag writes on Tenants and the setting and the removal on TenantDirectories", events)
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
		return err
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
	ID         string `json:"id"`
	Method     string `json:"method"`
	Connection string `json:"connection"`
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
