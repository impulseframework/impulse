package drivertest

// The declaration of a directory at a person's first sign-in through it, on both
// drivers: a declared directory belongs to its tenant, whose declaration decides a
// stranger before the registration's standard rule (signinflow's declaredRule), a
// default seat admitting and seating the person, in the transaction that provisions
// them, and none refusing them by invitation only; and a person who has an account is
// given the default seat in the transaction that links them, at once or once their
// password has confirmed the link, and nothing at any later sign-in.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// seatings records what the directory seat hook granted, by username, the way an
// application seats a person: the hook writes through the transaction that provisions
// the user, so its row is committed with the account or not at all, and what it knows
// in memory (an application's permission engine) it is told once the transaction has
// committed. fail, while set, is the error the hook answers after its write.
type seatings struct {
	mu  sync.Mutex
	got map[string][]signin.DirectorySeat
	// given counts the hook's calls that committed, by username.
	given map[string]int
	fail  error
}

func newSeatings() *seatings {
	return &seatings{got: map[string][]signin.DirectorySeat{}, given: map[string]int{}}
}

// hook is the seater: it writes the seats granted to username to HookRecords under
// seated:<username>, through the transaction it is handed, and asks to have them
// recorded in memory once that transaction has committed. While the seatings fail it
// answers the error, and the committed function with it, which the flow must not run.
func (s *seatings) hook(ctx context.Context, tx HookTx, _ impulse.UUID, username string, seats []signin.DirectorySeat) (func(ctx context.Context), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(seats))
	for _, seat := range seats {
		names = append(names, string(seat.Tenant)+"="+string(seat.Role))
	}
	if err := tx.Record(ctx, "seated:"+username, strings.Join(names, ",")); err != nil {
		return nil, errors.Wrap(err, "HookTx.Record()")
	}

	return func(context.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.got[username] = append(s.got[username], seats...)
		s.given[username]++
	}, s.fail
}

// remove takes username's seats away, as an administrator removes a person's seat.
func (s *seatings) remove(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.got, username)
}

// times reads how often the hook gave username seats in a transaction that committed.
func (s *seatings) times(username string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.given[username]
}

// failing makes the hook fail with err, or work when err is nil.
func (s *seatings) failing(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fail = err
}

// of reads the seats granted to username, nil for none.
func (s *seatings) of(username string) []signin.DirectorySeat {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.got[username]
}

// declaring is a directory reader that answers declared for every directory, nothing
// when none is given.
func declaring(declared ...signin.DeclaredDirectory) func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
	return func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
		return declared, nil
	}
}

// declaringOnly is a directory reader that answers declared for directory alone and
// fails the test when it is asked for any other: a directory belongs to one tenant, and
// a personal account is no directory, so nothing is read for one.
func declaringOnly(t *testing.T, directory signin.Directory, declared ...signin.DeclaredDirectory) func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
	return func(_ context.Context, _ HookTx, asked signin.Directory) ([]signin.DeclaredDirectory, error) {
		if asked != directory {
			t.Errorf("the directory reader was asked for %+v, want %+v alone", asked, directory)

			return nil, nil
		}

		return declared, nil
	}
}

func testDirectorySeatAtFirstLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	acc := newAcceptances()
	// The policy sees the seats: it denies a person the hook has not seated yet.
	policy := func(_ context.Context, _ HookTx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		if rec.of(req.Username) == nil {
			return signin.Deny("seat_missing"), nil
		}

		return signin.Allow(), nil
	}
	lakesideDirectory := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tenant-1"}
	cfg := AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory, Accept: acc.hook, Policy: policy,
		Admission: &DirectoryAdmission{Directories: declaringOnly(t, lakesideDirectory, signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	}
	in := h.New(ctx, t, AuthWithAppTables, cfg)

	// Pat's account came from another tenant's invitation: it stands, holding what
	// Riverside gave it, before Pat ever signs in through Lakeside's directory.
	const name = "pat@lakeside.edu"
	pat := createUser(ctx, t, in.Driver, name)
	invitation := insertInvitation(ctx, t, in.Driver, name, "riverside", []string{"Teacher"}, tokenHash(1))
	if err := in.Accept(ctx, pat.ID, name, "", invitationOf(invitation)); err != nil {
		t.Fatalf("accepting Riverside's invitation: %v", err)
	}
	assertAccepted(t, acc, "accepted:"+name, pat.ID.String()+" "+name+" riverside Teacher")
	if seats := rec.of(name); seats != nil {
		t.Fatalf("seats before the first sign-in through the directory = %v, want none", seats)
	}

	// The first sign-in through Lakeside's directory links the identity to the account
	// and gives it Lakeside's default seat in the one transaction, and the hook's
	// committed function has run before the policy decides.
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, req), req, identity, pat, "tenant-1")
	want := []signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted at the first sign-in through the directory (-want +got):\n%s", diff)
	}
	assertRecorded(ctx, t, h, in.Raw, "seated:"+name, "lakeside=Member")

	// The second sign-in finds the link and seats nothing: the hook is not called.
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
	if err := signInRequest(ctx, in, again); err != nil {
		t.Fatalf("second sign-in error = %v", err)
	}
	if again.User == nil || again.User.Source != sessioninfo.UserExistingLink {
		t.Errorf("second sign-in user = %+v, want the existing link", again.User)
	}
	if got := rec.times(name); got != 1 {
		t.Errorf("the seat hook gave seats %d time(s), want once for the link", got)
	}

	// A personal account is no directory: nothing is read for it (declaringOnly fails
	// the test otherwise), the registration's rule decides, and nothing is seated.
	open := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.OpenSignUp,
		Admission: &DirectoryAdmission{Directories: declaringOnly(t, lakesideDirectory, signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})
	const personal = "sam@gmail.com"
	personalIdentity := &sessioninfo.Identity{
		Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: "sub-sam", Email: personal, SignInName: personal,
	}
	sam := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: personal, Identity: personalIdentity}
	err := signInRequest(ctx, open, sam)
	assertProvisioned(ctx, t, open.Driver, err, sam, personalIdentity, signin.GoogleAnyAccountConnection)
	if seats := rec.of(personal); seats != nil {
		t.Errorf("seats granted to a personal account = %v, want none", seats)
	}
}

func testDirectorySeatAtConfirmedLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})

	// Kim has an account with a password: the directory's word alone does not link
	// her, so the sign-in waits, and nothing is linked or seated yet.
	const name = "kim@lakeside.edu"
	kim := passwordUser(ctx, t, in.Driver, name)
	identity := entra("oid-kim", name)
	signInAs := func() (*sessioninfo.NewSessionRequest, error) {
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-kim", name)}

		return req, signInRequest(ctx, in, req)
	}
	_, err := signInAs()
	assertConfirmationPending(ctx, t, in.Driver, err, identity, kim, "tenant-1")
	if seats := rec.of(name); seats != nil {
		t.Fatalf("seats while the link waits for the password = %v, want none", seats)
	}

	// The password confirms it: the link is made and the default seat given with it.
	if err := in.LinkConfirmed(ctx, kim.ID, name, identity, "tenant-1"); err != nil {
		t.Fatalf("LinkConfirmed() error = %v", err)
	}
	assertLinkedTo(ctx, t, in.Driver, identity, kim.ID, "tenant-1")
	want := []signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted with the confirmed link (-want +got):\n%s", diff)
	}
	assertRecorded(ctx, t, h, in.Raw, "seated:"+name, "lakeside=Member")

	// An administrator removes the seat. Kim signs in again: the link is found, and
	// signing in does not give the seat back.
	rec.remove(name)
	req, err := signInAs()
	if err != nil {
		t.Fatalf("sign-in through the link error = %v", err)
	}
	if req.User == nil || req.User.Source != sessioninfo.UserExistingLink {
		t.Errorf("sign-in user = %+v, want the existing link", req.User)
	}
	if seats := rec.of(name); seats != nil {
		t.Errorf("seats after the sign-in that followed the removal = %v, want none: a removed seat is not given back", seats)
	}
	if got := rec.times(name); got != 1 {
		t.Errorf("the seat hook gave seats %d time(s), want once for the link", got)
	}

	// The link is deleted. The next sign-in through the directory is a first one
	// again: it waits for the password, and the link it makes gives the seat again.
	link, err := in.Driver.Identity(ctx, identity.Method, identity.Connection, identity.Subject)
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if err := in.Driver.UnlinkIdentity(ctx, link.ID); err != nil {
		t.Fatalf("UnlinkIdentity() error = %v", err)
	}
	_, err = signInAs()
	assertConfirmationPending(ctx, t, in.Driver, err, identity, kim, "tenant-1")
	if err := in.LinkConfirmed(ctx, kim.ID, name, identity, "tenant-1"); err != nil {
		t.Fatalf("LinkConfirmed() after the link was deleted error = %v", err)
	}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted once the link is made again (-want +got):\n%s", diff)
	}
	if got := rec.times(name); got != 2 {
		t.Errorf("the seat hook gave seats %d time(s), want twice: once for each link", got)
	}

	// An identity that is linked already is a Conflict, and gives nothing more.
	if err := in.LinkConfirmed(ctx, kim.ID, name, identity, "tenant-1"); !httpio.HasConflict(err) {
		t.Errorf("LinkConfirmed() of a linked identity error = %v, want a Conflict", err)
	}
	if got := rec.times(name); got != 2 {
		t.Errorf("the seat hook gave seats %d time(s) after the refused link, want twice still", got)
	}
}

func testDirectorySeatHookFailsAtLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	failure := errors.New("the default seat could not be given")
	rec.failing(failure)
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})

	// A user created ahead, with no password, is linked by the sign-in itself: the
	// seat cannot be given, so the transaction fails and the link is not made.
	const name = "pat@lakeside.edu"
	pat := createUser(ctx, t, in.Driver, name)
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err := signInRequest(ctx, in, req)
	if !errors.Is(err, failure) {
		t.Fatalf("sign-in error = %v, want the seat hook's failure", err)
	}
	assertNoLink(ctx, t, in.Driver, identity)
	if value, found := h.Recorded(ctx, t, in.Raw, "seated:"+name); found {
		t.Errorf("HookRecords[seated:%s] = %q after the hook failed, want nothing committed", name, value)
	}

	// A user with a password is linked once the password confirms it: the same
	// failure leaves no link either.
	const confirmed = "kim@lakeside.edu"
	kim := passwordUser(ctx, t, in.Driver, confirmed)
	kimIdentity := entra("oid-kim", confirmed)
	if err := in.LinkConfirmed(ctx, kim.ID, confirmed, kimIdentity, "tenant-1"); !errors.Is(err, failure) {
		t.Fatalf("LinkConfirmed() error = %v, want the seat hook's failure", err)
	}
	assertNoLink(ctx, t, in.Driver, kimIdentity)
	if got := rec.times(name) + rec.times(confirmed); got != 0 {
		t.Errorf("the committed function ran %d time(s) for transactions that did not commit", got)
	}

	// Nothing was left behind: once the seat can be given, each is linked and seated.
	rec.failing(nil)
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, again), again, identity, pat, "tenant-1")
	if err := in.LinkConfirmed(ctx, kim.ID, confirmed, kimIdentity, "tenant-1"); err != nil {
		t.Fatalf("LinkConfirmed() once the hook works error = %v", err)
	}
	want := []signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}}
	for _, username := range []string{name, confirmed} {
		if diff := cmp.Diff(want, rec.of(username)); diff != "" {
			t.Errorf("seats granted to %s once the hook works (-want +got):\n%s", username, diff)
		}
	}
}

func testDirectoryDefaultSeat(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	// The policy sees the seats: it denies a person the hook has not seated yet.
	policy := func(_ context.Context, _ HookTx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		if rec.of(req.Username) == nil {
			return signin.Deny("seat_missing"), nil
		}

		return signin.Allow(), nil
	}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory, Policy: policy,
		Admission: &DirectoryAdmission{
			Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}, signin.DeclaredDirectory{Tenant: "riverside"}), Seat: rec.hook,
		},
	})

	// The stranger is provisioned and seated in the tenant that names a seat, in one
	// transaction, and the hook's committed function has run before the policy decides;
	// the tenant that names none gives nothing.
	const name = "pat@lakeside.edu"
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
	want := []signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted (-want +got):\n%s", diff)
	}
	assertRecorded(ctx, t, h, in.Raw, "seated:"+name, "lakeside=Member")

	// A second sign-in finds the link and seats nothing more.
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
	if err := signInRequest(ctx, in, again); err != nil {
		t.Fatalf("second sign-in error = %v", err)
	}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted after the second sign-in (-want +got):\n%s", diff)
	}
}

func testDirectoryInvitationOnly(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	acc := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory, Accept: acc.hook,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside"}), Seat: rec.hook},
	})

	// The stranger is refused and nothing is written, though the registration admits
	// members of the directory.
	strangerIdentity := entra("oid-new", "new@lakeside.edu")
	stranger := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "new@lakeside.edu", Identity: strangerIdentity}
	assertRefused(t, signInRequest(ctx, in, stranger), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
	assertNoUser(ctx, t, in.Driver, "new@lakeside.edu")
	if seats := rec.of("new@lakeside.edu"); seats != nil {
		t.Errorf("seats granted to the refused stranger = %v, want none", seats)
	}

	// A person given a role ahead of their first sign-in is no stranger: a role is held
	// by an account, and the user under the sign-in name is linked, as before. The
	// declaration names no default seat, so the link gives nothing.
	kim := createUser(ctx, t, in.Driver, "kim@lakeside.edu")
	kimIdentity := entra("oid-kim", "kim@lakeside.edu")
	kimReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "kim@lakeside.edu", Identity: kimIdentity}
	assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, kimReq), kimReq, kimIdentity, kim, "tenant-1")
	if seats := rec.of("kim@lakeside.edu"); seats != nil {
		t.Errorf("seats granted to the user created ahead = %v, want none", seats)
	}

	// An invitation is the way past: the stranger who carries one is provisioned, the
	// invitation accepted and granted, and no default seat given.
	link := insertInvitation(ctx, t, in.Driver, "pat@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(1))
	patIdentity := entra("oid-pat", "pat@lakeside.edu")
	pat := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "pat@lakeside.edu", Identity: patIdentity, Invitation: invitationOf(link)}
	err := signInRequest(ctx, in, pat)
	assertProvisioned(ctx, t, in.Driver, err, pat, patIdentity, "tenant-1")
	if !pat.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in, want accepted")
	}
	assertAccepted(t, acc, "accepted:pat@lakeside.edu", pat.UserID.String()+" pat@lakeside.edu lakeside Teacher")
	if seats := rec.of("pat@lakeside.edu"); seats != nil {
		t.Errorf("seats granted to the invited stranger = %v, want none: the invitation names the roles", seats)
	}

	// A dead invitation admits nobody: refused with invitation_expired, nothing written.
	dead := insertInvitation(ctx, t, in.Driver, "sam@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(2))
	if err := in.Driver.ConsumeMailLink(ctx, dead.TokenHash, time.Now()); err != nil {
		t.Fatalf("ConsumeMailLink() error = %v", err)
	}
	sam := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "sam@lakeside.edu", Identity: entra("oid-sam", "sam@lakeside.edu"), Invitation: invitationOf(dead)}
	assertRefused(t, signInRequest(ctx, in, sam), sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired)
	assertNoUser(ctx, t, in.Driver, "sam@lakeside.edu")
}

func testDirectoryUndeclared(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: signin.InvitedUsersOnly, Admission: &DirectoryAdmission{Directories: declaring(), Seat: rec.hook}})

	// No tenant declares the directory: the registration's rule decides as before.
	identity := entra("oid-new", "new@lakeside.edu")
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "new@lakeside.edu", Identity: identity}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedNotInvited, signin.ErrNotInvited)
	assertNoUser(ctx, t, in.Driver, "new@lakeside.edu")
}

func testDirectorySeatHookMissing(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"})},
	})

	// A reader without its hook is a wiring mistake: the sign-in fails inside the
	// transaction that would have provisioned the user, so no account is left holding
	// nothing.
	const name = "pat@lakeside.edu"
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err := signInRequest(ctx, in, req)
	if err == nil {
		t.Fatal("sign-in error = nil, want the missing hook reported")
	}
	var refusal *sessioninfo.LoginRefusal
	if errors.As(err, &refusal) {
		t.Errorf("sign-in error = %v, want a failure, not a refusal", err)
	}
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, identity)
}

func testDirectorySeatHookFails(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	failure := errors.New("the default seat could not be given")
	rec.failing(failure)
	// The policy sees the seats: it denies a person the hook has not seated yet.
	policy := func(_ context.Context, _ HookTx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		if rec.of(req.Username) == nil {
			return signin.Deny("seat_missing"), nil
		}

		return signin.Allow(), nil
	}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory, Policy: policy,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})

	// The seat cannot be given: the hook fails the transaction that provisions the
	// stranger, so there is no account, no link, none of the hook's own writes, and
	// its committed function never ran.
	const name = "pat@lakeside.edu"
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err := signInRequest(ctx, in, req)
	if !errors.Is(err, failure) {
		t.Fatalf("sign-in error = %v, want the seat hook's failure", err)
	}
	var refusal *sessioninfo.LoginRefusal
	if errors.As(err, &refusal) {
		t.Errorf("sign-in error = %v, want the hook's error, not a refusal", err)
	}
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, identity)
	if value, found := h.Recorded(ctx, t, in.Raw, "seated:"+name); found {
		t.Errorf("HookRecords[seated:%s] = %q after the hook failed, want nothing committed", name, value)
	}
	if seats := rec.of(name); seats != nil {
		t.Errorf("the committed function ran for a transaction that did not commit: seats = %v", seats)
	}

	// Nothing was left behind, so the person's next sign-in is a stranger's again: once
	// the seat can be given they are provisioned and seated, and admitted by the policy.
	rec.failing(nil)
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	err = signInRequest(ctx, in, again)
	assertProvisioned(ctx, t, in.Driver, err, again, identity, "tenant-1")
	if diff := cmp.Diff([]signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}}, rec.of(name)); diff != "" {
		t.Errorf("seats granted once the hook works (-want +got):\n%s", diff)
	}
	assertRecorded(ctx, t, h, in.Raw, "seated:"+name, "lakeside=Member")
}
