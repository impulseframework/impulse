package policy

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
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

// Test_Records_Hash_periods pins that an assignment's period is policy: a start
// or an end given, changed or taken away changes the hash, on a person's own
// membership, a group's role and a person's place in a group, so a period
// replaced in the store reaches every instance's snapshot; a start and an end
// of the same instant hash apart; and no start hashes apart from the earliest
// instant there is.
func Test_Records_Hash_periods(t *testing.T) {
	t.Parallel()

	tenant1 := accesstypes.DomainPolicyScope("tenant1")
	noon := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	records := func(own, group, place accesstypes.Period) *Records {
		return &Records{
			Memberships: []Membership{
				{Scope: tenant1, Member: Subject{Kind: SubjectUser, Name: "erin"}, Role: "Editor", Period: own},
				{Scope: tenant1, Member: Subject{Kind: SubjectGroup, Name: "night"}, Role: "Editor", Period: group},
			},
			Groups:       []Group{{ID: "night", Scope: tenant1}},
			GroupMembers: []GroupMember{{Group: "night", User: "bob", Period: place}},
		}
	}
	none := accesstypes.Period{}
	variants := map[string]accesstypes.Period{
		"an end":                      {End: noon},
		"a start":                     {Start: noon},
		"a start and an end":          {Start: noon, End: noon.Add(time.Hour)},
		"a later end":                 {End: noon.Add(time.Nanosecond)},
		"a start at the Unix epoch":   {Start: time.Unix(0, 0).UTC()},
		"an end at the Unix epoch":    {End: time.Unix(0, 0).UTC()},
		"a start in another location": {Start: noon.In(time.FixedZone("east", 3600)).Add(time.Hour)},
	}
	seen := map[[32]byte]string{records(none, none, none).Hash(): "no period anywhere"}
	for name, period := range variants {
		for where, r := range map[string]*Records{
			"a person's own membership": records(period, none, none),
			"a group's role":            records(none, period, none),
			"a place in a group":        records(none, none, period),
		} {
			label := name + " on " + where
			if other, ok := seen[r.Hash()]; ok {
				t.Errorf("%s hashes as %s does", label, other)
			}
			seen[r.Hash()] = label
		}
	}
	// The same instant in another location is the same instant.
	if records(accesstypes.Period{End: noon}, none, none).Hash() != records(accesstypes.Period{End: noon.In(time.FixedZone("east", 3600))}, none, none).Hash() {
		t.Error("an end at one instant hashes apart by the location it was written in")
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

// Test_GroupColumns_roundTrip pins where a group can be kept and how the stores
// spell it: for the whole application or in one tenant, on the default axis.
func Test_GroupColumns_roundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scope      accesstypes.PolicyScope
		wantKind   string
		wantDomain string
	}{
		{name: "the whole application", scope: accesstypes.GlobalPolicyScope(), wantKind: KindGlobal},
		{name: "one tenant", scope: accesstypes.DomainPolicyScope("tenant1"), wantKind: KindDomain, wantDomain: "tenant1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kind, axis, domain, err := GroupColumns(tt.scope)
			if err != nil {
				t.Fatalf("GroupColumns() error = %v", err)
			}
			if kind != tt.wantKind || axis != "" || domain != tt.wantDomain {
				t.Errorf("GroupColumns() = (%q, %q, %q), want (%q, %q, %q)", kind, axis, domain, tt.wantKind, "", tt.wantDomain)
			}
			got, err := GroupFromColumns(kind, axis, domain)
			if err != nil {
				t.Fatalf("GroupFromColumns() error = %v", err)
			}
			if got != tt.scope {
				t.Errorf("GroupFromColumns() = %v, want %v", got, tt.scope)
			}
		})
	}

	if _, _, _, err := GroupColumns(accesstypes.EveryDomainPolicyScope()); err == nil {
		t.Errorf("GroupColumns() of every tenant: want an error, a group is never kept in every tenant")
	}
}

// Test_GroupFromColumns_refuses pins that a group row no store writes is refused
// when it is read: one kept in every tenant, and one kept under an axis this
// build cannot name. Whatever tells a tenant's groups apart by the tenant's key
// alone (the generated set of a person's groups does) rests on the second: a key
// says which tenant only on one axis.
func Test_GroupFromColumns_refuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     string
		axis     string
		domain   string
		wantText string
	}{
		{name: "a group kept in every tenant", kind: KindEvery, wantText: `kind "every"`},
		{name: "a tenant's group under a named axis", kind: KindDomain, axis: "region", domain: "tenant1", wantText: "region"},
		{name: "a group for the whole application under a named axis", kind: KindGlobal, axis: "region", wantText: "region"},
		{name: "an unknown kind", kind: "all", wantText: `kind "all"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := GroupFromColumns(tt.kind, tt.axis, tt.domain)
			if err == nil {
				t.Fatalf("GroupFromColumns(%q, %q, %q) = %v, want an error", tt.kind, tt.axis, tt.domain, got)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("GroupFromColumns() error = %q, want it to contain %q", err, tt.wantText)
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

// Test_SameRoleName pins the one comparison of role names: the case of ASCII
// letters alone, never Unicode's folding, so a letter from outside ASCII is
// the same only as itself.
func Test_SameRoleName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "the same name", a: "Zed", b: "Zed", want: true},
		{name: "names that differ in the case of ASCII letters", a: "Zed", b: "zED", want: true},
		{name: "names of different lengths", a: "Zed", b: "Zeds", want: false},
		{name: "different names", a: "Zed", b: "Zee", want: false},
		{name: "the Kelvin sign is not k", a: "Key", b: "Key", want: false},
		{name: "the long s is not s", a: "ſteward", b: "Steward", want: false},
		{name: "letters from outside ASCII that differ in case are different names", a: "Été", b: "été", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SameRoleName(tt.a, tt.b); got != tt.want {
				t.Errorf("SameRoleName(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := FoldRoleName(tt.a) == FoldRoleName(tt.b); got != tt.want {
				t.Errorf("FoldRoleName(%q) == FoldRoleName(%q) is %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// Test_FoldRoleName pins the folded form: ASCII capital letters in lower case,
// every other byte as written.
func Test_FoldRoleName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"TenantAdministrator": "tenantadministrator",
		"Zed9":                "zed9",
		"Key":                 "Key",
		"ÉTE":                 "Éte",
	} {
		if got := FoldRoleName(name); got != want {
			t.Errorf("FoldRoleName(%q) = %q, want %q", name, got, want)
		}
	}
}

// Test_RoleNamesMeet pins where a new role's name is held to a role held:
// the global partition to itself, one domain to itself and every domain,
// every domain to itself and any one domain; nothing else.
func Test_RoleNamesMeet(t *testing.T) {
	t.Parallel()

	global := accesstypes.GlobalPolicyScope()
	every := accesstypes.EveryDomainPolicyScope()
	tenant1 := accesstypes.DomainPolicyScope("tenant1")
	tenant2 := accesstypes.DomainPolicyScope("tenant2")

	tests := []struct {
		name        string
		scope, held accesstypes.PolicyScope
		want        bool
	}{
		{name: "the global partition meets the global partition", scope: global, held: global, want: true},
		{name: "one domain meets the same domain", scope: tenant1, held: tenant1, want: true},
		{name: "one domain meets every domain", scope: tenant1, held: every, want: true},
		{name: "every domain meets every domain", scope: every, held: every, want: true},
		{name: "every domain meets one domain", scope: every, held: tenant1, want: true},
		{name: "every domain meets another domain", scope: every, held: tenant2, want: true},
		{name: "the global partition does not meet a domain", scope: global, held: tenant1, want: false},
		{name: "the global partition does not meet every domain", scope: global, held: every, want: false},
		{name: "a domain does not meet the global partition", scope: tenant1, held: global, want: false},
		{name: "every domain does not meet the global partition", scope: every, held: global, want: false},
		{name: "two different domains do not meet", scope: tenant1, held: tenant2, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RoleNamesMeet(tt.scope, tt.held); got != tt.want {
				t.Errorf("RoleNamesMeet(%s, %s) = %v, want %v", tt.scope, tt.held, got, tt.want)
			}
		})
	}
}

// Test_CaseTwin pins which held role a new name is a case twin of: one held
// where the name meets it, spelled otherwise but the same but for case, the
// first in the order the refusal names.
func Test_CaseTwin(t *testing.T) {
	t.Parallel()

	global := accesstypes.GlobalPolicyScope()
	every := accesstypes.EveryDomainPolicyScope()
	tenant1 := accesstypes.DomainPolicyScope("tenant1")
	tenant2 := accesstypes.DomainPolicyScope("tenant2")

	tests := []struct {
		name     string
		roles    []Role
		scope    accesstypes.PolicyScope
		role     accesstypes.Role
		want     Role
		wantTwin bool
	}{
		{name: "no twin", roles: []Role{{Scope: tenant1, Name: "Admin"}}, scope: tenant1, role: "Zed"},
		{name: "the same exact name in a place that meets is no twin", roles: []Role{{Scope: every, Name: "Zed"}, {Scope: tenant1, Name: "Zed"}}, scope: tenant1, role: "Zed"},
		{name: "a twin in the global partition", roles: []Role{{Scope: global, Name: "ZED"}}, scope: global, role: "Zed", want: Role{Scope: global, Name: "ZED"}, wantTwin: true},
		{name: "a twin in the same domain", roles: []Role{{Scope: tenant1, Name: "ZED"}}, scope: tenant1, role: "Zed", want: Role{Scope: tenant1, Name: "ZED"}, wantTwin: true},
		{name: "a twin of one domain's name in every domain", roles: []Role{{Scope: every, Name: "ZED"}}, scope: tenant1, role: "Zed", want: Role{Scope: every, Name: "ZED"}, wantTwin: true},
		{name: "a twin in every domain", roles: []Role{{Scope: every, Name: "ZED"}}, scope: every, role: "Zed", want: Role{Scope: every, Name: "ZED"}, wantTwin: true},
		{name: "a twin of every domain's name in one domain", roles: []Role{{Scope: tenant2, Name: "ZED"}}, scope: every, role: "Zed", want: Role{Scope: tenant2, Name: "ZED"}, wantTwin: true},
		{name: "rows that do not meet the scope are ignored", roles: []Role{{Scope: global, Name: "ZED"}, {Scope: tenant2, Name: "zed"}}, scope: tenant1, role: "Zed"},
		{name: "the scope's own twin first", roles: []Role{{Scope: every, Name: "ZED"}, {Scope: tenant1, Name: "zed"}}, scope: tenant1, role: "Zed", want: Role{Scope: tenant1, Name: "zed"}, wantTwin: true},
		{name: "every domain's twin before a domain's", roles: []Role{{Scope: tenant1, Name: "ZED"}, {Scope: tenant2, Name: "zed"}, {Scope: every, Name: "zED"}}, scope: every, role: "Zed", want: Role{Scope: every, Name: "zED"}, wantTwin: true},
		{name: "then by domain", roles: []Role{{Scope: tenant2, Name: "ZED"}, {Scope: tenant1, Name: "zed"}}, scope: every, role: "Zed", want: Role{Scope: tenant1, Name: "zed"}, wantTwin: true},
		{name: "then by name", roles: []Role{{Scope: tenant1, Name: "zed"}, {Scope: tenant1, Name: "ZEd"}}, scope: every, role: "Zed", want: Role{Scope: tenant1, Name: "ZEd"}, wantTwin: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, twin := CaseTwin(tt.roles, tt.scope, tt.role)
			if twin != tt.wantTwin || got != tt.want {
				t.Errorf("CaseTwin(%v, %s, %q) = (%v, %v), want (%v, %v)", tt.roles, tt.scope, tt.role, got, twin, tt.want, tt.wantTwin)
			}
		})
	}
}

// Test_CaseTwinError pins the refusal's words and that a wrap keeps it
// findable.
func Test_CaseTwinError(t *testing.T) {
	t.Parallel()

	err := errors.Wrap(&CaseTwinError{Held: Role{Scope: accesstypes.EveryDomainPolicyScope(), Name: "ZED"}}, "insert role")
	var twin *CaseTwinError
	if !errors.As(err, &twin) {
		t.Fatalf("errors.As(%v) found no *CaseTwinError", err)
	}
	if got, want := twin.Error(), `the role "ZED" held in scope every domain differs from the name only in case`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// Test_Records_Hash_recordMemberships pins that a role held on a record is policy: its
// row, each of its columns and its period change the hash, and the order rows come in
// does not.
func Test_Records_Hash_recordMemberships(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	project := accesstypes.NewRecordScope("tenant1", "Projects", "p-1")
	held := func(rows ...RecordMembership) *Records {
		return &Records{RecordMemberships: rows}
	}
	erin := RecordMembership{Scope: project, Member: Subject{Kind: SubjectUser, Name: "erin"}, Role: "ProjectEditor"}
	night := RecordMembership{Scope: project, Member: Subject{Kind: SubjectGroup, Name: "night"}, Role: "ProjectEditor"}

	if held(erin, night).Hash() != held(night, erin).Hash() {
		t.Error("the rows' order changes the hash")
	}
	seen := map[[32]byte]string{held().Hash(): "no row", held(erin).Hash(): "erin's row"}
	changed := func(change func(*RecordMembership)) *Records {
		m := erin
		change(&m)

		return held(m)
	}
	for name, r := range map[string]*Records{
		"another tenant":  changed(func(m *RecordMembership) { m.Scope = accesstypes.NewRecordScope("tenant2", "Projects", "p-1") }),
		"another kind":    changed(func(m *RecordMembership) { m.Scope = accesstypes.NewRecordScope("tenant1", "Clients", "p-1") }),
		"another record":  changed(func(m *RecordMembership) { m.Scope = accesstypes.NewRecordScope("tenant1", "Projects", "p-2") }),
		"a group":         changed(func(m *RecordMembership) { m.Member.Kind = SubjectGroup }),
		"another person":  changed(func(m *RecordMembership) { m.Member.Name = "bob" }),
		"another role":    changed(func(m *RecordMembership) { m.Role = "ProjectViewer" }),
		"an end":          changed(func(m *RecordMembership) { m.Period = accesstypes.Period{End: noon} }),
		"a start":         changed(func(m *RecordMembership) { m.Period = accesstypes.Period{Start: noon} }),
		"two rows":        held(erin, night),
		"a tenant's role": {Memberships: []Membership{{Scope: accesstypes.DomainPolicyScope("tenant1"), Member: erin.Member, Role: erin.Role}}},
	} {
		if other, ok := seen[r.Hash()]; ok {
			t.Errorf("%s hashes as %s does", name, other)
		}
		seen[r.Hash()] = name
	}
}

// Test_RecordColumns_roundTrip pins how the stores spell the record a role is held on,
// and that a row under an axis this build cannot name is refused.
func Test_RecordColumns_roundTrip(t *testing.T) {
	t.Parallel()

	for _, record := range []accesstypes.RecordScope{
		accesstypes.NewRecordScope("tenant1", "Projects", "p-1"),
		accesstypes.NewRecordScope("global", "Projects", "a key with spaces"),
		{},
	} {
		axis, domain, resource, key := RecordColumns(record)
		if axis != "" || domain != string(record.Domain()) || resource != string(record.Resource()) || key != record.Record() {
			t.Errorf("RecordColumns(%v) = (%q, %q, %q, %q), want the default axis and the record's parts", record, axis, domain, resource, key)
		}
		got, err := RecordFromColumns(axis, domain, resource, key)
		if err != nil {
			t.Fatalf("RecordFromColumns() error = %v", err)
		}
		if got != record {
			t.Errorf("RecordFromColumns() = %v, want %v", got, record)
		}
	}
	if got, err := RecordFromColumns("region", "tenant1", "Projects", "p-1"); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("RecordFromColumns() under a named axis = (%v, %v), want it refused naming the axis", got, err)
	}
}
