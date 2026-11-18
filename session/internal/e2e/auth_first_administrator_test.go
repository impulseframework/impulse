//go:build !skipAuth

// The first administrator's invitation through the real handlers on both backends: a
// release mails the address the deployment names one invitation while the Auth has no
// account, the person accepts it with a password or through a directory, however wide
// the directory's method is, and what a later release, a changed address and the
// operations action each do to the link that stands.

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/internal/oidctest"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The first administrator the scenarios' deployment names, another person of the same
// directory, and the roles the invitation gives.
const (
	firstAdministrator = "ada@lakeside.edu"
	sameDirectory      = "grace@lakeside.edu"
)

var firstAdministratorRoles = []string{"Administrator", "Super Administrator"}

// firstAdministratorApp is an application a deployment has just released: the password
// method beside a Google registration, in an application whose tenants have directories
// (the directory admission is set, and one tenant has the Lakeside Workspace as its
// own). Its acceptance hook keeps the roles it gave, by the username it gave them to.
type firstAdministratorApp[Tx any] struct {
	*authApp[Tx]
	google *oidctest.FakeIDP

	mu    sync.Mutex
	given map[string][]string
}

// firstAdministratorShape is what one scenario's application is built with.
type firstAdministratorShape struct {
	// workspaces is whom the Google registration admits: one Workspace (lakesideAlone),
	// or a method open to more than one organization.
	workspaces google.WorkspaceSet
	// sender is the Auth's mail sender; nil is a recorder, which delivers.
	sender mail.Sender
	// stands is how long an invitation stands; zero is the library's seven days.
	stands time.Duration
}

// lakesideAlone is a Google registration of one organization: the Lakeside Workspace.
func lakesideAlone() google.WorkspaceSet {
	return google.Workspace("lakeside", hostedDomain)
}

func newFirstAdministratorApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx], shape firstAdministratorShape) *firstAdministratorApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	db, store := be.newStore(ctx, t)
	googleIDP := oidctest.NewFakeIDP(t, "google-client")
	recorder := &mail.Recorder{}
	sender := shape.sender
	if sender == nil {
		sender = recorder
	}
	auth, err := session.NewAuth[appSessionData, appUserData](withCustomData(t, store, h), cookieKey, session.NoSuperAdministrators(),
		session.WithMail(sender, session.MailSettings{Application: "Lakeside", LoginURL: "https://app.example/login", InvitationTimeout: shape.stands}))
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	a := &firstAdministratorApp[Tx]{google: googleIDP, given: map[string][]string{}}
	auth.SetSignInPolicy(h.signInPolicy)
	auth.SetInvitationAccepted(a.accepted)
	// The application's tenants have directories: a name alone links no account, and a
	// stranger from a tenant's directory needs the tenant's default seat or an
	// invitation.
	auth.SetDirectoryAdmission(be.rows.directoryLookup, seatIn[Tx](&tenantRoles{}))
	password := session.PasswordSignIn(auth)
	googleIssuerMu.Lock()
	issuer := google.IssuerURL
	google.IssuerURL = googleIDP.Server.URL
	workspace := google.SignIn(auth, "google", nil, &google.Config{
		RoleSync: google.DisableRoleSync(), Workspaces: shape.workspaces,
		ClientID: "google-client", ClientSecret: "secret", RedirectURL: "https://app.example/google/callback",
	})
	google.IssuerURL = issuer
	googleIssuerMu.Unlock()

	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.With(auth.ValidateXSRFToken).Post("/login", password.Login())
		r.With(auth.ValidateXSRFToken).Post("/invitation/sign-up", auth.Invitation().SignUp())
		r.Get("/google/login", workspace.Login())
		r.Get("/google/callback", workspace.Callback())
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)
	a.authApp = &authApp[Tx]{db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder}
	// A tenant has the Lakeside Workspace as its directory and names no default seat.
	db.declareTenant(ctx, t, tenantLakeside, false, lakesideWorkspace)

	return a
}

// accepted is the application's acceptance hook: it keeps the roles the invitation
// gave, under the username it gave them to.
func (a *firstAdministratorApp[Tx]) accepted(_ context.Context, _ Tx, _ impulse.UUID, username string, inv *sessioninfo.Invitation) (func(context.Context), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.given[username] = append(a.given[username], inv.RoleNames...)

	return nil, nil
}

// holds are the roles the hook gave username, sorted.
func (a *firstAdministratorApp[Tx]) holds(username string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	held := slices.Clone(a.given[username])
	slices.Sort(held)

	return held
}

// release runs the release's step for the address and fails on an error.
func (a *firstAdministratorApp[Tx]) release(ctx context.Context, t *testing.T, email string) *session.FirstAdministrator {
	t.Helper()

	result, err := a.api.InviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: firstAdministratorRoles})
	if err != nil {
		t.Fatalf("InviteFirstAdministrator(%s) error = %v", email, err)
	}

	return result
}

// mailed are the addresses mailed so far, in order.
func (a *firstAdministratorApp[Tx]) mailed() []string {
	var to []string
	for _, msg := range a.mail.Messages() {
		to = append(to, msg.To)
	}

	return to
}

// open opens the invitation token names in b and returns the status.
func (a *firstAdministratorApp[Tx]) open(ctx context.Context, b *browser, token string) int {
	b.t.Helper()

	status, _ := b.do(ctx, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())

	return status
}

// users are the usernames of the Auth's accounts.
func (a *firstAdministratorApp[Tx]) users(ctx context.Context, t *testing.T) []string {
	t.Helper()

	users, err := a.api.Users(ctx)
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Username)
	}

	return names
}

// TestAuthSeams_FirstAdministrator runs the first administrator's scenarios on each
// backend.
func TestAuthSeams_FirstAdministrator(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runFirstAdministratorSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runFirstAdministratorSeams(t, spannerBackend)
	})
}

func runFirstAdministratorSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "a first release mails the address one invitation; the person chooses a password from it and holds the roles; no account stood before, and a release after it mails nothing",
			run:  seamFirstAdministratorByPassword[Tx],
		},
		{
			name: "a directory of one organization, where tenants have directories: another person of the directory gets nothing, with the link or without it, and the invited person's first sign-in through it makes the account holding the roles",
			run: func(ctx context.Context, t *testing.T, be backend[Tx]) {
				seamFirstAdministratorThroughDirectory(ctx, t, be, lakesideAlone())
			},
		},
		{
			name: "a directory method open to more than one organization, where tenants have directories: the same, since the invitation and not the method's width ties the sign-in to the roles",
			run: func(ctx context.Context, t *testing.T, be backend[Tx]) {
				seamFirstAdministratorThroughDirectory(ctx, t, be, google.Workspaces(lakesideAlone(), google.AnyGoogleAccount()))
			},
		},
		{
			name: "a second release while the invitation stands mails nothing, and a release after it ran out mails a new one, the old link dead",
			run:  seamFirstAdministratorReleases[Tx],
		},
		{
			name: "a changed address withdraws the old invitation: its link no longer opens, and the new address is mailed",
			run:  seamFirstAdministratorChangedAddress[Tx],
		},
		{
			name: "the operations action mails a fresh invitation and the earlier link no longer opens; it refuses once the Auth has an account",
			run:  seamFirstAdministratorSentAgain[Tx],
		},
		{
			name: "an application that writes its mail to the log is refused, and no invitation is made that a log could hold",
			run:  seamFirstAdministratorNoMailbox[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// seamFirstAdministratorByPassword is the scenario "a first release mails the address
// one invitation".
func seamFirstAdministratorByPassword[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newFirstAdministratorApp(ctx, t, be, firstAdministratorShape{workspaces: lakesideAlone()})

	first := a.release(ctx, t, firstAdministrator)
	if first.Outcome != session.FirstAdministratorInvited || first.Email != firstAdministrator {
		t.Fatalf("the first release = %+v, want an invitation mailed to %s", first, firstAdministrator)
	}
	if diff := cmp.Diff([]string{firstAdministrator}, a.mailed()); diff != "" {
		t.Errorf("addresses mailed by the first release (-want +got):\n%s", diff)
	}
	if users := a.users(ctx, t); len(users) != 0 {
		t.Errorf("accounts after the first release = %v, want none: the invitation creates no account and no password", users)
	}

	b := a.browser(t)
	b.prime(ctx)
	if status := a.open(ctx, b, lastInvitationToken(t, a.mail)); status != http.StatusOK {
		t.Fatalf("opening the invitation = %d, want 200", status)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/sign-up", map[string]string{"username": "ada", "password": seedPassword}, b.xsrfToken())
	signedInAs(ctx, t, b, "ada")
	if diff := cmp.Diff(firstAdministratorRoles, a.holds("ada")); diff != "" {
		t.Errorf("the roles the acceptance gave (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ada"}, a.users(ctx, t)); diff != "" {
		t.Errorf("accounts after the acceptance (-want +got): the person's own, and none named administrator\n%s", diff)
	}
	user, err := a.api.UserByUsername(ctx, "ada")
	if err != nil {
		t.Fatalf("UserByUsername(ada) error = %v", err)
	}
	if user.Email == nil || *user.Email != firstAdministrator || !user.EmailVerified() {
		t.Errorf("the account's address = %v (verified %v), want %s, proven by the link", user.Email, user.EmailVerified(), firstAdministrator)
	}

	after := a.release(ctx, t, firstAdministrator)
	if after.Outcome != session.FirstAdministratorHasAccount {
		t.Errorf("the release after the person accepted = %+v, want nobody invited", after)
	}
	if got := len(a.mailed()); got != 1 {
		t.Errorf("messages mailed in all = %d, want the one invitation", got)
	}
}

// seamFirstAdministratorThroughDirectory is the scenario of a first administrator who
// signs in through a directory admitting workspaces.
func seamFirstAdministratorThroughDirectory[Tx any](ctx context.Context, t *testing.T, be backend[Tx], workspaces google.WorkspaceSet) {
	a := newFirstAdministratorApp(ctx, t, be, firstAdministratorShape{workspaces: workspaces})
	a.release(ctx, t, firstAdministrator)
	token := lastInvitationToken(t, a.mail)

	// Another person of the same directory signs in first, without the link: the
	// directory is a tenant's, which names no default seat, so they are a stranger.
	stranger := a.browser(t)
	assertRedirect(t, stranger.oidcSignIn(ctx, a.google, "/google", googleClaims("sub-grace", sameDirectory)), "/login", url.Values{"code": {"invitation_only"}})
	a.assertNoRow(ctx, t, stranger, sameDirectory)

	// The same person holding the link: the invitation is for another address.
	holder := a.browser(t)
	holder.prime(ctx)
	if status := a.open(ctx, holder, token); status != http.StatusOK {
		t.Fatalf("opening the invitation = %d, want 200", status)
	}
	assertRedirect(t, holder.oidcSignIn(ctx, a.google, "/google", googleClaims("sub-grace", sameDirectory)), "/login", url.Values{"code": {"invitation_address"}})
	a.assertNoRow(ctx, t, holder, sameDirectory)
	if got := a.holds(sameDirectory); len(got) != 0 {
		t.Errorf("roles given to %s = %v, want none", sameDirectory, got)
	}
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 {
		t.Fatalf("standing invitations after the refusals = %d, want the first administrator's still standing", len(standing))
	}

	// The invited person: their first sign-in through the directory makes the account,
	// linked to the directory's identity, and gives the roles.
	b := a.browser(t)
	b.prime(ctx)
	if status := a.open(ctx, b, token); status != http.StatusOK {
		t.Fatalf("opening the invitation = %d, want 200", status)
	}
	assertRedirect(t, b.oidcSignIn(ctx, a.google, "/google", googleClaims("sub-ada", firstAdministrator)), "/home", url.Values{})
	v := signedInAs(ctx, t, b, firstAdministrator)
	if diff := cmp.Diff(firstAdministratorRoles, a.holds(firstAdministrator)); diff != "" {
		t.Errorf("the roles the acceptance gave (-want +got):\n%s", diff)
	}
	assertOneLink(t, a.links(ctx, t, v.UserID), "lakeside", tenantLakeside)
	if diff := cmp.Diff([]string{firstAdministrator}, a.users(ctx, t)); diff != "" {
		t.Errorf("accounts after the acceptance (-want +got):\n%s", diff)
	}
	if standing := a.standingInvitations(ctx, t); len(standing) != 0 {
		t.Errorf("standing invitations after the acceptance = %d, want none: the link is spent", len(standing))
	}
}

// seamFirstAdministratorReleases is the scenario "a second release while the
// invitation stands mails nothing".
func seamFirstAdministratorReleases[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newFirstAdministratorApp(ctx, t, be, firstAdministratorShape{workspaces: lakesideAlone(), stands: 2 * time.Second})

	first := a.release(ctx, t, firstAdministrator)
	old := lastInvitationToken(t, a.mail)
	second := a.release(ctx, t, firstAdministrator)
	// A database keeps the moment to its own precision, a microsecond on PostgreSQL.
	if second.Outcome != session.FirstAdministratorStanding || second.ExpiresAt.Sub(first.ExpiresAt).Abs() > time.Millisecond {
		t.Errorf("the second release = %+v, want the first release's invitation left standing until %s", second, first.ExpiresAt)
	}
	if got := len(a.mailed()); got != 1 {
		t.Fatalf("messages mailed after the second release = %d, want the first release's one", got)
	}

	// The invitation runs out.
	time.Sleep(2*time.Second + 200*time.Millisecond)
	third := a.release(ctx, t, firstAdministrator)
	if third.Outcome != session.FirstAdministratorInvited || len(third.Withdrawn) != 0 {
		t.Errorf("the release after the invitation ran out = %+v, want a new one mailed and nothing to withdraw", third)
	}
	if diff := cmp.Diff([]string{firstAdministrator, firstAdministrator}, a.mailed()); diff != "" {
		t.Errorf("addresses mailed (-want +got):\n%s", diff)
	}
	b := a.browser(t)
	b.prime(ctx)
	if status := a.open(ctx, b, old); status != http.StatusNotFound {
		t.Errorf("opening the invitation that ran out = %d, want 404", status)
	}
	if status := a.open(ctx, b, lastInvitationToken(t, a.mail)); status != http.StatusOK {
		t.Errorf("opening the new invitation = %d, want 200", status)
	}
}

// seamFirstAdministratorChangedAddress is the scenario "a changed address withdraws the
// old invitation".
func seamFirstAdministratorChangedAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newFirstAdministratorApp(ctx, t, be, firstAdministratorShape{workspaces: lakesideAlone()})

	a.release(ctx, t, sameDirectory)
	old := lastInvitationToken(t, a.mail)
	changed := a.release(ctx, t, firstAdministrator)
	if changed.Outcome != session.FirstAdministratorInvited || changed.Email != firstAdministrator {
		t.Errorf("the release naming another address = %+v, want %s invited", changed, firstAdministrator)
	}
	if diff := cmp.Diff([]string{sameDirectory}, changed.Withdrawn); diff != "" {
		t.Errorf("the addresses whose invitations were withdrawn (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{sameDirectory, firstAdministrator}, a.mailed()); diff != "" {
		t.Errorf("addresses mailed (-want +got):\n%s", diff)
	}
	b := a.browser(t)
	b.prime(ctx)
	if status := a.open(ctx, b, old); status != http.StatusNotFound {
		t.Errorf("opening the invitation mailed to the address taken back = %d, want 404", status)
	}
	if status := a.open(ctx, b, lastInvitationToken(t, a.mail)); status != http.StatusOK {
		t.Errorf("opening the invitation mailed to the new address = %d, want 200", status)
	}
	standing := a.standingInvitations(ctx, t)
	if len(standing) != 1 || standing[0].Email != firstAdministrator {
		t.Errorf("standing invitations = %+v, want the one to %s", standing, firstAdministrator)
	}
}

// seamFirstAdministratorSentAgain is the scenario "the operations action mails a fresh
// invitation".
func seamFirstAdministratorSentAgain[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newFirstAdministratorApp(ctx, t, be, firstAdministratorShape{workspaces: lakesideAlone()})
	req := &session.FirstAdministratorRequest{Email: firstAdministrator, RoleNames: firstAdministratorRoles}

	a.release(ctx, t, firstAdministrator)
	lost := lastInvitationToken(t, a.mail)
	again, err := a.api.ReinviteFirstAdministrator(ctx, req)
	if err != nil {
		t.Fatalf("ReinviteFirstAdministrator() error = %v", err)
	}
	if again.Outcome != session.FirstAdministratorInvited || !slices.Equal(again.Withdrawn, []string{firstAdministrator}) {
		t.Errorf("the action = %+v, want the standing invitation withdrawn and a fresh one mailed", again)
	}
	fresh := lastInvitationToken(t, a.mail)
	if fresh == lost {
		t.Fatal("the fresh invitation carries the earlier link's token, want one of its own")
	}
	b := a.browser(t)
	b.prime(ctx)
	if status := a.open(ctx, b, lost); status != http.StatusNotFound {
		t.Errorf("opening the earlier link = %d, want 404", status)
	}
	if status := a.open(ctx, b, fresh); status != http.StatusOK {
		t.Fatalf("opening the fresh invitation = %d, want 200", status)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/sign-up", map[string]string{"username": "ada", "password": seedPassword}, b.xsrfToken())
	signedInAs(ctx, t, b, "ada")

	// The Auth has an account: the action refuses, and mails nothing.
	if _, err := a.api.ReinviteFirstAdministrator(ctx, req); !errors.Is(err, session.ErrFirstAdministratorHasAccount) {
		t.Errorf("ReinviteFirstAdministrator() once the person has an account: error = %v, want %v", err, session.ErrFirstAdministratorHasAccount)
	}
	if got := len(a.mailed()); got != 2 {
		t.Errorf("messages mailed in all = %d, want 2: the release's and the action's", got)
	}
}

// seamFirstAdministratorNoMailbox is the scenario "an application that writes its mail
// to the log is refused".
func seamFirstAdministratorNoMailbox[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newFirstAdministratorApp(ctx, t, be, firstAdministratorShape{workspaces: lakesideAlone(), sender: mail.Log{}})

	_, err := a.api.InviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: firstAdministrator, RoleNames: firstAdministratorRoles})
	if !errors.Is(err, session.ErrFirstAdministratorNoMailbox) {
		t.Fatalf("InviteFirstAdministrator() with mail written to the log: error = %v, want %v", err, session.ErrFirstAdministratorNoMailbox)
	}
	if standing := a.standingInvitations(ctx, t); len(standing) != 0 {
		t.Errorf("standing invitations after the refusal = %d, want none: no link was made", len(standing))
	}
	if users := a.users(ctx, t); len(users) != 0 {
		t.Errorf("accounts after the refusal = %v, want none", users)
	}
}
