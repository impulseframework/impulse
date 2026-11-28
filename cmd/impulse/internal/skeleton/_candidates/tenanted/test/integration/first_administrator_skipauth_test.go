//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"

	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
)

// TestFirstAdministrator_invitedThroughADirectory proves the first administrator of a
// deployed environment comes in through a directory method open to more than one
// organization, where the application's tenants have directories. The staff auth's google
// registration admits any Workspace and any Google account, so nothing about the method
// says which organization the first administrator signs in from; the invitation mailed to
// the address the placement names does. Another person of the same directory who signs in
// first gets nothing: without the link they are a stranger, refused as not invited (the
// directory is no tenant's), and with the link the invitation is for another address.
// Neither makes an account, and the invitation stands. The invited person follows the link, signs in through Google,
// and holds the Super Administrator role and the first administrator's roles.
//
// Under the session library's skipAuth build tag Google is simulated: the callback signs
// in whoever APP_GOOGLE_USERNAME names. The test sets that variable, so it does not run
// in parallel.
func TestFirstAdministrator_invitedThroughADirectory(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)
	deployed(ctx, t, s)

	const invitedAddress, other = "ada@lakeside.example", "mallory@lakeside.example"
	invited, err := s.auth.InviteAdministrator(ctx, invitedAddress)
	if err != nil {
		t.Fatalf("staff.Auth.InviteAdministrator() error = %v", err)
	}
	if invited.Outcome != session.FirstAdministratorInvited || invited.Email != invitedAddress {
		t.Fatalf("the first release = %+v, want an invitation mailed to %s", invited, invitedAddress)
	}
	token := mailedInvitation(t, s, invitedAddress)

	// Another person of the directory, with no link: a stranger, and no account is made.
	t.Setenv("APP_GOOGLE_USERNAME", other)
	stranger := newBrowser(t, s)
	if status, body := stranger.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before the sign-in: status %d: %s", status, body)
	}
	if status, location := throughGoogle(ctx, t, stranger); status != http.StatusFound || location != "/login?code="+string(sessioninfo.RefusedNotInvited) {
		t.Errorf("another person of the directory signing in first, with no link: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedNotInvited)
	}

	// The same person with the link: the invitation is for another address.
	stranger = newBrowser(t, s)
	if status, page := showInvitation(ctx, t, stranger, token); status != http.StatusOK {
		t.Fatalf("opening the invitation as another person: status %d: %v", status, page)
	}
	if status, location := throughGoogle(ctx, t, stranger); status != http.StatusFound || location != "/login?code="+string(sessioninfo.RefusedInvitationAddress) {
		t.Errorf("another person of the directory accepting the first administrator's invitation: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedInvitationAddress)
	}
	if accountExists(ctx, t, s, other) {
		t.Error("an account was made for the person the invitation was not for")
	}
	if standing, err := s.auth.Session().API().Invitations(ctx); err != nil || len(standing) != 1 || standing[0].Email != invitedAddress {
		t.Fatalf("the invitations that stand after the refused sign-ins = %+v, %v, want the first administrator's alone", standing, err)
	}

	// The invited person: the link, the sign-in through Google, and the second factor
	// the auth's sign-in setting requires.
	t.Setenv("APP_GOOGLE_USERNAME", invitedAddress)
	b := newBrowser(t, s)
	if status, page := showInvitation(ctx, t, b, token); status != http.StatusOK {
		t.Fatalf("opening the invitation as the invited person: status %d: %v", status, page)
	}
	if status, location := throughGoogle(ctx, t, b); status != http.StatusFound || location != "/login?pending=mfa&returnUrl="+url.QueryEscape("/dashboard") {
		t.Fatalf("the invited person's sign-in through Google: status %d to %q, want %d to the login page's code step", status, location, http.StatusFound)
	}
	b.secondFactor(ctx, invitedAddress)
	if status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session after the acceptance: status %d: %s", status, body)
	}

	// They hold the Super Administrator role and the first administrator's roles, each
	// where it is held, and the invitation is spent.
	user, err := s.auth.Session().API().UserByEmail(ctx, invitedAddress)
	if err != nil {
		t.Fatalf("session.AuthAPI.UserByEmail(%s) error = %v: the acceptance made no account", invitedAddress, err)
	}
	held, err := s.access.UserManager().UserRoles(ctx, accesstypes.User(user.ID.String()))
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(%s) error = %v", invitedAddress, err)
	}
	global, everyDomain := staff.FirstAdministratorRoles()
	wantGlobal := slices.Sorted(slices.Values(append([]accesstypes.Role{accesstypes.SuperAdministrator}, global...)))
	if got := slices.Sorted(slices.Values(held[accesstypes.GlobalPolicyScope()])); !slices.Equal(got, wantGlobal) {
		t.Errorf("the first administrator's roles for the whole application = %v, want %v", got, wantGlobal)
	}
	if got, want := slices.Sorted(slices.Values(held[accesstypes.EveryDomainPolicyScope()])), slices.Sorted(slices.Values(everyDomain)); !slices.Equal(got, want) {
		t.Errorf("the first administrator's roles in every tenant = %v, want %v", got, want)
	}
	if standing, err := s.auth.Session().API().Invitations(ctx); err != nil || len(standing) != 0 {
		t.Errorf("the invitations that stand after the acceptance = %+v, %v, want none", standing, err)
	}

	// A release after the person accepted invites nobody.
	if after, err := s.auth.InviteAdministrator(ctx, invitedAddress); err != nil || after.Outcome != session.FirstAdministratorRoleHeld {
		t.Errorf("the release after the acceptance = %+v, %v, want nobody invited", after, err)
	}
}

// deployed makes the stack what a deployed environment starts from. The suites' stack
// opens with the development logins the bootstrap creates, one of which holds the Super
// Administrator role, and which a deployment never has: they leave, so nobody of the
// staff auth holds the role, which is when the migrate command invites the first
// administrator (staff.Auth.InviteAdministrator). Their memberships stay in the
// permission store and name no account any more: a holder who is no account of the auth
// is nobody of it.
func deployed(ctx context.Context, t *testing.T, s *served) {
	t.Helper()

	api := s.auth.Session().API()
	users, err := api.Users(ctx)
	if err != nil {
		t.Fatalf("session.AuthAPI.Users() error = %v", err)
	}
	for _, user := range users {
		if err := api.DeleteSessionUser(ctx, user.ID); err != nil {
			t.Fatalf("session.AuthAPI.DeleteSessionUser(%s) error = %v", user.Username, err)
		}
	}
}

// throughGoogle sends the browser through the staff auth's google registration,
// simulated: the login route sends it to the callback, which signs in whoever
// APP_GOOGLE_USERNAME names. It returns the callback's status and where it sent the
// browser.
func throughGoogle(ctx context.Context, t *testing.T, b *browser) (status int, location string) {
	t.Helper()

	status, location = b.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login: status %d, want %d to the directory", status, http.StatusFound)
	}

	return b.redirect(ctx, location)
}
