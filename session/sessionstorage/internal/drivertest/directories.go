package drivertest

// The tenants' declarations of a directory at a stranger's first sign-in, on both
// drivers: a declared directory belongs to its tenant, whose declaration decides
// before the registration's standard rule (signinflow's declaredRule), a default seat
// admitting and seating the person, in the transaction that provisions them, and none
// refusing them by invitation only.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// seatings records what the directory seat hook granted, by username, the way an
// application seats a person: the hook writes through the transaction that provisions
// the user, so its row is committed with the account or not at all, and what it knows
// in memory (an application's permission engine) it is told once the transaction has
// committed. fail, while set, is the error the hook answers after its write.
type seatings struct {
	mu   sync.Mutex
	got  map[string][]signin.DirectorySeat
	fail error
}

func newSeatings() *seatings {
	return &seatings{got: map[string][]signin.DirectorySeat{}}
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
	}, s.fail
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

// heldRoles is a role store holding the memberships given, by username, and never
// written.
type heldRoles map[accesstypes.User]accesstypes.RoleCollection

func (r heldRoles) UserRoles(_ context.Context, user accesstypes.User, _ ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	return r[user], nil
}

func (heldRoles) ResolveRole(context.Context, accesstypes.PolicyScope, string) (accesstypes.Role, bool, error) {
	return "", false, errors.New("the sign-in never resolves a role")
}

func (heldRoles) AddUserRoles(context.Context, accesstypes.PolicyScope, accesstypes.User, ...accesstypes.Role) error {
	return errors.New("the sign-in never writes a membership")
}

func (heldRoles) DeleteUserRoles(context.Context, accesstypes.PolicyScope, accesstypes.User, ...accesstypes.Role) error {
	return errors.New("the sign-in never removes a membership")
}

// declaring is a directory reader that answers declared for every directory, nothing
// when none is given.
func declaring(declared ...signin.DeclaredDirectory) func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
	return func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
		return declared, nil
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
	roles := heldRoles{"lee@lakeside.edu": {accesstypes.DomainPolicyScope("lakeside"): {"Member"}}, "ops@lakeside.edu": {accesstypes.GlobalPolicyScope(): {"Administrator"}}}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Accounts: signin.MembersOfDirectory, Accept: acc.hook,
		Admission: &DirectoryAdmission{Directories: declaring(signin.DeclaredDirectory{Tenant: "lakeside"}), Roles: roles, Seat: rec.hook},
	})

	// A person the tenant seated ahead of their first sign-in, and the application's
	// own staff, are not strangers: provisioned under the sign-in name, no seat added.
	for _, name := range []string{"lee@lakeside.edu", "ops@lakeside.edu"} {
		identity := entra("oid-"+name, name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		assertProvisioned(ctx, t, in.Driver, signInRequest(ctx, in, req), req, identity, "tenant-1")
		if seats := rec.of(name); seats != nil {
			t.Errorf("seats granted to %s = %v, want none: the memberships stand", name, seats)
		}
	}

	// The stranger is refused and nothing is written, though the registration admits
	// members of the directory.
	strangerIdentity := entra("oid-new", "new@lakeside.edu")
	stranger := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "new@lakeside.edu", Identity: strangerIdentity}
	assertRefused(t, signInRequest(ctx, in, stranger), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
	assertNoUser(ctx, t, in.Driver, "new@lakeside.edu")
	if seats := rec.of("new@lakeside.edu"); seats != nil {
		t.Errorf("seats granted to the refused stranger = %v, want none", seats)
	}

	// A pre-created user under the sign-in name is linked, as before: the declaration
	// decides strangers alone.
	kim := createUser(ctx, t, in.Driver, "kim@lakeside.edu")
	kimIdentity := entra("oid-kim", "kim@lakeside.edu")
	kimReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "kim@lakeside.edu", Identity: kimIdentity}
	assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, kimReq), kimReq, kimIdentity, kim, "tenant-1")

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
