//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
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
// that says what to do. A tenant admits a session established through a directory only
// when the directory is its own, and a tenant whose people enter through its directory
// only refuses a password session at reach time and, when every tenant of the person
// is such, at sign-in; its directory is not removed while it is directory only.
//
// The directory's row carries a default role, given at a person's first sign-in through
// the directory: with none, a stranger is refused by invitation only and no account is
// created; with a role named, a stranger's next sign-in creates them seated with it,
// and a person whose account stood before is given it at the sign-in that links the
// account to the directory; the role is cleared with an explicit null, the way the
// Sign-in page's picker clears it. A write through the API decides the next request,
// since the records' write paths reload the roster after their commit; the
// administrator, a super administrator, keeps every tenant; and every edit leaves a
// change event. Under the session library's skipAuth build tag Google is simulated:
// the callback signs in whoever APP_GOOGLE_USERNAME names, as a Workspace account whose
// connection is the simulated Workspace's domain. The test sets that variable, so it
// does not run in parallel.
func TestDeclaration_whoMayEnterATenant(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)

	// A person who signed in through Google, seated in south: south has no directory,
	// which admits a password and no directory, so their chart is empty.
	t.Setenv("APP_GOOGLE_USERNAME", "traveler")
	traveler := signedInThroughGoogle(ctx, t, s)
	seat(ctx, t, s, "traveler", south)
	if domains := userDomains(ctx, t, traveler); len(domains) != 0 {
		t.Fatalf("the traveler's chart before south has a directory = %v, want none", domains)
	}
	if status, body := traveler.do(ctx, http.MethodGet, "/api/tenants/"+south+"/announcements", nil); status != http.StatusNotFound {
		t.Fatalf("GET south announcements as the traveler before south has a directory: status %d, want 404: %s", status, body)
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
	// tenants. A tenant with no directory admits no directory session, so the person
	// who sets a tenant's first directory after its creation is a super administrator,
	// the one person who enters a tenant whatever it declares (asSuperAdministrator).
	t.Setenv("APP_GOOGLE_USERNAME", "organizer")
	organizer := signedInThroughGoogle(ctx, t, s)
	asSuperAdministrator(ctx, t, s, "organizer", north, south)
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
	// request never named, and the traveler's next request stands in south.
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

	// Removed, the directory admits nobody: the traveler's chart is empty again. Set
	// again, it is a new row and the traveler is back.
	row := "/tenants/" + south + "/tenant-directories/" + created.TenantDirectories[0]
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"`+row+`"}]`)); status != http.StatusOK {
		t.Fatalf("removing south's directory: status %d: %s", status, body)
	}
	if domains := userDomains(ctx, t, traveler); len(domains) != 0 {
		t.Errorf("the traveler's chart once south's directory is removed = %v, want none", domains)
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
	// TenantAdministrator named on the row, the newcomer's next sign-in creates them
	// seated with it in south, where their chart stands at once.
	nameSeat := `[{"op":"patch","path":"` + row + `","value":{"defaultSeat":"TenantAdministrator"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(nameSeat)); status != http.StatusOK {
		t.Fatalf("naming the row's default role: status %d: %s", status, body)
	}
	newcomer = signedInThroughGoogle(ctx, t, s)
	if domains := userDomains(ctx, t, newcomer); !slices.Equal(domains, []string{south}) {
		t.Errorf("the newcomer's chart after the seated sign-in = %v, want [%s]: the row's default role", domains, south)
	}
	if status, body := newcomer.do(ctx, http.MethodGet, "/api/tenants/"+south+"/announcements", nil); status != http.StatusOK {
		t.Errorf("GET south announcements as the newcomer: status %d, want 200: %s", status, body)
	}
	// A person whose account stood before, created under the name the directory
	// presents and holding nothing, is given the default role at their first sign-in
	// through the directory, the one that links the account to it. Their second
	// sign-in finds the link and gives nothing more.
	if _, err := s.auth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: "veteran"}); err != nil {
		t.Fatalf("CreateSessionUser(veteran) error = %v", err)
	}
	veteranID := accesstypes.User(accountID(ctx, t, s, "veteran"))
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); len(held) != 0 {
		t.Fatalf("the veteran's roles in south before their first sign-in = %v, want none", held)
	}
	t.Setenv("APP_GOOGLE_USERNAME", "veteran")
	veteran := signedInThroughGoogle(ctx, t, s)
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); !slices.Equal(held, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the veteran's roles in south after their first sign-in through its directory = %v, want the default role", held)
	}
	if domains := userDomains(ctx, t, veteran); !slices.Equal(domains, []string{south}) {
		t.Errorf("the veteran's chart after their first sign-in through the directory = %v, want [%s]", domains, south)
	}
	signedInThroughGoogle(ctx, t, s)
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); !slices.Equal(held, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the veteran's roles in south after their second sign-in = %v, want the one default role and nothing more", held)
	}
	// Cleared with an explicit null, the row names no default role again.
	clearSeat := `[{"op":"patch","path":"` + row + `","value":{"defaultSeat":null}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(clearSeat)); status != http.StatusOK {
		t.Fatalf("clearing the row's default role: status %d: %s", status, body)
	}
	if seats := defaultSeats(ctx, t, admin, south); !slices.Equal(seats, []string{""}) {
		t.Errorf("south's default role once cleared = %q, want the one row naming none", seats)
	}

	// A tenant that is directory only keeps its directory: its people enter through it
	// alone, so removing it is refused, saying to turn directory only off first; turned
	// off, the directory is removed, and the roles it gave stand.
	closeSouth := `[{"op":"patch","path":"/tenants/` + south + `","value":{"directoryOnly":true}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(closeSouth)); status != http.StatusOK {
		t.Fatalf("setting south directory only: status %d: %s", status, body)
	}
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"`+row+`"}]`)); status != http.StatusConflict || !strings.Contains(string(body), "Turn directory only off first, then remove the directory") {
		t.Errorf("removing the directory of a tenant that is directory only: status %d, want 409 saying what to do first: %s", status, body)
	}
	if rows := directories(ctx, t, admin, south); len(rows) != 1 {
		t.Errorf("south's directory after the refused removal = %+v, want it kept", rows)
	}
	openSouth := `[{"op":"patch","path":"/tenants/` + south + `","value":{"directoryOnly":false}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(openSouth)); status != http.StatusOK {
		t.Fatalf("setting south back to passwords: status %d: %s", status, body)
	}
	if status, body := organizer.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"`+row+`"}]`)); status != http.StatusOK {
		t.Errorf("removing south's directory once it admits passwords again: status %d: %s", status, body)
	}
	if held := rolesHeld(ctx, t, s, veteranID, accesstypes.DomainPolicyScope(south)); !slices.Equal(held, []accesstypes.Role{"TenantAdministrator"}) {
		t.Errorf("the veteran's roles in south once its directory is removed = %v, want the role it gave to stand", held)
	}

	// North admits its people through its directory only: the member, signed in by
	// password and seated in north alone, loses north at once (not listed, answered as a
	// tenant that does not exist), and a fresh password sign-in is refused naming the
	// rule, since every tenant of the member is directory only. The administrator, a
	// super administrator, keeps north.
	member := signedIn(ctx, t, s, memberUser)
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Fatalf("the member's chart before = %v, want [%s]", domains, north)
	}
	closeNorth := `[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":true}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(closeNorth)); status != http.StatusOK {
		t.Fatalf("setting north directory only: status %d: %s", status, body)
	}
	if domains := userDomains(ctx, t, member); len(domains) != 0 {
		t.Errorf("the member's chart once north is directory only = %v, want none", domains)
	}
	if status, body := member.do(ctx, http.MethodGet, "/api/tenants/"+north+"/announcements", nil); status != http.StatusNotFound {
		t.Errorf("GET north announcements as the member once north is directory only: status %d, want 404: %s", status, body)
	}
	if domains := userDomains(ctx, t, admin); !slices.Equal(domains, []string{north, south}) {
		t.Errorf("the administrator's chart = %v, want both: a super administrator reaches every tenant", domains)
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

	// Back to passwords: the member signs in again.
	openNorth := `[{"op":"patch","path":"/tenants/` + north + `","value":{"directoryOnly":false}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(openNorth)); status != http.StatusOK {
		t.Fatalf("setting north back to passwords: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, memberUser, adminPassword); status != http.StatusOK {
		t.Errorf("the member's password sign-in once north admits passwords again: status %d, want 200: %s", status, body)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Tenants", "TenantDirectories")
	if events["Tenants"] < 4 || events["TenantDirectories"] < 6 {
		t.Errorf("change events = %v, want the four flag writes on Tenants and the directory's six writes on TenantDirectories", events)
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

// asSuperAdministrator makes the person, by the username of their account, an
// administrator of the tenants named and a super administrator: the holder of the role
// the framework defines in every auth, whose session enters a tenant whatever the
// tenant's sign-in settings say. Nobody else is exempt from them, and a tenant with no
// directory admits no directory session, so it is such a person who sets a tenant's
// first directory once the tenant exists.
func asSuperAdministrator(ctx context.Context, t *testing.T, s *served, username string, tenants ...accesstypes.Domain) {
	t.Helper()

	user := accesstypes.User(accountID(ctx, t, s, username))
	if err := s.access.UserManager().AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), user, accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("AddUserRoles(%s, %s) error = %v", username, accesstypes.SuperAdministrator, err)
	}
	for _, tenant := range tenants {
		if err := s.access.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(tenant), user, "TenantAdministrator"); err != nil {
			t.Fatalf("AddUserRoles(%s, %s) error = %v", username, tenant, err)
		}
	}
	slices.Sort(tenants)
	waitForDomains(ctx, t, s.access, user, tenants)
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

// seat gives the person the tenant's administrator role in the tenant, held by the id of
// their account, and waits for the engine to answer it.
func seat(ctx context.Context, t *testing.T, s *served, username string, tenant accesstypes.Domain) {
	t.Helper()

	user := accesstypes.User(accountID(ctx, t, s, username))
	if err := s.access.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(tenant), user, "TenantAdministrator"); err != nil {
		t.Fatalf("AddUserRoles(%s, %s) error = %v", username, tenant, err)
	}
	waitForDomains(ctx, t, s.access, user, []accesstypes.Domain{tenant})
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
