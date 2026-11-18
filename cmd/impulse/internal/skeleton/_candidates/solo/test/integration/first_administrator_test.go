package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"

	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/solo/pkg/auth/staff"
)

// The first administrator a deployment names for the staff auth, and the person the
// placement names once the address is changed: each by the address the invitation is
// mailed to and the name the person gives their account.
const (
	firstAddress, firstName   = "ada@lakeside.example", "ada"
	secondAddress, secondName = "grace@lakeside.example", "grace"
	firstPassword             = "a-password-of-their-own"
)

// TestFirstAdministrator_invited drives the first sign-in of a deployed environment the
// way the migrate command and the login page do. The staff auth has no account, so the
// release has an invitation mailed to the address the placement names, and makes no
// account and no password. A second release while the invitation stands mails nothing.
// The person opens the link and chooses a password, their address proven by the link,
// and holds the Super Administrator role and the first administrator's roles, so they
// can invite the others. They sign in again from another browser with that password and
// the code mailed to their address. From then on a release invites nobody, and the fresh
// invitation the deployment's operations can ask for is refused: somebody is there to
// invite.
func TestFirstAdministrator_invited(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	deployed(ctx, t, s)

	// The first release: one invitation, mailed to the address named, and no account.
	before := len(s.mail.Messages())
	invited, err := s.auth.InviteAdministrator(ctx, firstAddress)
	if err != nil {
		t.Fatalf("staff.Auth.InviteAdministrator() error = %v", err)
	}
	if invited.Outcome != session.FirstAdministratorInvited || invited.Email != firstAddress || len(invited.Withdrawn) != 0 {
		t.Fatalf("the first release = %+v, want an invitation mailed to %s", invited, firstAddress)
	}
	if sent := s.mail.Messages()[before:]; len(sent) != 1 || sent[0].To != firstAddress {
		t.Fatalf("the first release mailed %d message(s) %+v, want one, to %s", len(sent), sent, firstAddress)
	}
	token := mailedToken(t, s, before, "invitation")
	if names := accountNames(ctx, t, s); len(names) != 0 {
		t.Fatalf("the accounts after the first release = %v, want none: the invitation makes no account ahead of its person", names)
	}

	// A second release while the invitation stands mails nothing.
	before = len(s.mail.Messages())
	again, err := s.auth.InviteAdministrator(ctx, firstAddress)
	if err != nil {
		t.Fatalf("the second staff.Auth.InviteAdministrator() error = %v", err)
	}
	if again.Outcome != session.FirstAdministratorStanding || again.Email != firstAddress || len(s.mail.Messages()) != before {
		t.Fatalf("the second release = %+v with %d message(s) mailed, want the invitation left standing and nothing mailed", again, len(s.mail.Messages())-before)
	}

	// The person opens the link and chooses a password; the link proves the address.
	b := accepted(ctx, t, s, token, firstName, firstAddress)
	sess := b.session(ctx)
	if !sess.Authenticated || sess.Username != firstName || sess.PasswordChangeRequired {
		t.Fatalf("session after the acceptance = %+v, want authenticated as %s with no password to change", sess, firstName)
	}
	if sess.Email != firstAddress || !sess.EmailVerified {
		t.Errorf("session after the acceptance = %+v, want the invited address %s held and proven by the link", sess, firstAddress)
	}
	if names := accountNames(ctx, t, s); !slices.Equal(names, []string{firstName}) {
		t.Errorf("the accounts after the acceptance = %v, want the person's alone: no account named administrator exists", names)
	}

	// They hold the Super Administrator role and the first administrator's roles, each
	// for the whole application, and nothing else.
	user, err := s.auth.Session().API().UserByUsername(ctx, firstName)
	if err != nil {
		t.Fatalf("session.AuthAPI.UserByUsername(%s) error = %v", firstName, err)
	}
	held, err := s.access.UserManager().UserRoles(ctx, accesstypes.User(user.ID.String()))
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(%s) error = %v", firstName, err)
	}
	global, _ := staff.FirstAdministratorRoles()
	want := slices.Sorted(slices.Values(append([]accesstypes.Role{accesstypes.SuperAdministrator}, global...)))
	if got := slices.Sorted(slices.Values(held[accesstypes.GlobalPolicyScope()])); !slices.Equal(got, want) {
		t.Errorf("the first administrator's roles for the whole application = %v, want %v", got, want)
	}

	// So they can invite the others, from the application.
	invite, err := json.Marshal(map[string]any{"email": "ines@mail.example", "name": "ines", "roles": []string{"FeatureAdministrator"}})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := b.do(ctx, http.MethodPost, "/api/invite-staff-user", invite); status != http.StatusOK {
		t.Errorf("POST /api/invite-staff-user as the first administrator: status %d: %s, want 200", status, body)
	}

	// From another browser the person signs in with the password they chose, the code
	// sent to the address they hold, and asks for the browser to be remembered.
	other := newBrowser(t, s)
	other.prime(ctx)
	status, body := other.login(ctx, firstName, firstPassword)
	if status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("login with the chosen password: status %d: %s, want 200 with the sign-in held for the second factor", status, body)
	}
	other.secondFactor(ctx, firstName, true)
	if sess := other.session(ctx); !sess.Authenticated || sess.Username != firstName {
		t.Fatalf("session with the chosen password = %+v, want authenticated as %s", sess, firstName)
	}
	if other.trustedCookie() == nil {
		t.Error("the browser asked to be remembered holds no trusted-device cookie")
	}

	// A release after the person accepted mails nothing, and the fresh invitation is
	// refused: the auth has an account.
	before = len(s.mail.Messages())
	after, err := s.auth.InviteAdministrator(ctx, firstAddress)
	if err != nil {
		t.Fatalf("staff.Auth.InviteAdministrator() after the acceptance error = %v", err)
	}
	if after.Outcome != session.FirstAdministratorHasAccount || len(s.mail.Messages()) != before {
		t.Errorf("the release after the acceptance = %+v with %d message(s) mailed, want nobody invited", after, len(s.mail.Messages())-before)
	}
	if _, err := s.auth.ReinviteAdministrator(ctx, firstAddress); !errors.Is(err, session.ErrFirstAdministratorHasAccount) {
		t.Errorf("staff.Auth.ReinviteAdministrator() once the auth has an account: error = %v, want %v", err, session.ErrFirstAdministratorHasAccount)
	}
	if len(s.mail.Messages()) != before {
		t.Errorf("the refused fresh invitation mailed %d message(s), want none", len(s.mail.Messages())-before)
	}
}

// TestFirstAdministrator_sentAgain proves the two ways an invitation is replaced while
// the auth has no account. The placement names another address: the next release
// withdraws the standing invitation, whose link no longer opens, and mails the address
// named now. The deployment's operations ask for a fresh one, for mail that was lost: the
// standing invitation is withdrawn, its link no longer opens, and a new one is mailed to
// the same address, which the person accepts.
func TestFirstAdministrator_sentAgain(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	deployed(ctx, t, s)

	before := len(s.mail.Messages())
	if _, err := s.auth.InviteAdministrator(ctx, firstAddress); err != nil {
		t.Fatalf("staff.Auth.InviteAdministrator(%s) error = %v", firstAddress, err)
	}
	first := mailedToken(t, s, before, "invitation")

	// The address changes: the old link is dead and the new address is mailed.
	before = len(s.mail.Messages())
	changed, err := s.auth.InviteAdministrator(ctx, secondAddress)
	if err != nil {
		t.Fatalf("staff.Auth.InviteAdministrator(%s) error = %v", secondAddress, err)
	}
	if changed.Outcome != session.FirstAdministratorInvited || changed.Email != secondAddress || !slices.Equal(changed.Withdrawn, []string{firstAddress}) {
		t.Fatalf("the release after the address changed = %+v, want %s invited and the invitation to %s withdrawn", changed, secondAddress, firstAddress)
	}
	if sent := s.mail.Messages()[before:]; len(sent) != 1 || sent[0].To != secondAddress {
		t.Fatalf("the release after the address changed mailed %+v, want one message, to %s", sent, secondAddress)
	}
	second := mailedToken(t, s, before, "invitation")
	if status, body := opened(ctx, t, s, first); status != http.StatusNotFound {
		t.Errorf("opening the link mailed to the address taken back: status %d: %s, want 404", status, body)
	}

	// The operations action: the standing link is dead and a fresh one is mailed.
	before = len(s.mail.Messages())
	fresh, err := s.auth.ReinviteAdministrator(ctx, secondAddress)
	if err != nil {
		t.Fatalf("staff.Auth.ReinviteAdministrator(%s) error = %v", secondAddress, err)
	}
	if fresh.Outcome != session.FirstAdministratorInvited || fresh.Email != secondAddress || !slices.Equal(fresh.Withdrawn, []string{secondAddress}) {
		t.Fatalf("the fresh invitation = %+v, want %s invited again and the earlier invitation withdrawn", fresh, secondAddress)
	}
	if sent := s.mail.Messages()[before:]; len(sent) != 1 || sent[0].To != secondAddress {
		t.Fatalf("the fresh invitation mailed %+v, want one message, to %s", sent, secondAddress)
	}
	third := mailedToken(t, s, before, "invitation")
	if status, body := opened(ctx, t, s, second); status != http.StatusNotFound {
		t.Errorf("opening the earlier link after the fresh invitation: status %d: %s, want 404", status, body)
	}

	// The fresh link is the one that opens, and the person it was mailed to gets in.
	b := accepted(ctx, t, s, third, secondName, secondAddress)
	if sess := b.session(ctx); !sess.Authenticated || sess.Username != secondName || sess.Email != secondAddress {
		t.Errorf("session after accepting the fresh invitation = %+v, want authenticated as %s holding %s", sess, secondName, secondAddress)
	}
	if names := accountNames(ctx, t, s); !slices.Equal(names, []string{secondName}) {
		t.Errorf("the accounts after the acceptance = %v, want %s alone: nobody got in by a withdrawn link", names, secondName)
	}
}

// deployed makes the stack what a deployed environment starts from. The suites' stack
// opens with the development login the bootstrap creates, which a deployment never has:
// it leaves, so the staff auth has no account, which is when the migrate command invites
// the first administrator (staff.Auth.InviteAdministrator).
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

// accountNames are the usernames of the staff auth's accounts, in order.
func accountNames(ctx context.Context, t *testing.T, s *served) []string {
	t.Helper()

	users, err := s.auth.Session().API().Users(ctx)
	if err != nil {
		t.Fatalf("session.AuthAPI.Users() error = %v", err)
	}
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Username)
	}

	return slices.Sorted(slices.Values(names))
}

// opened shows the invitation page what the link is, as a browser that follows the link
// does, and returns the route's answer: 404 for a link that no longer opens.
func opened(ctx context.Context, t *testing.T, s *served, token string) (status int, body []byte) {
	t.Helper()

	b := newBrowser(t, s)
	b.prime(ctx)

	return b.do(ctx, http.MethodPost, "/api/user/invitation", []byte(`{"token":"`+token+`"}`))
}

// accepted has the invited person follow the link and create their account from it with
// a password they choose, as the login page does. The link and the password open no
// session by themselves: the sign-in waits for the code mailed to the invited address,
// so whoever holds the link without the mailbox gets no further. It returns the person's
// browser, signed in.
func accepted(ctx context.Context, t *testing.T, s *served, token, username, address string) *browser {
	t.Helper()

	b := newBrowser(t, s)
	b.prime(ctx)
	if status, body := b.do(ctx, http.MethodPost, "/api/user/invitation", []byte(`{"token":"`+token+`"}`)); status != http.StatusOK {
		t.Fatalf("POST /api/user/invitation as %s: status %d: %s", username, status, body)
	}
	accept, err := json.Marshal(map[string]string{"password": firstPassword, "username": username})
	if err != nil {
		t.Fatal(err)
	}
	status, body := b.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", accept)
	if status != http.StatusOK {
		t.Fatalf("POST /api/user/invitation/sign-up as %s: status %d: %s", username, status, body)
	}
	if strings.Contains(string(body), firstPassword) {
		t.Errorf("the acceptance answered with the password the person chose: %s", body)
	}
	if !heldForFactor(t, body) {
		t.Fatalf("the acceptance as %s answered %s, want the sign-in held for the second factor: the link alone opens no session", username, body)
	}
	if sess := b.session(ctx); sess.Authenticated {
		t.Fatalf("session before the sign-in code = %+v, want none: the link and a password are not the mailbox", sess)
	}
	b.secondFactor(ctx, username, false)
	if sent := s.mail.Messages(); len(sent) == 0 || sent[len(sent)-1].To != address {
		t.Errorf("the sign-in code was mailed to %+v, want the invited address %s", sent[max(len(sent)-1, 0):], address)
	}

	return b
}
