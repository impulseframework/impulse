package resource

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// digestCounter is a stubPermissions that counts the digests it is asked for.
type digestCounter struct {
	*stubPermissions

	digests int
}

func (d *digestCounter) PermissionDigest(ctx context.Context, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	d.digests++

	return d.stubPermissions.PermissionDigest(ctx, scope)
}

// TestAdmittedPermissions_zeroScope pins the session's admission asked in the zero
// scope: Check and PermissionDigest refuse it with accesstypes.ErrNoScope, and
// neither a tenant's declaration nor the checker inside is asked. Asked in a
// tenant's scope or the global scope, they answer as they did.
func TestAdmittedPermissions_zeroScope(t *testing.T) {
	t.Parallel()

	const anvil accesstypes.Domain = "anvil"
	tests := []struct {
		name        string
		scope       accesstypes.Scope
		wantErr     error
		wantReads   int
		wantAsked   int
		wantDigests int
	}{
		{name: "the zero Scope", scope: accesstypes.Scope{}, wantErr: accesstypes.ErrNoScope},
		{name: "the scope of an empty tenant", scope: accesstypes.DomainScope(""), wantErr: accesstypes.ErrNoScope},
		{name: "a tenant's scope", scope: accesstypes.DomainScope(anvil), wantReads: 1, wantAsked: 1, wantDigests: 1},
		{name: "the global scope", scope: accesstypes.GlobalScope(), wantAsked: 1, wantDigests: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &declarationReader{}
			inner := &digestCounter{stubPermissions: &stubPermissions{user: "alice", domains: []accesstypes.Domain{anvil}}}
			admitted := &admittedPermissions{UserPermissionChecker: inner, declarations: reader.read, reached: map[accesstypes.Domain]bool{}}

			decisions, err := admitted.Check(context.Background(), accesstypes.NewEnvironment(), tt.scope, accesstypes.Read, "documents")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Check() error = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil || !decisions["documents"].IsGranted() {
				t.Errorf("Check() = %v, %v; want documents granted", decisions, err)
			}
			_, err = admitted.PermissionDigest(context.Background(), tt.scope)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("PermissionDigest() error = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("PermissionDigest() error = %v", err)
			}

			reads := 0
			for _, n := range reader.reads {
				reads += n
			}
			if reads != tt.wantReads {
				t.Errorf("a tenant's declaration was read %d times, want %d", reads, tt.wantReads)
			}
			if got := len(inner.checkPerms); got != tt.wantAsked {
				t.Errorf("the checker inside was asked %d checks, want %d", got, tt.wantAsked)
			}
			if inner.digests != tt.wantDigests {
				t.Errorf("the checker inside was asked %d digests, want %d", inner.digests, tt.wantDigests)
			}
		})
	}
}

// TestMaskedPermissions_zeroScope pins an impersonated session's check asked in the
// zero scope: under a mask that does not keep the permission's kind, and under one
// that refuses every resource asked about, Check refuses it with
// accesstypes.ErrNoScope and the checker inside is not asked. Asked in a tenant's
// scope, each answers Denied.
func TestMaskedPermissions_zeroScope(t *testing.T) {
	t.Parallel()

	const anvil accesstypes.Domain = "anvil"
	masks := []struct {
		name string
		mask accesstypes.PermissionMask
	}{
		{name: "a mask that does not keep the kind", mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Execute)},
		{name: "a mask that refuses every resource asked about", mask: accesstypes.AllowAll().Refusing(accesstypes.RefusePermission(accesstypes.Read, "documents"))},
	}
	scopes := []struct {
		name    string
		scope   accesstypes.Scope
		wantErr bool
	}{
		{name: "the zero Scope", scope: accesstypes.Scope{}, wantErr: true},
		{name: "the scope of an empty tenant", scope: accesstypes.DomainScope(""), wantErr: true},
		{name: "a tenant's scope", scope: accesstypes.DomainScope(anvil)},
	}
	for _, m := range masks {
		for _, s := range scopes {
			t.Run(m.name+", "+s.name, func(t *testing.T) {
				t.Parallel()

				inner := &stubPermissions{user: "alice", domains: []accesstypes.Domain{anvil}}
				decisions, err := Masked(inner, m.mask, nil).Check(context.Background(), accesstypes.NewEnvironment(), s.scope, accesstypes.Read, "documents")
				if s.wantErr {
					if !errors.Is(err, accesstypes.ErrNoScope) {
						t.Errorf("Check() error = %v, want accesstypes.ErrNoScope", err)
					}
				} else if err != nil || !decisions["documents"].IsDenied() {
					t.Errorf("Check() = %v, %v; want documents denied", decisions, err)
				}
				if len(inner.checkPerms) != 0 {
					t.Errorf("the checker inside was asked %d checks, want none", len(inner.checkPerms))
				}
			})
		}
	}
}

// TestUnsupportedRolePermissions_zeroScope pins a role principal with no role
// checker asked in the zero scope: Check, PermissionDigest and HasGrants refuse it
// with accesstypes.ErrNoScope. Asked in a tenant's scope or the global scope, they
// answer as they did: Denied, an empty digest, and no grants.
func TestUnsupportedRolePermissions_zeroScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scope   accesstypes.Scope
		wantErr bool
	}{
		{name: "the zero Scope", scope: accesstypes.Scope{}, wantErr: true},
		{name: "the scope of an empty tenant", scope: accesstypes.DomainScope(""), wantErr: true},
		{name: "a tenant's scope", scope: accesstypes.DomainScope("anvil")},
		{name: "the global scope", scope: accesstypes.GlobalScope()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			perms := unsupportedRolePermissions{user: "alice", role: "Auditor"}
			decisions, checkErr := perms.Check(ctx, accesstypes.NewEnvironment(), tt.scope, accesstypes.Read, "documents")
			digest, digestErr := perms.PermissionDigest(ctx, tt.scope)
			holds, grantsErr := perms.HasGrants(ctx, tt.scope)
			errs := map[string]error{"Check": checkErr, "PermissionDigest": digestErr, "HasGrants": grantsErr}
			if tt.wantErr {
				for method, err := range errs {
					if !errors.Is(err, accesstypes.ErrNoScope) {
						t.Errorf("%s() error = %v, want accesstypes.ErrNoScope", method, err)
					}
				}

				return
			}
			for method, err := range errs {
				if err != nil {
					t.Errorf("%s() error = %v, want an answer", method, err)
				}
			}
			if !decisions["documents"].IsDenied() {
				t.Errorf("Check() = %v, want documents denied", decisions)
			}
			if len(digest) != 0 {
				t.Errorf("PermissionDigest() = %v, want an empty digest", digest)
			}
			if holds {
				t.Error("HasGrants() = true, want false")
			}
		})
	}
}
