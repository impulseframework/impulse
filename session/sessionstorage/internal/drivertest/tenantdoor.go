package drivertest

// A tenant's sign-in settings restrict that tenant's own door and nobody else's, on
// both drivers, through the real sign-in flow: the account rules, the directory
// admission, the invitation's acceptance and the tenant policy together. A session
// reaches every tenant that seats the person, except a tenant that is directory only
// and whose directory is not the one the person signed in through; a tenant that sets
// nothing admits everyone it seats, in an application that keeps declarations too.

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
	return &seats{roleStore: roleStore{held: map[accesstypes.User]accesstypes.RoleCollection{}}}
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
	// accounts is the registrations' standard account rule, members of the directory
	// when it is not set, and resolve the registrations' own account rule, nil for none.
	accounts signin.StandardAccountRule
	resolve  func(ctx context.Context, tx HookTx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error)

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
// application's reach is decided by (signin.TenantDeclaration.Admits).
func (w *doorWorld) reached(userID impulse.UUID, identity *sessioninfo.Identity) []accesstypes.Domain {
	var reached []accesstypes.Domain
	for _, tenant := range w.seats.tenants(userID) {
		var declaration *signin.TenantDeclaration
		if !w.gone(tenant) {
			declaration = w.declared[tenant]
		}
		if declaration.Admits(identity.Method, identity.Connection) {
			reached = append(reached, tenant)
		}
	}

	return reached
}

// start builds the case's Auth over a fresh database: members of a directory are
// provisioned by the registration's rule (or the case's own, accounts), the tenants'
// declarations decide a stranger first, an invitation's roles and a directory's default
// seat are held once they commit, and the tenant policy decides the sign-in.
func (w *doorWorld) start(ctx context.Context, t *testing.T, h *AuthHarness) *AuthInstance {
	t.Helper()

	accounts := w.accounts
	if accounts == "" {
		accounts = signin.MembersOfDirectory
	}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{
		Auth: true, Resolve: w.resolve, Accounts: accounts, Accept: w.seats.accept,
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

	t.Run("a person seated in a directory-only tenant and an open one reaches both through the directory and the open one alone by password", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{"lakeside": {Directories: []signin.Directory{lakesideDirectory}, DirectoryOnly: true}}, nil)
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

		if _, err := passwordSignIn(ctx, in, kim, sessioninfo.ReasonLogin); err != nil {
			t.Errorf("password sign-in error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, kim.ID, password(kim.ID), "riverside")

		// The open tenant unseats her: the directory-only tenant is all that seats her, so
		// a password reaches nothing and is refused, and the directory still enters.
		w.seats.unseat(kim.ID, "riverside")
		_, err := passwordSignIn(ctx, in, kim, sessioninfo.ReasonLogin)
		assertRefused(t, err, sessioninfo.RefusedDirectoryOnly, signin.ErrDirectoryOnly)
		if err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: entra("oid-kim", name)}); err != nil {
			t.Errorf("sign-in through the directory after the open tenant unseated her error = %v (code %q), want allowed", err, sessioninfo.LoginRefusalCodeOf(err))
		}
		assertReached(t, w, kim.ID, identity, "lakeside")
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

		through := []*sessioninfo.Identity{
			entra("oid-sam", name),
			lakeside("idp_sam"),
			{Method: sessioninfo.MethodGoogle, Connection: "hilltop.example", Subject: "sub-sam", Email: name, SignInName: name, EmailVerified: true},
			{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: "sub-sam-personal", Email: name, SignInName: name, EmailVerified: true},
			{Method: sessioninfo.MethodOIDC, Connection: "https://id.elsewhere.example", Subject: "sub-sam", Email: name, SignInName: name},
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
	})
}
