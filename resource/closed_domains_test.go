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
