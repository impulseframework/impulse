package resource

import (
	"context"
	"slices"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// stubPermissions is a UserPermissions that grants everything it is asked and
// records what it was asked, so masking and routing can be asserted.
type stubPermissions struct {
	user       accesstypes.User
	digest     accesstypes.PermissionDigest
	domains    []accesstypes.Domain
	err        error
	checkPerms []accesstypes.Permission
	// checkResources are the resources each delegated check asked about, in order.
	checkResources []accesstypes.Resource
	// global makes the checker hold a grant in the global partition, which exempts
	// nobody from a tenant's sign-in settings.
	global bool
	// super makes the user a super administrator: a holder of the Super
	// Administrator role.
	super bool
	// administers are the tenants the user administers, administersErr fails the
	// question, and administersAsked counts how often it was asked, when set.
	administers      []accesstypes.Domain
	administersErr   error
	administersAsked *int
}

func (s *stubPermissions) AdministersTenant(_ context.Context, domain accesstypes.Domain) (bool, error) {
	if s.administersAsked != nil {
		*s.administersAsked++
	}
	if s.administersErr != nil {
		return false, s.administersErr
	}

	return slices.Contains(s.administers, domain), nil
}

func (s *stubPermissions) SuperAdministrator(context.Context) (bool, error) {
	if s.err != nil {
		return false, s.err
	}

	return s.super, nil
}

func (s *stubPermissions) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	s.checkPerms = append(s.checkPerms, perm)
	s.checkResources = append(s.checkResources, resources...)
	if s.err != nil {
		return nil, s.err
	}

	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Granted()
	}

	return decisions, nil
}

func (s *stubPermissions) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	if s.err != nil {
		return nil, s.err
	}

	return s.digest, nil
}

func (s *stubPermissions) HasGrants(_ context.Context, scope accesstypes.Scope) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	domain, ok := scope.Domain()
	if !ok {
		return s.global, nil
	}

	return slices.Contains(s.domains, domain), nil
}

func (s *stubPermissions) Domains(context.Context) ([]accesstypes.Domain, error) {
	return s.domains, nil
}

func (s *stubPermissions) Reaches(context.Context, accesstypes.Domain) (bool, error) {
	return true, nil
}

func (s *stubPermissions) User() accesstypes.User { return s.user }

// stubRolePermissions is a RolePermissions — deliberately without User(), as
// a role checker is not anyone — that grants everything it is asked, so the
// role route can be asserted to accept such a checker and to supply User()
// itself.
type stubRolePermissions struct {
	role accesstypes.Role
}

func (s *stubRolePermissions) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Granted()
	}

	return decisions, nil
}

func (s *stubRolePermissions) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{"documents": {accesstypes.Update: accesstypes.DigestGranted}}, nil
}

func (s *stubRolePermissions) HasGrants(_ context.Context, scope accesstypes.Scope) (bool, error) {
	domain, ok := scope.Domain()

	return ok && domain == "tenant", nil
}

func sessionCtx(username string, imp *sessioninfo.Impersonation, events ...sessioninfo.AuthEvent) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo:   &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString("de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5")), Username: username},
		Impersonation: imp,
		AuthEvents:    events,
	})
}

// aliceID and bobID are the accounts of the tests' two people, and aliceUser and
// bobUser the people as the permission store names them: by the id of their account,
// as text. The usernames alice and bob are what people read.
var (
	aliceID   = impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	bobID     = impulse.Must(impulse.UUIDFromString("b0b00000-0000-4000-8000-000000000002"))
	aliceUser = accesstypes.User(aliceID.String())
	bobUser   = accesstypes.User(bobID.String())
)

// accountCtx is sessionCtx for a session that belongs to an account, as an Auth's
// sessions do: UserID is the account's id.
func accountCtx(username string, userID impulse.UUID, imp *sessioninfo.Impersonation, events ...sessioninfo.AuthEvent) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo:   &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString("de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5")), Username: username},
		Impersonation: imp,
		AuthEvents:    events,
		UserID:        impulse.NullUUIDFromUUID(userID),
	})
}

// resolvedAs is ctx with the request's principal set, as the application's principal
// resolver sets it when the session is validated: what the request is authorized as,
// and nothing of whose session it is.
func resolvedAs(ctx context.Context, principal accesstypes.Principal) context.Context {
	sess := *sessioninfo.DataFromCtx(ctx)
	sess.Principal = principal

	return context.WithValue(ctx, sessioninfo.CtxSessionInfo, &sess)
}

func TestMasked(t *testing.T) {
	t.Parallel()

	fullDigest := accesstypes.PermissionDigest{
		"documents": {accesstypes.Read: accesstypes.DigestGranted, accesstypes.Update: accesstypes.DigestConditional},
		"images":    {accesstypes.Delete: accesstypes.DigestGranted},
	}

	tests := []struct {
		name        string
		mask        accesstypes.PermissionMask
		perm        accesstypes.Permission
		wantGranted bool
		wantChecked []accesstypes.Permission
		wantDigest  accesstypes.PermissionDigest
		wantDomains []accesstypes.Domain
	}{
		{
			name:        "unrestricted mask returns the checker itself",
			perm:        accesstypes.Update,
			wantGranted: true,
			wantChecked: []accesstypes.Permission{accesstypes.Update},
			wantDigest:  fullDigest,
			wantDomains: []accesstypes.Domain{"tenant"},
		},
		{
			name:        "allowed permission delegates to policy",
			mask:        accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			perm:        accesstypes.Read,
			wantGranted: true,
			wantChecked: []accesstypes.Permission{accesstypes.Read},
			wantDigest:  accesstypes.PermissionDigest{"documents": {accesstypes.Read: accesstypes.DigestGranted}},
			wantDomains: []accesstypes.Domain{"tenant"},
		},
		{
			name:        "masked permission is denied without consulting policy",
			mask:        accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			perm:        accesstypes.Update,
			wantChecked: nil,
			wantDigest:  accesstypes.PermissionDigest{"documents": {accesstypes.Read: accesstypes.DigestGranted}},
			wantDomains: []accesstypes.Domain{"tenant"},
		},
		{
			name:        "mask that allows nothing denies everything, empties the digest and lists no domain",
			mask:        accesstypes.DenyAll(),
			perm:        accesstypes.Read,
			wantChecked: nil,
			wantDigest:  accesstypes.PermissionDigest{},
			wantDomains: []accesstypes.Domain{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stub := &stubPermissions{user: "bob", digest: fullDigest, domains: []accesstypes.Domain{"tenant"}}
			perms := Masked(stub, tt.mask, nil)
			if tt.mask.IsZero() && perms != UserPermissions(stub) {
				t.Fatal("Masked() with the unrestricted mask did not return the checker itself")
			}

			decisions, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), tt.perm, "documents", "images")
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			for _, res := range []accesstypes.Resource{"documents", "images"} {
				if got := decisions[res].IsGranted(); got != tt.wantGranted {
					t.Errorf("Check() %s granted = %v, want %v", res, got, tt.wantGranted)
				}
			}
			if diff := cmp.Diff(tt.wantChecked, stub.checkPerms); diff != "" {
				t.Errorf("delegated permissions mismatch (-want +got):\n%s", diff)
			}

			digest, err := perms.PermissionDigest(context.Background(), accesstypes.GlobalScope())
			if err != nil {
				t.Fatalf("PermissionDigest() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDigest, digest); diff != "" {
				t.Errorf("PermissionDigest() mismatch (-want +got):\n%s", diff)
			}

			domains, err := perms.Domains(context.Background())
			if err != nil {
				t.Fatalf("Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDomains, domains); diff != "" {
				t.Errorf("Domains() mismatch (-want +got):\n%s", diff)
			}
			has, err := perms.HasGrants(context.Background(), accesstypes.DomainScope("tenant"))
			if err != nil {
				t.Fatalf("HasGrants() error = %v", err)
			}
			if want := len(tt.wantDomains) > 0; has != want {
				t.Errorf("HasGrants(tenant) = %v, want %v", has, want)
			}
			if perms.User() != "bob" {
				t.Errorf("User() = %q, want bob", perms.User())
			}
		})
	}
}

func TestMasked_WrapsDelegateErrors(t *testing.T) {
	t.Parallel()

	stub := &stubPermissions{user: "bob", err: errors.New("snapshot unavailable")}
	perms := Masked(stub, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read), nil)

	if _, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), accesstypes.Read, "documents"); err == nil {
		t.Error("Check() error = nil, want the delegate's error")
	}
	if _, err := perms.PermissionDigest(context.Background(), accesstypes.GlobalScope()); err == nil {
		t.Error("PermissionDigest() error = nil, want the delegate's error")
	}
}

// checkerRecorder builds the forUser/forRole arguments for SessionPermissions and
// records which principals they were asked for.
type checkerRecorder struct {
	users []accesstypes.User
	roles []accesstypes.Role
}

func (r *checkerRecorder) forUser(user accesstypes.User) *stubPermissions {
	r.users = append(r.users, user)

	return &stubPermissions{user: user, digest: accesstypes.PermissionDigest{"documents": {accesstypes.Update: accesstypes.DigestGranted}}, domains: []accesstypes.Domain{"tenant"}}
}

func (r *checkerRecorder) forRole(role accesstypes.Role) *stubRolePermissions {
	r.roles = append(r.roles, role)

	return &stubRolePermissions{role: role}
}

// TestSessionPermissions pins which checker a session is answered by and who it is.
// A person is named as the permission store names them, by the id of their account: a
// session that belongs to an account asks forUser for the id and never the username,
// and one that belongs to none (a session type that keeps no accounts) has only its
// username. A role principal is nobody. The User() of a session established to act as
// a role is the actor who established it, their account's id where the impersonation
// carries one and their name where it does not; a session the application's resolver
// gave a role stays its own person's, who is the person viewed as when it views as
// someone, never the actor.
func TestSessionPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		ctx           context.Context
		unsupported   bool
		wantUsers     []accesstypes.User
		wantRoles     []accesstypes.Role
		wantUser      accesstypes.User
		wantUpdate    bool
		wantDigestLen int
		// wantDomains is the roster (tenant, other) filtered by the session's
		// footholds: the checker holds a grant in tenant alone, and a mask
		// that allows nothing it holds leaves no foothold.
		wantDomains []accesstypes.Domain
	}{
		{
			name:          "ordinary session of an account routes to the user checker for the account's id, never the username, unmasked",
			ctx:           accountCtx("alice", aliceID, nil),
			wantUsers:     []accesstypes.User{aliceUser},
			wantUser:      aliceUser,
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name:          "ordinary session that belongs to no account routes to the user checker for its username",
			ctx:           sessionCtx("alice", nil),
			wantUsers:     []accesstypes.User{"alice"},
			wantUser:      "alice",
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name: "impersonated user of an account routes to the user checker for the impersonated account's id and is masked",
			ctx: accountCtx("bob", bobID, &sessioninfo.Impersonation{
				Actor:     "alice",
				ActorID:   impulse.NullUUIDFromUUID(aliceID),
				Principal: accesstypes.UserPrincipal(bobUser),
				Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			}),
			wantUsers:     []accesstypes.User{bobUser},
			wantUser:      bobUser,
			wantDigestLen: 0,
			wantDomains:   []accesstypes.Domain{},
		},
		{
			name: "impersonated user of no account routes to the user checker for the impersonated username and is masked",
			ctx: sessionCtx("bob", &sessioninfo.Impersonation{
				Actor:     "alice",
				Principal: accesstypes.UserPrincipal("bob"),
				Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			}),
			wantUsers:     []accesstypes.User{"bob"},
			wantUser:      "bob",
			wantDigestLen: 0,
			wantDomains:   []accesstypes.Domain{},
		},
		{
			name: "role principal routes to the role checker, which has no User(); the actor's account id is supplied",
			ctx: sessionCtx("alice", &sessioninfo.Impersonation{
				Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      aliceUser,
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name: "role principal of an actor with no account here: the actor's name is supplied",
			ctx: sessionCtx("alice", &sessioninfo.Impersonation{
				Actor: "alice", ActorRealm: "admin-portal", Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      "alice",
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name: "role principal the session was established as: the actor's account id is supplied whatever role a resolver chose for the request",
			ctx: resolvedAs(sessionCtx("alice", &sessioninfo.Impersonation{
				Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}), accesstypes.RolePrincipal("Auditor")),
			wantRoles:     []accesstypes.Role{"Auditor"},
			wantUser:      aliceUser,
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name:          "role principal a resolver chose for an ordinary session of an account: the session's own account id is supplied",
			ctx:           resolvedAs(accountCtx("alice", aliceID, nil), accesstypes.RolePrincipal("PartnerViewer")),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      aliceUser,
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name:          "role principal a resolver chose for an ordinary session that belongs to no account: its username is supplied",
			ctx:           resolvedAs(sessionCtx("alice", nil), accesstypes.RolePrincipal("PartnerViewer")),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      "alice",
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name: "role principal a resolver chose for an impersonated user: the account id of the person viewed as is supplied, never the actor's",
			ctx: resolvedAs(accountCtx("bob", bobID, &sessioninfo.Impersonation{
				Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
			}), accesstypes.RolePrincipal("PartnerViewer")),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      bobUser,
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name: "role principal a resolver chose for an impersonated user, an actor with no account here: the account id of the person viewed as is supplied, never the actor's name",
			ctx: resolvedAs(accountCtx("bob", bobID, &sessioninfo.Impersonation{
				Actor: "alice", ActorRealm: "admin-portal", Principal: accesstypes.UserPrincipal(bobUser),
			}), accesstypes.RolePrincipal("PartnerViewer")),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      bobUser,
			wantUpdate:    true,
			wantDigestLen: 1,
			wantDomains:   []accesstypes.Domain{"tenant"},
		},
		{
			name: "masked role principal is attenuated like any other session",
			ctx: sessionCtx("alice", &sessioninfo.Impersonation{
				Actor:     "alice",
				Principal: accesstypes.RolePrincipal("PartnerViewer"),
				Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			}),
			wantRoles:     []accesstypes.Role{"PartnerViewer"},
			wantUser:      "alice",
			wantDigestLen: 0,
			wantDomains:   []accesstypes.Domain{},
		},
		{
			name: "role principal without a role checker fails closed with the actor's account id as User()",
			ctx: sessionCtx("alice", &sessioninfo.Impersonation{
				Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}),
			unsupported: true,
			wantUser:    aliceUser,
			wantDomains: []accesstypes.Domain{},
		},
		{
			name:        "role principal without a role checker, of an actor with no account here, fails closed with the actor's name as User()",
			ctx:         sessionCtx("alice", &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("PartnerViewer")}),
			unsupported: true,
			wantUser:    "alice",
			wantDomains: []accesstypes.Domain{},
		},
		{
			name: "role principal a resolver chose for an impersonated user, without a role checker, fails closed, and User() is the account id of the person viewed as",
			ctx: resolvedAs(accountCtx("bob", bobID, &sessioninfo.Impersonation{
				Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
			}), accesstypes.RolePrincipal("PartnerViewer")),
			unsupported: true,
			wantUser:    bobUser,
			wantDomains: []accesstypes.Domain{},
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"tenant", "other"}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &checkerRecorder{}
			var perms UserPermissions
			if tt.unsupported {
				perms = SessionPermissions(tt.ctx, rec.forUser, RolePrincipalsUnsupported, roster, nil, nil)
			} else {
				perms = SessionPermissions(tt.ctx, rec.forUser, rec.forRole, roster, nil, nil)
			}

			if diff := cmp.Diff(tt.wantUsers, rec.users); diff != "" {
				t.Errorf("forUser calls mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRoles, rec.roles); diff != "" {
				t.Errorf("forRole calls mismatch (-want +got):\n%s", diff)
			}
			if perms.User() != tt.wantUser {
				t.Errorf("User() = %q, want %q", perms.User(), tt.wantUser)
			}

			decisions, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), accesstypes.Update, "documents")
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if got := decisions["documents"].IsGranted(); got != tt.wantUpdate {
				t.Errorf("Check(Update) granted = %v, want %v", got, tt.wantUpdate)
			}

			digest, err := perms.PermissionDigest(context.Background(), accesstypes.GlobalScope())
			if err != nil {
				t.Fatalf("PermissionDigest() error = %v", err)
			}
			if len(digest) != tt.wantDigestLen {
				t.Errorf("PermissionDigest() = %v, want %d entries", digest, tt.wantDigestLen)
			}

			domains, err := perms.Domains(context.Background())
			if err != nil {
				t.Fatalf("Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDomains, domains); diff != "" {
				t.Errorf("Domains() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUserEvent pins the event source a data change carries. A person is written as
// their username with the id of their account, and by name alone where they have no
// account here; the session comes last. An impersonated session names the actor first.
func TestUserEvent(t *testing.T) {
	t.Parallel()

	const session = "de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"
	alice, bob := aliceID.String(), bobID.String()
	actorAlice := impulse.NullUUIDFromUUID(aliceID)

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "ordinary session: the username, the account id, the session",
			ctx:  accountCtx("alice@example.com", aliceID, nil),
			want: "alice@example.com (" + alice + "), session " + session,
		},
		{
			name: "ordinary session of no account: the person by name alone",
			ctx:  sessionCtx("alice@example.com", nil),
			want: "alice@example.com, session " + session,
		},
		{
			name: "impersonated user names the actor first, each with the id of their account",
			ctx: accountCtx("bob@partner.org", bobID, &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.UserPrincipal(bobUser),
			}),
			want: "alice@example.com (" + alice + ") impersonating bob@partner.org (" + bob + "), session " + session,
		},
		{
			name: "impersonated user, an actor with no account here: the actor by name alone",
			ctx: accountCtx("bob@partner.org", bobID, &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorRealm: "admin-portal", Principal: accesstypes.UserPrincipal(bobUser),
			}),
			want: "alice@example.com impersonating bob@partner.org (" + bob + "), session " + session,
		},
		{
			name: "impersonated user of no account, an actor with one: the user by name alone",
			ctx: sessionCtx("bob@partner.org", &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.UserPrincipal("bob@partner.org"),
			}),
			want: "alice@example.com (" + alice + ") impersonating bob@partner.org, session " + session,
		},
		{
			name: "impersonated user, neither with an account: both by name alone",
			ctx:  sessionCtx("bob@partner.org", &sessioninfo.Impersonation{Actor: "alice@example.com", Principal: accesstypes.UserPrincipal("bob@partner.org")}),
			want: "alice@example.com impersonating bob@partner.org, session " + session,
		},
		{
			name: "impersonated role names the actor, with the id of their account, and the role",
			ctx: sessionCtx("alice@example.com", &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}),
			want: "alice@example.com (" + alice + ") as role PartnerViewer, session " + session,
		},
		{
			name: "impersonated role, an actor with no account here: the actor by name alone",
			ctx:  sessionCtx("alice@example.com", &sessioninfo.Impersonation{Actor: "alice@example.com", Principal: accesstypes.RolePrincipal("PartnerViewer")}),
			want: "alice@example.com as role PartnerViewer, session " + session,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := UserEvent(tt.ctx); got != tt.want {
				t.Errorf("UserEvent() = %q, want %q", got, tt.want)
			}
			if got, want := UserProcessEvent(tt.ctx, "nightly"), tt.want+": Process nightly"; got != want {
				t.Errorf("UserProcessEvent() = %q, want %q", got, want)
			}
		})
	}
}

// TestSessionPermissions_roster pins how the session's domains come from the
// application's roster: each tenant the roster lists where the checker has a
// foothold, sorted and without repeats; an application without tenants (nil
// roster) lists none, never nil; a roster error is the answer's error.
func TestSessionPermissions_roster(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		roster  DomainRoster
		holds   []accesstypes.Domain
		want    []accesstypes.Domain
		wantErr bool
	}{
		{
			name: "the roster filtered by footholds, sorted",
			roster: func(context.Context) ([]accesstypes.Domain, error) {
				return []accesstypes.Domain{"zeta", "alpha", "mid", "alpha"}, nil
			},
			holds: []accesstypes.Domain{"zeta", "alpha"},
			want:  []accesstypes.Domain{"alpha", "zeta"},
		},
		{
			name:   "no footholds lists nothing",
			roster: func(context.Context) ([]accesstypes.Domain, error) { return []accesstypes.Domain{"alpha"}, nil },
			want:   []accesstypes.Domain{},
		},
		{
			name:  "an application without tenants lists nothing",
			holds: []accesstypes.Domain{"alpha"},
			want:  []accesstypes.Domain{},
		},
		{
			name:    "a roster error is returned",
			roster:  func(context.Context) ([]accesstypes.Domain, error) { return nil, errors.New("roster unavailable") },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, domains: tt.holds}
			}
			perms := SessionPermissions(sessionCtx("alice", nil), forUser, RolePrincipalsUnsupported, tt.roster, nil, nil)
			got, err := perms.Domains(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Domains() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Domains() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// declarationReader scripts TenantDeclarations: a declaration per tenant, nil
// for a tenant that declares nothing, an error for every read when set, and a
// count of the reads per tenant.
type declarationReader struct {
	declarations map[accesstypes.Domain]*signin.TenantDeclaration
	err          error
	reads        map[accesstypes.Domain]int
}

func (d *declarationReader) read(_ context.Context, domain accesstypes.Domain) (*signin.TenantDeclaration, error) {
	if d.reads == nil {
		d.reads = map[accesstypes.Domain]int{}
	}
	d.reads[domain]++
	if d.err != nil {
		return nil, d.err
	}

	return d.declarations[domain], nil
}

// TestSessionPermissions_admission pins the reach-time admission: a session
// reaches every tenant it holds a grant in, except a tenant that is directory
// only and whose directory is not the one the session was established through,
// unless the session's actor is a super administrator, or the tenant's own
// administrator come with a password and a second factor, and nobody else
// enters it another way; in a tenant the session does not reach it holds nothing, on every
// question the composed permissions answer, and the tenant is listed closed
// (ClosedDomains).
func TestSessionPermissions_admission(t *testing.T) {
	t.Parallel()

	const (
		anvil   accesstypes.Domain = "anvil"
		bastion accesstypes.Domain = "bastion"
	)
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	workspace := sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "acme"}
	impersonation := sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"}
	declaresWorkspace := &signin.TenantDeclaration{Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "acme"}}}
	workspaceOnly := &signin.TenantDeclaration{Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "acme"}}, DirectoryOnly: true}
	// lockedOut is the declaration that shuts everyone out: directory only, and no
	// directory listed.
	lockedOut := &signin.TenantDeclaration{DirectoryOnly: true}
	asBob := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.UserPrincipal("bob")}
	asRole := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("PartnerViewer")}
	mailedCode := sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode}
	rememberedBrowser := sessioninfo.AuthEvent{Method: sessioninfo.MethodTrustedDevice}

	tests := []struct {
		name string
		ctx  context.Context
		// administers are the tenants the session's actor administers, and
		// administersErr fails the question.
		administers    []accesstypes.Domain
		administersErr error
		// declarations is nil for an application that keeps none; reader scripts
		// one that does.
		reader *declarationReader
		// holds are the tenants the principal's checker holds a grant in; global
		// makes it, and the actor's checker, hold a grant in the global partition;
		// super makes them a super administrator.
		holds  []accesstypes.Domain
		global bool
		super  bool
		// wantDomains is the session's tenant list; wantReaches the answer per
		// tenant of the roster (anvil, bastion) when the list is not the whole story.
		wantDomains []accesstypes.Domain
		wantReaches map[accesstypes.Domain]bool
		// wantClosed are the tenants the session holds a grant in and does not reach
		// (ClosedDomains); nil expects none.
		wantClosed []accesstypes.Domain
		// wantReads is how many times each tenant's declaration was read.
		wantReads map[accesstypes.Domain]int
		wantErr   bool
	}{
		{
			name:        "an application that keeps no declarations: every tenant admits every session",
			ctx:         sessionCtx("alice", nil, workspace),
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: true},
		},
		{
			name:        "a tenant that declares nothing admits a password session",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReads:   map[accesstypes.Domain]int{anvil: 1, bastion: 1},
		},
		{
			name:        "a tenant that declares nothing admits a session established through a directory: no other tenant's settings close it",
			ctx:         sessionCtx("alice", nil, workspace),
			reader:      &declarationReader{},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: true},
		},
		{
			name:        "a tenant with a directory admits the session that came through it, and so does the tenant beside it that declares nothing",
			ctx:         sessionCtx("alice", nil, workspace),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: declaresWorkspace}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: true},
		},
		{
			name: "a tenant with a directory admits a session through another directory unless it is directory only",
			ctx:  sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "other"}),
			reader: &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{
				anvil: declaresWorkspace,
			}},
			holds:       []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{anvil},
			wantReaches: map[accesstypes.Domain]bool{anvil: true},
		},
		{
			name: "a directory-only tenant refuses a session through another directory; the other tenant admits it",
			ctx:  sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "other"}),
			reader: &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{
				anvil: workspaceOnly,
			}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name:        "a directory-only tenant refuses a password session; the other tenant admits it",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name:        "a directory-only tenant admits the session that came through its directory, and so does the open tenant beside it",
			ctx:         sessionCtx("alice", nil, workspace),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: true},
		},
		{
			name:        "a session that records no establishment is not through a directory",
			ctx:         sessionCtx("alice", nil),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly, bastion: declaresWorkspace}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name:        "a tenant the session holds no grant in is closed to nobody: it is not theirs to see",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantReads:   map[accesstypes.Domain]int{bastion: 1},
		},
		{
			name:        "a super administrator enters a tenant that is directory only and lists no directory",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: lockedOut}},
			holds:       []accesstypes.Domain{anvil, bastion},
			global:      true,
			super:       true,
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: true},
		},
		{
			name:        "a person holding another global role does not enter it: a grant in the global partition exempts nobody",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: lockedOut}},
			holds:       []accesstypes.Domain{anvil, bastion},
			global:      true,
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			// A membership that covers every tenant domain is a grant in each tenant and
			// none in the global partition: held to each tenant's declaration, as the
			// sign-in rule holds it to be no super administrator's.
			name:        "a person whose only membership covers every tenant is held to each tenant's declaration",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			// The tenant is refused and not listed as closed: that list is a person's own
			// sign-in's, and this session views the application as someone else.
			name:        "an impersonated session is established by its impersonation, which a directory-only tenant refuses",
			ctx:         sessionCtx("bob", asBob, impersonation),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
		},
		{
			name:        "an impersonated session reaches by its actor's rights: a super administrator's reaches the directory-only tenant",
			ctx:         sessionCtx("bob", asBob, impersonation),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			super:       true,
			wantDomains: []accesstypes.Domain{anvil, bastion},
		},
		{
			// The stub answers the super administrator question for whatever name it is
			// asked under: an actor another application authenticated is not asked.
			name:        "an actor from another application is no super administrator here, whatever their name spells",
			ctx:         sessionCtx("bob", &sessioninfo.Impersonation{Actor: "alice", ActorRealm: "support-portal", Principal: accesstypes.UserPrincipal("bob")}, impersonation),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			super:       true,
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
		},
		{
			// The role checker (stubRolePermissions) holds a grant in "tenant" alone, so
			// the list stays empty; the reach is the actor's, who is no super
			// administrator.
			name:        "a role principal is admitted like any session, by its actor's rights",
			ctx:         sessionCtx("alice", asRole, impersonation),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			wantDomains: []accesstypes.Domain{},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
		},
		{
			name:        "the tenant's administrator who signed in with a password and gave the mailed code enters the directory-only tenant",
			ctx:         sessionCtx("alice", nil, password, mailedCode),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{anvil, bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: true},
		},
		{
			name:        "a remembered browser stands in for the code",
			ctx:         sessionCtx("alice", nil, password, rememberedBrowser),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{anvil},
			wantReaches: map[accesstypes.Domain]bool{anvil: true},
		},
		{
			name:        "the tenant's administrator enters a tenant that is directory only and lists no directory: the tenant nobody else can mend",
			ctx:         sessionCtx("alice", nil, password, mailedCode),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: lockedOut}},
			holds:       []accesstypes.Domain{anvil},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{anvil},
			wantReaches: map[accesstypes.Domain]bool{anvil: true},
		},
		{
			name:        "the same administrator without the second factor does not enter",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name:        "a member with a password and the mailed code does not enter: the way in is the administrator's",
			ctx:         sessionCtx("alice", nil, password, mailedCode),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil, bastion},
			wantDomains: []accesstypes.Domain{bastion},
			wantReaches: map[accesstypes.Domain]bool{anvil: false, bastion: true},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name: "the administrator of one directory-only tenant enters that one and not the other they hold a seat in",
			ctx:  sessionCtx("alice", nil, password, mailedCode),
			reader: &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{
				anvil: workspaceOnly, bastion: lockedOut,
			}},
			holds:       []accesstypes.Domain{anvil, bastion},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{anvil},
			wantReaches: map[accesstypes.Domain]bool{anvil: true, bastion: false},
			wantClosed:  []accesstypes.Domain{bastion},
		},
		{
			name:        "the tenant's administrator who signed in through another organization's directory does not enter, attested or not",
			ctx:         sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "other"}, sessioninfo.AuthEvent{Method: sessioninfo.MethodAttestedByEvidence, Connection: "other"}),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{},
			wantReaches: map[accesstypes.Domain]bool{anvil: false},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name:        "the tenant's administrator who signed in with a personal account does not enter",
			ctx:         sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection}, mailedCode),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{},
			wantReaches: map[accesstypes.Domain]bool{anvil: false},
			wantClosed:  []accesstypes.Domain{anvil},
		},
		{
			name:        "a session that views the application as the tenant's administrator does not enter by their password: it was established by the impersonation",
			ctx:         sessionCtx("bob", asBob, impersonation, mailedCode),
			reader:      &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:       []accesstypes.Domain{anvil},
			administers: []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{},
			wantReaches: map[accesstypes.Domain]bool{anvil: false},
		},
		{
			name:           "a failure to learn who administers the tenant is the answer's error",
			ctx:            sessionCtx("alice", nil, password, mailedCode),
			reader:         &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: workspaceOnly}},
			holds:          []accesstypes.Domain{anvil},
			administersErr: errors.New("engine unavailable"),
			wantErr:        true,
		},
		{
			name:        "a tenant the session holds no grant in is not asked",
			ctx:         sessionCtx("alice", nil, password),
			reader:      &declarationReader{},
			holds:       []accesstypes.Domain{anvil},
			wantDomains: []accesstypes.Domain{anvil},
			wantReads:   map[accesstypes.Domain]int{anvil: 1},
		},
		{
			name:    "a reader error is the answer's error",
			ctx:     sessionCtx("alice", nil, password),
			reader:  &declarationReader{err: errors.New("declarations unavailable")},
			holds:   []accesstypes.Domain{anvil},
			wantErr: true,
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{bastion, anvil}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{
					user: user, digest: accesstypes.PermissionDigest{"documents": {accesstypes.Read: accesstypes.DigestGranted}}, domains: tt.holds, global: tt.global, super: tt.super,
					administers: tt.administers, administersErr: tt.administersErr,
				}
			}
			forRole := func(accesstypes.Role) *stubRolePermissions {
				return &stubRolePermissions{}
			}
			var declarations TenantDeclarations
			if tt.reader != nil {
				declarations = tt.reader.read
			}
			perms := SessionPermissions(tt.ctx, forUser, forRole, roster, declarations, nil)

			domains, err := perms.Domains(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Domains() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if _, err := perms.Reaches(context.Background(), anvil); err == nil {
					t.Error("Reaches() error = nil, want the reader's error")
				}
				if _, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.DomainScope(anvil), accesstypes.Read, "documents"); err == nil {
					t.Error("Check() error = nil, want the reader's error")
				}
				if _, err := perms.HasGrants(context.Background(), accesstypes.DomainScope(anvil)); err == nil {
					t.Error("HasGrants() error = nil, want the reader's error")
				}

				return
			}
			if diff := cmp.Diff(tt.wantDomains, domains); diff != "" {
				t.Errorf("Domains() mismatch (-want +got):\n%s", diff)
			}
			closed, err := ClosedDomains(context.Background(), perms)
			if err != nil {
				t.Fatalf("ClosedDomains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantClosed, closed, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ClosedDomains() mismatch (-want +got):\n%s", diff)
			}
			for domain, want := range tt.wantReaches {
				reaches, err := perms.Reaches(context.Background(), domain)
				if err != nil {
					t.Fatalf("Reaches(%s) error = %v", domain, err)
				}
				if reaches != want {
					t.Errorf("Reaches(%s) = %v, want %v", domain, reaches, want)
				}
				// In a tenant the session does not reach it holds nothing; in one it
				// reaches, the checker's grants answer.
				wantHolds := want && slices.Contains(tt.holds, domain)
				has, err := perms.HasGrants(context.Background(), accesstypes.DomainScope(domain))
				if err != nil {
					t.Fatalf("HasGrants(%s) error = %v", domain, err)
				}
				if has != wantHolds {
					t.Errorf("HasGrants(%s) = %v, want %v", domain, has, wantHolds)
				}
				decisions, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.DomainScope(domain), accesstypes.Read, "documents")
				if err != nil {
					t.Fatalf("Check(%s) error = %v", domain, err)
				}
				if got := decisions["documents"].IsGranted(); got != want {
					t.Errorf("Check(%s) granted = %v, want %v", domain, got, want)
				}
				digest, err := perms.PermissionDigest(context.Background(), accesstypes.DomainScope(domain))
				if err != nil {
					t.Fatalf("PermissionDigest(%s) error = %v", domain, err)
				}
				if got := len(digest) > 0; got != want {
					t.Errorf("PermissionDigest(%s) = %v, want entries %v", domain, digest, want)
				}
			}
			// The global partition is never attenuated.
			decisions, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), accesstypes.Read, "documents")
			if err != nil {
				t.Fatalf("Check(global) error = %v", err)
			}
			if !decisions["documents"].IsGranted() {
				t.Error("Check(global) denied, want the checker's grant")
			}
			if tt.wantReads != nil {
				if diff := cmp.Diff(tt.wantReads, tt.reader.reads); diff != "" {
					t.Errorf("declaration reads mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestSessionPermissions_admissionReadsOnce pins that one composed value reads a
// tenant's declaration once and asks the super administrator question once, whatever is asked
// about the tenant after: the guard, the handler's checks and the digest share
// one answer.
func TestSessionPermissions_admissionReadsOnce(t *testing.T) {
	t.Parallel()

	const anvil accesstypes.Domain = "anvil"
	reader := &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: {DirectoryOnly: true}}}
	users := 0
	forUser := func(user accesstypes.User) *stubPermissions {
		users++

		return &stubPermissions{user: user, domains: []accesstypes.Domain{anvil}}
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{anvil}, nil
	}
	perms := SessionPermissions(sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}), forUser, RolePrincipalsUnsupported, roster, reader.read, nil)

	for range 3 {
		if reaches, err := perms.Reaches(context.Background(), anvil); err != nil || reaches {
			t.Fatalf("Reaches() = %v, %v; want false, nil", reaches, err)
		}
		if _, err := perms.Domains(context.Background()); err != nil {
			t.Fatalf("Domains() error = %v", err)
		}
		if _, err := perms.PermissionDigest(context.Background(), accesstypes.DomainScope(anvil)); err != nil {
			t.Fatalf("PermissionDigest() error = %v", err)
		}
	}
	if got := reader.reads[anvil]; got != 1 {
		t.Errorf("the declaration was read %d times, want once", got)
	}
	if users != 1 {
		t.Errorf("forUser was called %d times, want once: an ordinary session's actor is its user", users)
	}
}

// TestSessionPermissions_administratorAskedWhereItCouldAdmit pins when the engine is
// asked who administers a tenant: only for a session the administrators' way in could
// admit, a password with a second factor, in a tenant that is directory only, and then
// once for the tenant in one composed value. A session it could not admit asks nothing,
// so nothing is learned from it and nothing is spent on it.
func TestSessionPermissions_administratorAskedWhereItCouldAdmit(t *testing.T) {
	t.Parallel()

	const (
		anvil   accesstypes.Domain = "anvil"
		bastion accesstypes.Domain = "bastion"
	)
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	mailedCode := sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode}
	tests := []struct {
		name      string
		ctx       context.Context
		wantAsked int
	}{
		{name: "a password with the mailed code asks once for the directory-only tenant, and never for the open one", ctx: sessionCtx("alice", nil, password, mailedCode), wantAsked: 1},
		{name: "a password alone asks nothing", ctx: sessionCtx("alice", nil, password)},
		{name: "a sign-in through another directory asks nothing", ctx: sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "other"}, mailedCode)},
		{
			name: "an actor another application authenticated is never asked about",
			ctx: sessionCtx("bob", &sessioninfo.Impersonation{Actor: "alice", ActorRealm: "support-portal", Principal: accesstypes.UserPrincipal("bob")},
				password, mailedCode),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			asked := 0
			reader := &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: {DirectoryOnly: true}}}
			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, domains: []accesstypes.Domain{anvil, bastion}, administers: []accesstypes.Domain{anvil, bastion}, administersAsked: &asked}
			}
			roster := func(context.Context) ([]accesstypes.Domain, error) {
				return []accesstypes.Domain{anvil, bastion}, nil
			}
			perms := SessionPermissions(tt.ctx, forUser, RolePrincipalsUnsupported, roster, reader.read, nil)

			for range 3 {
				for _, domain := range []accesstypes.Domain{anvil, bastion} {
					if _, err := perms.Reaches(context.Background(), domain); err != nil {
						t.Fatalf("Reaches(%s) error = %v", domain, err)
					}
				}
				if _, err := perms.Domains(context.Background()); err != nil {
					t.Fatalf("Domains() error = %v", err)
				}
			}
			if asked != tt.wantAsked {
				t.Errorf("the engine was asked who administers a tenant %d times, want %d", asked, tt.wantAsked)
			}
		})
	}
}

// TestSessionPermissions_actorByAccount pins who the super administrator question is
// asked of when the people of a session are accounts: the session's actor as the
// permission store names them, by the id of their account. An ordinary session's actor
// is its user, and the checker in hand answers; an impersonated session's actor is the
// real person behind it, asked for under their account's id and never their username,
// so the reach follows the role the actor's account holds: a session started by a
// super administrator, viewed as a person who is none, enters a directory-only tenant,
// and the reverse does not; an actor with no account here is asked for by the name the
// impersonation carries.
func TestSessionPermissions_actorByAccount(t *testing.T) {
	t.Parallel()

	const anvil accesstypes.Domain = "anvil"
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	impersonation := sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"}
	asBob := func(actorID impulse.NullUUID) *sessioninfo.Impersonation {
		return &sessioninfo.Impersonation{Actor: "alice", ActorID: actorID, Principal: accesstypes.UserPrincipal(bobUser)}
	}
	asRole := &sessioninfo.Impersonation{Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("PartnerViewer")}

	tests := []struct {
		name string
		ctx  context.Context
		// supers are the users who hold the Super Administrator role; every user's
		// checker holds a grant in the global partition, which exempts nobody.
		supers []accesstypes.User
		// wantAsked are the users forUser was asked for, in order: the principal's
		// checker first, for a user principal, then the actor's for the super
		// administrator question.
		wantAsked []accesstypes.User
		wantUser  accesstypes.User
		// wantReaches is whether the session reaches anvil, which admits a directory's
		// sessions alone: only a super administrator's do.
		wantReaches bool
	}{
		{
			name:        "an ordinary session's actor is its user: the account's checker answers, asked for once",
			ctx:         accountCtx("alice", aliceID, nil, password),
			supers:      []accesstypes.User{aliceUser},
			wantAsked:   []accesstypes.User{aliceUser},
			wantUser:    aliceUser,
			wantReaches: true,
		},
		{
			name:      "an ordinary session: the role held under the username is nobody's",
			ctx:       accountCtx("alice", aliceID, nil, password),
			supers:    []accesstypes.User{"alice"},
			wantAsked: []accesstypes.User{aliceUser},
			wantUser:  aliceUser,
		},
		{
			name:        "an impersonated session of a person who is no super administrator, started by one who is, enters: the question is asked of the actor's account id",
			ctx:         accountCtx("bob", bobID, asBob(impulse.NullUUIDFromUUID(aliceID)), impersonation),
			supers:      []accesstypes.User{aliceUser},
			wantAsked:   []accesstypes.User{bobUser, aliceUser},
			wantUser:    bobUser,
			wantReaches: true,
		},
		{
			name:      "the reverse does not: an impersonated session of a super administrator, started by a person who is none",
			ctx:       accountCtx("bob", bobID, asBob(impulse.NullUUIDFromUUID(aliceID)), impersonation),
			supers:    []accesstypes.User{bobUser},
			wantAsked: []accesstypes.User{bobUser, aliceUser},
			wantUser:  bobUser,
		},
		{
			name:      "an impersonated session: the role held under the actor's username is nobody's",
			ctx:       accountCtx("bob", bobID, asBob(impulse.NullUUIDFromUUID(aliceID)), impersonation),
			supers:    []accesstypes.User{"alice"},
			wantAsked: []accesstypes.User{bobUser, aliceUser},
			wantUser:  bobUser,
		},
		{
			name:        "an impersonated session of an actor with no account here: the question is asked of the actor's name",
			ctx:         accountCtx("bob", bobID, asBob(impulse.NullUUID{}), impersonation),
			supers:      []accesstypes.User{"alice"},
			wantAsked:   []accesstypes.User{bobUser, "alice"},
			wantUser:    bobUser,
			wantReaches: true,
		},
		{
			name:        "a role principal: the question is asked of the actor's account id, which is the session's User()",
			ctx:         sessionCtx("alice", asRole, impersonation),
			supers:      []accesstypes.User{aliceUser},
			wantAsked:   []accesstypes.User{aliceUser},
			wantUser:    aliceUser,
			wantReaches: true,
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{anvil}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked []accesstypes.User
			forUser := func(user accesstypes.User) *stubPermissions {
				asked = append(asked, user)

				return &stubPermissions{user: user, domains: []accesstypes.Domain{anvil}, global: true, super: slices.Contains(tt.supers, user)}
			}
			forRole := func(accesstypes.Role) *stubRolePermissions {
				return &stubRolePermissions{}
			}
			reader := &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{anvil: {DirectoryOnly: true}}}
			perms := SessionPermissions(tt.ctx, forUser, forRole, roster, reader.read, nil)

			if perms.User() != tt.wantUser {
				t.Errorf("User() = %q, want %q", perms.User(), tt.wantUser)
			}
			// Asked twice: the super administrator question is asked once for the composed value.
			for range 2 {
				reaches, err := perms.Reaches(context.Background(), anvil)
				if err != nil {
					t.Fatalf("Reaches() error = %v", err)
				}
				if reaches != tt.wantReaches {
					t.Errorf("Reaches(%s) = %v, want %v", anvil, reaches, tt.wantReaches)
				}
			}
			if diff := cmp.Diff(tt.wantAsked, asked); diff != "" {
				t.Errorf("forUser calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
