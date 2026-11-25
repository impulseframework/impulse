package drivertest

// A tenant's sign-in settings restrict that tenant's own door and nobody else's, on
// both drivers, through the real sign-in flow: the account rules, the directory
// admission, the invitation's acceptance and the tenant policy together. A session
// reaches every tenant that seats the person, except a tenant that is directory only
// and whose directory is not the one the person signed in through; a tenant that sets
// nothing admits everyone it seats, in an application that keeps declarations too. An
// account that signs in through a directory-only tenant's directory is the exception:
// it signs in through that directory alone, whichever tenants seat the person.

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// seats stands in for an application's permission engine in the tenant-door cases: the
// memberships each account holds, by the id of the account, as the tenant policy reads
// them. The directory seat hook and the invitation acceptor write to it once their
// transaction has committed, the way an application's hooks tell its engine, and an
// administrator's unseating and a tenant's deletion take memberships away.
type seats struct {
	roleStore

	mu sync.Mutex
}

func newSeats() *seats {
	return &seats{roleStore: roleStore{held: map[accesstypes.User]accesstypes.RoleCollection{}, administers: map[accesstypes.User][]accesstypes.Domain{}}}
}

// UserRoles answers the memberships the account holds, as the tenant policy asks.
func (s *seats) UserRoles(ctx context.Context, user accesstypes.User, scopes ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.roleStore.UserRoles(ctx, user, scopes...)
}

// give seats the account with role in tenant.
func (s *seats) give(userID impulse.UUID, tenant accesstypes.Domain, role accesstypes.Role) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user := accesstypes.User(userID.String())
	if s.held[user] == nil {
		s.held[user] = accesstypes.RoleCollection{}
	}
	scope := accesstypes.DomainPolicyScope(tenant)
	if !slices.Contains(s.held[user][scope], role) {
		s.held[user][scope] = append(s.held[user][scope], role)
	}
}

// unseat takes every role the account holds in tenant away.
func (s *seats) unseat(userID impulse.UUID, tenant accesstypes.Domain) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.held[accesstypes.User(userID.String())], accesstypes.DomainPolicyScope(tenant))
}

// administer seats the account as tenant's administrator: the administrator's role held
// in the tenant, and the tenant among those the account administers.
func (s *seats) administer(userID impulse.UUID, tenant accesstypes.Domain) {
	s.give(userID, tenant, "TenantAdministrator")

	s.mu.Lock()
	defer s.mu.Unlock()

	user := accesstypes.User(userID.String())
	s.administers[user] = append(s.administers[user], tenant)
}

// TenantsAdministered answers the tenants the account administers, as the tenant
// policy asks.
func (s *seats) TenantsAdministered(ctx context.Context, user accesstypes.User) ([]accesstypes.Domain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.roleStore.TenantsAdministered(ctx, user)
}

// tenants lists the tenants the account holds a role in, sorted.
func (s *seats) tenants(userID impulse.UUID) []accesstypes.Domain {
	s.mu.Lock()
	defer s.mu.Unlock()

	var tenants []accesstypes.Domain
	for scope, roles := range s.held[accesstypes.User(userID.String())] {
		if domain, ok := scope.Domain(); ok && len(roles) > 0 {
			tenants = append(tenants, domain)
		}
	}
	slices.Sort(tenants)

	return tenants
}

// onlyIn reports whether tenant is the only place the account holds anything, as the
// rule on a first link made on a name alone asks: it holds a role in tenant itself, and
// none in another tenant, in every tenant or outside the tenants.
func (s *seats) onlyIn(_ context.Context, _ HookTx, userID impulse.UUID, tenant accesstypes.Domain) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return onlyIn(s.held[accesstypes.User(userID.String())], tenant), nil
}

// onlyIn reports whether every role of held is held in tenant itself, and one is.
func onlyIn(held accesstypes.RoleCollection, tenant accesstypes.Domain) bool {
	own := accesstypes.DomainPolicyScope(tenant)
	for scope, roles := range held {
		if len(roles) > 0 && scope != own {
			return false
		}
	}

	return len(held[own]) > 0
}

// seat is the directory seat hook: the default seats are held once the transaction
// that provisions or links the person has committed.
func (s *seats) seat(_ context.Context, _ HookTx, userID impulse.UUID, _ string, granted []signin.DirectorySeat) (func(ctx context.Context), error) {
	return func(context.Context) {
		for _, seat := range granted {
			s.give(userID, seat.Tenant, seat.Role)
		}
	}, nil
}

// accept is the invitation acceptor: the invitation's roles are held in the tenant it
// names once the transaction that consumes it has committed.
func (s *seats) accept(_ context.Context, _ HookTx, userID impulse.UUID, _ string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
	return func(context.Context) {
		for _, role := range inv.RoleNames {
			s.give(userID, accesstypes.Domain(inv.TenantID), accesstypes.Role(role))
		}
	}, nil
}

// doorWorld is one tenant-door case's application: the tenants' declarations, the
// tenants that were deleted since, and the memberships.
type doorWorld struct {
	seats    *seats
	declared map[accesstypes.Domain]*signin.TenantDeclaration
	// defaultSeats are the default seat each tenant's directory row names, "" for none.
	defaultSeats map[accesstypes.Domain]accesstypes.Role
	// accounts is what was chosen for the sign-in method, open to sign-up when it is not
	// set; seeded says the registration declared its provider's names the application's
	// own; and resolve is the registration's own account rule, nil for none.
	accounts Accounts
	seeded   bool
	resolve  func(ctx context.Context, tx HookTx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error)
	// noPassword says the application's Auth registers no password method.
	noPassword bool
	// admits is whom the sign-in method's registration admits, one organization when it
	// is not set.
	admits Admits

	mu      sync.Mutex
	deleted map[accesstypes.Domain]bool
}

func newDoorWorld(declared map[accesstypes.Domain]*signin.TenantDeclaration, defaultSeats map[accesstypes.Domain]accesstypes.Role) *doorWorld {
	return &doorWorld{seats: newSeats(), declared: declared, defaultSeats: defaultSeats, deleted: map[accesstypes.Domain]bool{}}
}

// gone reports whether tenant was deleted.
func (w *doorWorld) gone(tenant accesstypes.Domain) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.deleted[tenant]
}

// declaration is the application's reader of a tenant's declaration, in the sign-in's
// transaction: the row recorded under the tenant, nothing for a tenant that was
// deleted or that declares nothing.
func (w *doorWorld) declaration(ctx context.Context, tx HookTx, tenant accesstypes.Domain) (*signin.TenantDeclaration, error) {
	if w.gone(tenant) {
		return nil, nil
	}

	return readDeclaration(ctx, tx, tenant)
}

// directories is the application's reader of the tenant that has a directory: the one
// tenant whose declaration carries it, with the default seat its row names.
func (w *doorWorld) directories(_ context.Context, _ HookTx, directory signin.Directory) ([]signin.DeclaredDirectory, error) {
	var declared []signin.DeclaredDirectory
	for tenant, declaration := range w.declared {
		if w.gone(tenant) || !slices.Contains(declaration.Directories, directory) {
			continue
		}
		declared = append(declared, signin.DeclaredDirectory{Tenant: tenant, DefaultSeat: w.defaultSeats[tenant]})
	}

	return declared, nil
}

// declare rewrites tenant's declaration between two sign-ins: the row the reader reads,
// and what the case's reach asks.
func (w *doorWorld) declare(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, tenant accesstypes.Domain, declaration *signin.TenantDeclaration) {
	t.Helper()

	w.declared[tenant] = declaration
	h.Record(ctx, t, in.Raw, declarationKeyPrefix+string(tenant), declarationValue(declaration))
}

// remove deletes tenant: its declaration and its directory go, and so does every seat
// held in it.
func (w *doorWorld) remove(tenant accesstypes.Domain, seated ...impulse.UUID) {
	w.mu.Lock()
	w.deleted[tenant] = true
	w.mu.Unlock()
	for _, userID := range seated {
		w.seats.unseat(userID, tenant)
	}
}

// reached lists the tenants that seat the account and that a session established as
// identity says reaches: each tenant's declaration asked with the one function the
// application's reach is decided by (signin.TenantDeclaration.Admits), after whether
// the tenant is disabled, which no session of these cases reaches: none is a super
// administrator's.
func (w *doorWorld) reached(userID impulse.UUID, identity *sessioninfo.Identity) []accesstypes.Domain {
	var reached []accesstypes.Domain
	for _, tenant := range w.seats.tenants(userID) {
		var declaration *signin.TenantDeclaration
		if !w.gone(tenant) {
			declaration = w.declared[tenant]
		}
		if !declaration.IsDisabled() && declaration.Admits(identity.Method, identity.Connection) {
			reached = append(reached, tenant)
		}
	}

	return reached
}

// start builds the case's Auth over a fresh database: the sign-in method is open to
// sign-up, so its row gives a person with no account one (or is what the case chose,
// accounts), the tenants' declarations decide a stranger first, an invitation's roles
// and a directory's default seat are held once they commit, and the tenant policy
// decides the sign-in.
func (w *doorWorld) start(ctx context.Context, t *testing.T, h *AuthHarness) *AuthInstance {
	t.Helper()

	accounts := w.accounts
	if accounts == NoStandardRule {
		accounts = Open
	}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Resolve: w.resolve, Accounts: accounts, SeededUsers: w.seeded, NoPassword: w.noPassword, Admits: w.admits, Accept: w.seats.accept,
		Policy:    signin.TenantPolicy(w.seats, w.declaration),
		Admission: &DirectoryAdmission{Directories: w.directories, Seat: w.seats.seat, OnlyIn: w.seats.onlyIn},
	})
	for tenant, declaration := range w.declared {
		h.Record(ctx, t, in.Raw, declarationKeyPrefix+string(tenant), declarationValue(declaration))
	}

	return in
}

// assertReached fails unless a session established as identity reaches want among the
// tenants that seat the account.
func assertReached(t *testing.T, w *doorWorld, userID impulse.UUID, identity *sessioninfo.Identity, want ...accesstypes.Domain) {
	t.Helper()

	if diff := cmp.Diff(want, w.reached(userID, identity), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("tenants a %s session through %q reaches (-want +got):\n%s", identity.Method, identity.Connection, diff)
	}
}

func testTenantDoor(_ context.Context, t *testing.T, h *AuthHarness) {
	// Lakeside's directory is the Entra tenant the directory's identities carry
	// (entra's connection); the other directories are other organizations'.
	lakesideDirectory := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tenant-1"}
	hilltopDirectory := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "hilltop.example"}
	password := func(userID impulse.UUID) *sessioninfo.Identity {
		return &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
	}

	t.Run("a stranger invited by one tenant, from the directory of a tenant that names a default seat, is seated in both and reaches both", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(
			map[accesstypes.Domain]*signin.TenantDeclaration{"lakeside": {Directories: []signin.Directory{lakesideDirectory}}},
			map[accesstypes.Domain]accesstypes.Role{"lakeside": "Member"},
		)
		in := w.start(ctx, t, h)

		const name = "pat@lakeside.edu"
		invitation := insertInvitation(ctx, t, in.Driver, name, "riverside", []string{"Teacher"}, tokenHash(1))
		identity := entra("oid-pat", name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(invitation)}
		err := signInRequest(ctx, in, req)
		assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
		if !req.Invitation.Accepted {
			t.Error("Invitation.Accepted = false after the sign-in, want accepted")
		}
		want := accesstypes.RoleCollection{
			accesstypes.DomainPolicyScope("lakeside"):  {"Member"},
			accesstypes.DomainPolicyScope("riverside"): {"Teacher"},
		}
		held, err := w.seats.UserRoles(ctx, accesstypes.User(req.UserID.String()))
		if err != nil {
			t.Fatalf("UserRoles() error = %v", err)
		}
		if diff := cmp.Diff(want, held); diff != "" {
			t.Errorf("seats after the first sign-in (-want +got):\n%s", diff)
		}
		assertReached(t, w, req.UserID, identity, "lakeside", "riverside")
	})

	t.Run("a stranger invited by one tenant, from the directory of a tenant that names no default seat, is seated in the inviting tenant alone and enters it", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{"lakeside": {Directories: []signin.Directory{lakesideDirectory}}}, nil)
		in := w.start(ctx, t, h)

		const name = "pat@lakeside.edu"
		invitation := insertInvitation(ctx, t, in.Driver, name, "riverside", []string{"Teacher"}, tokenHash(1))
		identity := entra("oid-pat", name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(invitation)}
		err := signInRequest(ctx, in, req)
		assertProvisioned(ctx, t, in.Driver, err, req, identity, "tenant-1")
		if diff := cmp.Diff([]accesstypes.Domain{"riverside"}, w.seats.tenants(req.UserID)); diff != "" {
			t.Errorf("tenants that seat the invited person (-want +got):\n%s", diff)
		}
		assertReached(t, w, req.UserID, identity, "riverside")

		// The invitation is spent: the next sign-in through the directory finds the link,
		// is admitted by the tenant that seats the person, and seats nothing more.
		again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
		if err := signInRequest(ctx, in, again); err != nil {
			t.Errorf("second sign-in error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		if diff := cmp.Diff([]accesstypes.Domain{"riverside"}, w.seats.tenants(req.UserID)); diff != "" {
			t.Errorf("tenants that seat the person after the second sign-in (-want +got):\n%s", diff)
		}
	})

	t.Run("a stranger invited into a disabled tenant is refused with no_tenant and holds the invitation's role, and once the tenant is enabled signs in and reaches it", func(t *testing.T) {
		testDisabledTenantInvitation(t, h)
	})

	t.Run("an account that signs in through a directory-only tenant's directory is refused a password, though an open tenant seats the person too", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{
			"lakeside": {Directories: []signin.Directory{lakesideDirectory}, DirectoryOnly: true},
			"hilltop":  {Directories: []signin.Directory{hilltopDirectory}},
		}, nil)
		in := w.start(ctx, t, h)

		const name = "kim@lakeside.edu"
		kim := passwordUser(ctx, t, in.Driver, name)
		w.seats.give(kim.ID, "lakeside", "Member")
		w.seats.give(kim.ID, "riverside", "Teacher")
		identity := entra("oid-kim", name)
		mustLink(ctx, t, in.Driver, kim.ID, identity)

		button := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		if err := signInRequest(ctx, in, button); err != nil {
			t.Errorf("sign-in through the directory error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, kim.ID, identity, "lakeside", "riverside")

		// Lakeside's directory signs her account in, so Riverside's seat opens no other
		// way: a password, a personal account and another organization's directory are
		// refused, each with the code that is true of it.
		_, err := passwordSignIn(ctx, in, kim, sessioninfo.ReasonLogin)
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnly, signin.ErrDirectoryOnly)
		personal := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: "sub-kim-personal", Email: name, SignInName: name, EmailVerified: true}
		mustLink(ctx, t, in.Driver, kim.ID, personal)
		err = signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: personal})
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnlyPersonalAccount, signin.ErrDirectoryOnly)
		hilltop := &sessioninfo.Identity{Method: hilltopDirectory.Method, Connection: hilltopDirectory.Connection, Subject: "sub-kim", Email: name, SignInName: name, EmailVerified: true}
		mustLink(ctx, t, in.Driver, kim.ID, hilltop)
		err = signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: hilltop})
		assertRefused(t, err, sessioninfo.RefusedConnectionNotInTenant, signin.ErrConnectionNotInTenant)

		// Lakeside unseats her: her account still signs in through Lakeside's directory,
		// so the password is still refused, and the directory signs her in to Riverside.
		w.seats.unseat(kim.ID, "lakeside")
		_, err = passwordSignIn(ctx, in, kim, sessioninfo.ReasonLogin)
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnly, signin.ErrDirectoryOnly)
		if err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-kim", name)}); err != nil {
			t.Errorf("sign-in through the directory after Lakeside unseated her error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, kim.ID, identity, "riverside")

		// Lakeside turns directory only off: nothing was taken from her account, and the
		// password signs her in again.
		w.declare(ctx, t, h, in, "lakeside", &signin.TenantDeclaration{Directories: []signin.Directory{lakesideDirectory}})
		if _, err := passwordSignIn(ctx, in, kim, sessioninfo.ReasonLogin); err != nil {
			t.Errorf("password sign-in once Lakeside is not directory only error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, kim.ID, password(kim.ID), "riverside")
	})

	t.Run("a person a directory-only tenant and an open one seat, whose account is linked through no directory, signs in with a password and reaches the open one alone", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{"lakeside": {Directories: []signin.Directory{lakesideDirectory}, DirectoryOnly: true}}, nil)
		in := w.start(ctx, t, h)

		const name = "joe@lakeside.edu"
		joe := passwordUser(ctx, t, in.Driver, name)
		w.seats.give(joe.ID, "lakeside", "Member")
		w.seats.give(joe.ID, "riverside", "Teacher")

		if _, err := passwordSignIn(ctx, in, joe, sessioninfo.ReasonLogin); err != nil {
			t.Errorf("password sign-in error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, joe.ID, password(joe.ID), "riverside")

		// The open tenant unseats him: the directory-only tenant is all that seats him, so
		// a password reaches nothing and is refused.
		w.seats.unseat(joe.ID, "riverside")
		_, err := passwordSignIn(ctx, in, joe, sessioninfo.ReasonLogin)
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnly, signin.ErrDirectoryOnly)
	})

	t.Run("the directory-only tenant's own administrator keeps the password, held for the mailed code, and an administrator of another directory-only tenant does not", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{
			"lakeside": {Directories: []signin.Directory{lakesideDirectory}, DirectoryOnly: true},
			"hilltop":  {Directories: []signin.Directory{hilltopDirectory}, DirectoryOnly: true},
		}, nil)
		in := w.start(ctx, t, h)

		// Ann administers Lakeside, whose directory signs her account in.
		const annName = "ann@lakeside.edu"
		ann := passwordUser(ctx, t, in.Driver, annName)
		w.seats.administer(ann.ID, "lakeside")
		mustLink(ctx, t, in.Driver, ann.ID, entra("oid-ann", annName))
		_, err := passwordSignIn(ctx, in, ann, sessioninfo.ReasonLogin)
		assertHeldForCode(t, err)

		// Hal administers Hilltop, directory only with its own directory, and is a member
		// of Lakeside, whose directory signs his account in: administering another tenant
		// keeps no password for him.
		const halName = "hal@lakeside.edu"
		hal := passwordUser(ctx, t, in.Driver, halName)
		w.seats.administer(hal.ID, "hilltop")
		w.seats.give(hal.ID, "lakeside", "Member")
		mustLink(ctx, t, in.Driver, hal.ID, entra("oid-hal", halName))
		_, err = passwordSignIn(ctx, in, hal, sessioninfo.ReasonLogin)
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnly, signin.ErrDirectoryOnly)
	})

	t.Run("the administrator of a disabled directory-only tenant, whose directory signs their account in, is refused a password and not held for the code", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{
			"lakeside": {Directories: []signin.Directory{lakesideDirectory}, DirectoryOnly: true, Disabled: true},
		}, nil)
		in := w.start(ctx, t, h)

		// Ann administers Lakeside, whose directory signs her account in, as in the case
		// above where she is held for the code; Lakeside is disabled, and gives her no
		// way in.
		const annName = "ann@lakeside.edu"
		ann := passwordUser(ctx, t, in.Driver, annName)
		w.seats.administer(ann.ID, "lakeside")
		mustLink(ctx, t, in.Driver, ann.ID, entra("oid-ann", annName))
		_, err := passwordSignIn(ctx, in, ann, sessioninfo.ReasonLogin)
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnly, signin.ErrDirectoryOnly)
	})

	t.Run("a tenant that sets nothing admits a seated person through each method, in an application whose other tenants declare", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// Lakeside and Hilltop keep declarations, Hilltop directory only; Riverside sets
		// nothing, and the person is seated there alone.
		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{
			"lakeside": {Directories: []signin.Directory{lakesideDirectory}},
			"hilltop":  {Directories: []signin.Directory{hilltopDirectory}, DirectoryOnly: true},
		}, nil)
		in := w.start(ctx, t, h)

		const name = "sam@elsewhere.example"
		sam := passwordUser(ctx, t, in.Driver, name)
		w.seats.give(sam.ID, "riverside", "Teacher")

		if _, err := passwordSignIn(ctx, in, sam, sessioninfo.ReasonLogin); err != nil {
			t.Errorf("password sign-in error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, sam.ID, password(sam.ID), "riverside")

		// Each identity is linked just before its own sign-in, and Hilltop's directory
		// comes last: once Sam's account is linked through the directory of Hilltop,
		// which is directory only, every other sign-in of his is refused, whichever
		// tenants seat him.
		through := []*sessioninfo.Identity{
			entra("oid-sam", name),
			lakeside("idp_sam"),
			{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: "sub-sam-personal", Email: name, SignInName: name, EmailVerified: true},
			{Method: sessioninfo.MethodOIDC, Connection: "https://id.elsewhere.example", Subject: "sub-sam", Email: name, SignInName: name},
			{Method: sessioninfo.MethodGoogle, Connection: "hilltop.example", Subject: "sub-sam", Email: name, SignInName: name, EmailVerified: true},
		}
		for _, identity := range through {
			mustLink(ctx, t, in.Driver, sam.ID, identity)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
			if err := signInRequest(ctx, in, req); err != nil {
				t.Errorf("sign-in by %s through %q error = %v (code %q), want allowed", identity.Method, identity.Connection, err, sessioninfo.LoginRefusalCodeOf(err))
			}
			assertReached(t, w, sam.ID, identity, "riverside")
		}
	})

	t.Run("a session reaches all five open tenants that seat the person, whichever of them have a directory", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{
			"lakeside": {Directories: []signin.Directory{lakesideDirectory}},
			"hilltop":  {Directories: []signin.Directory{hilltopDirectory}},
			"seaview":  {},
		}, nil)
		in := w.start(ctx, t, h)

		const name = "lee@lakeside.edu"
		lee := passwordUser(ctx, t, in.Driver, name)
		five := []accesstypes.Domain{"hilltop", "lakeside", "meadow", "riverside", "seaview"}
		for _, tenant := range five {
			w.seats.give(lee.ID, tenant, "Member")
		}
		identity := entra("oid-lee", name)
		mustLink(ctx, t, in.Driver, lee.ID, identity)

		if err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}); err != nil {
			t.Errorf("sign-in through the directory error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, lee.ID, identity, five...)
		if _, err := passwordSignIn(ctx, in, lee, sessioninfo.ReasonLogin); err != nil {
			t.Errorf("password sign-in error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, lee.ID, password(lee.ID), five...)
	})

	t.Run("the tenant whose directory a person signs in through is deleted: they sign in as before and reach the tenants that still seat them", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(
			map[accesstypes.Domain]*signin.TenantDeclaration{"lakeside": {Directories: []signin.Directory{lakesideDirectory}, DirectoryOnly: true}},
			map[accesstypes.Domain]accesstypes.Role{"lakeside": "Member"},
		)
		in := w.start(ctx, t, h)

		// Pat's first sign-in through Lakeside's directory seats them there by its default
		// seat; Riverside seats them too.
		const name = "pat@lakeside.edu"
		identity := entra("oid-pat", name)
		first := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		err := signInRequest(ctx, in, first)
		assertProvisioned(ctx, t, in.Driver, err, first, identity, "tenant-1")
		w.seats.give(first.UserID, "riverside", "Teacher")
		assertReached(t, w, first.UserID, identity, "lakeside", "riverside")

		// Lakeside is deleted: its seats and its declaration go, and nothing else. The
		// link stands, so the same button signs Pat in, into the tenant that still seats
		// them.
		w.remove("lakeside", first.UserID)
		again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
		if err := signInRequest(ctx, in, again); err != nil {
			t.Errorf("sign-in after the tenant was deleted error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		if again.UserID != first.UserID {
			t.Errorf("sign-in after the tenant was deleted is user %s, want the same account %s", again.UserID, first.UserID)
		}
		assertReached(t, w, first.UserID, identity, "riverside")
		// The link stands and the directory is still listed, now with no tenant.
		assertDirectories(t, again, sessioninfo.UserDirectory{Method: lakesideDirectory.Method, Connection: lakesideDirectory.Connection})
	})

	t.Run("the sign-in's rule is told the directories an account signs in through", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{
			"lakeside": {Directories: []signin.Directory{lakesideDirectory}},
			"hilltop":  {Directories: []signin.Directory{hilltopDirectory}},
		}, nil)
		in := w.start(ctx, t, h)

		const name = "ana@elsewhere.example"
		ana := passwordUser(ctx, t, in.Driver, name)
		w.seats.give(ana.ID, "riverside", "Teacher")
		const issuer = "https://id.elsewhere.example"
		for _, identity := range []*sessioninfo.Identity{
			entra("oid-ana", name),
			entra("oid-ana-2", name),
			{Method: hilltopDirectory.Method, Connection: hilltopDirectory.Connection, Subject: "sub-ana", Email: name, SignInName: name, EmailVerified: true},
			{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: "sub-ana-personal", Email: name, SignInName: name, EmailVerified: true},
			{Method: sessioninfo.MethodOIDC, Connection: issuer, Subject: "sub-ana", Email: name, SignInName: name},
		} {
			mustLink(ctx, t, in.Driver, ana.ID, identity)
		}

		req, err := passwordSignIn(ctx, in, ana, sessioninfo.ReasonLogin)
		if err != nil {
			t.Fatalf("password sign-in error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertDirectories(t, req,
			sessioninfo.UserDirectory{Method: lakesideDirectory.Method, Connection: lakesideDirectory.Connection, Tenants: []accesstypes.Domain{"lakeside"}},
			sessioninfo.UserDirectory{Method: hilltopDirectory.Method, Connection: hilltopDirectory.Connection, Tenants: []accesstypes.Domain{"hilltop"}},
			sessioninfo.UserDirectory{Method: sessioninfo.MethodOIDC, Connection: issuer},
		)
	})
}

// testDisabledTenantInvitation is testTenantDoor's case of a stranger invited into a
// disabled tenant: refused with no_tenant, holding the invitation's role, and admitted
// once the tenant is enabled.
func testDisabledTenantInvitation(t *testing.T, h *AuthHarness) {
	t.Parallel()
	ctx := t.Context()

	w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{"riverside": {Disabled: true}}, nil)
	in := w.start(ctx, t, h)

	const name = "pat@lakeside.edu"
	invitation := insertInvitation(ctx, t, in.Driver, name, "riverside", []string{"Teacher"}, tokenHash(1))
	identity := entra("oid-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(invitation)}
	err := signInRequest(ctx, in, req)
	assertRefused(t, err, sessioninfo.RefusedNoTenant, signin.ErrNoTenant)

	// The invitation is left alone: it is accepted, and the person holds its role in
	// the tenant they cannot enter while it is disabled.
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in, want accepted")
	}
	pat, err := in.Driver.UserByUserName(ctx, name)
	if err != nil {
		t.Fatalf("UserByUserName(%q) error = %v, want the account the sign-in made", name, err)
	}
	want := accesstypes.RoleCollection{accesstypes.DomainPolicyScope("riverside"): {"Teacher"}}
	held, err := w.seats.UserRoles(ctx, accesstypes.User(pat.ID.String()))
	if err != nil {
		t.Fatalf("UserRoles() error = %v", err)
	}
	if diff := cmp.Diff(want, held); diff != "" {
		t.Errorf("seats after the refused sign-in (-want +got):\n%s", diff)
	}
	assertReached(t, w, pat.ID, identity)

	// The tenant is enabled: the same person's next sign-in is admitted, and the
	// session reaches it with the role the invitation gave.
	w.declare(ctx, t, h, in, "riverside", &signin.TenantDeclaration{})
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-pat", name)}
	if err := signInRequest(ctx, in, again); err != nil {
		t.Errorf("sign-in once the tenant is enabled error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
	}
	assertReached(t, w, pat.ID, identity, "riverside")
}

// assertDirectories fails unless the sign-in told its policy want, in any order, as the
// directories the account signs in through.
func assertDirectories(t *testing.T, req *sessioninfo.NewSessionRequest, want ...sessioninfo.UserDirectory) {
	t.Helper()

	if req.User == nil {
		t.Fatal("the sign-in's request has no user, want one with the account's directories")
	}
	byConnection := cmpopts.SortSlices(func(a, b sessioninfo.UserDirectory) bool { return a.Connection < b.Connection })
	if diff := cmp.Diff(want, req.User.Directories, byConnection, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("directories the sign-in told its policy (-want +got):\n%s", diff)
	}
}
