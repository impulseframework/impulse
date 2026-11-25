package resource

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestSessionPermissions_tenantDoor pins the rule a session's reach follows: a
// tenant's sign-in settings restrict that tenant's own door and nobody else's. A
// session reaches every tenant that seats the person, except a tenant that is
// directory only and whose directory is not the one the person signed in through,
// which is listed closed instead.
func TestSessionPermissions_tenantDoor(t *testing.T) {
	t.Parallel()

	lakeside := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tid-lakeside"}
	hilltop := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "hilltop.example"}
	through := func(directory signin.Directory) sessioninfo.AuthEvent {
		return sessioninfo.AuthEvent{Method: directory.Method, Connection: directory.Connection}
	}
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	five := []accesstypes.Domain{"hilltop", "lakeside", "meadow", "riverside", "seaview"}

	tests := []struct {
		name string
		// declared are the tenants' declarations; a tenant missing from it sets nothing.
		declared map[accesstypes.Domain]*signin.TenantDeclaration
		// seated are the tenants the person holds a grant in.
		seated      []accesstypes.Domain
		established sessioninfo.AuthEvent
		// impersonation is the record of a session that views as someone else, with its
		// mask; nil for a person's own session.
		impersonation *sessioninfo.Impersonation
		wantReached   []accesstypes.Domain
		wantClosed    []accesstypes.Domain
	}{
		{
			name: "five open tenants seat one person and a directory session reaches all five, whichever of them have a directory",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}},
				"hilltop":  {Directories: []signin.Directory{hilltop}},
				"seaview":  {},
			},
			seated:      five,
			established: through(lakeside),
			wantReached: five,
		},
		{
			name: "five open tenants seat one person and a password session reaches all five",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}},
				"hilltop":  {Directories: []signin.Directory{hilltop}},
			},
			seated:      five,
			established: password,
			wantReached: five,
		},
		{
			name: "a tenant that sets nothing admits a seated person through a directory another tenant has",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
			},
			seated:      []accesstypes.Domain{"riverside"},
			established: through(lakeside),
			wantReached: []accesstypes.Domain{"riverside"},
		},
		{
			name: "a tenant that sets nothing admits a seated person through an OpenID Connect provider no tenant has",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}},
			},
			seated:      []accesstypes.Domain{"riverside"},
			established: sessioninfo.AuthEvent{Method: sessioninfo.MethodOIDC, Connection: "https://id.elsewhere.example"},
			wantReached: []accesstypes.Domain{"riverside"},
		},
		{
			name: "a tenant that sets nothing admits a seated person with a personal account",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}},
			},
			seated:      []accesstypes.Domain{"riverside"},
			established: sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection},
			wantReached: []accesstypes.Domain{"riverside"},
		},
		{
			name: "a person seated in a directory-only tenant and an open one reaches both through the tenant's directory",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
			},
			seated:      []accesstypes.Domain{"lakeside", "riverside"},
			established: through(lakeside),
			wantReached: []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name: "a person seated in a directory-only tenant and an open one reaches the open one alone by password, and the other is closed",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
			},
			seated:      []accesstypes.Domain{"lakeside", "riverside"},
			established: password,
			wantReached: []accesstypes.Domain{"riverside"},
			wantClosed:  []accesstypes.Domain{"lakeside"},
		},
		{
			name: "a disabled tenant is reached by no session of a person it seats, through its directory or not, and is listed with the tenants the session cannot enter",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, Disabled: true},
			},
			seated:      []accesstypes.Domain{"lakeside", "riverside"},
			established: through(lakeside),
			wantReached: []accesstypes.Domain{"riverside"},
			wantClosed:  []accesstypes.Domain{"lakeside"},
		},
		{
			name: "two directory-only tenants seat one person: the session reaches the one whose directory it came through, and the other is closed",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
				"hilltop":  {Directories: []signin.Directory{hilltop}, DirectoryOnly: true},
			},
			seated:      []accesstypes.Domain{"hilltop", "lakeside", "riverside"},
			established: through(hilltop),
			wantReached: []accesstypes.Domain{"hilltop", "riverside"},
			wantClosed:  []accesstypes.Domain{"lakeside"},
		},
		{
			name: "a session that views the application as someone else reaches as that session does, and is told of no closed tenant: the list is a person's own sign-in's",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
			},
			seated:      []accesstypes.Domain{"lakeside", "riverside"},
			established: sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: &sessioninfo.Impersonation{
				Actor: "alice", Principal: accesstypes.UserPrincipal("bob"),
			},
			wantReached: []accesstypes.Domain{"riverside"},
		},
		{
			name: "nor under a mask",
			declared: map[accesstypes.Domain]*signin.TenantDeclaration{
				"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
			},
			seated:      []accesstypes.Domain{"lakeside", "riverside"},
			established: sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: &sessioninfo.Impersonation{
				Actor: "alice", Principal: accesstypes.UserPrincipal("bob"), Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read),
			},
			wantReached: []accesstypes.Domain{"riverside"},
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"seaview", "riverside", "meadow", "lakeside", "hilltop", "unseated"}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, digest: accesstypes.PermissionDigest{"documents": {accesstypes.Read: accesstypes.DigestGranted}}, domains: tt.seated}
			}
			reader := &declarationReader{declarations: tt.declared}
			perms := SessionPermissions(sessionCtx("alice", tt.impersonation, tt.established), forUser, RolePrincipalsUnsupported, roster, reader.read, nil)

			reached, err := perms.Domains(context.Background())
			if err != nil {
				t.Fatalf("Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantReached, reached, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Domains() mismatch (-want +got):\n%s", diff)
			}
			closed, err := ClosedDomains(context.Background(), perms)
			if err != nil {
				t.Fatalf("ClosedDomains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantClosed, closed, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ClosedDomains() mismatch (-want +got):\n%s", diff)
			}
			// A closed tenant answers the session as a tenant it holds nothing in.
			for _, domain := range tt.wantClosed {
				reaches, err := perms.Reaches(context.Background(), domain)
				if err != nil {
					t.Fatalf("Reaches(%s) error = %v", domain, err)
				}
				if reaches {
					t.Errorf("Reaches(%s) = true, want a closed tenant not reached", domain)
				}
				has, err := perms.HasGrants(context.Background(), accesstypes.DomainScope(domain))
				if err != nil {
					t.Fatalf("HasGrants(%s) error = %v", domain, err)
				}
				if has {
					t.Errorf("HasGrants(%s) = true, want nothing held in a closed tenant", domain)
				}
			}
		})
	}
}

// TestClosedDomains_otherPermissions pins that permissions SessionPermissions did not
// compose, and an application that keeps no declarations, list no closed tenant.
func TestClosedDomains_otherPermissions(t *testing.T) {
	t.Parallel()

	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"lakeside"}, nil
	}
	forUser := func(user accesstypes.User) *stubPermissions {
		return &stubPermissions{user: user, domains: []accesstypes.Domain{"lakeside"}}
	}
	// The role checker of these tests holds a grant in the domain "tenant" alone, and
	// that tenant admits its own directory only.
	roleRoster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"tenant"}, nil
	}
	forRole := func(role accesstypes.Role) *stubRolePermissions {
		return &stubRolePermissions{role: role}
	}
	directoryOnly := &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{
		"tenant": {Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "lakeside.example"}}, DirectoryOnly: true},
	}}
	asRole := resolvedAs(sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}), accesstypes.RolePrincipal("PartnerViewer"))
	tests := []struct {
		name  string
		perms UserPermissions
		// wantUnreached is a tenant the permissions hold a grant in and do not reach, so
		// the empty list is no accident of a tenant that is open.
		wantUnreached accesstypes.Domain
	}{
		{name: "permissions that are not a session's", perms: &stubPermissions{user: "alice", domains: []accesstypes.Domain{"lakeside"}}},
		{
			name:          "a session that acts as a role: its role holds a grant in a tenant this sign-in does not open, and the list is a person's own",
			perms:         SessionPermissions(asRole, forUser, forRole, roleRoster, directoryOnly.read, nil),
			wantUnreached: "tenant",
		},
		{
			name:  "an application that keeps no declarations",
			perms: SessionPermissions(sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}), forUser, RolePrincipalsUnsupported, roster, nil, nil),
		},
		{
			name:  "an application without tenants",
			perms: SessionPermissions(sessionCtx("alice", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}), forUser, RolePrincipalsUnsupported, nil, nil, nil),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			closed, err := ClosedDomains(context.Background(), tt.perms)
			if err != nil {
				t.Fatalf("ClosedDomains() error = %v", err)
			}
			if closed == nil || len(closed) != 0 {
				t.Errorf("ClosedDomains() = %v, want an empty list, never nil", closed)
			}
			if tt.wantUnreached == "" {
				return
			}
			reaches, err := tt.perms.Reaches(context.Background(), tt.wantUnreached)
			if err != nil {
				t.Fatalf("Reaches(%s) error = %v", tt.wantUnreached, err)
			}
			if reaches {
				t.Errorf("Reaches(%s) = true, want the tenant not reached", tt.wantUnreached)
			}
		})
	}
}

// TestUnenteredDomains pins the capability plan's question for a method a row offers
// only to a caller whose session enters the row's tenants: every tenant of the roster
// the session does not reach, sorted, for every session the permissions
// SessionPermissions composes answer, its own seats or not; none where every session
// reaches every tenant, and none from permissions SessionPermissions did not compose.
// Never nil.
func TestUnenteredDomains(t *testing.T) {
	t.Parallel()

	const (
		anvil   accesstypes.Domain = "anvil"
		bastion accesstypes.Domain = "bastion"
		cove    accesstypes.Domain = "cove"
	)
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	mailedCode := sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode}
	workspace := sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "acme"}
	impersonation := sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"}
	asBob := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.UserPrincipal("bob")}
	asBobMasked := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.UserPrincipal("bob"), Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read)}
	// Of the roster's three tenants, bastion admits its own directory only. The reader
	// counts its reads, so each case reads its own.
	declared := func() *declarationReader {
		return &declarationReader{declarations: map[accesstypes.Domain]*signin.TenantDeclaration{
			bastion: {Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "acme"}}, DirectoryOnly: true},
		}}
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{cove, bastion, anvil}, nil
	}

	tests := []struct {
		name           string
		ctx            context.Context
		noRoster       bool
		noDeclarations bool
		// disabled are the tenants of the roster that are disabled, each with what it
		// declares otherwise.
		disabled []accesstypes.Domain
		// holds are the tenants the person holds a grant in; super makes them a super
		// administrator; administers are the tenants they administer.
		holds       []accesstypes.Domain
		super       bool
		administers []accesstypes.Domain
		want        []accesstypes.Domain
	}{
		{
			name:  "a password session the directory-only tenant does not admit is given that tenant",
			ctx:   sessionCtx("alice", nil, password),
			holds: []accesstypes.Domain{anvil, bastion, cove},
			want:  []accesstypes.Domain{bastion},
		},
		{
			name: "so is one that holds no grant there: the list is the roster's, not the person's seats",
			ctx:  sessionCtx("alice", nil, password),
			want: []accesstypes.Domain{bastion},
		},
		{
			name:  "a session through the tenant's directory is given none",
			ctx:   sessionCtx("alice", nil, workspace),
			holds: []accesstypes.Domain{bastion},
			want:  []accesstypes.Domain{},
		},
		{
			name:  "a super administrator is given none",
			ctx:   sessionCtx("alice", nil, password),
			holds: []accesstypes.Domain{anvil},
			super: true,
			want:  []accesstypes.Domain{},
		},
		{
			name:        "the tenant's administrator with a password and a second factor is given none",
			ctx:         sessionCtx("alice", nil, password, mailedCode),
			holds:       []accesstypes.Domain{bastion},
			administers: []accesstypes.Domain{bastion},
			want:        []accesstypes.Domain{},
		},
		{
			name:        "the same administrator without the second factor is given the tenant",
			ctx:         sessionCtx("alice", nil, password),
			holds:       []accesstypes.Domain{bastion},
			administers: []accesstypes.Domain{bastion},
			want:        []accesstypes.Domain{bastion},
		},
		{
			name:  "a session that views the application as someone else is given the tenant, as the refusal asks of it",
			ctx:   sessionCtx("bob", asBob, impersonation),
			holds: []accesstypes.Domain{bastion},
			want:  []accesstypes.Domain{bastion},
		},
		{
			name:  "under a mask it is what the permissions under the mask answer",
			ctx:   sessionCtx("bob", asBobMasked, impersonation),
			holds: []accesstypes.Domain{bastion},
			want:  []accesstypes.Domain{bastion},
		},
		{
			// Being disabled is not a sign-in setting: what the session may not do there
			// is the door's, and the rows offer what the tenant's settings admit.
			name:     "a disabled tenant that is not directory only is not given to a password session",
			ctx:      sessionCtx("alice", nil, password),
			disabled: []accesstypes.Domain{anvil},
			holds:    []accesstypes.Domain{anvil, bastion},
			want:     []accesstypes.Domain{bastion},
		},
		{
			name:     "a disabled directory-only tenant is given to a password session, as its settings say",
			ctx:      sessionCtx("alice", nil, password),
			disabled: []accesstypes.Domain{bastion},
			holds:    []accesstypes.Domain{bastion},
			want:     []accesstypes.Domain{bastion},
		},
		{
			name:     "and not to a session through its directory",
			ctx:      sessionCtx("alice", nil, workspace),
			disabled: []accesstypes.Domain{bastion},
			holds:    []accesstypes.Domain{bastion},
			want:     []accesstypes.Domain{},
		},
		{
			name:     "an application without tenants is given none",
			ctx:      sessionCtx("alice", nil, password),
			noRoster: true,
			want:     []accesstypes.Domain{},
		},
		{
			name:           "an application that keeps no declarations is given none",
			ctx:            sessionCtx("alice", nil, password),
			noDeclarations: true,
			holds:          []accesstypes.Domain{bastion},
			want:           []accesstypes.Domain{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, domains: tt.holds, global: tt.super, super: tt.super, administers: tt.administers}
			}
			forRole := func(accesstypes.Role) *stubRolePermissions {
				return &stubRolePermissions{}
			}
			var tenants DomainRoster
			if !tt.noRoster {
				tenants = roster
			}
			var declarations TenantDeclarations
			if !tt.noDeclarations {
				reader := declared()
				for _, domain := range tt.disabled {
					declaration := reader.declarations[domain]
					if declaration == nil {
						declaration = &signin.TenantDeclaration{}
					}
					declaration.Disabled = true
					reader.declarations[domain] = declaration
				}
				declarations = reader.read
			}
			perms := SessionPermissions(tt.ctx, forUser, forRole, tenants, declarations, nil)

			got, err := unenteredDomains(context.Background(), perms)
			if err != nil {
				t.Fatalf("unenteredDomains() error = %v", err)
			}
			if got == nil {
				t.Fatal("unenteredDomains() = nil, want a list, never nil")
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("unenteredDomains() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("permissions that are not a session's are given none", func(t *testing.T) {
		t.Parallel()

		got, err := unenteredDomains(context.Background(), &stubPermissions{user: "alice", domains: []accesstypes.Domain{bastion}})
		if err != nil {
			t.Fatalf("unenteredDomains() error = %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("unenteredDomains() = %v, want an empty list, never nil", got)
		}
	})
}
