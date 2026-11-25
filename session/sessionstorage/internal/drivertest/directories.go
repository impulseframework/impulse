package drivertest

// The declaration of a directory at a person's sign-in through it, on both drivers: a
// declared directory belongs to its tenant, whose declaration decides a stranger before
// the registration's standard rule (signinflow's declaredRule), a default seat admitting
// and seating the person, in the transaction that provisions them, and none refusing
// them by invitation only; a person who has an account is handed to the seat hook in
// the transaction that links them, at once or once their password has confirmed the
// link; and every later sign-in through the directory by an account that is not
// disabled hands the hook the default seat again, the hook alone deciding whether the
// person holds anything in the tenant.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// seatings records what the directory seat hook granted, by username, the way an
// application seats a person: the hook writes through the transaction that provisions
// the user, so its row is committed with the account or not at all, and what it knows
// in memory (an application's permission engine) it is told once the transaction has
// committed. fail, while set, is the error the hook answers after its write. ahead is
// the roles an administrator gave before a person's first sign-in, by the id of the
// account: the tenants the account holds a role in itself, which are all it holds.
type seatings struct {
	mu  sync.Mutex
	got map[string][]signin.DirectorySeat
	// given counts the hook's calls that committed, by username.
	given map[string]int
	fail  error
	ahead map[impulse.UUID][]accesstypes.Domain
}

func newSeatings() *seatings {
	return &seatings{got: map[string][]signin.DirectorySeat{}, given: map[string]int{}, ahead: map[impulse.UUID][]accesstypes.Domain{}}
}

// roleAhead gives the account a role in tenant ahead of its first sign-in, as an
// administrator of the tenant does for one of its people.
func (s *seatings) roleAhead(userID impulse.UUID, tenant accesstypes.Domain) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ahead[userID] = append(s.ahead[userID], tenant)
}

// onlyIn reports whether tenant is the only place the account holds anything, as the
// rule on a first link made on a name alone asks: every role it was given ahead is in
// tenant, and one was given.
func (s *seatings) onlyIn(_ context.Context, _ HookTx, userID impulse.UUID, tenant accesstypes.Domain) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ahead := s.ahead[userID]

	return len(ahead) > 0 && !slices.ContainsFunc(ahead, func(held accesstypes.Domain) bool { return held != tenant }), nil
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
		Auth: true, Accounts: Open, Accept: acc.hook, Policy: policy,
		Admission: &DirectoryAdmission{
			Directories: declaringOnly(t, lakesideDirectory, signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook, OnlyIn: rec.onlyIn,
		},
	}
	in := h.New(ctx, t, AuthWithAppTables, cfg)

	// Pat's account stands before Pat ever signs in: it was created ahead, with no
	// password, and Riverside's invitation was mailed to its address.
	const name = "pat@lakeside.edu"
	pat := createUser(ctx, t, in.Driver, name)
	invitation := insertInvitation(ctx, t, in.Driver, name, "riverside", []string{"Teacher"}, tokenHash(1))
	if seats := rec.of(name); seats != nil {
		t.Fatalf("seats before the first sign-in through the directory = %v, want none", seats)
	}

	// Lakeside's directory asserting Pat's name is not enough to link the account: Pat
	// holds no role in Lakeside, and the sign-in carries nothing of Pat's.
	identity := entra("oid-pat", name)
	bare := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	assertRefused(t, signInRequest(ctx, in, bare), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
	assertNoLink(ctx, t, in.Driver, identity)
	if seats := rec.of(name); seats != nil {
		t.Fatalf("seats after the refused sign-in = %v, want none", seats)
	}

	// Pat follows Riverside's invitation and signs in through Lakeside's directory: the
	// invitation mailed to the account's address vouches for the link, so the identity
	// is linked to the account, the invitation accepted, and Lakeside's default seat
	// given in the one transaction, and the hook's committed function has run before
	// the policy decides.
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(invitation)}
	assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, req), req, identity, pat, "tenant-1")
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in that carried Riverside's invitation, want accepted")
	}
	assertAccepted(t, acc, "accepted:"+name, pat.ID.String()+" "+name+" riverside Teacher")
	want := []signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted at the first sign-in through the directory (-want +got):\n%s", diff)
	}
	assertRecorded(ctx, t, h, in.Raw, "seated:"+name, "lakeside=Member")

	// The second sign-in and the third find the link, and each hands the hook
	// Lakeside's default seat again: the hook alone reads whether Pat holds anything in
	// the tenant.
	for n, ordinal := range []string{"second", "third"} {
		again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
		if err := signInRequest(ctx, in, again); err != nil {
			t.Fatalf("%s sign-in error = %v", ordinal, err)
		}
		if again.User == nil || again.User.Source != sessioninfo.UserExistingLink {
			t.Errorf("%s sign-in user = %+v, want the existing link", ordinal, again.User)
		}
		if got, wantTimes := rec.times(name), n+2; got != wantTimes {
			t.Errorf("the seat hook was called %d time(s) after the %s sign-in, want %d: once at every sign-in", got, ordinal, wantTimes)
		}
		if diff := cmp.Diff(slices.Repeat(want, n+2), rec.of(name)); diff != "" {
			t.Errorf("seats handed to the hook by the %s sign-in (-want +got):\n%s", ordinal, diff)
		}
	}

	// A personal account is no directory: nothing is read for it (declaringOnly fails
	// the test otherwise), the registration's rule decides, and nothing is seated.
	open := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open,
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
		Auth: true, Accounts: Open,
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

	// An administrator removes the seat. Kim signs in again, twice: each sign-in finds
	// the link and hands the hook the default seat again, so the removed seat is given
	// back.
	for n, ordinal := range []string{"second", "third"} {
		rec.remove(name)
		req, err := signInAs()
		if err != nil {
			t.Fatalf("%s sign-in through the link error = %v", ordinal, err)
		}
		if req.User == nil || req.User.Source != sessioninfo.UserExistingLink {
			t.Errorf("%s sign-in user = %+v, want the existing link", ordinal, req.User)
		}
		if diff := cmp.Diff(want, rec.of(name)); diff != "" {
			t.Errorf("seats after the %s sign-in that followed the removal (-want +got):\n%s", ordinal, diff)
		}
		if got, wantTimes := rec.times(name), n+2; got != wantTimes {
			t.Errorf("the seat hook was called %d time(s) after the %s sign-in, want %d: once at every sign-in", got, ordinal, wantTimes)
		}
	}

	// The link is deleted. The next sign-in through the directory is a first one
	// again: it waits for the password, and the link it makes gives the seat again.
	link, err := in.Driver.Identity(ctx, identity.Method, identity.Connection, identity.Subject)
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if err := in.Driver.UnlinkIdentity(ctx, link.ID, everyLinkSignsIn); err != nil {
		t.Fatalf("UnlinkIdentity() error = %v", err)
	}
	_, err = signInAs()
	assertConfirmationPending(ctx, t, in.Driver, err, identity, kim, "tenant-1")
	rec.remove(name)
	if err := in.LinkConfirmed(ctx, kim.ID, name, identity, "tenant-1"); err != nil {
		t.Fatalf("LinkConfirmed() after the link was deleted error = %v", err)
	}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats granted once the link is made again (-want +got):\n%s", diff)
	}
	if got := rec.times(name); got != 4 {
		t.Errorf("the seat hook was called %d time(s), want four: once for each link and once at each sign-in through one", got)
	}

	// An identity that is linked already is a Conflict, and gives nothing more.
	if err := in.LinkConfirmed(ctx, kim.ID, name, identity, "tenant-1"); !httpio.HasConflict(err) {
		t.Errorf("LinkConfirmed() of a linked identity error = %v, want a Conflict", err)
	}
	if got := rec.times(name); got != 4 {
		t.Errorf("the seat hook was called %d time(s) after the refused link, want four still", got)
	}
}

func testDirectorySeatNotForDisabledLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})

	// Gus's identity is linked through Lakeside's directory, and his account is
	// disabled: his sign-in is refused as before, and the hook is not called.
	const name = "gus@lakeside.edu"
	gus := createUser(ctx, t, in.Driver, name)
	identity := entra("oid-gus", name)
	mustLink(ctx, t, in.Driver, gus.ID, identity)
	if err := in.Driver.DeactivateUser(ctx, gus.ID); err != nil {
		t.Fatalf("DeactivateUser() error = %v", err)
	}
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-gus", name)}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedUserDisabled, signin.ErrUserDisabled)
	if got := rec.times(name); got != 0 {
		t.Errorf("the seat hook was called %d time(s) for a disabled account, want never", got)
	}
	if value, found := h.Recorded(ctx, t, in.Raw, "seated:"+name); found {
		t.Errorf("HookRecords[seated:%s] = %q after a disabled account's sign-in, want nothing", name, value)
	}
}

func testDirectorySeatNotForDisabledFirstLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook, OnlyIn: rec.onlyIn},
	})

	// Gus's account was created ahead in Lakeside, with no password, so the sign-in
	// through Lakeside's directory links his identity for the first time; his account is
	// disabled. The link is written and the sign-in refused, as before, and the
	// directory's default seat is not handed to the hook.
	const name = "gus@lakeside.edu"
	gus := createUser(ctx, t, in.Driver, name)
	rec.roleAhead(gus.ID, "lakeside")
	if err := in.Driver.DeactivateUser(ctx, gus.ID); err != nil {
		t.Fatalf("DeactivateUser() error = %v", err)
	}
	identity := entra("oid-gus", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedUserDisabled, signin.ErrUserDisabled)
	assertLinkedTo(ctx, t, in.Driver, identity, gus.ID, "tenant-1")
	if got := rec.times(name); got != 0 {
		t.Errorf("the seat hook was called %d time(s) for a disabled account at the sign-in that linked it, want never", got)
	}
	if value, found := h.Recorded(ctx, t, in.Raw, "seated:"+name); found {
		t.Errorf("HookRecords[seated:%s] = %q after a disabled account's first sign-in through the directory, want nothing", name, value)
	}
}

func testDirectorySeatNotForDisabledConfirmedLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})

	// Kim's account has a password and is disabled. The link her password confirms is
	// made, as before, and the directory's default seat is not handed to the hook.
	const name = "kim@lakeside.edu"
	kim := passwordUser(ctx, t, in.Driver, name)
	if err := in.Driver.DeactivateUser(ctx, kim.ID); err != nil {
		t.Fatalf("DeactivateUser() error = %v", err)
	}
	identity := entra("oid-kim", name)
	if err := in.LinkConfirmed(ctx, kim.ID, name, identity, "tenant-1"); err != nil {
		t.Fatalf("LinkConfirmed() error = %v", err)
	}
	assertLinkedTo(ctx, t, in.Driver, identity, kim.ID, "tenant-1")
	if got := rec.times(name); got != 0 {
		t.Errorf("the seat hook was called %d time(s) for a disabled account's confirmed link, want never", got)
	}
	if value, found := h.Recorded(ctx, t, in.Raw, "seated:"+name); found {
		t.Errorf("HookRecords[seated:%s] = %q after a disabled account's confirmed link, want nothing", name, value)
	}
}

func testDirectorySeatBeyondInvitationAtLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	acc := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open, Accept: acc.hook,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook},
	})

	// Ada's identity is linked through Lakeside's directory. She follows an invitation
	// into Lakeside that names its default role, Member: the invitation gives it, and
	// the hook is not handed the same seat in the same transaction.
	const name = "ada@lakeside.edu"
	ada := createUser(ctx, t, in.Driver, name)
	mustLink(ctx, t, in.Driver, ada.ID, entra("oid-ada", name))
	invitation := insertInvitation(ctx, t, in.Driver, name, "lakeside", []string{"Member"}, tokenHash(1))
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-ada", name), Invitation: invitationOf(invitation)}
	if err := signInRequest(ctx, in, req); err != nil {
		t.Fatalf("sign-in with the invitation error = %v", err)
	}
	if req.User == nil || req.User.Source != sessioninfo.UserExistingLink {
		t.Errorf("sign-in user = %+v, want the existing link", req.User)
	}
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in that carried it, want accepted")
	}
	assertAccepted(t, acc, "accepted:"+name, ada.ID.String()+" "+name+" lakeside Member")
	if got := rec.times(name); got != 0 {
		t.Errorf("the seat hook was called %d time(s) with the seat the invitation gives, want never", got)
	}
}

// twoTenants is a directory reader for an application with two tenants, each the owner
// of its own directories: north declares the Entra tenant tenant-1 and the Google
// Workspace lakeside, south declares the Entra tenant tenant-2, each with the default
// seat Member, and no tenant declares any other directory.
func twoTenants(_ context.Context, _ HookTx, directory signin.Directory) ([]signin.DeclaredDirectory, error) {
	switch directory {
	case signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tenant-1"}, signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "lakeside"}:
		return []signin.DeclaredDirectory{{Tenant: "north", DefaultSeat: "Member"}}, nil
	case signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tenant-2"}:
		return []signin.DeclaredDirectory{{Tenant: "south", DefaultSeat: "Member"}}, nil
	default:
		return nil, nil
	}
}

// throughConnection is identity as it came through connection, as entra's identity of
// another Entra tenant than tenant-1 does.
func throughConnection(identity *sessioninfo.Identity, connection string) *sessioninfo.Identity {
	identity.Connection = connection

	return identity
}

// confirmedThrough links identity to user the way a person with a password is linked
// through a directory: the sign-in waits for the password, with tenant on the link to
// be made, and the password confirms the link.
func confirmedThrough(ctx context.Context, t *testing.T, in *AuthInstance, user *dbtype.SessionUser, identity *sessioninfo.Identity, tenant string) {
	t.Helper()

	signingIn := *identity
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, Identity: &signingIn}
	assertConfirmationPending(ctx, t, in.Driver, signInRequest(ctx, in, req), identity, user, tenant)
	if err := in.LinkConfirmed(ctx, user.ID, user.Username, identity, tenant); err != nil {
		t.Fatalf("LinkConfirmed() error = %v", err)
	}
	assertLinkedTo(ctx, t, in.Driver, identity, user.ID, tenant)
}

func testSecondDirectoryRefusedBeforeConfirmation(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open, Admission: &DirectoryAdmission{Directories: twoTenants, Seat: rec.hook},
	})

	// Kim has a password and signs in through north's directory, which seats her in
	// north.
	const name = "kim@lakeside.edu"
	kim := passwordUser(ctx, t, in.Driver, name)
	confirmedThrough(ctx, t, in, kim, entra("oid-kim", name), "tenant-1")

	// A sign-in under her name through south's directory is refused at once: no
	// confirmation is asked for, nothing is linked, and south seats nothing.
	south := throughConnection(entra("oid-kim-south", name), "tenant-2")
	signingIn := *south
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: &signingIn}
	err := signInRequest(ctx, in, req)
	var pending *signin.PendingSignInError
	if errors.As(err, &pending) {
		t.Fatalf("sign-in through a second tenant's directory waits for a confirmation (%+v), want a refusal", pending)
	}
	assertRefused(t, err, sessioninfo.RefusedSecondDirectory, signin.ErrLinkSecondDirectory)
	assertNoLink(ctx, t, in.Driver, south)
	want := []signin.DirectorySeat{{Tenant: "north", Role: "Member"}}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats after the refused sign-in (-want +got):\n%s", diff)
	}
	if got := rec.times(name); got != 1 {
		t.Errorf("the seat hook gave seats %d time(s), want once for north's link", got)
	}
}

func testSecondDirectoryRefusedAtConfirmedLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open, Admission: &DirectoryAdmission{Directories: twoTenants, Seat: rec.hook},
	})

	// Kim, who has a password and no link yet, signs in through south's directory: the
	// sign-in waits for her password.
	const name = "kim@lakeside.edu"
	kim := passwordUser(ctx, t, in.Driver, name)
	south := throughConnection(entra("oid-kim-south", name), "tenant-2")
	signingIn := *south
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: &signingIn}
	assertConfirmationPending(ctx, t, in.Driver, signInRequest(ctx, in, req), south, kim, "tenant-2")

	// Before she confirms it, she signs in through north's directory and confirms that
	// link.
	confirmedThrough(ctx, t, in, kim, entra("oid-kim", name), "tenant-1")

	// The confirmation south's sign-in waited for now links nothing and seats nothing.
	err := in.LinkConfirmed(ctx, kim.ID, name, south, "tenant-2")
	assertRefused(t, err, sessioninfo.RefusedSecondDirectory, signin.ErrLinkSecondDirectory)
	assertNoLink(ctx, t, in.Driver, south)
	want := []signin.DirectorySeat{{Tenant: "north", Role: "Member"}}
	if diff := cmp.Diff(want, rec.of(name)); diff != "" {
		t.Errorf("seats after the refused confirmation (-want +got):\n%s", diff)
	}
	if got := rec.times(name); got != 1 {
		t.Errorf("the seat hook gave seats %d time(s), want once for north's link", got)
	}
}

func testSecondDirectoryRefusedForRegistrationRule(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	var kimID impulse.UUID
	// The registration's own rule links any sign-in through south's directory to Kim's
	// account, trusted, and hands every other to the standard rule.
	rule := func(_ context.Context, _ HookTx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
		if req.Identity.Connection == "tenant-2" {
			return signin.Link(kimID, true, signin.Tenant("tenant-2")), nil
		}

		return signin.Standard(), nil
	}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open, Resolve: rule, Admission: &DirectoryAdmission{Directories: twoTenants, Seat: rec.hook},
	})

	const name = "kim@lakeside.edu"
	kim := passwordUser(ctx, t, in.Driver, name)
	kimID = kim.ID
	confirmedThrough(ctx, t, in, kim, entra("oid-kim", name), "tenant-1")

	// The rule's trusted link of south's identity to the account that signs in through
	// north's directory is refused, and nothing is linked.
	south := throughConnection(entra("oid-kim-south", name), "tenant-2")
	signingIn := *south
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: &signingIn}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedSecondDirectory, signin.ErrLinkSecondDirectory)
	assertNoLink(ctx, t, in.Driver, south)
	if got := rec.times(name); got != 1 {
		t.Errorf("the seat hook gave seats %d time(s), want once for north's link", got)
	}
}

func testSecondDirectoryNotRefused(ctx context.Context, t *testing.T, h *AuthHarness) {
	const name = "kim@lakeside.edu"
	tests := []struct {
		name     string
		readsAll bool
		second   *sessioninfo.Identity
		tenant   string
	}{
		{
			name: "a second directory no tenant declares", readsAll: true,
			second: throughConnection(entra("oid-kim-3", name), "tenant-3"), tenant: "tenant-3",
		},
		{
			name: "a second directory of the same tenant", readsAll: true,
			second: google("sub-kim", name), tenant: "lakeside",
		},
		{
			name: "a personal account", readsAll: true,
			second: throughConnection(entra("oid-kim-personal", name), signin.MicrosoftPersonalAccountsConnection), tenant: signin.MicrosoftPersonalAccountsConnection,
		},
		{
			name: "an application that reads no directories", readsAll: false,
			second: throughConnection(entra("oid-kim-south", name), "tenant-2"), tenant: "tenant-2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AuthConfig{Auth: true, Accounts: Open}
			if tt.readsAll {
				cfg.Admission = &DirectoryAdmission{Directories: twoTenants, Seat: newSeatings().hook}
			}
			in := h.New(ctx, t, AuthWithAppTables, cfg)

			// Kim signs in through north's directory, then through the second one: each
			// sign-in waits for her password, and each confirmation links.
			kim := passwordUser(ctx, t, in.Driver, name)
			confirmedThrough(ctx, t, in, kim, entra("oid-kim", name), "tenant-1")
			confirmedThrough(ctx, t, in, kim, tt.second, tt.tenant)
		})
	}
}

func testDirectorySeatHookFailsAtLink(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	failure := errors.New("the default seat could not be given")
	rec.failing(failure)
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member"}), Seat: rec.hook, OnlyIn: rec.onlyIn},
	})

	// A user created ahead in Lakeside, with no password, is linked by the sign-in
	// through Lakeside's directory itself: the seat cannot be given, so the transaction
	// fails and the link is not made.
	const name = "pat@lakeside.edu"
	pat := createUser(ctx, t, in.Driver, name)
	rec.roleAhead(pat.ID, "lakeside")
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
		Auth: true, Accounts: Open, Policy: policy,
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

	// A second sign-in finds the link and hands the hook the default seat again, which
	// alone reads whether the person holds anything in the tenant.
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
	if err := signInRequest(ctx, in, again); err != nil {
		t.Fatalf("second sign-in error = %v", err)
	}
	if diff := cmp.Diff(slices.Repeat(want, 2), rec.of(name)); diff != "" {
		t.Errorf("seats handed to the hook by the two sign-ins (-want +got):\n%s", diff)
	}
}

func testDirectoryInvitationOnly(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newSeatings()
	acc := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open, Accept: acc.hook,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside"}), Seat: rec.hook, OnlyIn: rec.onlyIn},
	})

	// The stranger is refused and nothing is written, though the method is open to
	// sign-up.
	strangerIdentity := entra("oid-new", "new@lakeside.edu")
	stranger := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "new@lakeside.edu", Identity: strangerIdentity}
	assertRefused(t, signInRequest(ctx, in, stranger), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
	assertNoUser(ctx, t, in.Driver, "new@lakeside.edu")
	if seats := rec.of("new@lakeside.edu"); seats != nil {
		t.Errorf("seats granted to the refused stranger = %v, want none", seats)
	}

	// A person given a role in the tenant ahead of their first sign-in is no stranger: a
	// role is held by an account, and the tenant's own directory vouches for the
	// tenant's own people, so the user under the sign-in name is linked. The declaration
	// names no default seat, so the link gives nothing.
	kim := createUser(ctx, t, in.Driver, "kim@lakeside.edu")
	rec.roleAhead(kim.ID, "lakeside")
	kimIdentity := entra("oid-kim", "kim@lakeside.edu")
	kimReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "kim@lakeside.edu", Identity: kimIdentity}
	assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, kimReq), kimReq, kimIdentity, kim, "tenant-1")
	if seats := rec.of("kim@lakeside.edu"); seats != nil {
		t.Errorf("seats granted to the user created ahead = %v, want none", seats)
	}

	// Kim signs in again through the link: the tenant names no default seat, so the
	// hook is not called.
	kimAgain := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "kim@lakeside.edu", Identity: entra("oid-kim", "kim@lakeside.edu")}
	if err := signInRequest(ctx, in, kimAgain); err != nil {
		t.Fatalf("Kim's sign-in through the link error = %v", err)
	}
	if kimAgain.User == nil || kimAgain.User.Source != sessioninfo.UserExistingLink {
		t.Errorf("Kim's sign-in user = %+v, want the existing link", kimAgain.User)
	}
	if got := rec.times("kim@lakeside.edu"); got != 0 {
		t.Errorf("the seat hook was called %d time(s) for a linked identity whose tenant names no default seat, want never", got)
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
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, Admission: &DirectoryAdmission{Directories: declaring(), Seat: rec.hook}})

	// No tenant declares the directory: the registration's rule decides as before.
	identity := entra("oid-new", "new@lakeside.edu")
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "new@lakeside.edu", Identity: identity}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedNotInvited, signin.ErrNotInvited)
	assertNoUser(ctx, t, in.Driver, "new@lakeside.edu")
}

func testDirectorySeatHookMissing(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: Open,
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
		Auth: true, Accounts: Open, Policy: policy,
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
