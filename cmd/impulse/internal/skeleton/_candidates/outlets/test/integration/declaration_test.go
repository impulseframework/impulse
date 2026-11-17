//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/app"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestDeclaration_whoMayEnterATenant proves the Tenant record's declaration end to end
// on the served stack, over both auths. A tenant has one directory and a directory
// belongs to one tenant: the development seed makes the members auth's directory
// north's. The portal's member, who came through that directory, reaches north only
// while the directory is north's, and a fresh directory sign-in is refused naming the
// rule once it is nobody's. Nobody types a directory in: the console's staff sign in by
// password, so the console's create is refused with how a directory is set, a request
// that names a method and a connection is refused, and the directory is set in code
// through the generated step a sign-in takes (setDirectory), which holds it to the
// rules: a second directory for a tenant, and the directory on another tenant, are each
// refused with what to do. A tenant whose people enter through its directory only
// refuses a console password session at reach time and, when every tenant of the person
// is such, at sign-in, while the directory session keeps the tenant, and its directory
// is not removed while it is directory only; the administrator, who holds a global
// role, keeps every tenant. A write decides the next request, since the write paths
// reload the roster after their commit, and every edit leaves a change event. The
// portal's simulated sign-in sets a process variable, so the test does not run in
// parallel.
func TestDeclaration_whoMayEnterATenant(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)

	admin := newBrowser(t, s, consoleAPI)
	admin.signIn(ctx, adminUser)

	// The member reaches north through the seeded directory.
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

	// The administrator removes north's directory on the console: the member's next
	// request stands in no tenant, and a fresh sign-in through the directory is refused,
	// since it is no tenant's.
	remove := `[{"op":"remove","path":"/tenants/` + north + `/tenant-directories/` + seeded[0].ID + `"}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(remove)); status != http.StatusOK {
		t.Fatalf("removing north's directory: status %d: %s", status, body)
	}
	if domains := chart(ctx, t, client, portalAPI); len(domains) != 0 {
		t.Errorf("the member's chart once north's directory is removed = %v, want none", domains)
	}
	if status, body := client.do(ctx, http.MethodGet, portalAPI+"/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
		t.Errorf("GET north announcements as the member once the directory is removed: status %d, want 404: %s", status, body)
	}
	refused := newBrowser(t, s, portalAPI)
	if status, body := refused.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET portal session before the refused login: status %d: %s", status, body)
	}
	if status, location := refused.loginPortal(ctx, clientUser); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedConnectionNotInTenant)) {
		t.Errorf("a directory sign-in with the directory no tenant's: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedConnectionNotInTenant)
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
	// north.
	if err := setDirectory(ctx, t, s, north, entra); err != nil {
		t.Fatalf("setting north's directory again: %v", err)
	}
	if domains := chart(ctx, t, client, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's chart once the directory is north's again = %v, want [%s]", domains, north)
	}

	// North admits its people through its directory only: the staff member, signed in
	// by password and seated in north alone, loses north at once (not listed, answered as
	// a tenant that does not exist), and a fresh password sign-in is refused naming the
	// rule, since every tenant of theirs is directory only. The administrator, a global
	// role, keeps every tenant, and the portal's member, who came through the directory,
	// keeps north.
	member := newBrowser(t, s, consoleAPI)
	member.signIn(ctx, memberUser)
	if domains := chart(ctx, t, member, consoleAPI); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the staff member's chart before = %v, want [%s]", domains, north)
	}
	closeNorth := `[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":true}}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(closeNorth)); status != http.StatusOK {
		t.Fatalf("setting north directory only: status %d: %s", status, body)
	}
	if domains := chart(ctx, t, member, consoleAPI); len(domains) != 0 {
		t.Errorf("the staff member's chart once north is directory only = %v, want none", domains)
	}
	if status, body := member.do(ctx, http.MethodGet, consoleAPI+"/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
		t.Errorf("GET north announcements as the staff member once north is directory only: status %d, want 404: %s", status, body)
	}
	if domains := chart(ctx, t, admin, consoleAPI); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the administrator's chart = %v, want both: a global role reaches every tenant", domains)
	}
	if domains := chart(ctx, t, client, portalAPI); !slices.Equal(domains, []string{north}) {
		t.Errorf("the portal member's chart once north is directory only = %v, want [%s]: the directory session is admitted", domains, north)
	}
	fresh := newBrowser(t, s, consoleAPI)
	if status, body := fresh.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET console session before the refused login: status %d: %s", status, body)
	}
	status, body := fresh.login(ctx, memberUser, adminPassword)
	if status != http.StatusUnauthorized {
		t.Fatalf("the staff member's password sign-in with every tenant directory only: status %d, want 401: %s", status, body)
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
		t.Fatalf("north's directory = %+v, want the one set again", current)
	}
	keep := `[{"op":"remove","path":"/tenants/` + north + `/tenant-directories/` + current[0].ID + `"}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(keep)); status != http.StatusConflict || !strings.Contains(string(body), "Turn directory only off first, then remove the directory") {
		t.Errorf("removing the directory of a tenant that is directory only: status %d, want 409 saying what to do first: %s", status, body)
	}
	if rows := directories(ctx, t, admin, north); len(rows) != 1 {
		t.Errorf("north's directory after the refused removal = %+v, want it kept", rows)
	}

	// Back to passwords: the staff member signs in again.
	openNorth := `[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":false}}]`
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(openNorth)); status != http.StatusOK {
		t.Fatalf("setting north back to passwords: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, memberUser, adminPassword); status != http.StatusOK {
		t.Errorf("the staff member's password sign-in once north admits passwords again: status %d, want 200: %s", status, body)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Tenants", "TenantDirectories")
	if events["Tenants"] < 2 || events["TenantDirectories"] < 2 {
		t.Errorf("change events = %v, want the two flag writes on Tenants and the removal and the setting on TenantDirectories", events)
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
		return err
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
	ID         string `json:"id"`
	Method     string `json:"method"`
	Connection string `json:"connection"`
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
