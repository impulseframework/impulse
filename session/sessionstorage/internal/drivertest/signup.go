package drivertest

// What an administrator chose for a sign-in method, on both drivers: the method's row
// of the sign-in methods table, which the sign-in reads in its own transaction. No row
// is a method that is invite only; a row that says open admits a person with no
// account and gives them the default role it names, in the transaction that makes the
// account; a person who has an account, and a person who carries an invitation, sign in
// the same under either choice. The password method's sign-up reads the same row.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// signUps records what the sign-up hook gave, the way an application gives a role: the
// hook writes through the transaction that makes the account, so its row is committed
// with the account or not at all, and what it knows in memory it is told once the
// transaction has committed. fail, while set, is the error the hook answers after its
// write.
type signUps struct {
	mu sync.Mutex
	// given counts the hook's calls that committed, by username.
	given map[string]int
	fail  error
}

func newSignUps() *signUps {
	return &signUps{given: map[string]int{}}
}

// hook is the seater: it writes the role to HookRecords under signed-up:<username>,
// through the transaction it is handed, and asks to have the call counted once that
// transaction has committed. It reads nothing of the account: the user row is written
// in the same transaction, which Spanner applies at the commit, so the hook is handed
// the id the row is made under and never looks for the row.
func (s *signUps) hook(ctx context.Context, tx HookTx, userID impulse.UUID, username, method, role string) (func(ctx context.Context), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if userID.IsNil() {
		return nil, errors.New("the sign-up hook was handed no user id, want the id the account is made under")
	}
	if err := tx.Record(ctx, "signed-up:"+username, method+"="+role); err != nil {
		return nil, errors.Wrap(err, "HookTx.Record()")
	}

	return func(context.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.given[username]++
	}, s.fail
}

// times reads how often the hook gave username a role in a transaction that committed.
func (s *signUps) times(username string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.given[username]
}

// failing makes the hook fail with err, or work when err is nil.
func (s *signUps) failing(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fail = err
}

// assertRoleGiven fails unless the committed HookRecords say username was given role
// through the instance's method, exactly once.
func assertRoleGiven(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, rec *signUps, username, role string) {
	t.Helper()

	got, found := h.Recorded(ctx, t, in.Raw, "signed-up:"+username)
	if want := MethodName + "=" + role; !found || got != want {
		t.Errorf("the role given to %q = %q (found %v), want %q", username, got, found, want)
	}
	if got := rec.times(username); got != 1 {
		t.Errorf("the sign-up hook gave %q a role %d time(s), want once", username, got)
	}
}

// assertNoRoleGiven fails when the sign-up hook gave username anything.
func assertNoRoleGiven(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, rec *signUps, username string) {
	t.Helper()

	if got, found := h.Recorded(ctx, t, in.Raw, "signed-up:"+username); found {
		t.Errorf("the role given to %q = %q, want none", username, got)
	}
	if got := rec.times(username); got != 0 {
		t.Errorf("the sign-up hook gave %q a role %d time(s), want never", username, got)
	}
}

func testSignInMethodRows(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	opts := cmp.Options{cmpopts.EquateEmpty(), cmpopts.IgnoreFields(dbtype.SignInMethod{}, "UpdatedAt")}

	// Nobody has chosen anything: there is no row, which is every method invite only.
	got, err := in.Driver.SignInMethods(ctx)
	if err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("SignInMethods() = %v, want no row before anyone chose", got)
	}

	// A caller's UpdatedAt is ignored: the row takes the database's time.
	stale := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	w := startWindow(h.ClockLead)
	if err := in.Driver.SetSignInMethod(ctx, &dbtype.SignInMethod{Name: "google", OpenSignUp: true, DefaultRole: strPtr("Member"), UpdatedAt: stale}); err != nil {
		t.Fatalf("SetSignInMethod(google) error = %v", err)
	}
	w.done()
	if err := in.Driver.SetSignInMethod(ctx, &dbtype.SignInMethod{Name: "azure", OpenSignUp: true}); err != nil {
		t.Fatalf("SetSignInMethod(azure) error = %v", err)
	}
	got, err = in.Driver.SignInMethods(ctx)
	if err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}
	want := []*dbtype.SignInMethod{{Name: "azure", OpenSignUp: true}, {Name: "google", OpenSignUp: true, DefaultRole: strPtr("Member")}}
	if diff := cmp.Diff(want, got, opts); diff != "" {
		t.Fatalf("SignInMethods() mismatch, want the rows by name (-want +got):\n%s", diff)
	}
	w.assertStamped(t, "google UpdatedAt", got[1].UpdatedAt)
	if !got[1].Open() || got[1].Role() != "Member" || got[0].Role() != "" {
		t.Errorf("Open(), Role() = %v, %q and %q; want an open method with the role Member and one with none", got[1].Open(), got[1].Role(), got[0].Role())
	}

	// A second write replaces the row: the method is invite only again, with no role.
	if err := in.Driver.SetSignInMethod(ctx, &dbtype.SignInMethod{Name: "google"}); err != nil {
		t.Fatalf("SetSignInMethod(google) error = %v", err)
	}
	got, err = in.Driver.SignInMethods(ctx)
	if err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}
	want = []*dbtype.SignInMethod{{Name: "azure", OpenSignUp: true}, {Name: "google"}}
	if diff := cmp.Diff(want, got, opts); diff != "" {
		t.Errorf("SignInMethods() mismatch after the second write (-want +got):\n%s", diff)
	}
	if got[1].Open() {
		t.Error("Open() = true for a method made invite only again, want false")
	}
}

func testSignInMethodsMissingTable(ctx context.Context, t *testing.T, h *AuthHarness) {
	const table = "NoSuchTable"
	const name = "pat.lee@lakeside.edu"
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: InviteOnly})
	if err := in.Driver.SetSignInMethodsTableName(table); err != nil {
		t.Fatalf("SetSignInMethodsTableName() error = %v", err)
	}

	_, err := in.Driver.SignInMethods(ctx)
	wantMissingTableError(t, "SignInMethods", table, err)
	err = in.Driver.SetSignInMethod(ctx, &dbtype.SignInMethod{Name: MethodName, OpenSignUp: true})
	wantMissingTableError(t, "SetSignInMethod", table, err)

	// A sign-in that cannot read the choice fails: a missing table is never read as
	// "no row", which would be a refusal that says the person was not invited.
	identity := entra("oid-pat", name)
	err = signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity})
	wantMissingTableError(t, "SignIn", table, err)
	var refusal *sessioninfo.LoginRefusal
	if errors.As(err, &refusal) {
		t.Errorf("sign-in error = %v, want a failure that is not a refusal", err)
	}
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, identity)

	link := newMailLink(dbtype.MailLinkSignUp, name, tokenHash(1))
	if err := in.Driver.InsertMailLink(ctx, link); err != nil {
		t.Fatalf("InsertMailLink() error = %v", err)
	}
	_, err = in.SignUp(ctx, &dbtype.InsertSessionUser{Username: name, Email: strPtr(name), EmailVerified: true}, link.ID)
	wantMissingTableError(t, "SignUp", table, err)
	assertNoUser(ctx, t, in.Driver, name)
}

func testOpenMethodDefaultRole(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	rec := newSignUps()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, DefaultRole: "Member", SignUp: rec.hook})

	// A person with no account and no invitation is admitted by the open method: the
	// account is made and the default role given with it, in one transaction.
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
	assertRoleGiven(ctx, t, h, in, rec, name, "Member")

	// The role is given with the account, once: a later sign-in gives nothing, so a
	// role an administrator took away stays away.
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
	if err := signInRequest(ctx, in, again); err != nil {
		t.Fatalf("second sign-in error = %v", err)
	}
	if again.User == nil || again.User.Source != sessioninfo.UserExistingLink {
		t.Errorf("second sign-in user = %+v, want the existing link", again.User)
	}
	if got := rec.times(name); got != 1 {
		t.Errorf("the sign-up hook gave a role %d time(s) after the second sign-in, want once still", got)
	}

	// A person an administrator created an account for is not signing up: the account
	// is linked, and holds what the administrator gave it.
	const created = "kim@lakeside.edu"
	kim := createUser(ctx, t, in.Driver, created)
	kimIdentity := entra("oid-kim", created)
	kimReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: created, Identity: kimIdentity}
	err = signInRequest(ctx, in, kimReq)
	assertNewlyLinked(ctx, t, in.Driver, err, kimReq, kimIdentity, kim, "tenant-1")
	assertNoRoleGiven(ctx, t, h, in, rec, created)
}

func testOpenMethodWithoutRole(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	rec := newSignUps()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, SignUp: rec.hook})

	// An open method that names no role, as in an application with tenants, makes the
	// account and gives nothing.
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
	assertNoRoleGiven(ctx, t, h, in, rec, name)
}

func testEitherChoiceSameForAccountsAndInvitations(_ context.Context, t *testing.T, h *AuthHarness) {
	const (
		member  = "kim@lakeside.edu"
		invited = "pat.lee@lakeside.edu"
	)

	tests := []struct {
		name     string
		accounts Accounts
	}{
		{name: "invite only", accounts: InviteOnly},
		{name: "open, with a default role", accounts: Open},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			acc := newAcceptances()
			rec := newSignUps()
			in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: tt.accounts, DefaultRole: "Member", Accept: acc.hook, SignUp: rec.hook})

			// A person with an account signs in by the link: the choice is not read
			// for them, and nothing is given.
			kim := createUser(ctx, t, in.Driver, member)
			mustLink(ctx, t, in.Driver, kim.ID, entra("oid-kim", member))
			kimReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: member, Identity: entra("oid-kim", member)}
			if err := signInRequest(ctx, in, kimReq); err != nil {
				t.Fatalf("sign-in of a person with an account error = %v", err)
			}
			if kimReq.UserID != kim.ID || kimReq.User == nil || kimReq.User.Source != sessioninfo.UserExistingLink {
				t.Errorf("sign-in = user %v (%+v), want the linked user %v", kimReq.UserID, kimReq.User, kim.ID)
			}
			assertNoRoleGiven(ctx, t, h, in, rec, member)

			// A person with an invitation gets the account and the invitation's role,
			// and never the method's default role beside it.
			link := insertInvitation(ctx, t, in.Driver, invited, "", []string{"Teacher"}, tokenHash(1))
			identity := entra("oid-pat", invited)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: invited, Identity: identity, Invitation: invitationOf(link)}
			err := signInRequest(ctx, in, req)
			assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
			if !req.Invitation.Accepted {
				t.Error("Invitation.Accepted = false, want the invitation accepted with the account")
			}
			assertAccepted(t, acc, "accepted:"+invited, req.UserID.String()+" "+invited+"  Teacher")
			if consumed := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumed == nil {
				t.Error("the invitation is still live, want it consumed with the account")
			}
			assertNoRoleGiven(ctx, t, h, in, rec, invited)
		})
	}
}

func testOpenMethodDeadInvitation(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	acc := newAcceptances()
	rec := newSignUps()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, DefaultRole: "Member", Accept: acc.hook, SignUp: rec.hook})

	// The method admits the person, not the invitation: one that died gives nothing,
	// and the person gets what any new person gets through the method.
	link := insertInvitation(ctx, t, in.Driver, name, "", []string{"Teacher"}, tokenHash(1))
	if err := in.Driver.ConsumeMailLink(ctx, link.TokenHash, time.Now()); err != nil {
		t.Fatalf("ConsumeMailLink() error = %v", err)
	}
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(link)}
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
	if _, found := acc.recorded("accepted:" + name); found {
		t.Error("the invitation hook ran for a dead invitation, want nothing granted by it")
	}
	assertRoleGiven(ctx, t, h, in, rec, name, "Member")
}

func testOpenMethodHookMissing(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, DefaultRole: "Member"})

	// A default role with nothing to give it is a wiring mistake: the sign-in fails in
	// the transaction that would have made the account, so no account holds nothing.
	identity := entra("oid-pat", name)
	err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity})
	var refusal *sessioninfo.LoginRefusal
	if err == nil || errors.As(err, &refusal) {
		t.Fatalf("sign-in error = %v, want a failure that is not a refusal", err)
	}
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, identity)
}

func testOpenMethodHookFails(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	rec := newSignUps()
	failure := errors.New("the default role could not be given")
	rec.failing(failure)
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, DefaultRole: "Member", SignUp: rec.hook})

	// The role cannot be given, so the transaction fails: no account, no link, and
	// none of the hook's own writes.
	identity := entra("oid-pat", name)
	err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity})
	if !errors.Is(err, failure) {
		t.Fatalf("sign-in error = %v, want the hook's failure", err)
	}
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, identity)
	assertNoRoleGiven(ctx, t, h, in, rec, name)

	// Once the hook works, the next sign-in makes the account with its role.
	rec.failing(nil)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
	err = signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
	assertRoleGiven(ctx, t, h, in, rec, name, "Member")
}

// signUpUser is the account a password sign-up makes for email: the address, verified
// by the link that carried the person there, as the username.
func signUpUser(email string) *dbtype.InsertSessionUser {
	return &dbtype.InsertSessionUser{Username: email, Email: strPtr(email), EmailVerified: true}
}

// signUpLink inserts the link mailed to a person who started a sign-up with email.
func signUpLink(ctx context.Context, t *testing.T, d AuthDriver, email string, hash []byte) *dbtype.MailLink {
	t.Helper()

	link := newMailLink(dbtype.MailLinkSignUp, email, hash)
	if err := d.InsertMailLink(ctx, link); err != nil {
		t.Fatalf("InsertMailLink() error = %v", err)
	}

	return link
}

// assertLinkLive fails unless the mail link is still live: nothing consumed it.
func assertLinkLive(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, link *dbtype.MailLink) {
	t.Helper()

	if consumed := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumed != nil {
		t.Errorf("the sign-up link was consumed at %v, want it left standing by a sign-up that made nothing", consumed)
	}
}

func testPasswordSignUpClosed(_ context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"

	tests := []struct {
		name string
		// seed writes the method's row, nil for none.
		seed *dbtype.SignInMethod
	}{
		{name: "a method with no row"},
		{name: "a method made invite only again", seed: &dbtype.SignInMethod{Name: MethodName, DefaultRole: strPtr("Member")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			rec := newSignUps()
			in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, SignUp: rec.hook})
			if tt.seed != nil {
				if err := in.Driver.SetSignInMethod(ctx, tt.seed); err != nil {
					t.Fatalf("SetSignInMethod() error = %v", err)
				}
			}
			link := signUpLink(ctx, t, in.Driver, name, tokenHash(1))

			// A link mailed while the method was open makes nothing once it is not.
			_, err := in.SignUp(ctx, signUpUser(name), link.ID)

			if !httpio.HasNotFound(err) || !errors.Is(err, signin.ErrNotInvited) {
				t.Fatalf("SignUp() error = %v, want NotFound caused by ErrNotInvited", err)
			}
			assertNoUser(ctx, t, in.Driver, name)
			assertLinkLive(ctx, t, h, in, link)
			assertNoRoleGiven(ctx, t, h, in, rec, name)
		})
	}
}

func testPasswordSignUpOpen(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	rec := newSignUps()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, DefaultRole: "Member", SignUp: rec.hook})

	// The link is spent, the account made and the role held together.
	link := signUpLink(ctx, t, in.Driver, name, tokenHash(1))
	id, err := in.SignUp(ctx, signUpUser(name), link.ID)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}
	user := mustUser(ctx, t, in.Driver, id)
	if user.Username != name {
		t.Errorf("User().Username = %q, want %q", user.Username, name)
	}
	assertEmail(t, "User() of the person who signed up", user, strPtr(name), true)
	if consumed := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumed == nil {
		t.Error("the sign-up link is still live, want it consumed with the account")
	}
	assertRoleGiven(ctx, t, h, in, rec, name, "Member")

	// The link made one account: followed again it is gone, and nothing more is given.
	if _, err := in.SignUp(ctx, signUpUser("second@lakeside.edu"), link.ID); !httpio.HasNotFound(err) {
		t.Errorf("SignUp() with a consumed link error = %v, want NotFound", err)
	}
	assertNoUser(ctx, t, in.Driver, "second@lakeside.edu")

	// An address another user holds is a conflict, and the link stands.
	held := signUpLink(ctx, t, in.Driver, name, tokenHash(2))
	if _, err := in.SignUp(ctx, &dbtype.InsertSessionUser{Username: "pat", Email: strPtr(name), EmailVerified: true}, held.ID); !httpio.HasConflict(err) {
		t.Errorf("SignUp() with a held address error = %v, want a Conflict", err)
	}
	assertNoUser(ctx, t, in.Driver, "pat")
	assertLinkLive(ctx, t, h, in, held)
}

func testPasswordSignUpHookFails(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "pat.lee@lakeside.edu"
	rec := newSignUps()
	failure := errors.New("the default role could not be given")
	rec.failing(failure)
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, DefaultRole: "Member", SignUp: rec.hook})
	link := signUpLink(ctx, t, in.Driver, name, tokenHash(1))

	// The role cannot be given: no account, the hook's write gone, and the link left
	// for the next attempt.
	if _, err := in.SignUp(ctx, signUpUser(name), link.ID); !errors.Is(err, failure) {
		t.Fatalf("SignUp() error = %v, want the hook's failure", err)
	}
	assertNoUser(ctx, t, in.Driver, name)
	assertLinkLive(ctx, t, h, in, link)
	assertNoRoleGiven(ctx, t, h, in, rec, name)

	rec.failing(nil)
	if _, err := in.SignUp(ctx, signUpUser(name), link.ID); err != nil {
		t.Fatalf("SignUp() once the hook works error = %v", err)
	}
	assertRoleGiven(ctx, t, h, in, rec, name, "Member")
}
