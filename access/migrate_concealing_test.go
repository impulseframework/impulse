package access

// These tests pin the concealing-key warning: which conditional List grants
// raise it (a concealing field the resource orders by or admits as a sort or
// filter key, whose condition the role's other grants leave standing in the
// query), which do not (the Veteran's one condition on every field, a
// positional field, an unconditional key, a key the role is not granted, a
// key whose condition the other grants imply), and what the line says.

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// concealingCollection is the fixture vocabulary, shaped like Lodestar's
// Missions: a domain-scoped resource listed by deadline whose fee is a filter
// key, with the conditions' attributes. Every field conceals.
type concealingCollection struct{}

func (concealingCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	return map[accesstypes.Permission][]accesstypes.Resource{
		"List": {"Missions", "Missions.title", "Missions.hazard", "Missions.fee", "Missions.deadline", "Missions.settlement"},
		"Read": {"Missions", "Missions.title", "Missions.hazard", "Missions.fee", "Missions.deadline", "Missions.settlement"},
	}
}

func (concealingCollection) Scope(accesstypes.Resource) accesstypes.PermissionScope {
	return accesstypes.DomainPermissionScope
}

func (concealingCollection) IsResourceImmutable(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (concealingCollection) AttributeComparisonType(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	if res != "Missions" {
		return "", false
	}
	switch name {
	case "hazard", "fee":
		return accesstypes.AttributeTypeNumber, true
	case "state":
		return accesstypes.AttributeTypeString, true
	default:
		return "", false
	}
}

func (concealingCollection) AttributeIsColumn(_ accesstypes.PermissionScope, res accesstypes.Resource, _ string) bool {
	return res == "Missions"
}

func (concealingCollection) SubjectSetComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (concealingCollection) SubjectValueComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (concealingCollection) IsComputedResource(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (concealingCollection) MethodTarget(accesstypes.PermissionScope, accesstypes.Resource) (accesstypes.Resource, bool) {
	return "", false
}

func (concealingCollection) ConcealingKeys(_ accesstypes.PermissionScope, res accesstypes.Resource) (order, keys []accesstypes.Tag) {
	if res != "Missions" {
		return nil, nil
	}

	return []accesstypes.Tag{"deadline"}, []accesstypes.Tag{"fee"}
}

// positionalDeadlineCollection is concealingCollection with the deadline
// declared masking:"positional": the collection no longer reports it.
type positionalDeadlineCollection struct{ concealingCollection }

func (positionalDeadlineCollection) ConcealingKeys(_ accesstypes.PermissionScope, res accesstypes.Resource) (order, keys []accesstypes.Tag) {
	if res != "Missions" {
		return nil, nil
	}

	return nil, []accesstypes.Tag{"fee"}
}

const (
	closedStates = "state IN ('completed', 'failed', 'stood_down')"
	completed    = "state = 'completed'"
)

var (
	// The Archivist's two grants: the row fields on the closed states, the
	// money fields on completed only.
	archivistRows  = Grant{Resource: "Missions", Fields: []accesstypes.Tag{"title", "hazard", "deadline"}, Condition: closedStates}
	archivistMoney = Grant{Resource: "Missions", Fields: []accesstypes.Tag{"fee", "settlement"}, Condition: completed}
)

func TestValidateRoles_concealingKeyWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		store PermissionCollection
		roles ScopedRoles
		want  []Warning
	}{
		{
			name:  "one condition on every field prunes the CASE: the Veteran raises nothing",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Veteran", Description: "Reads the missions it served on.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {{Resource: "Missions", Fields: []accesstypes.Tag{"title", "hazard", "fee", "deadline"}, Condition: "NOT (hazard IN (1, 2) OR fee < 5000)"}},
			}}}},
		},
		{
			name:  "the Archivist's completed grant implies the closed states: the default order is covered, the narrower fee is not",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Archivist", Description: "Keeps the archive.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {archivistRows, archivistMoney},
			}}}},
			want: []Warning{
				ConcealingKeyWarning{Role: "Archivist", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "fee", Conditions: []string{completed}, Uncovered: []string{closedStates}},
			},
		},
		{
			name:  "a sibling condition outside the key's IN list is not implied: the default order warns",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title", "deadline"}, Condition: closedStates},
					{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}, Condition: "state = 'open'"},
				},
			}}}},
			want: []Warning{
				ConcealingKeyWarning{Role: "Clerk", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "deadline", Conditions: []string{closedStates}, DefaultOrder: true, Uncovered: []string{"state = 'open'"}},
			},
		},
		{
			name:  "the key's equalities merge into one set that a sibling's IN list implies",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title"}, Condition: "state IN ('failed', 'stood_down')"},
					{Resource: "Missions", Fields: []accesstypes.Tag{"deadline"}, Condition: "state = 'failed' OR state = 'stood_down'"},
				},
			}}}},
		},
		{
			name:  "a sibling's conjunction implies the key's condition through one conjunct; the conjunction itself is implied by nothing",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title", "deadline"}, Condition: closedStates},
					{Resource: "Missions", Fields: []accesstypes.Tag{"fee"}, Condition: "state = 'completed' AND fee > 0"},
				},
			}}}},
			want: []Warning{
				ConcealingKeyWarning{Role: "Clerk", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "fee", Conditions: []string{"state = 'completed' AND fee > 0"}, Uncovered: []string{closedStates}},
			},
		},
		{
			name:  "a positional deadline is not the collection's to report: only the fee warns",
			store: positionalDeadlineCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Archivist", Description: "Keeps the archive.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {archivistRows, archivistMoney},
			}}}},
			want: []Warning{
				ConcealingKeyWarning{Role: "Archivist", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "fee", Conditions: []string{completed}, Uncovered: []string{closedStates}},
			},
		},
		{
			name:  "an unconditional sibling field keeps every CASE",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title"}},
					{Resource: "Missions", Fields: []accesstypes.Tag{"fee"}, Condition: completed},
				},
			}}}},
			want: []Warning{
				ConcealingKeyWarning{Role: "Clerk", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "fee", Conditions: []string{completed}, Unconditional: true},
			},
		},
		{
			name:  "a key the role is not granted is refused as a sort, so nothing masks",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title"}, Condition: closedStates},
					{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}, Condition: completed},
				},
			}}}},
		},
		{
			name:  "a key granted unconditionally is a plain column",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"fee", "deadline"}},
					{Resource: "Missions", Fields: []accesstypes.Tag{"title"}, Condition: completed},
				},
			}}}},
		},
		{
			name:  "a key granted under both of the role's conditions covers the row predicate",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Clerk", Description: "Keeps the records.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"title", "fee", "deadline"}, Condition: closedStates},
					{Resource: "Missions", Fields: []accesstypes.Tag{"hazard", "fee", "deadline"}, Condition: completed},
				},
			}}}},
			want: []Warning{
				// hazard is neither ordered by nor a key; title is not a key; the keys cover.
			},
		},
		{
			name:  "Read grants sort nothing",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Archivist", Description: "Keeps the archive.", Permissions: map[accesstypes.Permission][]Grant{
				"Read": {archivistRows, archivistMoney},
			}}}},
		},
		{
			name:  "a role without List field grants on the resource raises nothing",
			store: concealingCollection{},
			roles: ScopedRoles{Domain: []*Role{{Name: "Gatekeeper", Description: "Admits the crews.", Permissions: map[accesstypes.Permission][]Grant{
				"List": {{Resource: "Missions", Condition: completed}},
			}}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateRoles(tt.store, &RoleConfig{Roles: tt.roles})
			if err != nil {
				t.Fatalf("ValidateRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ValidateRoles() warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConcealingKeyWarning_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		warning ConcealingKeyWarning
		want    string
	}{
		{
			name:    "a filter key under a narrower condition than a sibling's",
			warning: ConcealingKeyWarning{Role: "Archivist", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "fee", Conditions: []string{completed}, Uncovered: []string{closedStates}},
			want: `role Archivist: List on Missions.fee is granted under "state = 'completed'", and fee is a sort or filter key of Missions whose masked cells conceal; ` +
				`this role also lists Missions fields under "state IN ('completed', 'failed', 'stood_down')", which the field's condition does not cover, ` +
				`so the row filter does not prove the field's condition and a page this role sorts or filters by fee orders on CASE WHEN <condition> THEN column END, which no index serves: it sorts the tenant's whole partition. ` +
				`Grant fee unconditionally in this role, tag the field masking:"positional" and disclose where its hidden values fall, or accept the cost for a table that never pages at volume.`,
		},
		{
			name:    "the default order beside an unconditional sibling",
			warning: ConcealingKeyWarning{Role: "Clerk", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Field: "deadline", Conditions: []string{"hazard < 3", completed}, DefaultOrder: true, Unconditional: true},
			want: `role Clerk: List on Missions.deadline is granted under "hazard < 3" or "state = 'completed'", and deadline is the default order of Missions whose masked cells conceal; ` +
				`this role lists other Missions fields unconditionally, ` +
				`so the row filter does not prove the field's condition and every page this role lists orders on CASE WHEN <condition> THEN column END, which no index serves: it sorts the tenant's whole partition. ` +
				`Grant deadline unconditionally in this role, tag the field masking:"positional" and disclose where its hidden values fall, or accept the cost for a table that never pages at volume.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.warning.String(); got != tt.want {
				t.Errorf("ConcealingKeyWarning.String() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestWarning_kinds pins that both kinds satisfy the sealed interface and that
// a mixed slice prints one line each, the contract a deploy test ranges over.
func TestWarning_kinds(t *testing.T) {
	t.Parallel()

	warnings := []Warning{
		GrantWarning{Role: "Paymaster", Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
		ConcealingKeyWarning{Role: "Archivist", Resource: "Missions", Field: "fee", Conditions: []string{completed}, Uncovered: []string{closedStates}},
	}
	for _, w := range warnings {
		if !strings.HasPrefix(w.String(), "role ") {
			t.Errorf("Warning.String() = %q, want a line opening with the role", w.String())
		}
	}
}
