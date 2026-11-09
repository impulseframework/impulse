package policy

import (
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func Test_Records_Hash(t *testing.T) {
	t.Parallel()

	tenant1 := accesstypes.DomainPolicyScope("tenant1")
	tenant2 := accesstypes.DomainPolicyScope("tenant2")
	base := &Records{
		Grants: []Grant{
			{Scope: tenant1, Subject: Subject{Kind: SubjectRole, Name: "Editor"}, Perm: "Read", Resource: "employees"},
			{Scope: tenant1, Subject: Subject{Kind: SubjectRole, Name: "Editor"}, Perm: "Read", Resource: "employees", Field: "name"},
			{Scope: tenant2, Subject: Subject{Kind: SubjectUser, Name: "alice"}, Perm: "List", Resource: "widgets"},
		},
		Memberships: []Membership{
			{Scope: tenant1, Member: Subject{Kind: SubjectUser, Name: "erin"}, Role: "Editor"},
			{Scope: tenant2, Member: Subject{Kind: SubjectUser, Name: "bob"}, Role: "Viewer"},
		},
		Roles: []Role{
			{Scope: tenant1, Name: "Editor"},
			{Scope: tenant2, Name: "Viewer"},
		},
	}

	tests := []struct {
		name string
		// variant builds the records to compare against base.
		variant func() *Records
		// wantSameHash: row order must not matter; any content change must.
		wantSameHash bool
	}{
		{
			name: "row order does not change the hash",
			variant: func() *Records {
				return &Records{
					Grants:      []Grant{base.Grants[2], base.Grants[0], base.Grants[1]},
					Memberships: []Membership{base.Memberships[1], base.Memberships[0]},
					Roles:       []Role{base.Roles[1], base.Roles[0]},
				}
			},
			wantSameHash: true,
		},
		{
			name: "changed grant field changes the hash",
			variant: func() *Records {
				grants := slices.Clone(base.Grants)
				grants[2].Field = "name"

				return &Records{Grants: grants, Memberships: base.Memberships, Roles: base.Roles}
			},
			wantSameHash: false,
		},
		{
			name: "added grant condition changes the hash",
			variant: func() *Records {
				grants := slices.Clone(base.Grants)
				grants[2].Condition = "owner = @subject"

				return &Records{Grants: grants, Memberships: base.Memberships, Roles: base.Roles}
			},
			wantSameHash: false,
		},
		{
			name: "removed membership changes the hash",
			variant: func() *Records {
				return &Records{Grants: base.Grants, Memberships: base.Memberships[:1], Roles: base.Roles}
			},
			wantSameHash: false,
		},
		{
			name: "removed role row changes the hash",
			variant: func() *Records {
				return &Records{Grants: base.Grants, Memberships: base.Memberships, Roles: base.Roles[:1]}
			},
			wantSameHash: false,
		},
		{
			name: "the global partition hashes differently from a tenant",
			variant: func() *Records {
				grants := slices.Clone(base.Grants)
				grants[2].Scope = accesstypes.GlobalPolicyScope()

				return &Records{Grants: grants, Memberships: base.Memberships, Roles: base.Roles}
			},
			wantSameHash: false,
		},
		{
			name: "every domain hashes differently from a tenant",
			variant: func() *Records {
				memberships := slices.Clone(base.Memberships)
				memberships[1].Scope = accesstypes.EveryDomainPolicyScope()

				return &Records{Grants: base.Grants, Memberships: memberships, Roles: base.Roles}
			},
			wantSameHash: false,
		},
		{
			name: "a tenant literally named global is not the global partition",
			variant: func() *Records {
				grants := slices.Clone(base.Grants)
				grants[2].Scope = accesstypes.DomainPolicyScope("global")

				return &Records{Grants: grants, Memberships: base.Memberships, Roles: base.Roles}
			},
			wantSameHash: false,
		},
		{
			name: "changed subject kind changes the hash",
			variant: func() *Records {
				grants := slices.Clone(base.Grants)
				grants[2].Subject.Kind = SubjectRole

				return &Records{Grants: grants, Memberships: base.Memberships, Roles: base.Roles}
			},
			wantSameHash: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotSame := tt.variant().Hash() == base.Hash()
			if gotSame != tt.wantSameHash {
				t.Errorf("Hash() same as base = %v, want %v", gotSame, tt.wantSameHash)
			}
		})
	}
}

func Test_ScopeColumns_roundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scope      accesstypes.PolicyScope
		wantKind   string
		wantDomain string
	}{
		{name: "the global partition", scope: accesstypes.GlobalPolicyScope(), wantKind: KindGlobal},
		{name: "one tenant", scope: accesstypes.DomainPolicyScope("tenant1"), wantKind: KindDomain, wantDomain: "tenant1"},
		{name: "every tenant", scope: accesstypes.EveryDomainPolicyScope(), wantKind: KindEvery},
		{name: "a tenant named global", scope: accesstypes.DomainPolicyScope("global"), wantKind: KindDomain, wantDomain: "global"},
		{name: "a tenant named every", scope: accesstypes.DomainPolicyScope("every"), wantKind: KindDomain, wantDomain: "every"},
		{name: "the zero value is the zero domain's partition", scope: accesstypes.PolicyScope{}, wantKind: KindDomain},
		{name: "a converted tenant scope", scope: accesstypes.DomainScope("tenant1").PolicyScope(), wantKind: KindDomain, wantDomain: "tenant1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Every constructible scope belongs to the default axis, stored as "".
			kind, axis, domain := ScopeColumns(tt.scope)
			if kind != tt.wantKind || axis != "" || domain != tt.wantDomain {
				t.Errorf("ScopeColumns() = (%q, %q, %q), want (%q, %q, %q)", kind, axis, domain, tt.wantKind, "", tt.wantDomain)
			}
			got, err := ScopeFromColumns(kind, axis, domain)
			if err != nil {
				t.Fatalf("ScopeFromColumns() error = %v", err)
			}
			if got != tt.scope {
				t.Errorf("ScopeFromColumns() = %v, want %v", got, tt.scope)
			}
		})
	}
}

// Test_ScopeFromColumns_refuses pins the fail-closed posture: a stored row
// under an axis this build cannot name, or of a kind the stores never write,
// is refused rather than folded into a partition it was not written for.
func Test_ScopeFromColumns_refuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     string
		axis     string
		domain   string
		wantText string
	}{
		{name: "tenant row under a named axis", kind: KindDomain, axis: "region", domain: "tenant1", wantText: "region"},
		{name: "global row under a named axis", kind: KindGlobal, axis: "region", wantText: "region"},
		{name: "every-domain row under a named axis", kind: KindEvery, axis: "region", wantText: "region"},
		{name: "an unknown kind", kind: "all", wantText: `kind "all"`},
		{name: "an empty kind", kind: "", wantText: `kind ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ScopeFromColumns(tt.kind, tt.axis, tt.domain)
			if err == nil {
				t.Fatalf("ScopeFromColumns(%q, %q, %q) = %v, want an error", tt.kind, tt.axis, tt.domain, got)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("ScopeFromColumns() error = %q, want it to contain %q", err, tt.wantText)
			}
		})
	}
}

// Test_CompareScopes pins the order a store lists rows in: by kind, then
// axis, then domain, with the kinds in their name order.
func Test_CompareScopes(t *testing.T) {
	t.Parallel()

	scopes := []accesstypes.PolicyScope{
		accesstypes.GlobalPolicyScope(),
		accesstypes.EveryDomainPolicyScope(),
		accesstypes.DomainPolicyScope("b"),
		accesstypes.DomainPolicyScope("a"),
	}
	slices.SortFunc(scopes, CompareScopes)
	want := []accesstypes.PolicyScope{
		accesstypes.DomainPolicyScope("a"),
		accesstypes.DomainPolicyScope("b"),
		accesstypes.EveryDomainPolicyScope(),
		accesstypes.GlobalPolicyScope(),
	}
	if !slices.Equal(scopes, want) {
		t.Errorf("sorted scopes = %v, want %v", scopes, want)
	}
}
