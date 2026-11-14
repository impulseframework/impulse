package resource

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// listFixtureBindings is the vocabulary of a resource with list columns: the
// roles an invitation gives and its labels, both lists of strings, a list
// reached through a join path (which generation refuses, here to prove the
// backstop), and two plain columns.
func listFixtureBindings(t *testing.T) (*GeneratedCollection, Bindings) {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:  "Invitations",
		Scope: accesstypes.DomainPermissionScope,
		Attributes: []AttributeData{
			{Name: "pending", Column: "Pending", Type: AttributeTypeBool},
			{Name: "email", Column: "Email", Type: AttributeTypeString},
			{Name: "role", Column: "RoleNames", Type: AttributeTypeString, List: true},
			{Name: "label", Column: "Labels", Type: AttributeTypeString, List: true},
			{Name: "badge", Column: "SenderId", Type: AttributeTypeString, List: true, Path: []BindingHop{{Table: "Senders", JoinColumn: "Id", Column: "Badges"}}},
		},
		Domain: &DomainBindingData{Column: "Tenant"},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	bindings, ok := g.Bindings(accesstypes.DomainPermissionScope, "Invitations")
	if !ok {
		t.Fatal("fixture bindings missing")
	}

	return g, bindings
}

// TestLowerRowCondition_list pins the rendering of a condition that reads a
// list attribute: the whole condition, with the attribute read as one item,
// held to every item of the row's list in one predicate. The items are joined
// to a one-row source, so a list with no item is judged once with a NULL item;
// IS NOT TRUE refuses an item the condition cannot decide. Both databases take
// the shape, PostgreSQL naming the correlated call LATERAL. A condition that
// reads no list renders as it always did.
func TestLowerRowCondition_list(t *testing.T) {
	t.Parallel()

	collection, bindings := listFixtureBindings(t)

	tests := []struct {
		name         string
		source       string
		wantSpanner  string
		wantPostgres string
		wantParams   []QueryParam
	}{
		{
			name:         "a row term and a list term",
			source:       "pending = true AND role != 'Admin'",
			wantSpanner:  "NOT EXISTS (SELECT 1 FROM (SELECT 1) AS `ca1_row` LEFT JOIN UNNEST(`t`.`RoleNames`) AS `ca1` ON TRUE WHERE ((`t`.`Pending` = @_c1 AND `ca1` <> @_c2)) IS NOT TRUE)",
			wantPostgres: `NOT EXISTS (SELECT 1 FROM (SELECT 1) AS "ca1_row" LEFT JOIN LATERAL unnest("t"."RoleNames") AS "ca1" ON TRUE WHERE (("t"."Pending" = @_c1 AND "ca1" <> @_c2)) IS NOT TRUE)`,
			wantParams:   []QueryParam{{Name: "_c1", Value: true}, {Name: "_c2", Value: "Admin"}},
		},
		{
			name:         "membership, no value and negation read the item",
			source:       "role IS NULL OR NOT (role IN ('Admin', 'Owner'))",
			wantSpanner:  "NOT EXISTS (SELECT 1 FROM (SELECT 1) AS `ca1_row` LEFT JOIN UNNEST(`t`.`RoleNames`) AS `ca1` ON TRUE WHERE ((`ca1` IS NULL OR NOT (`ca1` IN (@_c1, @_c2)))) IS NOT TRUE)",
			wantPostgres: `NOT EXISTS (SELECT 1 FROM (SELECT 1) AS "ca1_row" LEFT JOIN LATERAL unnest("t"."RoleNames") AS "ca1" ON TRUE WHERE (("ca1" IS NULL OR NOT ("ca1" IN (@_c1, @_c2)))) IS NOT TRUE)`,
			wantParams:   []QueryParam{{Name: "_c1", Value: "Admin"}, {Name: "_c2", Value: "Owner"}},
		},
		{
			name:         "the item against the requester",
			source:       "role != subject",
			wantSpanner:  "NOT EXISTS (SELECT 1 FROM (SELECT 1) AS `ca1_row` LEFT JOIN UNNEST(`t`.`RoleNames`) AS `ca1` ON TRUE WHERE (`ca1` <> @subject) IS NOT TRUE)",
			wantPostgres: `NOT EXISTS (SELECT 1 FROM (SELECT 1) AS "ca1_row" LEFT JOIN LATERAL unnest("t"."RoleNames") AS "ca1" ON TRUE WHERE ("ca1" <> @subject) IS NOT TRUE)`,
		},
		{
			name:         "a condition that reads no list is not wrapped",
			source:       "pending = true",
			wantSpanner:  "`t`.`Pending` = @_c1",
			wantPostgres: `"t"."Pending" = @_c1`,
			wantParams:   []QueryParam{{Name: "_c1", Value: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := condition.Parse(tt.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.source, err)
			}
			for dialect, want := range map[SQLDialect]string{Spanner: tt.wantSpanner, PostgreSQL: tt.wantPostgres} {
				registry := newParamRegistry()
				sql, err := lowerToSQL(expr, &loweringContext{outer: "t", bindings: bindings, collection: collection}, newSQLGenerator(dialect), registry)
				if err != nil {
					t.Fatalf("lowerToSQL(%q) error = %v", tt.source, err)
				}
				if sql != want {
					t.Errorf("SQL mismatch:\n got %s\nwant %s", sql, want)
				}
				if diff := cmp.Diff(tt.wantParams, registry.boundParams()); diff != "" {
					t.Errorf("bound params mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestLowerRowCondition_listRefusals pins the runtime backstop of the rules
// deploy validation and generation hold a list read to: one list, on the left
// of a comparison, as the row holds it, a column of the row's own.
func TestLowerRowCondition_listRefusals(t *testing.T) {
	t.Parallel()

	collection, bindings := listFixtureBindings(t)

	tests := []struct {
		name     string
		source   string
		proposed map[string]any
		insert   bool
		want     string
	}{
		{name: "two lists", source: "role != 'Admin' AND label = 'a'", want: "reads two list attributes"},
		{name: "a proposed list", source: "new.role != 'Admin'", proposed: map[string]any{"RoleNames": []string{"Admin"}}, want: "new.role reads a proposed list"},
		{name: "a list on the right side", source: "new.email != role", proposed: map[string]any{"Email": "a@example.com"}, want: "on the right side of a comparison"},
		{name: "a list in an insert's check", source: "role != 'Admin'", proposed: map[string]any{"RoleNames": []string{"Admin"}}, insert: true, want: "read in an insert's check"},
		{name: "a list reached through a join path", source: "badge != 'x'", want: "reached through a join path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := condition.Parse(tt.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.source, err)
			}
			lctx := &loweringContext{outer: "t", bindings: bindings, collection: collection, insertImage: tt.insert}
			if tt.proposed != nil {
				lctx.proposed = newProposedOverlay(tt.proposed)
			}
			_, err = lowerToSQL(expr, lctx, newSQLGenerator(Spanner), newParamRegistry())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("lowerToSQL(%q) error = %v, want one containing %q", tt.source, err, tt.want)
			}
		})
	}

	// A list attribute lowered outside an item-by-item judgment has no single value.
	expr, err := condition.Parse("role != 'Admin'")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lowerCondition(expr, &loweringContext{outer: "t", bindings: bindings, collection: collection}, newParamRegistry()); err == nil || !strings.Contains(err.Error(), "outside an item-by-item judgment") {
		t.Errorf("lowerCondition() of a list read error = %v, want the invariant's refusal", err)
	}
}
