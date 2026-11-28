package resource

// These tests pin the two parts a subject set's definition may write, who: and
// where: (collection_bindings.go, lowering.go): what the collection refuses when
// it is built, the text both databases are sent for each shape the parts take,
// and the reference the database-backed suites hold that text to
// (setReference), which states a set's meaning in Go with the condition
// package's evaluator and reads no statement.

import (
	"math/big"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// setRows is the rows a subject set's reference reads: each table's rows, column
// by column, in the evaluator's storage types.
type setRows map[string][]map[string]any

// setRowsOf reads spanner-tagged structs as a table's rows.
func setRowsOf[T any](rows []*T) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		v := reflect.ValueOf(row).Elem()
		columns := make(map[string]any, v.NumField())
		for i := range v.NumField() {
			columns[v.Type().Field(i).Tag.Get("spanner")] = semanticValue(v.Field(i).Interface())
		}
		out = append(out, columns)
	}

	return out
}

// setReference computes subject sets by the language's stated meaning
// (accesstypes/condition's doc, "Evaluation"): a set is the non-null values the
// requester's anchor rows yield, the anchor rows being the rows of the set's
// table, in the request's partition where the table has one, on which the set's
// who: is TRUE and its where: is TRUE. Both parts are judged by the reference
// evaluator over the row, a who: test against another set reading that set by
// this same rule. It is written from that statement and knows nothing of the
// lowering.
type setReference struct {
	data        CollectionData
	rows        setRows
	subject     string
	now         time.Time
	domain      string
	partitioned bool

	sets map[string][]any

	// viaSet counts the anchor rows a who: admitted through another set alone,
	// and filtered the rows a who: admitted and the where: dropped: a suite that
	// sees neither has proven nothing about the parts.
	viaSet, filtered int
}

func newSetReference(collection *GeneratedCollection, rows setRows, subject string, now time.Time, scope accesstypes.Scope) *setReference {
	domain, partitioned := scope.Domain()

	return &setReference{
		data:        collection.Data(),
		rows:        rows,
		subject:     subject,
		now:         now,
		domain:      string(domain),
		partitioned: partitioned,
		sets:        make(map[string][]any),
	}
}

// values returns one set's values, computed once.
func (r *setReference) values(t *testing.T, name string) []any {
	t.Helper()

	if values, ok := r.sets[name]; ok {
		return values
	}
	for i := range r.data.Resources {
		res := &r.data.Resources[i]
		for j := range res.SubjectSets {
			if set := &res.SubjectSets[j]; set.Name == name {
				values := r.compute(t, res, set)
				r.sets[name] = values

				return values
			}
		}
	}
	t.Fatalf("subject.%s is not a set of the fixture", name)

	return nil
}

func (r *setReference) compute(t *testing.T, res *CollectionResource, set *SubjectBindingData) []any {
	t.Helper()

	tests := []condition.WhoTest{{Column: set.UserColumn}}
	if len(set.Who) > 0 {
		tests = tests[:0]
		for _, test := range set.Who {
			tests = append(tests, condition.WhoTest{Column: test.Column, Set: test.Set})
		}
	}
	var own []condition.WhoTest
	inner := make(map[string][]any)
	for _, test := range tests {
		if test.Set == "" {
			own = append(own, test)

			continue
		}
		inner[test.Set] = r.values(t, test.Set)
	}

	var where condition.Expr
	if set.Where != nil {
		parsed, err := condition.ParseWhere(set.Where.Condition)
		if err != nil {
			t.Fatalf("subject.%s: ParseWhere(%q) error = %v", set.Name, set.Where.Condition, err)
		}
		columns := make(map[string]string, len(set.Where.Columns))
		for _, column := range set.Where.Columns {
			columns[column.Name] = column.Column
		}
		where = condition.RenameBindings(parsed, func(name string) string {
			return columns[name]
		})
	}

	holds := func(expr condition.Expr, row map[string]any) bool {
		truth, err := condition.Evaluate(expr, &condition.Image{Pre: row, Subject: r.subject, Now: r.now, SubjectSets: inner})
		if err != nil {
			t.Fatalf("subject.%s: Evaluate(%s) error = %v", set.Name, expr.String(), err)
		}

		return truth.Permits()
	}

	var values []any
	for _, row := range r.rows[string(res.Name)] {
		if r.partitioned && res.Domain != nil && r.follow(row[res.Domain.Column], res.Domain.Path) != r.domain {
			continue
		}
		if !holds(condition.WhoExpr(tests), row) {
			continue
		}
		if len(own) == 0 || !holds(condition.WhoExpr(own), row) {
			r.viaSet++
		}
		if where != nil && !holds(where, row) {
			r.filtered++

			continue
		}
		if value := r.follow(row[set.Column], set.Path); value != nil {
			values = append(values, value)
		}
	}

	return values
}

// follow reads a value through a join path: hop by hop, the column of the row
// the previous value keys, nothing where a key has no value or reaches no row.
func (r *setReference) follow(value any, path []BindingHop) any {
	for _, hop := range path {
		if value == nil {
			return nil
		}
		var next any
		for _, row := range r.rows[hop.Table] {
			if row[hop.JoinColumn] == value {
				next = row[hop.Column]
			}
		}
		value = next
	}

	return value
}

// TestSubjectSetParts_rendering pins the text both databases are sent for each
// shape a subject set's two parts take, in the EXISTS a condition's membership
// test renders: a set with a where:; two sets one table yields by a column; a
// who: through another set; sets two levels deep; a partitioned set building on
// one outside every partition, and the reverse, each filtered by its own
// table's tenancy and by nothing of the other's.
func TestSubjectSetParts_rendering(t *testing.T) {
	t.Parallel()

	collection := semanticCollection(t)
	bindings, ok := collection.Bindings(accesstypes.DomainPermissionScope, semanticResource)
	if !ok {
		t.Fatal("fixture bindings missing")
	}

	tests := []struct {
		name        string
		source      string
		global      bool
		wantSpanner string
		wantPG      string
		wantParams  []QueryParam
		wantNames   []string
	}{
		{
			name:        "a set with a where: on a table outside every partition",
			source:      "label IN subject.circles",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Circles` `ca1` WHERE `ca1`.`UserId` = @subject AND (`ca1`.`ValidUntil` IS NULL OR `ca1`.`ValidUntil` > @now) AND `ca1`.`Circle` = `p`.`Label`) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Circles" "ca1" WHERE "ca1"."UserId" = @subject AND ("ca1"."ValidUntil" IS NULL OR "ca1"."ValidUntil" > @now) AND "ca1"."Circle" = "p"."Label") END`,
			wantNames:   []string{"now", "subject"},
		},
		{
			name:        "a who: through another set: a partitioned set building on one outside every partition",
			source:      "label IN subject.editTeams",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Passes` `ca1` WHERE (`ca1`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`Circle`)) AND (`ca1`.`Level` = @_c1 AND (`ca1`.`ExpiresAt` IS NULL OR `ca1`.`ExpiresAt` > @now)) AND `ca1`.`Team` = `p`.`Label` AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Passes" "ca1" WHERE ("ca1"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."ValidUntil" IS NULL OR "ca2"."ValidUntil" > @now) AND "ca2"."Circle" = "ca1"."Circle")) AND ("ca1"."Level" = @_c1 AND ("ca1"."ExpiresAt" IS NULL OR "ca1"."ExpiresAt" > @now)) AND "ca1"."Team" = "p"."Label" AND "ca1"."Depot" = @domain) END`,
			wantParams:  []QueryParam{{Name: "_c1", Value: "edit"}},
			wantNames:   []string{"domain", "now", "subject"},
		},
		{
			name:        "the second set of the same table, by the same column",
			source:      "label IN subject.viewTeams",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Passes` `ca1` WHERE (`ca1`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`Circle`)) AND `ca1`.`Level` IN (@_c1, @_c2) AND `ca1`.`Team` = `p`.`Label` AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Passes" "ca1" WHERE ("ca1"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."ValidUntil" IS NULL OR "ca2"."ValidUntil" > @now) AND "ca2"."Circle" = "ca1"."Circle")) AND "ca1"."Level" IN (@_c1, @_c2) AND "ca1"."Team" = "p"."Label" AND "ca1"."Depot" = @domain) END`,
			wantParams:  []QueryParam{{Name: "_c1", Value: "view"}, {Name: "_c2", Value: "edit"}},
			wantNames:   []string{"domain", "now", "subject"},
		},
		{
			name:        "two sets of one table in one condition keep their own rows",
			source:      "label IN subject.viewTeams AND note NOT IN subject.editTeams",
			wantSpanner: "(CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Passes` `ca1` WHERE (`ca1`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`Circle`)) AND `ca1`.`Level` IN (@_c1, @_c2) AND `ca1`.`Team` = `p`.`Label` AND `ca1`.`Depot` = @domain) END AND CASE WHEN `p`.`Note` IS NULL THEN NULL ELSE NOT (EXISTS (SELECT 1 FROM `Passes` `ca3` WHERE (`ca3`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca4` WHERE `ca4`.`UserId` = @subject AND (`ca4`.`ValidUntil` IS NULL OR `ca4`.`ValidUntil` > @now) AND `ca4`.`Circle` = `ca3`.`Circle`)) AND (`ca3`.`Level` = @_c3 AND (`ca3`.`ExpiresAt` IS NULL OR `ca3`.`ExpiresAt` > @now)) AND `ca3`.`Team` = `p`.`Note` AND `ca3`.`Depot` = @domain)) END)",
			wantPG:      `(CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Passes" "ca1" WHERE ("ca1"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."ValidUntil" IS NULL OR "ca2"."ValidUntil" > @now) AND "ca2"."Circle" = "ca1"."Circle")) AND "ca1"."Level" IN (@_c1, @_c2) AND "ca1"."Team" = "p"."Label" AND "ca1"."Depot" = @domain) END AND CASE WHEN "p"."Note" IS NULL THEN NULL ELSE NOT (EXISTS (SELECT 1 FROM "Passes" "ca3" WHERE ("ca3"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca4" WHERE "ca4"."UserId" = @subject AND ("ca4"."ValidUntil" IS NULL OR "ca4"."ValidUntil" > @now) AND "ca4"."Circle" = "ca3"."Circle")) AND ("ca3"."Level" = @_c3 AND ("ca3"."ExpiresAt" IS NULL OR "ca3"."ExpiresAt" > @now)) AND "ca3"."Team" = "p"."Note" AND "ca3"."Depot" = @domain)) END)`,
			wantParams:  []QueryParam{{Name: "_c1", Value: "view"}, {Name: "_c2", Value: "edit"}, {Name: "_c3", Value: "edit"}},
			wantNames:   []string{"domain", "now", "subject"},
		},
		{
			name:        "sets two levels deep, the requester named in the innermost alone",
			source:      "label IN subject.farTeams",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Passes` `ca1` WHERE EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE EXISTS (SELECT 1 FROM `Circles` `ca3` WHERE `ca3`.`UserId` = @subject AND (`ca3`.`ValidUntil` IS NULL OR `ca3`.`ValidUntil` > @now) AND `ca3`.`Circle` = `ca2`.`InnerCircle`) AND `ca2`.`Circle` = `ca1`.`Circle`) AND `ca1`.`Team` = `p`.`Label` AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Passes" "ca1" WHERE EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE EXISTS (SELECT 1 FROM "Circles" "ca3" WHERE "ca3"."UserId" = @subject AND ("ca3"."ValidUntil" IS NULL OR "ca3"."ValidUntil" > @now) AND "ca3"."Circle" = "ca2"."InnerCircle") AND "ca2"."Circle" = "ca1"."Circle") AND "ca1"."Team" = "p"."Label" AND "ca1"."Depot" = @domain) END`,
			wantNames:   []string{"domain", "now", "subject"},
		},
		{
			name:        "a set outside every partition building on a partitioned one: the inner set alone is filtered",
			source:      "label IN subject.badges",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Badges` `ca1` WHERE EXISTS (SELECT 1 FROM `Memberships` `ca2` WHERE `ca2`.`UserId` = @subject AND `ca2`.`Team` = `ca1`.`Team` AND `ca2`.`Depot` = @domain) AND `ca1`.`Badge` = `p`.`Label`) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Badges" "ca1" WHERE EXISTS (SELECT 1 FROM "Memberships" "ca2" WHERE "ca2"."UserId" = @subject AND "ca2"."Team" = "ca1"."Team" AND "ca2"."Depot" = @domain) AND "ca1"."Badge" = "p"."Label") END`,
			wantNames:   []string{"domain", "subject"},
		},
		{
			name:        "a global request filters no set to a partition",
			source:      "label IN subject.editTeams",
			global:      true,
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Passes` `ca1` WHERE (`ca1`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`Circle`)) AND (`ca1`.`Level` = @_c1 AND (`ca1`.`ExpiresAt` IS NULL OR `ca1`.`ExpiresAt` > @now)) AND `ca1`.`Team` = `p`.`Label`) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Passes" "ca1" WHERE ("ca1"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."ValidUntil" IS NULL OR "ca2"."ValidUntil" > @now) AND "ca2"."Circle" = "ca1"."Circle")) AND ("ca1"."Level" = @_c1 AND ("ca1"."ExpiresAt" IS NULL OR "ca1"."ExpiresAt" > @now)) AND "ca1"."Team" = "p"."Label") END`,
			wantParams:  []QueryParam{{Name: "_c1", Value: "edit"}},
			wantNames:   []string{"now", "subject"},
		},
		{
			name:        "a where: over a bool and a double binds each literal as its column's type",
			source:      "weight IN subject.passTiers",
			wantSpanner: "CASE WHEN `p`.`Weight` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Passes` `ca1` WHERE `ca1`.`UserId` = @subject AND (`ca1`.`Sealed` = @_c1 OR `ca1`.`Mass` >= @_c2) AND `ca1`.`Tier` = `p`.`Weight` AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Weight" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Passes" "ca1" WHERE "ca1"."UserId" = @subject AND ("ca1"."Sealed" = @_c1 OR "ca1"."Mass" >= @_c2) AND "ca1"."Tier" = "p"."Weight" AND "ca1"."Depot" = @domain) END`,
			wantParams:  []QueryParam{{Name: "_c1", Value: false}, {Name: "_c2", Value: mustRat("10.5")}},
			wantNames:   []string{"domain", "subject"},
		},
		{
			// The proof that a set which writes neither part renders as it did before
			// the parts existed: the statement the lowering tests have always pinned.
			name:        "a set that writes neither part is unchanged",
			source:      "label IN subject.teams",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Memberships` `ca1` WHERE `ca1`.`UserId` = @subject AND `ca1`.`Team` = `p`.`Label` AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Memberships" "ca1" WHERE "ca1"."UserId" = @subject AND "ca1"."Team" = "p"."Label" AND "ca1"."Depot" = @domain) END`,
			wantNames:   []string{"domain", "subject"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := condition.Parse(tt.source)
			if err != nil {
				t.Fatalf("condition.Parse(%q) error = %v", tt.source, err)
			}
			for dialect, want := range map[SQLDialect]string{Spanner: tt.wantSpanner, PostgreSQL: tt.wantPG} {
				registry := newParamRegistry()
				sql, err := lowerToSQL(expr, &loweringContext{outer: "p", bindings: bindings, collection: collection, partitioned: !tt.global}, newSQLGenerator(dialect), registry)
				if err != nil {
					t.Fatalf("lowerToSQL(%q, %v) error = %v", tt.source, dialect, err)
				}
				if sql != want {
					t.Errorf("lowerToSQL(%q, %v) =\n%s\nwant\n%s", tt.source, dialect, sql, want)
				}
				if got := registry.boundParams(); !slices.EqualFunc(got, tt.wantParams, equalParam) {
					t.Errorf("lowerToSQL(%q, %v) bound %v, want %v", tt.source, dialect, got, tt.wantParams)
				}
				if got := registry.referencedNames(); !slices.Equal(got, tt.wantNames) {
					t.Errorf("lowerToSQL(%q, %v) names %v, want %v", tt.source, dialect, got, tt.wantNames)
				}
			}
		})
	}
}

// TestSubjectSetParts_dottedValue pins a set that writes both parts and yields a
// value one hop away (value: HubID.Region): the parts say which rows of the set's
// own table count, the hop then leaves from those rows, and the set's tenant filter
// closes the statement, so a dotted value and the two parts do not disturb each
// other. Its text beside the same set without the parts shows the parts as the only
// difference.
func TestSubjectSetParts_dottedValue(t *testing.T) {
	t.Parallel()

	hop := []BindingHop{{Table: "Hubs", JoinColumn: "Id", Column: "Region"}}
	collection := semanticCollectionOf(t, append(semanticResources(), CollectionResource{
		Name:   "Postings",
		Scope:  accesstypes.DomainPermissionScope,
		Domain: &DomainBindingData{Column: "Depot"},
		SubjectSets: []SubjectBindingData{
			{Name: "postedRegions", UserColumn: "UserId", Column: "HubId", Type: AttributeTypeString, Path: hop},
			{
				Name: "standingRegions", UserColumn: "UserId", Column: "HubId", Type: AttributeTypeString, Path: hop,
				Who: []SubjectWhoData{{Column: "UserId"}, {Column: "Circle", Set: "circles"}},
				Where: &SubjectWhereData{
					Condition: "Standing = true",
					Columns:   []SubjectColumnData{{Name: "Standing", Column: "Standing", Type: AttributeTypeBool}},
				},
			},
		},
	}))
	bindings, ok := collection.Bindings(accesstypes.DomainPermissionScope, semanticResource)
	if !ok {
		t.Fatal("fixture bindings missing")
	}

	tests := []struct {
		name        string
		source      string
		wantSpanner string
		wantPG      string
		wantNames   []string
	}{
		{
			name:        "the dotted set with neither part, as it has always rendered",
			source:      "label IN subject.postedRegions",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Postings` `ca1` WHERE `ca1`.`UserId` = @subject AND EXISTS (SELECT 1 FROM `Hubs` `ca2` WHERE `ca2`.`Id` = `ca1`.`HubId` AND `ca2`.`Region` = `p`.`Label`) AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Postings" "ca1" WHERE "ca1"."UserId" = @subject AND EXISTS (SELECT 1 FROM "Hubs" "ca2" WHERE "ca2"."Id" = "ca1"."HubId" AND "ca2"."Region" = "p"."Label") AND "ca1"."Depot" = @domain) END`,
			wantNames:   []string{"domain", "subject"},
		},
		{
			name:        "the same set with a who: and a where:, the hop and the tenant filter where they were",
			source:      "label IN subject.standingRegions",
			wantSpanner: "CASE WHEN `p`.`Label` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `Postings` `ca1` WHERE (`ca1`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`Circle`)) AND `ca1`.`Standing` = @_c1 AND EXISTS (SELECT 1 FROM `Hubs` `ca3` WHERE `ca3`.`Id` = `ca1`.`HubId` AND `ca3`.`Region` = `p`.`Label`) AND `ca1`.`Depot` = @domain) END",
			wantPG:      `CASE WHEN "p"."Label" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "Postings" "ca1" WHERE ("ca1"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."ValidUntil" IS NULL OR "ca2"."ValidUntil" > @now) AND "ca2"."Circle" = "ca1"."Circle")) AND "ca1"."Standing" = @_c1 AND EXISTS (SELECT 1 FROM "Hubs" "ca3" WHERE "ca3"."Id" = "ca1"."HubId" AND "ca3"."Region" = "p"."Label") AND "ca1"."Depot" = @domain) END`,
			wantNames:   []string{"domain", "now", "subject"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := condition.Parse(tt.source)
			if err != nil {
				t.Fatalf("condition.Parse(%q) error = %v", tt.source, err)
			}
			for dialect, want := range map[SQLDialect]string{Spanner: tt.wantSpanner, PostgreSQL: tt.wantPG} {
				registry := newParamRegistry()
				sql, err := lowerToSQL(expr, &loweringContext{outer: "p", bindings: bindings, collection: collection, partitioned: true}, newSQLGenerator(dialect), registry)
				if err != nil {
					t.Fatalf("lowerToSQL(%q, %v) error = %v", tt.source, dialect, err)
				}
				if sql != want {
					t.Errorf("lowerToSQL(%q, %v) =\n%s\nwant\n%s", tt.source, dialect, sql, want)
				}
				if got := registry.referencedNames(); !slices.Equal(got, tt.wantNames) {
					t.Errorf("lowerToSQL(%q, %v) names %v, want %v", tt.source, dialect, got, tt.wantNames)
				}
			}
		})
	}
}

// mustRat reads an exact decimal as the NUMERIC value a literal binds as.
func mustRat(text string) *big.Rat {
	rat, ok := new(big.Rat).SetString(text)
	if !ok {
		panic("mustRat: " + text + " is not a number")
	}

	return rat
}

// equalParam compares two bound parameters, a NUMERIC by value.
func equalParam(a, b QueryParam) bool {
	return a.Name == b.Name && compareSemantic(semanticValue(a.Value), semanticValue(b.Value)) == 0
}

// TestSubjectSetParts_factStatement pins the statement that reads a set with
// parts for a computed resource's conditions: the same rows the EXISTS reads,
// the value selected and nothing matched.
func TestSubjectSetParts_factStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fact        string
		wantSpanner string
		wantPG      string
		wantNames   []string
	}{
		{
			name:        "a who: through another set and a where:",
			fact:        "editTeams",
			wantSpanner: "SELECT `ca1`.`Team` AS `fact` FROM `Passes` `ca1` WHERE (`ca1`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`Circle`)) AND (`ca1`.`Level` = @_c1 AND (`ca1`.`ExpiresAt` IS NULL OR `ca1`.`ExpiresAt` > @now)) AND `ca1`.`Depot` = @domain",
			wantPG:      `SELECT "ca1"."Team" AS "fact" FROM "Passes" "ca1" WHERE ("ca1"."UserId" = @subject OR EXISTS (SELECT 1 FROM "Circles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."ValidUntil" IS NULL OR "ca2"."ValidUntil" > @now) AND "ca2"."Circle" = "ca1"."Circle")) AND ("ca1"."Level" = @_c1 AND ("ca1"."ExpiresAt" IS NULL OR "ca1"."ExpiresAt" > @now)) AND "ca1"."Depot" = @domain`,
			wantNames:   []string{"domain", "now", "subject"},
		},
		{
			name:        "a set outside every partition building on a partitioned one",
			fact:        "badges",
			wantSpanner: "SELECT `ca1`.`Badge` AS `fact` FROM `Badges` `ca1` WHERE EXISTS (SELECT 1 FROM `Memberships` `ca2` WHERE `ca2`.`UserId` = @subject AND `ca2`.`Team` = `ca1`.`Team` AND `ca2`.`Depot` = @domain)",
			wantPG:      `SELECT "ca1"."Badge" AS "fact" FROM "Badges" "ca1" WHERE EXISTS (SELECT 1 FROM "Memberships" "ca2" WHERE "ca2"."UserId" = @subject AND "ca2"."Team" = "ca1"."Team" AND "ca2"."Depot" = @domain)`,
			wantNames:   []string{"domain", "subject"},
		},
	}

	collection := semanticCollection(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range map[DBType]string{SpannerDBType: tt.wantSpanner, PostgresDBType: tt.wantPG} {
				sql, registry, err := subjectFactStatement(collection, dbType, true, tt.fact, true)
				if err != nil {
					t.Fatalf("subjectFactStatement(%s) error = %v", dbType, err)
				}
				if sql != want {
					t.Errorf("subjectFactStatement(%s) =\n%s\nwant\n%s", dbType, sql, want)
				}
				if names := registry.referencedNames(); !slices.Equal(names, tt.wantNames) {
					t.Errorf("subjectFactStatement(%s) names %v, want %v", dbType, names, tt.wantNames)
				}
			}
		})
	}
}

// TestSubjectFactParams_now pins where a set's where: gets its instant when the
// set is read for a computed resource: the request's decision instant, and a
// refusal where the request carries none.
func TestSubjectFactParams_now(t *testing.T) {
	t.Parallel()

	_, registry, err := subjectFactStatement(semanticCollection(t), SpannerDBType, true, "circles", true)
	if err != nil {
		t.Fatalf("subjectFactStatement() error = %v", err)
	}
	at := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)

	params, err := subjectFactParams(registry, "u1", testScope, accesstypes.EnvironmentAt(at))
	if err != nil {
		t.Fatalf("subjectFactParams() error = %v", err)
	}
	if got, ok := params[nowParamName].(time.Time); !ok || !got.Equal(at) {
		t.Errorf("subjectFactParams() now = %v, want the request's decision instant %v", params[nowParamName], at)
	}

	_, err = subjectFactParams(registry, "u1", testScope, accesstypes.NewEnvironment())
	if err == nil || !strings.Contains(err.Error(), "reads now but the request environment carries no decision instant") {
		t.Errorf("subjectFactParams() without an instant error = %v, want the refusal", err)
	}
}

// TestNewGeneratedCollection_subjectSetParts pins what a collection refuses of
// the two parts when it is built: the mistakes the generator refuses with the
// struct named, met again where a collection is stale or written by hand.
func TestNewGeneratedCollection_subjectSetParts(t *testing.T) {
	t.Parallel()

	set := func(name string, mutate func(*SubjectBindingData)) CollectionResource {
		binding := SubjectBindingData{Name: name, UserColumn: "UserId", Column: "Team", Type: AttributeTypeString}
		if mutate != nil {
			mutate(&binding)
		}

		return CollectionResource{Name: accesstypes.Resource("T" + name), Scope: accesstypes.GlobalPermissionScope, SubjectSets: []SubjectBindingData{binding}}
	}
	who := func(tests ...SubjectWhoData) func(*SubjectBindingData) {
		return func(binding *SubjectBindingData) {
			binding.Who = tests
		}
	}
	where := func(text string, columns ...SubjectColumnData) func(*SubjectBindingData) {
		return func(binding *SubjectBindingData) {
			binding.Where = &SubjectWhereData{Condition: text, Columns: columns}
		}
	}
	level := SubjectColumnData{Name: "Level", Column: "Level", Type: AttributeTypeString}

	tests := []struct {
		name      string
		resources []CollectionResource
		wantErr   string
	}{
		{name: "the parts as the generator writes them", resources: []CollectionResource{
			set("crewGroups", nil),
			set("teams", func(binding *SubjectBindingData) {
				who(SubjectWhoData{Column: "UserId"}, SubjectWhoData{Column: "GroupId", Set: "crewGroups"})(binding)
				where("Level = 'edit'", level)(binding)
			}),
		}},
		{
			name:      "a who: naming a set nothing declares",
			resources: []CollectionResource{set("teams", who(SubjectWhoData{Column: "GroupId", Set: "crewGroups"}))},
			wantErr:   `subject set "teams" tests GroupId against subject.crewGroups, which is not a declared subject set`,
		},
		{
			name:      "a who: test naming no column",
			resources: []CollectionResource{set("teams", who(SubjectWhoData{}))},
			wantErr:   `resource "Tteams" subject set "teams" writes a who: test that names no column`,
		},
		{
			name: "sets that build on each other in a loop",
			resources: []CollectionResource{
				set("a", who(SubjectWhoData{Column: "X", Set: "b"})),
				set("b", who(SubjectWhoData{Column: "X", Set: "c"})),
				set("c", who(SubjectWhoData{Column: "X", Set: "a"})),
			},
			wantErr: "subject sets build on each other in a loop (a → b → c → a)",
		},
		{
			name:      "a set that builds on itself",
			resources: []CollectionResource{set("a", who(SubjectWhoData{Column: "X", Set: "a"}))},
			wantErr:   "subject sets build on each other in a loop (a → a)",
		},
		{
			name:      "a where: that names the requester",
			resources: []CollectionResource{set("teams", where("Level = subject", level))},
			wantErr:   `where: "Level = subject" names the requester; a where: reads the row's own columns alone`,
		},
		{
			name:      "a where: reading a name the collection resolves to no column",
			resources: []CollectionResource{set("teams", where("Level = 'edit' AND Rank > 2", level))},
			wantErr:   `where: reads "Rank", which the collection resolves to no column`,
		},
		{
			name:      "a where: column of no comparison type",
			resources: []CollectionResource{set("teams", where("Level = 'edit'", SubjectColumnData{Name: "Level", Column: "Level", Type: "uuid"}))},
			wantErr:   `where: resolves "Level" to column "Level" of comparison type "uuid"`,
		},
		{
			name: "a subject value that writes a part",
			resources: []CollectionResource{{
				Name: "Profiles", Scope: accesstypes.GlobalPermissionScope,
				SubjectValues: []SubjectBindingData{{Name: "rank", UserColumn: "UserId", Column: "Rank", Type: AttributeTypeNumber, Who: []SubjectWhoData{{Column: "UserId"}}}},
			}},
			wantErr: `resource "Profiles" subject value "rank" writes a who: or a where: part, which a subject set alone takes`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(CollectionData{Resources: tt.resources})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewGeneratedCollection() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewGeneratedCollection() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestSubjectSetRows_loop pins the backstop where a collection reached the
// lowering unvalidated (one assembled inside the package): sets that build on each
// other in a loop are refused, never rendered without end.
func TestSubjectSetRows_loop(t *testing.T) {
	t.Parallel()

	builder := NewCollectionBuilder()
	builder.SetResourceBindings(accesstypes.GlobalPermissionScope, "Parcels", &Bindings{Attributes: []AttributeData{{Name: "label", Column: "Label", Type: AttributeTypeString}}})
	builder.SetResourceBindings(accesstypes.GlobalPermissionScope, "As", &Bindings{SubjectSets: []SubjectBindingData{
		{Name: "a", UserColumn: "X", Column: "V", Type: AttributeTypeString, Who: []SubjectWhoData{{Column: "X", Set: "b"}}},
	}})
	builder.SetResourceBindings(accesstypes.GlobalPermissionScope, "Bs", &Bindings{SubjectSets: []SubjectBindingData{
		{Name: "b", UserColumn: "X", Column: "V", Type: AttributeTypeString, Who: []SubjectWhoData{{Column: "X", Set: "a"}}},
	}})
	collection := builder.g
	bindings, _ := collection.Bindings(accesstypes.GlobalPermissionScope, "Parcels")

	expr, err := condition.Parse("label IN subject.a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = lowerToSQL(expr, &loweringContext{outer: "p", bindings: bindings, collection: collection}, newSQLGenerator(Spanner), newParamRegistry())
	if err == nil || !strings.Contains(err.Error(), "subject sets build on each other in a loop (a → b → a)") {
		t.Fatalf("lowerToSQL() error = %v, want the loop refused", err)
	}
}
