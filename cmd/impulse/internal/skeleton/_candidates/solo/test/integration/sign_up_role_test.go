package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/signin"
)

// TestSignUp_theDefaultRoleIsOneTheOpenerMayGive proves, on the served stack, that
// opening a sign-in method gives nobody a power that giving a role directly would not.
//
// A default role is given to every person who signs up, so whoever opens a method with
// one gives that role to people nobody named. The greeter holds a custom role that
// opens sign-up and gives one role, the feature flags': its grant on AssignStaffRole
// carries the condition new.role = 'FeatureAdministrator', the way a role file limits
// which roles its holder hands out. The picker of the Open sign-up dialog lists the
// greeter that role alone, and lists the administrator every role. Opening the method
// with a role the greeter may not give is refused with 403 naming the role, at the
// warning as at the save, and writes nothing: the Administrator's role, and the
// greeter's own. With the role the greeter may give, the warning is answered and the
// method opens.
func TestSignUp_theDefaultRoleIsOneTheOpenerMayGive(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)

	const role accesstypes.Role = "Greeter"
	global := accesstypes.GlobalPolicyScope()
	manager := s.access.UserManager()
	if err := manager.AddRole(ctx, global, role, "Decides who may sign up, and gives the feature flags' role."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := manager.AddRoleGrants(ctx, global, role,
		access.GrantRow{Permission: accesstypes.Execute, Resource: "AssignStaffRole", Condition: "new.role = 'FeatureAdministrator'"},
		access.GrantRow{Permission: accesstypes.List, Resource: "StaffSignUpRoles", Condition: ""},
		access.GrantRow{Permission: accesstypes.List, Resource: "StaffSignUpRoles.description", Condition: ""},
		access.GrantRow{Permission: accesstypes.Execute, Resource: "OpenStaffSignUp", Condition: ""},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	password := adminPassword
	id, err := s.auth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: "greeter", Password: &password})
	if err != nil {
		t.Fatalf("CreateSessionUser(greeter) error = %v", err)
	}
	holder := accesstypes.User(id.String())
	if err := manager.AddUserRoles(ctx, global, holder, role); err != nil {
		t.Fatalf("AddUserRoles(greeter) error = %v", err)
	}
	// The store writes signal a reload and the swap is asynchronous: once the grant with
	// no condition shows, every answer below reads a policy that holds the whole role.
	if d := decisionFor(ctx, t, s.access.ForUser(holder), accesstypes.GlobalScope(), accesstypes.Execute, "OpenStaffSignUp", time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("the greeter's Execute on OpenStaffSignUp = %s, want granted before the cases run", d)
	}
	greeter := newBrowser(t, s)
	greeter.signIn(ctx, "greeter")
	if status, body := greeter.do(ctx, http.MethodGet, "/api/permission-digest", nil); status != http.StatusOK {
		t.Fatalf("the greeter reading what they may do: status %d: %s", status, body)
	}

	// The picker lists a caller the roles they may give, each with its description.
	if roles := signUpRoles(ctx, t, greeter); len(roles) != 1 || roles["FeatureAdministrator"] == "" {
		t.Errorf("the roles the picker lists the greeter = %v, want FeatureAdministrator alone, the one role they may give", roles)
	}
	if roles := signUpRoles(ctx, t, admin); len(roles) != 3 || roles["Administrator"] == "" || roles["FeatureAdministrator"] == "" || roles[string(role)] == "" {
		t.Errorf("the roles the picker lists the administrator = %v, want Administrator, FeatureAdministrator and %s", roles, role)
	}

	// A role the greeter may not give is refused by name, at the warning and at the
	// save, and nothing is written.
	for _, refused := range []string{"Administrator", string(role)} {
		request := `{"name":"password","defaultRole":"` + refused + `"}`
		want := "gives every person who signs up the role " + refused + ", which you may not give"
		if status, body := openWarning(ctx, greeter, request); status != http.StatusForbidden || !strings.Contains(string(body), want) {
			t.Errorf("the warning of opening with the role %s, as the greeter: status %d: %s, want 403 saying %q", refused, status, body, want)
		}
		if status, body := greeter.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(request)); status != http.StatusForbidden || !strings.Contains(string(body), want) {
			t.Errorf("opening with the role %s, as the greeter: status %d: %s, want 403 saying %q", refused, status, body, want)
		}
	}
	if rows := countRows(ctx, t, s, "StaffSessionSignInMethods"); rows != 0 {
		t.Fatalf("rows of the sign-in methods table after the refused saves = %d, want none", rows)
	}

	// The role the greeter may give opens the method.
	request := `{"name":"password","defaultRole":"FeatureAdministrator"}`
	if status, body := openWarning(ctx, greeter, request); status != http.StatusOK || !strings.Contains(string(body), openConfirm) {
		t.Errorf("the warning of opening with the role the greeter may give: status %d: %s, want 200 with the button %q", status, body, openConfirm)
	}
	if status, body := greeter.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(request)); status != http.StatusNoContent {
		t.Fatalf("opening with the role the greeter may give: status %d: %s", status, body)
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != "FeatureAdministrator" {
		t.Errorf("the sign-in methods once the greeter opened the password method = %+v, want it open with the role", got)
	}
}

// TestSignUp_anOpenRowCountsForTheAudienceItWasOpenedFor proves, on the served stack,
// that a sign-in method is open for the people it admitted when an administrator opened
// it, and for nobody else.
//
// The administrator who opens a method confirms a warning that names who can sign in
// through it, which is a fact of the method's registration in code. Opening the
// password method keeps that audience in the method's row. A release that changes whom
// a registration admits leaves the row naming the audience of before; the test plays
// one by rewriting the row as a row opened when the method admitted one organization's
// people. From then on the method is invite only: the list says for whom it was opened
// and whom it admits now, the sign-up routes are not found, and no account is written.
// Opened again, the row names the audience of today and a stranger signs up. A row that
// names no audience, as one written before the audience was kept, opens nothing either.
func TestSignUp_anOpenRowCountsForTheAudienceItWasOpenedFor(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)

	open := []byte(`{"name":"password","defaultRole":"FeatureAdministrator"}`)
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", open); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up: status %d: %s", status, body)
	}
	registered := signin.Audience{Organizations: nil, Accounts: "an email address"}.Key()
	if got := openedFor(ctx, t, s); got == nil || *got != registered {
		t.Fatalf("the audience the row keeps once the password method is opened = %v, want %s", got, registered)
	}

	// The registration admitted one organization's people when the row was written.
	before := signin.Audience{Organizations: []string{"lakeside.example"}, Accounts: ""}.Key()
	setOpenedFor(ctx, t, s, &before)
	wantSays := "Invite only: this method was opened for anyone with an account at lakeside.example, and it admits anyone with an email address now. Open it again to admit them."
	if got := signInMethods(ctx, t, admin); len(got) != 1 || got[0].OpenSignUp || got[0].DefaultRole != "" || got[0].Says != wantSays {
		t.Errorf("the sign-in methods with a row opened for another audience = %+v, want invite only, saying %q", got, wantSays)
	}
	stranger := newBrowser(t, s)
	stranger.prime(ctx)
	accounts := countRows(ctx, t, s, "StaffSessionUsers")
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/sign-up", []byte(`{"email":"pat@mail.example"}`)); status != http.StatusNotFound {
		t.Errorf("POST /api/user/sign-up with a row opened for another audience: status %d, want 404: %s", status, body)
	}
	if after := countRows(ctx, t, s, "StaffSessionUsers"); after != accounts {
		t.Errorf("accounts after the refused sign-up = %d, want the %d there were", after, accounts)
	}

	// Opened again, for the audience of today.
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", open); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up, again: status %d: %s", status, body)
	}
	if got := openedFor(ctx, t, s); got == nil || *got != registered {
		t.Fatalf("the audience the row keeps once the method is opened again = %v, want %s", got, registered)
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != "FeatureAdministrator" {
		t.Errorf("the sign-in methods once the method is opened again = %+v, want it open with the role", got)
	}
	signedUp(ctx, t, s, "pat@mail.example", "pat")

	// A row that names no audience opens nothing.
	setOpenedFor(ctx, t, s, nil)
	wantSays = "Invite only: this method was opened before its audience was kept, and it admits anyone with an email address now. Open it again to admit them."
	if got := signInMethods(ctx, t, admin); len(got) != 1 || got[0].OpenSignUp || got[0].Says != wantSays {
		t.Errorf("the sign-in methods with a row that names no audience = %+v, want invite only, saying %q", got, wantSays)
	}
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/sign-up", []byte(`{"email":"quinn@mail.example"}`)); status != http.StatusNotFound {
		t.Errorf("POST /api/user/sign-up with a row that names no audience: status %d, want 404: %s", status, body)
	}
}

// openedFor reads the audience the password method's row keeps, nil for a row that
// names none.
func openedFor(ctx context.Context, t *testing.T, s *served) *string {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{SQL: "SELECT OpenedFor FROM StaffSessionSignInMethods WHERE Name = 'password'"})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the password method's row: %v", err)
	}
	var audience cloudspanner.NullString
	if err := row.Columns(&audience); err != nil {
		t.Fatalf("reading the audience of the password method's row: %v", err)
	}
	if !audience.Valid {
		return nil
	}

	return &audience.StringVal
}

// setOpenedFor rewrites the audience the password method's row keeps, as a row written
// when the method's registration admitted other people holds it. No request does this:
// the audience of a row is written by opening the method, and changes under it only
// when a release changes the registration.
func setOpenedFor(ctx context.Context, t *testing.T, s *served, audience *string) {
	t.Helper()

	value := cloudspanner.NullString{}
	if audience != nil {
		value = cloudspanner.NullString{StringVal: *audience, Valid: true}
	}
	_, err := s.db.Client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *cloudspanner.ReadWriteTransaction) error {
		changed, err := txn.Update(ctx, cloudspanner.Statement{
			SQL:    "UPDATE StaffSessionSignInMethods SET OpenedFor = @audience WHERE Name = 'password'",
			Params: map[string]any{"audience": value},
		})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		if changed != 1 {
			return errors.Newf("%d rows of the password method changed, want one", changed)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("rewriting the audience of the password method's row: %v", err)
	}
}
