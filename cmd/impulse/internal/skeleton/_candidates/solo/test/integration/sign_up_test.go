package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/resource"
)

// What a person with no account and no invitation meets at the password method, and the
// words the Sign-in page's dialog shows before an administrator opens it.
const (
	inviteOnlySays  = "Invite only: a person signs in once they are invited."
	openConsequence = "Anyone with an email address will be able to sign up without an invitation. Each of them gets the role Feature administrator."
	openConfirm     = "Open to anyone with an email address"
)

// TestSignUp_whoGetsAnAccount proves, on the served stack of an application without
// tenants, how a person with no account gets one, and who decides.
//
// Every sign-in method starts invite only, with no row in the auth's sign-in methods
// table: the password method's sign-up routes are not found and nothing is written,
// while a person an administrator invited creates their account from the invitation's
// link. An administrator opens the method through the generated OpenStaffSignUp, which
// declares a warning: asked with X-Warning, the method's own route says who will be
// admitted and what each of them gets, in the server's words for the role the request
// names, and writes nothing. Opening with no default role is refused at the save, since
// nobody signs up holding nothing here, and so are the Super Administrator's role and a
// role the application does not have. Opened with a role, a stranger signs up from a
// mailed link and holds that role and nothing more, the list the Sign-in page draws says
// the method is open and to whom, and the change record keeps who opened it and when. A
// person without the grant reads no list and no warning and is refused the save. Made
// invite only again, the people who have accounts keep them and sign in as before, an
// invited person still gets in, the routes are not found again, and a link mailed while
// the method was open creates nothing.
func TestSignUp_whoGetsAnAccount(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)

	// Invite only, with no row: the list says so, the sign-up routes are not found, and
	// no account is written for the stranger.
	if got := signInMethods(ctx, t, admin); len(got) != 1 || got[0].Name != "password" || got[0].OpenSignUp || got[0].Says != inviteOnlySays || got[0].Admits != "Anyone with an email address" {
		t.Fatalf("the sign-in methods before anyone chose = %+v, want the password method, invite only", got)
	}
	stranger := newBrowser(t, s)
	stranger.prime(ctx)
	accounts := countRows(ctx, t, s, "StaffSessionUsers")
	closed := []struct {
		path string
		body string
	}{
		{path: "/api/user/sign-up", body: `{"email":"pat@mail.example"}`},
		{path: "/api/user/sign-up/open", body: `{"token":"none"}`},
		{path: "/api/user/sign-up/complete", body: `{"token":"none","password":"a-long-password-of-my-own","username":"pat"}`},
	}
	for _, call := range closed {
		if status, body := stranger.do(ctx, http.MethodPost, call.path, []byte(call.body)); status != http.StatusNotFound {
			t.Errorf("POST %s while the method is invite only: status %d, want 404: %s", call.path, status, body)
		}
	}
	if after := countRows(ctx, t, s, "StaffSessionUsers"); after != accounts {
		t.Errorf("accounts after the refused sign-up = %d, want the %d there were: nothing is written", after, accounts)
	}
	if rows := countRows(ctx, t, s, "StaffSessionSignInMethods"); rows != 0 {
		t.Errorf("rows of the sign-in methods table before anyone chose = %d, want none: no row is invite only", rows)
	}

	// A person with an invitation gets in while the method is invite only.
	invited(ctx, t, s, admin, "ines@mail.example", "ines")

	// The warning: the server's words for this method and the role named, the words for
	// the button, and nothing written by asking. Before a role is picked it says a role
	// is to be chosen.
	status, body := openWarning(ctx, admin, `{"name":"password","defaultRole":"FeatureAdministrator"}`)
	var asked resource.Warning
	if err := json.Unmarshal(body, &asked); status != http.StatusOK || err != nil || asked.Consequence != openConsequence || asked.Confirm != openConfirm {
		t.Fatalf("the warning of opening the password method: status %d: %s, want %q on the button %q", status, body, openConsequence, openConfirm)
	}
	if status, body := openWarning(ctx, admin, `{"name":"password"}`); status != http.StatusOK || !strings.Contains(string(body), "Choose the role each of them gets.") {
		t.Errorf("the warning before a role is picked: status %d: %s, want 200 saying the role is to be chosen", status, body)
	}
	if rows := countRows(ctx, t, s, "StaffSessionSignInMethods"); rows != 0 {
		t.Fatalf("rows of the sign-in methods table after the warning was asked = %d, want none: asking writes nothing", rows)
	}

	// Refused at the save: no default role, the Super Administrator's role, a role the
	// application does not have, and a method the auth does not register.
	refused := []struct {
		name       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "no default role", body: `{"name":"password"}`, wantStatus: http.StatusBadRequest, wantBody: "choose the default role"},
		{name: "the Super Administrator's role", body: `{"name":"password","defaultRole":"SuperAdministrator"}`, wantStatus: http.StatusBadRequest, wantBody: "is never a default role"},
		{name: "a role the application does not have", body: `{"name":"password","defaultRole":"Nobody"}`, wantStatus: http.StatusBadRequest, wantBody: "is not a role held for the whole application"},
		{name: "a method the auth does not register", body: `{"name":"google","defaultRole":"FeatureAdministrator"}`, wantStatus: http.StatusNotFound, wantBody: "This way to sign in does not exist."},
	}
	for _, call := range refused {
		if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(call.body)); status != call.wantStatus || !strings.Contains(string(body), call.wantBody) {
			t.Errorf("opening with %s: status %d: %s, want %d saying %q", call.name, status, body, call.wantStatus, call.wantBody)
		}
	}
	if rows := countRows(ctx, t, s, "StaffSessionSignInMethods"); rows != 0 {
		t.Fatalf("rows of the sign-in methods table after the refused saves = %d, want none", rows)
	}

	// Opened with a role: the list says so in the warning's words, the picker lists the
	// roles held for the whole application with their descriptions and never the Super
	// Administrator's, and the change record keeps who opened the method.
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"password","defaultRole":"FeatureAdministrator"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up: status %d: %s", status, body)
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != "FeatureAdministrator" || got[0].Says != "Open: "+openConsequence {
		t.Errorf("the sign-in methods once the password method is open = %+v, want it open with the role, saying what the warning said", got)
	}
	roles := signUpRoles(ctx, t, admin)
	if roles["FeatureAdministrator"] == "" || roles["Administrator"] == "" || len(roles) != 2 {
		t.Errorf("the roles the dialog's picker lists = %v, want Administrator and FeatureAdministrator, each with its description", roles)
	}
	if sources := changeSources(ctx, t, s, "StaffSessionSignInMethods"); len(sources) != 1 || !strings.HasPrefix(sources[0], adminUser+" ("+s.adminID+"), session ") {
		t.Errorf("who changed the sign-in methods = %q, want the one change, by %s", sources, adminUser)
	}

	// A stranger signs up from the mailed link and holds the default role and nothing
	// more: the feature flags are theirs to read, the accounts are not.
	pat := signedUp(ctx, t, s, "pat@mail.example", "pat")
	if status, body := pat.do(ctx, http.MethodGet, "/api/feature-flags", nil); status != http.StatusOK {
		t.Errorf("GET /api/feature-flags as the person who signed up: status %d, want 200 by the default role: %s", status, body)
	}
	if status, body := pat.do(ctx, http.MethodGet, "/api/staff-session-users", nil); status != http.StatusForbidden {
		t.Errorf("GET /api/staff-session-users as the person who signed up: status %d, want 403: the default role alone: %s", status, body)
	}

	// Without the grant: no list, no warning, and the save is refused, so the choice
	// stands as the administrator left it.
	denied := []struct {
		name string
		do   func() (int, []byte)
	}{
		{name: "the list", do: func() (int, []byte) { return pat.do(ctx, http.MethodGet, "/api/staff-sign-in-methods", nil) }},
		{name: "the warning", do: func() (int, []byte) {
			return openWarning(ctx, pat, `{"name":"password","defaultRole":"Administrator"}`)
		}},
		{name: "opening", do: func() (int, []byte) {
			return pat.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"password","defaultRole":"Administrator"}`))
		}},
		{name: "making invite only", do: func() (int, []byte) {
			return pat.do(ctx, http.MethodPost, "/api/make-staff-invite-only", []byte(`{"name":"password"}`))
		}},
	}
	for _, call := range denied {
		if status, body := call.do(); status != http.StatusForbidden {
			t.Errorf("%s as a person without the grant: status %d, want 403: %s", call.name, status, body)
		}
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != "FeatureAdministrator" {
		t.Errorf("the sign-in methods after the refused calls = %+v, want the method open with the role the administrator chose", got)
	}

	// A link mailed while the method is open, held until after it is closed.
	late := newBrowser(t, s)
	late.prime(ctx)
	lateToken := signUpLink(ctx, t, s, late, "sam@mail.example")

	// Invite only again: a second change in the record, the people with accounts sign in
	// as before, an invited person gets in, and nobody signs up, the late link included.
	if status, body := admin.do(ctx, http.MethodPost, "/api/make-staff-invite-only", []byte(`{"name":"password"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/make-staff-invite-only: status %d: %s", status, body)
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || got[0].OpenSignUp || got[0].DefaultRole != "" || got[0].Says != inviteOnlySays {
		t.Errorf("the sign-in methods once made invite only = %+v, want invite only, naming no role", got)
	}
	if sources := changeSources(ctx, t, s, "StaffSessionSignInMethods"); len(sources) != 2 {
		t.Errorf("changes to the sign-in methods = %q, want two: opened, then made invite only", sources)
	}
	accounts = countRows(ctx, t, s, "StaffSessionUsers")
	if status, body := late.do(ctx, http.MethodPost, "/api/user/sign-up/complete", []byte(`{"token":"`+lateToken+`","password":"a-long-password-of-my-own","username":"sam"}`)); status != http.StatusNotFound {
		t.Errorf("completing a sign-up mailed while the method was open, after it was closed: status %d, want 404: %s", status, body)
	}
	if status, body := late.do(ctx, http.MethodPost, "/api/user/sign-up", []byte(`{"email":"tess@mail.example"}`)); status != http.StatusNotFound {
		t.Errorf("POST /api/user/sign-up once invite only again: status %d, want 404: %s", status, body)
	}
	if after := countRows(ctx, t, s, "StaffSessionUsers"); after != accounts {
		t.Errorf("accounts after the refused sign-ups = %d, want the %d there were", after, accounts)
	}
	again := newBrowser(t, s)
	again.prime(ctx)
	if status, body := again.do(ctx, http.MethodPost, "/api/user/password/login", []byte(`{"username":"pat","password":"a-long-password-of-my-own"}`)); status != http.StatusOK {
		t.Errorf("the person who signed up, signing in once the method is invite only: status %d, want 200: they keep their account: %s", status, body)
	}
	if status, body := pat.do(ctx, http.MethodGet, "/api/feature-flags", nil); status != http.StatusOK {
		t.Errorf("GET /api/feature-flags as the person who signed up, once invite only: status %d, want 200: they keep the role: %s", status, body)
	}
	invited(ctx, t, s, admin, "noor@mail.example", "noor")
}

// signInMethod is a row of the list the Sign-in page draws.
type signInMethod struct {
	Name        string `json:"name"`
	Admits      string `json:"admits"`
	OpenSignUp  bool   `json:"openSignUp"`
	DefaultRole string `json:"defaultRole"`
	Says        string `json:"says"`
}

// signInMethods reads the auth's sign-in methods as the Sign-in page lists them.
func signInMethods(ctx context.Context, t *testing.T, b *browser) []signInMethod {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-sign-in-methods", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-sign-in-methods: status %d: %s", status, body)
	}
	var methods []signInMethod
	if err := json.Unmarshal(body, &methods); err != nil {
		t.Fatalf("decoding the sign-in methods: %v: %s", err, body)
	}

	return methods
}

// signUpRoles reads the roles the Open sign-up dialog's picker lists, each with its
// description.
func signUpRoles(ctx context.Context, t *testing.T, b *browser) map[string]string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-sign-up-roles", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-sign-up-roles: status %d: %s", status, body)
	}
	var rows []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decoding the sign-up roles: %v: %s", err, body)
	}
	roles := map[string]string{}
	for _, row := range rows {
		roles[row.Name] = row.Description
	}

	return roles
}

// openWarning asks the method that opens a sign-in method for its warning in place of
// running it, the way the library's dialog does: the request goes to the method's own
// route with X-Warning.
func openWarning(ctx context.Context, b *browser, body string) (status int, respBody []byte) {
	b.t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/api/open-staff-sign-up", bytes.NewReader([]byte(body)))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(resource.WarningHeader, "true")
	if token := b.xsrfToken(); token != "" {
		req.Header.Set("X-XSRF-TOKEN", token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}

	return resp.StatusCode, respBody
}

// prime starts the browser's XSRF handshake, as the login page does when it loads.
func (b *browser) prime(ctx context.Context) {
	b.t.Helper()

	if status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		b.t.Fatalf("GET /api/user/session before signing in: status %d: %s", status, body)
	}
}

// signUpLink starts a sign-up for the address and returns the token of the link the
// auth mailed.
func signUpLink(ctx context.Context, t *testing.T, s *served, b *browser, email string) string {
	t.Helper()

	before := len(s.mail.Messages())
	start, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := b.do(ctx, http.MethodPost, "/api/user/sign-up", start); status != http.StatusOK {
		t.Fatalf("POST /api/user/sign-up for %s: status %d: %s", email, status, body)
	}

	return mailedToken(t, s, before, "signUp")
}

// mailedToken finds the token of the link of the kind (signUp, invitation) among the
// messages the auth sent since before.
func mailedToken(t *testing.T, s *served, before int, kind string) string {
	t.Helper()

	link := regexp.MustCompile(`http://solo\.test/login#` + kind + `=([A-Za-z0-9_-]+)`)
	for _, m := range s.mail.Messages()[before:] {
		if match := link.FindStringSubmatch(m.Text); match != nil {
			return match[1]
		}
	}
	t.Fatalf("no %s link was mailed (%d messages before)", kind, before)

	return ""
}

// signedUp signs a stranger up through the open password method the way the login page
// does: the address, the mailed link opened, the account completed with a password,
// then the second factor where the sign-in waits for it.
func signedUp(ctx context.Context, t *testing.T, s *served, email, username string) *browser {
	t.Helper()

	b := newBrowser(t, s)
	b.prime(ctx)
	token := signUpLink(ctx, t, s, b, email)
	if status, body := b.do(ctx, http.MethodPost, "/api/user/sign-up/open", []byte(`{"token":"`+token+`"}`)); status != http.StatusOK || !strings.Contains(string(body), email) {
		t.Fatalf("POST /api/user/sign-up/open: status %d: %s, want 200 naming %s", status, body, email)
	}
	complete, err := json.Marshal(map[string]string{"token": token, "password": "a-long-password-of-my-own", "username": username})
	if err != nil {
		t.Fatal(err)
	}
	status, body := b.do(ctx, http.MethodPost, "/api/user/sign-up/complete", complete)
	if status != http.StatusOK {
		t.Fatalf("POST /api/user/sign-up/complete as %s: status %d: %s", username, status, body)
	}
	factorIfHeld(ctx, t, b, username, body)
	if sess := b.session(ctx); !sess.Authenticated || sess.Username != username {
		t.Fatalf("session after the sign-up = %+v, want authenticated as %s", sess, username)
	}

	return b
}

// factorIfHeld completes the second factor where the answer says the sign-in waits for
// it, as the login page does.
func factorIfHeld(ctx context.Context, t *testing.T, b *browser, username string, body []byte) {
	t.Helper()

	if heldForFactor(t, body) {
		b.secondFactor(ctx, username, false)
	}
}

// invited has the administrator invite the address with the FeatureAdministrator role
// and the invited person create their account from the link and the code mailed to the
// address, whatever was chosen for the method: an invitation is the way in under either
// choice.
func invited(ctx context.Context, t *testing.T, s *served, admin *browser, email, username string) {
	t.Helper()

	before := len(s.mail.Messages())
	invite, err := json.Marshal(map[string]any{"email": email, "name": username, "roles": []string{"FeatureAdministrator"}})
	if err != nil {
		t.Fatal(err)
	}
	status, body := admin.do(ctx, http.MethodPost, "/api/invite-staff-user", invite)
	if status != http.StatusOK {
		t.Fatalf("POST /api/invite-staff-user for %s: status %d: %s", email, status, body)
	}
	token := mailedToken(t, s, before, "invitation")

	b := newBrowser(t, s)
	b.prime(ctx)
	if status, body := b.do(ctx, http.MethodPost, "/api/user/invitation", []byte(`{"token":"`+token+`"}`)); status != http.StatusOK {
		t.Fatalf("POST /api/user/invitation as %s: status %d: %s", username, status, body)
	}
	accept, err := json.Marshal(map[string]string{"password": "a-long-password-of-my-own", "username": username})
	if err != nil {
		t.Fatal(err)
	}
	status, body = b.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", accept)
	if status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("POST /api/user/invitation/sign-up as %s: status %d: %s, want 200 held for the code mailed to the invited address", username, status, body)
	}
	b.secondFactor(ctx, username, false)
	if status, body := b.do(ctx, http.MethodGet, "/api/feature-flags", nil); status != http.StatusOK {
		t.Fatalf("GET /api/feature-flags as the invited %s: status %d, want 200 by the invitation's role: %s", username, status, body)
	}
}

// countRows counts the rows of a table of the suite's database.
func countRows(ctx context.Context, t *testing.T, s *served, table string) int64 {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{SQL: "SELECT COUNT(*) FROM " + table})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the rows of %s: %v", table, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of %s: %v", table, err)
	}

	return n
}

// changeSources lists who made each change the change record holds for a table, oldest
// first: the person, the id of their account and their session.
func changeSources(ctx context.Context, t *testing.T, s *served, table string) []string {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{
		SQL:    "SELECT EventSource FROM DataChangeEvents WHERE TableName = @table ORDER BY EventTime",
		Params: map[string]any{"table": table},
	})
	defer iter.Stop()
	var sources []string
	if err := iter.Do(func(row *cloudspanner.Row) error {
		var source string
		if err := row.Columns(&source); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		sources = append(sources, source)

		return nil
	}); err != nil {
		t.Fatalf("reading the change events of %s: %v", table, err)
	}

	return sources
}
