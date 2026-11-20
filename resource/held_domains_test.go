package resource

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestHeldDomains pins the list a rule on a person's tenants reads: every tenant the
// session's person holds a grant in, whatever the session shows of it. A mask that
// leaves nothing of the person's grants in their tenant takes the tenant off the
// session's own list (Domains) and not off this one, and so does a tenant that is closed
// to the way the session signed in. The first case is the one the create-your-tenant
// route met: a mask that keeps the creation, Execute alone, over a person whose tenant
// gives them reading, lists no tenant, and the person holds one.
func TestHeldDomains(t *testing.T) {
	t.Parallel()

	lakeside := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tid-lakeside"}
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	directoryOnly := map[accesstypes.Domain]*signin.TenantDeclaration{
		"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
	}
	actingAsBob := func(mask accesstypes.PermissionMask) *sessioninfo.Impersonation {
		return &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.UserPrincipal("bob"), Mask: mask}
	}
	executeAlone := accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Execute)

	tests := []struct {
		name string
		// declared are the tenants' declarations, nil for an application that keeps none.
		declared map[accesstypes.Domain]*signin.TenantDeclaration
		// seated are the tenants the session's person holds a grant in: reading, in each.
		seated        []accesstypes.Domain
		established   sessioninfo.AuthEvent
		impersonation *sessioninfo.Impersonation
		// wantListed is the session's own list (Domains), and wantHeld the person's.
		wantListed []accesstypes.Domain
		wantHeld   []accesstypes.Domain
	}{
		{
			name:        "a person's own session lists what they hold",
			seated:      []accesstypes.Domain{"riverside", "lakeside"},
			established: password,
			wantListed:  []accesstypes.Domain{"lakeside", "riverside"},
			wantHeld:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a person who holds no tenant holds none",
			established: password,
			wantListed:  []accesstypes.Domain{},
			wantHeld:    []accesstypes.Domain{},
		},
		{
			name:          "a session that acts as a person under a mask that keeps Execute alone lists no tenant, and the person holds theirs",
			seated:        []accesstypes.Domain{"riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(executeAlone),
			wantListed:    []accesstypes.Domain{},
			wantHeld:      []accesstypes.Domain{"riverside"},
		},
		{
			name:          "the same in an application that keeps declarations",
			declared:      directoryOnly,
			seated:        []accesstypes.Domain{"riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(executeAlone),
			wantListed:    []accesstypes.Domain{},
			wantHeld:      []accesstypes.Domain{"riverside"},
		},
		{
			name:          "a session that acts as a person with no mask lists what the person holds",
			seated:        []accesstypes.Domain{"riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(accesstypes.AllowAll()),
			wantListed:    []accesstypes.Domain{"riverside"},
			wantHeld:      []accesstypes.Domain{"riverside"},
		},
		{
			name:        "a tenant closed to a password session is off the session's list, and the person holds it",
			declared:    directoryOnly,
			seated:      []accesstypes.Domain{"lakeside"},
			established: password,
			wantListed:  []accesstypes.Domain{},
			wantHeld:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:          "a tenant closed to a session that acts as its person, under a mask that hides the other: the person holds both",
			declared:      directoryOnly,
			seated:        []accesstypes.Domain{"lakeside", "riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(executeAlone),
			wantListed:    []accesstypes.Domain{},
			wantHeld:      []accesstypes.Domain{"lakeside", "riverside"},
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"riverside", "lakeside", "unseated", "riverside"}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, digest: accesstypes.PermissionDigest{"documents": {accesstypes.Read: accesstypes.DigestGranted}}, domains: tt.seated}
			}
			var declarations TenantDeclarations
			if tt.declared != nil {
				declarations = (&declarationReader{declarations: tt.declared}).read
			}
			perms := SessionPermissions(sessionCtx("alice", tt.impersonation, tt.established), forUser, RolePrincipalsUnsupported, roster, declarations, nil)

			listed, err := perms.Domains(context.Background())
			if err != nil {
				t.Fatalf("Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantListed, listed); diff != "" {
				t.Errorf("Domains(), the session's own list, mismatch (-want +got):\n%s", diff)
			}
			held, err := HeldDomains(context.Background(), perms)
			if err != nil {
				t.Fatalf("HeldDomains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantHeld, held); diff != "" {
				t.Errorf("HeldDomains(), every tenant the person holds, mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHeldDomains_otherPermissions pins what the list is for permissions that
// SessionPermissions did not compose, which can say no more than they list; for an
// application without tenants, which has none to hold; and for a roster that fails,
// whose error is the answer's.
func TestHeldDomains_otherPermissions(t *testing.T) {
	t.Parallel()

	forUser := func(user accesstypes.User) *stubPermissions {
		return &stubPermissions{user: user, domains: []accesstypes.Domain{"lakeside"}}
	}
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	failing := func(context.Context) ([]accesstypes.Domain, error) {
		return nil, errors.New("roster unavailable")
	}
	tests := []struct {
		name    string
		perms   UserPermissions
		want    []accesstypes.Domain
		wantErr bool
	}{
		{
			name:  "permissions that are not a session's answer with what they list",
			perms: &stubPermissions{user: "alice", domains: []accesstypes.Domain{"lakeside"}},
			want:  []accesstypes.Domain{"lakeside"},
		},
		{
			name:  "permissions that list nothing answer an empty list, never nil",
			perms: &stubPermissions{user: "alice"},
			want:  []accesstypes.Domain{},
		},
		{
			name:  "the same under a mask",
			perms: Masked(&stubPermissions{user: "alice", domains: []accesstypes.Domain{"lakeside"}}, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Execute), nil),
			want:  []accesstypes.Domain{"lakeside"},
		},
		{
			name:  "an application without tenants",
			perms: SessionPermissions(sessionCtx("alice", nil, password), forUser, RolePrincipalsUnsupported, nil, nil, nil),
			want:  []accesstypes.Domain{},
		},
		{
			name:    "a roster that fails",
			perms:   SessionPermissions(sessionCtx("alice", nil, password), forUser, RolePrincipalsUnsupported, failing, nil, nil),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held, err := HeldDomains(context.Background(), tt.perms)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HeldDomains() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if held == nil {
				t.Fatal("HeldDomains() = nil, want a list, never nil")
			}
			if diff := cmp.Diff(tt.want, held); diff != "" {
				t.Errorf("HeldDomains() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
