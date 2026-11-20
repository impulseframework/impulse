package generation

import (
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// subjectSetFixtureClient is the synthetic schema behind the subjectsetfixture
// structs. A table's columns are its struct's spanner tags, nullable where the
// Go field can hold no value, so the fixture file is the one place a column is
// declared. The key is Id unless named here; the Level columns are foreign keys
// into the Levels enumeration table, whose rows the schema read loads; Stake's
// ProjectId is a foreign key into Projects, which its dotted value leaves through;
// every PoolId is a foreign key into Pools, which a domain: leaves through; and the
// indexes are the ones each struct's doc comment speaks of.
func subjectSetFixtureClient(t *testing.T, structs map[string]*parser.Struct) *client {
	t.Helper()

	c := &client{
		enumerateTables: map[string]string{"Levels": "Level"},
		enumValues: map[string][]*enumData{"Levels": {
			{ID: "lead", Description: "Lead"},
			{ID: "member", Description: "Member"},
			{ID: "guest", Description: "Guest"},
		}},
	}

	keys := map[string][]string{
		"TeamMember":          {"TeamId", "UserId"},
		"Seat":                {"UserId"},
		"ValueWithParts":      {"UserId"},
		"ValueWithStringPart": {"UserId"},
		"KeyedByBoth":         {"ProjectId", "UserId"},
		"SecondaryThenKey":    {"ProjectId"},
		"PoolMember":          {"PoolId", "UserId"},
		"DomainOnValue":       {"UserId"},
	}
	index := func(name string, nullFiltered bool, columns ...string) indexMeta {
		meta := indexMeta{Name: name, NullFiltered: nullFiltered}
		for _, column := range columns {
			meta.Key = append(meta.Key, indexColumn{Column: column})
		}

		return meta
	}
	indexes := map[string][]indexMeta{
		"Assignment": {
			index("AssignmentsByTenantIdUserIdProjectId", false, "TenantId", "UserId", "ProjectId"),
			index("AssignmentsByTenantIdTeamIdProjectId", false, "TenantId", "TeamId", "ProjectId"),
		},
		"Delegation": {index("DelegationsByProjectIdTaskId", false, "ProjectId", "TaskId")},
		"OwnWhere":   {index("OwnWheresByUserIdProjectId", false, "UserId", "ProjectId")},
		"IndexedBoth": {
			// The key holds the three columns in another order, one of them
			// descending, and a trailing column: a lookup that binds each by
			// equality seeks it all the same.
			{Name: "IndexedBothsByUser", Key: []indexColumn{{Column: "UserId"}, {Column: "TenantId"}, {Column: "ProjectId", Descending: true}, {Column: "Id"}}},
			index("IndexedBothsByTenantIdTeamIdProjectId", false, "TenantId", "TeamId", "ProjectId"),
		},
		"IndexedOne": {index("IndexedOnesByTenantIdUserIdProjectId", false, "TenantId", "UserId", "ProjectId")},
		"IndexedNeither": {
			index("IndexedNeithersByUserIdProjectId", false, "UserId", "ProjectId"),
			index("IndexedNeithersByTenantIdTeamIdProjectId", true, "TenantId", "TeamId", "ProjectId"),
			// A search index holds the columns and serves no seek.
			{Name: "IndexedNeithersSearch", Search: true, Key: []indexColumn{{Column: "TenantId"}, {Column: "UserId"}, {Column: "ProjectId"}}},
		},
		"SecondaryThenKey": {index("SecondaryThenKeysByUserId", false, "UserId")},
		"Stake": {
			index("StakesByUserIdProjectId", false, "UserId", "ProjectId"),
			index("StakesByTeamIdProjectId", false, "TeamId", "ProjectId"),
		},
		"DeclaredLevel": {index("DeclaredLevelsByUserIdProjectId", false, "UserId", "ProjectId")},
	}

	c.tableMap = map[string]*tableMetadata{
		"Levels": {PkCount: 1, Columns: map[string]columnMeta{"Id": {IsPrimaryKey: true}}, Indexes: []indexMeta{index("PRIMARY_KEY", false, "Id")}},
	}
	for name, s := range structs {
		key, ok := keys[name]
		if !ok {
			key = []string{"Id"}
		}
		nullable, err := fieldNullability(s)
		if err != nil {
			t.Fatalf("fieldNullability(%s) error = %v", name, err)
		}

		table := &tableMetadata{PkCount: len(key), Columns: make(map[string]columnMeta)}
		for _, field := range s.Fields() {
			column, _ := field.LookupTag(spannerTagKey)
			meta := columnMeta{IsNullable: nullable[column]}
			if position := indexOf(key, column); position >= 0 {
				meta.IsPrimaryKey = true
				meta.KeyOrdinalPosition = int64(position)
			}
			if column == "Level" {
				meta.IsForeignKey, meta.ReferencedTable, meta.ReferencedColumn = true, "Levels", "Id"
			}
			if name == "Stake" && column == "ProjectId" {
				meta.IsForeignKey, meta.ReferencedTable, meta.ReferencedColumn = true, "Projects", "Id"
			}
			if column == "PoolId" {
				meta.IsForeignKey, meta.ReferencedTable, meta.ReferencedColumn = true, "Pools", "Id"
			}
			table.Columns[column] = meta
		}
		primaryKey := index("PRIMARY_KEY", false, key...)
		primaryKey.PrimaryKey, primaryKey.Unique = true, true
		table.Indexes = append([]indexMeta{primaryKey}, indexes[name]...)
		table.deriveIndexFlags()
		c.tableMap[c.pluralize(name)] = table
	}

	return c
}

// indexOf is the position of value in values, or -1.
func indexOf(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}

	return -1
}

// subjectSetStructs returns the named fixture structs in order, the way a
// package's structs reach structsToResources.
func subjectSetStructs(t *testing.T, structs map[string]*parser.Struct, names ...string) []*parser.Struct {
	t.Helper()

	picked := make([]*parser.Struct, 0, len(names))
	for _, name := range names {
		s := structs[name]
		if s == nil {
			t.Fatalf("struct %q not found in fixture package", name)
		}
		picked = append(picked, s)
	}

	return picked
}

// TestResolveSubjectSetParts pins what the two parts of a @subjectSet resolve
// to, as the collection literal carries them: a who: of several tests with each
// column named by its schema name and the set a test follows, a where: in the
// author's own names beside the columns and types it reads, and a set that
// writes neither part carrying neither.
func TestResolveSubjectSetParts(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)

	tests := []struct {
		name       string
		structName string
		want       []resource.SubjectBindingData
	}{
		{
			name:       "a set that writes neither part carries neither",
			structName: "TeamMember",
			want: []resource.SubjectBindingData{
				{Name: "teams", UserColumn: "UserId", Column: "TeamId", Type: resource.AttributeTypeString},
			},
		},
		{
			name:       "one table yields two sets by a column, each with a who: of two tests and its own where:",
			structName: "Assignment",
			want: []resource.SubjectBindingData{
				{
					Name: "leads", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}},
					Where: &resource.SubjectWhereData{
						Condition: "Level = 'lead' AND (ExpiresAt IS NULL OR ExpiresAt > now)",
						Columns: []resource.SubjectColumnData{
							{Name: "ExpiresAt", Column: "ExpiresAt", Type: resource.AttributeTypeTimestamp},
							{Name: "Level", Column: "Level", Type: resource.AttributeTypeString},
						},
					},
				},
				{
					Name: "follows", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}},
					Where: &resource.SubjectWhereData{
						Condition: "Level IN ('lead', 'member') AND Active = true AND Rank >= 2 AND StartsOn <= '2026-10-01'",
						Columns: []resource.SubjectColumnData{
							{Name: "Active", Column: "Active", Type: resource.AttributeTypeBool},
							{Name: "Level", Column: "Level", Type: resource.AttributeTypeString},
							{Name: "Rank", Column: "Rank", Type: resource.AttributeTypeNumber},
							{Name: "StartsOn", Column: "StartDate", Type: resource.AttributeTypeDate},
						},
					},
				},
			},
		},
		{
			name:       "a who: that follows another set alone sits on the column it tests",
			structName: "Delegation",
			want: []resource.SubjectBindingData{
				{
					Name: "delegated", UserColumn: "ProjectId", Column: "TaskId", Type: resource.AttributeTypeString,
					Who: []resource.SubjectWhoData{{Column: "ProjectId", Set: "leads"}},
				},
			},
		},
		{
			name:       "a dotted value with both parts: the parts read the set's own row, the hop is as it was",
			structName: "Stake",
			want: []resource.SubjectBindingData{
				{
					Name: "portfolios", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Path: []resource.BindingHop{{Table: "Projects", JoinColumn: "Id", Column: "PortfolioId"}},
					Who:  []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}},
					Where: &resource.SubjectWhereData{
						Condition: "Active = true",
						Columns:   []resource.SubjectColumnData{{Name: "Active", Column: "Active", Type: resource.AttributeTypeBool}},
					},
				},
			},
		},
		{
			name:       "a where: alone keeps the who: a set has always had",
			structName: "OwnWhere",
			want: []resource.SubjectBindingData{
				{
					Name: "current", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Where: &resource.SubjectWhereData{
						Condition: "ExpiresAt IS NULL OR ExpiresAt > now",
						Columns:   []resource.SubjectColumnData{{Name: "ExpiresAt", Column: "ExpiresAt", Type: resource.AttributeTypeTimestamp}},
					},
				},
			},
		},
		{
			name:       "a set on a table that is no tenant's carries the path its domain: names to a row's tenant",
			structName: "PoolMember",
			want: []resource.SubjectBindingData{
				{
					Name: "pools", UserColumn: "UserId", Column: "PoolId", Type: resource.AttributeTypeString,
					Domain: &resource.DomainBindingData{Column: "PoolId", Path: []resource.BindingHop{{Table: "Pools", JoinColumn: "Id", Column: "TenantId"}}},
				},
			},
		},
		{
			name:       "a string that holds a closing parenthesis is kept whole",
			structName: "WhereClosingParenthesis",
			want: []resource.SubjectBindingData{
				{
					Name: "smiling", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Where: &resource.SubjectWhereData{
						Condition: "Code = ':)'",
						Columns:   []resource.SubjectColumnData{{Name: "Code", Column: "Code", Type: resource.AttributeTypeString}},
					},
				},
			},
		},
		{
			name:       "a string that holds an opening parenthesis is kept whole",
			structName: "WhereOpeningParenthesis",
			want: []resource.SubjectBindingData{
				{
					Name: "opened", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Where: &resource.SubjectWhereData{
						Condition: "Code = '('",
						Columns:   []resource.SubjectColumnData{{Name: "Code", Column: "Code", Type: resource.AttributeTypeString}},
					},
				},
			},
		},
		{
			name:       "strings that hold parentheses, a comma, a double quote and a quote written twice, in a definition continued over lines",
			structName: "WhereStrings",
			want: []resource.SubjectBindingData{
				{
					Name: "marked", UserColumn: "UserId", Column: "ProjectId", Type: resource.AttributeTypeString,
					Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}},
					Where: &resource.SubjectWhereData{
						Condition: `Code IN (':)', ';(') AND (Note IS NULL OR Note != 'it''s ("so", then')`,
						Columns: []resource.SubjectColumnData{
							{Name: "Code", Column: "Code", Type: resource.AttributeTypeString},
							{Name: "Note", Column: "Note", Type: resource.AttributeTypeString},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := resolveFixtureBindings(t, c, structs, tt.structName)
			if err != nil {
				t.Fatalf("resolveBindingAnnotations(%s) error = %v", tt.structName, err)
			}
			got := make([]resource.SubjectBindingData, 0, len(res.SubjectSets))
			for _, set := range res.SubjectSets {
				got = append(got, collectionSubjectBinding(set))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("subject sets of %s mismatch (-want +got):\n%s", tt.structName, diff)
			}
		})
	}
}

// TestResolveSubjectSetParts_refusals pins every refusal one struct alone can
// earn: a who: that does not tie the row to the requester, a who: or a where:
// naming something that is not a column of the row, a literal that does not fit
// the column it is compared to, a value an enumerated column does not have, the
// requester or a proposed value named in a where:, an annotation sitting on a
// column its who: does not name, and a part written on a subject value. Each
// message says what is wrong and what to write.
func TestResolveSubjectSetParts_refusals(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)

	tests := []struct {
		name       string
		structName string
		want       []string
	}{
		{
			name:       "a who: that filters and names the requester nowhere",
			structName: "WhoLiteral",
			want:       []string{"@subjectSet(everyones)", `who: "Level = 'lead'" does not tie the row to the requester`, "each who: test is written Column = subject or Column IN subject.<set>", "a test over the row's other columns belongs in the where: part"},
		},
		{
			name:       "a who: that joins a filter to the requester's test",
			structName: "WhoWithFilter",
			want:       []string{"@subjectSet(mixed)", "does not tie the row to the requester", "belongs in the where: part"},
		},
		{
			name:       "a who: that names everyone but the requester",
			structName: "WhoEveryoneElse",
			want:       []string{"@subjectSet(others)", `who: "UserID != subject" does not tie the row to the requester`},
		},
		{
			name:       "a who: naming a field the row does not have",
			structName: "WhoUnknownField",
			want:       []string{"@subjectSet(owned)", "who: names OwnerID, which is not a field of the resource", "named by its Go field"},
		},
		{
			name:       "a who: comparing a number column to the requester",
			structName: "WhoNumberRequester",
			want:       []string{"@subjectSet(byRank)", `who: "Rank = subject" compares a number column to the requester, who is named by a user id, a string`, "name the column that holds the user's id"},
		},
		{
			name:       "a who: testing a column no condition can compare",
			structName: "WhoUncomparable",
			want:       []string{"@subjectSet(sealed)", "who: Payload cannot be tested", "is not a supported attribute comparison type"},
		},
		{
			name:       "a who: writing one test twice",
			structName: "WhoTwice",
			want:       []string{"@subjectSet(doubled)", `who: writes the test "UserID = subject" twice; write it once`},
		},
		{
			name:       "an annotation on a column its who: does not name",
			structName: "WhoMisplaced",
			want:       []string{"@subjectSet(misplaced)", "the annotation sits on ProjectID, a column its who: does not name", "the annotation sits on one of the columns it names (UserID, TeamID)"},
		},
		{
			name:       "a where: naming something that is not a column of the row",
			structName: "WhereUnknownField",
			want:       []string{"@subjectSet(byStatus)", "where: names Status, which is not a field of the resource", "each named by its Go field"},
		},
		{
			name:       "a where: reading a column no condition can compare",
			structName: "WhereUncomparable",
			want:       []string{"@subjectSet(byPayload)", "where: Payload cannot be compared", "is not a supported attribute comparison type"},
		},
		{
			name:       "a string compared to a number column, under AND",
			structName: "WhereStringToNumber",
			want:       []string{"@subjectSet(highRank)", "where: Rank is a number column and cannot be compared to the string 'high'"},
		},
		{
			name:       "a boolean compared to a number column",
			structName: "WhereBoolToNumber",
			want:       []string{"@subjectSet(ranked)", "where: Rank is a number column and cannot be compared to true, a boolean"},
		},
		{
			name:       "a number in a list compared to a string column",
			structName: "WhereNumberToString",
			want:       []string{"@subjectSet(third)", "where: Note is a string column and cannot be compared to the number 3"},
		},
		{
			name:       "a timestamp column compared to a string that is no instant, under OR",
			structName: "WhereBadTimestamp",
			want:       []string{"@subjectSet(tomorrow)", "where: ExpiresAt is a timestamp column, and 'tomorrow' is not an RFC 3339 timestamp such as '2026-10-01T00:00:00Z'"},
		},
		{
			name:       "a date column compared to a string that is no date",
			structName: "WhereBadDate",
			want:       []string{"@subjectSet(october)", "where: StartsOn is a date column, and '10/01/2026' is not a date written YYYY-MM-DD"},
		},
		{
			name:       "a number compared to a bool column, under NOT",
			structName: "WhereNumberToBool",
			want:       []string{"@subjectSet(switchedOn)", "where: Active is a bool column and cannot be compared to the number 1"},
		},
		{
			name:       "a date column compared to now",
			structName: "WhereDateToNow",
			want:       []string{"@subjectSet(started)", "where: StartsOn is a date column and cannot be compared to now, a timestamp"},
		},
		{
			name:       "a value an enumerated column does not have",
			structName: "WhereOffTheList",
			want:       []string{"@subjectSet(misspelled)", "where: Level is enumerated by Level, and 'membre' is not one of its values (lead, member, guest)"},
		},
		{
			name:       "a where: comparing a column to the requester",
			structName: "WhereRequester",
			want:       []string{"@subjectSet(mine)", "names the requester", "belongs in the who: part"},
		},
		{
			name:       "a where: testing a column against another set",
			structName: "WhereOtherSet",
			want:       []string{"@subjectSet(teamed)", "names the requester", "belongs in the who: part"},
		},
		{
			name:       "a where: comparing a column to a subject value",
			structName: "WhereSubjectValue",
			want:       []string{"@subjectSet(outranked)", "names the requester", "belongs in the who: part"},
		},
		{
			name:       "a where: reading a proposed value",
			structName: "WhereProposed",
			want:       []string{"@subjectSet(proposed)", "reads a proposed value (new.)"},
		},
		{
			name:       "a part written on a subject value",
			structName: "ValueWithParts",
			want:       []string{"@subjectValue(seatRank)", "@subjectValue takes the name and value: alone; who: and where: are the parts of a @subjectSet"},
		},
		{
			name:       "a part written on a subject value, a parenthesis in its string",
			structName: "ValueWithStringPart",
			want:       []string{"@subjectValue(seatCode)", "@subjectValue takes the name and value: alone; who: and where: are the parts of a @subjectSet"},
		},
		{
			name:       "a domain: on a table that carries its tenant itself",
			structName: "DomainOnTenantTable",
			want:       []string{"@subjectSet(tenantPools) writes domain:", "DomainOnTenantTable is domain-scoped, and its @domain binding already keeps the set to the request's tenant", "remove domain:"},
		},
		{
			name:       "a domain: that names a column and no path from it",
			structName: "DomainNoPath",
			want:       []string{"@subjectSet(pathless)", `domain: "PoolID" is not a path to a tenant`, "write a foreign key of the row, then the column of its table that holds the tenant's key, as GroupID.DomainID"},
		},
		{
			name:       "a domain: starting at a field the row does not have",
			structName: "DomainUnknownField",
			want:       []string{"@subjectSet(strayPath)", "domain: names CrewID, which is not a field of the resource", "the path starts at a foreign key of the same row"},
		},
		{
			name:       "a domain: starting at a column that is no foreign key",
			structName: "DomainNotForeignKey",
			want:       []string{"@subjectSet(looseEnd)", "domain:", "a join path must leave through a foreign key, and Note is not one"},
		},
		{
			name:       "a domain: ending at a number",
			structName: "DomainNumber",
			want:       []string{"@subjectSet(sized)", "domain: PoolID.Size ends at a number column, and a tenant's key is text", "end the path at the column that holds the tenant's key"},
		},
		{
			name:       "a domain: on a subject value",
			structName: "DomainOnValue",
			want:       []string{"@subjectValue(homePool)", "@subjectValue takes the name and value: alone; domain: is a part of a @subjectSet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveFixtureBindings(t, c, structs, tt.structName)
			if err == nil {
				t.Fatalf("resolveBindingAnnotations(%s) error = nil, want a refusal", tt.structName)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("resolveBindingAnnotations(%s) error = %q, want it to contain %q", tt.structName, err.Error(), want)
				}
			}
		})
	}
}

// TestResolveSubjectSetReferences pins what generation holds a who: to once
// every resource is extracted, driven through structsToResources as a
// generation run drives it: the set a test names is declared, is a set and not a
// subject value, and yields values of the tested column's own type; and sets
// never build on each other in a loop, a set on itself included. Sets two
// levels deep that end at a set which names the requester are accepted. It also
// drives the one where: refusal that reads a fact the run gathers before the
// bindings resolve: the enumeration a plain column declares on its field.
func TestResolveSubjectSetReferences(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))

	tests := []struct {
		name    string
		structs []string
		want    []string
	}{
		{
			name:    "sets two levels deep, a tenant's set on a global one and a global set on a tenant's",
			structs: []string{"TeamMember", "Seat", "Assignment", "Delegation", "OwnWhere", "Project", "Stake"},
		},
		{
			// A field-scope @enumerate is read before the bindings resolve, as a
			// generation run reads it, so this refusal is driven from here.
			name:    "a where: naming values of the enumeration a plain column declares",
			structs: []string{"DeclaredLevel"},
		},
		{
			name:    "a where: naming a value the enumeration a plain column declares does not have",
			structs: []string{"WhereOffTheDeclaredList"},
			want:    []string{"@subjectSet(misgraded)", "where: Grade is enumerated by Level, and 'chief' is not one of its values (lead, member, guest)"},
		},
		{
			name:    "a who: naming a set nothing declares",
			structs: []string{"TeamMember", "Assignment", "WhoUndeclaredSet"},
			want:    []string{"struct WhoUndeclaredSet field TeamID: @subjectSet(viaNowhere): who: tests TeamID against subject.squads, which no @subjectSet declares", "the sets declared are: follows, leads, teams, viaNowhere"},
		},
		{
			name:    "a who: naming a subject value",
			structs: []string{"TeamMember", "Seat", "WhoSubjectValue"},
			want:    []string{"struct WhoSubjectValue field Rank: @subjectSet(viaValue): who: tests Rank against subject.rank, which Seat declares as a @subjectValue, one value", "a who: tests a column for membership in a @subjectSet"},
		},
		{
			name:    "a who: testing a column against a set of another type",
			structs: []string{"TeamMember", "WhoOtherType"},
			want:    []string{"struct WhoOtherType field Rank: @subjectSet(viaNumber): who: Rank is a number column and subject.teams (declared on TeamMember) is a set of string values", "a column is tested against a set of its own type"},
		},
		{
			name:    "two sets that build on each other",
			structs: []string{"TeamMember", "LoopFirst", "LoopSecond"},
			want:    []string{"subject sets build on each other in a loop: loopFirst (LoopFirst) → loopSecond (LoopSecond) → loopFirst (LoopFirst)", "a set's who: may name only sets that do not come back to it"},
		},
		{
			name:    "a set that builds on itself",
			structs: []string{"LoopSelf"},
			want:    []string{"subject sets build on each other in a loop: loopSelf (LoopSelf) → loopSelf (LoopSelf)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := subjectSetFixtureClient(t, structs)
			_, err := c.structsToResources(subjectSetStructs(t, structs, tt.structs...))
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("structsToResources() error = %v, want none", err)
				}

				return
			}
			if err == nil {
				t.Fatal("structsToResources() error = nil, want a refusal")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("structsToResources() error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestSubjectSetIndexWarnings pins the warning for a set whose lookup no index
// of its table serves: one warning for each column the who: names that has no
// index, the CREATE INDEX it wants with the tenant column first where the table
// has one, and silence where an index, the primary key, or a secondary index
// with the key Spanner appends to it leads with the columns. It never refuses:
// the resources resolve, and the warnings are returned beside them.
func TestSubjectSetIndexWarnings(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	resources, err := c.structsToResources(subjectSetStructs(t, structs,
		"TeamMember", "Assignment", "Delegation", "OwnWhere", "Project", "Stake", "DeclaredLevel",
		"IndexedBoth", "IndexedOne", "IndexedNeither", "PlainUnindexed", "KeyedByBoth", "SecondaryThenKey",
	))
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}

	want := []Warning{
		SubjectSetIndexWarning{
			Resource: "IndexedNeither", Table: "IndexedNeithers", Set: "indexedNeither",
			TenantColumn: "TenantId", Column: "UserId", Value: "ProjectId",
			Index: "CREATE INDEX IndexedNeithersByTenantIdUserIdProjectId ON IndexedNeithers(TenantId, UserId, ProjectId)",
		},
		SubjectSetIndexWarning{
			Resource: "IndexedNeither", Table: "IndexedNeithers", Set: "indexedNeither",
			TenantColumn: "TenantId", Column: "TeamId", Value: "ProjectId",
			Index: "CREATE INDEX IndexedNeithersByTenantIdTeamIdProjectId ON IndexedNeithers(TenantId, TeamId, ProjectId)",
		},
		SubjectSetIndexWarning{
			Resource: "IndexedOne", Table: "IndexedOnes", Set: "indexedOne",
			TenantColumn: "TenantId", Column: "TeamId", Value: "ProjectId",
			Index: "CREATE INDEX IndexedOnesByTenantIdTeamIdProjectId ON IndexedOnes(TenantId, TeamId, ProjectId)",
		},
		SubjectSetIndexWarning{
			Resource: "PlainUnindexed", Table: "PlainUnindexeds", Set: "plainUnindexed",
			Column: "UserId", Value: "ProjectId",
			Index: "CREATE INDEX PlainUnindexedsByUserIdProjectId ON PlainUnindexeds(UserId, ProjectId)",
		},
	}
	if diff := cmp.Diff(want, c.schemaWarnings(resources)); diff != "" {
		t.Errorf("schemaWarnings() mismatch (-want +got):\n%s", diff)
	}
}

// TestSubjectSetIndexWarning_String pins the one-line text a runner prints, with
// and without a tenant column, and for a set that yields the column it finds the
// requester's rows by.
func TestSubjectSetIndexWarning_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		warning SubjectSetIndexWarning
		want    string
	}{
		{
			name: "a tenant's table",
			warning: SubjectSetIndexWarning{
				Resource: "IndexedOne", Table: "IndexedOnes", Set: "indexedOne",
				TenantColumn: "TenantId", Column: "TeamId", Value: "ProjectId",
				Index: "CREATE INDEX IndexedOnesByTenantIdTeamIdProjectId ON IndexedOnes(TenantId, TeamId, ProjectId)",
			},
			want: "IndexedOne declares subject.indexedOne, which finds the requester's rows by TeamId with no index leading with TenantId, TeamId, ProjectId, so every check of the set reads all of IndexedOnes; wanted: CREATE INDEX IndexedOnesByTenantIdTeamIdProjectId ON IndexedOnes(TenantId, TeamId, ProjectId)",
		},
		{
			name: "a table outside every tenant",
			warning: SubjectSetIndexWarning{
				Resource: "PlainUnindexed", Table: "PlainUnindexeds", Set: "plainUnindexed",
				Column: "UserId", Value: "ProjectId",
				Index: "CREATE INDEX PlainUnindexedsByUserIdProjectId ON PlainUnindexeds(UserId, ProjectId)",
			},
			want: "PlainUnindexed declares subject.plainUnindexed, which finds the requester's rows by UserId with no index leading with UserId, ProjectId, so every check of the set reads all of PlainUnindexeds; wanted: CREATE INDEX PlainUnindexedsByUserIdProjectId ON PlainUnindexeds(UserId, ProjectId)",
		},
		{
			name: "a set that yields the column it is found by",
			warning: SubjectSetIndexWarning{
				Resource: "Watcher", Table: "Watchers", Set: "watched",
				Column: "ProjectId", Value: "ProjectId",
				Index: "CREATE INDEX WatchersByProjectId ON Watchers(ProjectId)",
			},
			want: "Watcher declares subject.watched, which finds the requester's rows by ProjectId with no index leading with ProjectId, so every check of the set reads all of Watchers; wanted: CREATE INDEX WatchersByProjectId ON Watchers(ProjectId)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.warning.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// renderSubjectSetCollection renders the generated collection file for the subject
// sets the named fixture structs declare, in order, as gofmt leaves it.
func renderSubjectSetCollection(t *testing.T, names ...string) string {
	t.Helper()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	var sets []resource.SubjectBindingData
	for _, name := range names {
		res, err := resolveFixtureBindings(t, c, structs, name)
		if err != nil {
			t.Fatalf("resolveBindingAnnotations(%s) error = %v", name, err)
		}
		for _, set := range res.SubjectSets {
			sets = append(sets, collectionSubjectBinding(set))
		}
	}

	r := &resourceGenerator{client: &client{}}
	output, err := r.generateTemplateOutput("collectionTemplate", collectionTemplate, &collectionFileData{
		Source:  "pkg/resources",
		Package: "router",
		Data: resource.CollectionData{Resources: []resource.CollectionResource{{
			Name:        "Assignments",
			Scope:       accesstypes.GlobalPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			SubjectSets: sets,
		}}},
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	formatted, err := format.Source(output)
	if err != nil {
		t.Fatalf("rendered collection file is not valid Go: %v\n%s", err, output)
	}

	return string(formatted)
}

// Test_collectionTemplate_subjectSetParts renders the generated collection file
// for sets that write a part and one that writes none. The set that writes none
// renders the literal it rendered before the parts existed, character for
// character, and a set's parts follow the fields it has always had.
func Test_collectionTemplate_subjectSetParts(t *testing.T) {
	t.Parallel()

	formatted := renderSubjectSetCollection(t, "TeamMember", "Assignment", "Delegation", "Stake")

	want := `				SubjectSets: []resource.SubjectBindingData{
					{Name: "teams", UserColumn: "UserId", Column: "TeamId", Type: "string"},
					{Name: "leads", UserColumn: "UserId", Column: "ProjectId", Type: "string", Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}}, Where: &resource.SubjectWhereData{Condition: "Level = 'lead' AND (ExpiresAt IS NULL OR ExpiresAt > now)", Columns: []resource.SubjectColumnData{{Name: "ExpiresAt", Column: "ExpiresAt", Type: "timestamp"}, {Name: "Level", Column: "Level", Type: "string"}}}},
					{Name: "follows", UserColumn: "UserId", Column: "ProjectId", Type: "string", Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}}, Where: &resource.SubjectWhereData{Condition: "Level IN ('lead', 'member') AND Active = true AND Rank >= 2 AND StartsOn <= '2026-10-01'", Columns: []resource.SubjectColumnData{{Name: "Active", Column: "Active", Type: "bool"}, {Name: "Level", Column: "Level", Type: "string"}, {Name: "Rank", Column: "Rank", Type: "number"}, {Name: "StartsOn", Column: "StartDate", Type: "date"}}}},
					{Name: "delegated", UserColumn: "ProjectId", Column: "TaskId", Type: "string", Who: []resource.SubjectWhoData{{Column: "ProjectId", Set: "leads"}}},
					{Name: "portfolios", UserColumn: "UserId", Column: "ProjectId", Type: "string", Path: []resource.BindingHop{{Table: "Projects", JoinColumn: "Id", Column: "PortfolioId"}}, Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}}, Where: &resource.SubjectWhereData{Condition: "Active = true", Columns: []resource.SubjectColumnData{{Name: "Active", Column: "Active", Type: "bool"}}}},
				},
`
	if !strings.Contains(formatted, want) {
		t.Errorf("rendered collection file does not hold the subject sets as\n%s\ngot:\n%s", want, formatted)
	}
}

// Test_collectionTemplate_subjectSetStrings renders the generated collection file
// for sets whose where: compares to a string that holds a parenthesis with no
// partner, a quote written twice among them: the generated set carries each string
// as its author wrote it.
func Test_collectionTemplate_subjectSetStrings(t *testing.T) {
	t.Parallel()

	formatted := renderSubjectSetCollection(t, "WhereClosingParenthesis", "WhereOpeningParenthesis", "WhereStrings")

	want := `				SubjectSets: []resource.SubjectBindingData{
					{Name: "smiling", UserColumn: "UserId", Column: "ProjectId", Type: "string", Where: &resource.SubjectWhereData{Condition: "Code = ':)'", Columns: []resource.SubjectColumnData{{Name: "Code", Column: "Code", Type: "string"}}}},
					{Name: "opened", UserColumn: "UserId", Column: "ProjectId", Type: "string", Where: &resource.SubjectWhereData{Condition: "Code = '('", Columns: []resource.SubjectColumnData{{Name: "Code", Column: "Code", Type: "string"}}}},
					{Name: "marked", UserColumn: "UserId", Column: "ProjectId", Type: "string", Who: []resource.SubjectWhoData{{Column: "UserId"}, {Column: "TeamId", Set: "teams"}}, Where: &resource.SubjectWhereData{Condition: "Code IN (':)', ';(') AND (Note IS NULL OR Note != 'it''s (\"so\", then')", Columns: []resource.SubjectColumnData{{Name: "Code", Column: "Code", Type: "string"}, {Name: "Note", Column: "Note", Type: "string"}}}},
				},
`
	if !strings.Contains(formatted, want) {
		t.Errorf("rendered collection file does not hold the subject sets as\n%s\ngot:\n%s", want, formatted)
	}
}

// Test_resourceKeywords_expressions pins which annotations are scanned as holding
// expressions of the condition language: the two the subject binding spec parses,
// and no other, so an apostrophe in any other annotation's arguments is a character
// like the rest.
func Test_resourceKeywords_expressions(t *testing.T) {
	t.Parallel()

	var got []string
	for keyword, opts := range resourceKeywords() {
		for _, flags := range opts {
			if flags&genlang.Expressions != 0 {
				got = append(got, keyword)
			}
		}
	}
	slices.Sort(got)
	if diff := cmp.Diff([]string{subjectSetKeyword, subjectValueKeyword}, got); diff != "" {
		t.Errorf("keywords scanned as holding expressions mismatch (-want +got):\n%s", diff)
	}
}

// Test_collectionTemplate_subjectSetDomain renders the generated collection file for
// a set that names its own way to a row's tenant: the set's literal ends with the
// binding, the foreign key the path leaves through and the hop to the tenant's key.
func Test_collectionTemplate_subjectSetDomain(t *testing.T) {
	t.Parallel()

	formatted := renderSubjectSetCollection(t, "Pool", "PoolMember")

	want := `					{Name: "pools", UserColumn: "UserId", Column: "PoolId", Type: "string", Domain: &resource.DomainBindingData{Column: "PoolId", Path: []resource.BindingHop{{Table: "Pools", JoinColumn: "Id", Column: "TenantId"}}}},
`
	if !strings.Contains(formatted, want) {
		t.Errorf("rendered collection file does not hold the subject set as\n%s\ngot:\n%s", want, formatted)
	}
	if got := subjectDomainLiteral(nil); got != "" {
		t.Errorf("subjectDomainLiteral(nil) = %q, want nothing", got)
	}
}

// Test_subjectPartsLiteral pins that a set which writes neither part adds nothing
// to its literal, which is what keeps every collection file written before the
// parts existed the file it is.
func Test_subjectPartsLiteral(t *testing.T) {
	t.Parallel()

	if got := subjectPartsLiteral(nil, nil); got != "" {
		t.Errorf("subjectPartsLiteral(nil, nil) = %q, want nothing", got)
	}
	if got := subjectPartsLiteral([]resource.SubjectWhoData{}, nil); got != "" {
		t.Errorf("subjectPartsLiteral(empty, nil) = %q, want nothing", got)
	}
}

// TestGenerateBindingsGraph_subjectSetParts pins what the bindings graph shows of
// the two parts: each set's who: and where: listed under it in its box, in the
// columns' names; the requester entering each column a who: compares to it, once,
// with the sets it yields there; and a set that builds on another as a dashed
// edge from the resource declaring that other set, labeled with the test. A set
// whose value is one hop away (Stake) is drawn as that hop and still lists its
// parts in its box. The header says how to read the parts only in a graph that
// has some: a package whose sets write none keeps its file
// (TestGenerateBindingsGraph).
func TestGenerateBindingsGraph_subjectSetParts(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	resources, err := c.structsToResources(subjectSetStructs(t, structs, "TeamMember", "Seat", "Assignment", "Delegation", "OwnWhere", "Project", "Stake"))
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}

	dir := t.TempDir()
	r := &resourceGenerator{client: c}
	r.resources = resources
	r.resource = packageDir(dir)
	if err := r.generateBindingsGraph(); err != nil {
		t.Fatalf("generateBindingsGraph() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "zz_gen_bindings.dot"))
	if err != nil {
		t.Fatalf("reading bindings graph: %v", err)
	}

	want := `// Code generated by resourcegeneration. DO NOT EDIT.
// Bindings: every attribute and subject binding this package declares. A box per
// declaring resource lists its bare bindings as ` + "`name: column`" + `; dashed boxes are
// the tables a path lands on or a foreign key points at; the subject ellipse is
// the requester, entering each anchor once with every name it yields there. One
// edge per hop, drawn once however many paths share it: solid for @attribute,
// dotted for @domain, dashed for a subject binding, labeled ` + "`name: column`" + ` on a
// continuing hop and ` + "`name: column ⇒ column`" + ` where the path terminates; a thin
// grey edge points a bare binding on a foreign key at its table. Facts only: no
// grant or condition is drawn.
// A subject set that writes a who: or a where: part lists each under the set, in
// the columns' names: who: says which rows are the requester's, where: which of
// those rows count. A set that builds on another is a dashed edge from the
// resource declaring that other set, labeled ` + "`name: column IN subject.other`" + `.
digraph Bindings {
	rankdir=LR;
	node [shape=box];
	"Assignment" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>Assignment</b></td></tr><tr><td align="left">subject.follows: ProjectId</td></tr><tr><td align="left">subject.follows who: UserId = subject OR TeamId IN subject.teams</td></tr><tr><td align="left">subject.follows where: Level IN (&#39;lead&#39;, &#39;member&#39;) AND Active = true AND Rank &gt;= 2 AND StartDate &lt;= &#39;2026-10-01&#39;</td></tr><tr><td align="left">subject.leads: ProjectId</td></tr><tr><td align="left">subject.leads who: UserId = subject OR TeamId IN subject.teams</td></tr><tr><td align="left">subject.leads where: Level = &#39;lead&#39; AND (ExpiresAt IS NULL OR ExpiresAt &gt; now)</td></tr></table>>];
	"Delegation" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>Delegation</b></td></tr><tr><td align="left">subject.delegated: TaskId</td></tr><tr><td align="left">subject.delegated who: ProjectId IN subject.leads</td></tr></table>>];
	"OwnWhere" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>OwnWhere</b></td></tr><tr><td align="left">subject.current: ProjectId</td></tr><tr><td align="left">subject.current where: ExpiresAt IS NULL OR ExpiresAt &gt; now</td></tr></table>>];
	"Seat" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>Seat</b></td></tr><tr><td align="left">subject.rank: Rank</td></tr></table>>];
	"Stake" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>Stake</b></td></tr><tr><td align="left">subject.portfolios who: UserId = subject OR TeamId IN subject.teams</td></tr><tr><td align="left">subject.portfolios where: Active = true</td></tr></table>>];
	"TeamMember" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>TeamMember</b></td></tr><tr><td align="left">subject.teams: TeamId</td></tr></table>>];
	"Project" [style=dashed];
	"subject" [shape=ellipse];
	"Assignment" -> "Delegation" [style=dashed, label="delegated: ProjectId IN subject.leads"];
	"Stake" -> "Project" [style=dashed, label="portfolios: ProjectId ⇒ PortfolioId"];
	"TeamMember" -> "Assignment" [style=dashed, label="follows: TeamId IN subject.teams"];
	"TeamMember" -> "Assignment" [style=dashed, label="leads: TeamId IN subject.teams"];
	"TeamMember" -> "Stake" [style=dashed, label="portfolios: TeamId IN subject.teams"];
	"subject" -> "Assignment" [style=dashed, label="follows, leads: UserId"];
	"subject" -> "OwnWhere" [style=dashed, label="current: UserId"];
	"subject" -> "Seat" [style=dashed, label="rank: UserId"];
	"subject" -> "Stake" [style=dashed, label="portfolios: UserId"];
	"subject" -> "TeamMember" [style=dashed, label="teams: UserId"];
	subgraph cluster_legend {
		label="legend";
		"declares a binding" [shape=box];
		"landed on / pointed at" [shape=box, style=dashed];
		"requester" [shape=ellipse];
		"legend:attribute" [shape=point];
		"legend:attribute:to" [shape=point];
		"legend:domain" [shape=point];
		"legend:domain:to" [shape=point];
		"legend:subject" [shape=point];
		"legend:subject:to" [shape=point];
		"legend:reference" [shape=point];
		"legend:reference:to" [shape=point];
		"legend:attribute" -> "legend:attribute:to" [label="@attribute path"];
		"legend:domain" -> "legend:domain:to" [style=dotted, label="@domain path"];
		"legend:subject" -> "legend:subject:to" [style=dashed, label="@subjectSet / @subjectValue"];
		"legend:reference" -> "legend:reference:to" [color=gray55, fontcolor=gray35, arrowhead=vee, arrowsize=0.6, label="bare binding on a foreign key"];
	}
}
`
	if got := string(content); got != want {
		t.Errorf("bindings graph mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestGenerateBindingsGraph_subjectSetDomain pins what the bindings graph shows of a
// set's domain: part, the path to the tenant of the set's rows: drawn as an @domain
// path is, dotted, from the resource declaring the set to the table that holds the
// tenant's key, labeled with the set's name, the column the path leaves through and
// the column it reads. A set that writes no domain: (TeamMember's) draws no such
// edge. The header says how to read the path only in a graph that has one: a package
// whose sets write none keeps its file (TestGenerateBindingsGraph,
// TestGenerateBindingsGraph_subjectSetParts).
func TestGenerateBindingsGraph_subjectSetDomain(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	resources, err := c.structsToResources(subjectSetStructs(t, structs, "Pool", "PoolMember", "TeamMember"))
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}

	dir := t.TempDir()
	r := &resourceGenerator{client: c}
	r.resources = resources
	r.resource = packageDir(dir)
	if err := r.generateBindingsGraph(); err != nil {
		t.Fatalf("generateBindingsGraph() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "zz_gen_bindings.dot"))
	if err != nil {
		t.Fatalf("reading bindings graph: %v", err)
	}

	want := `// Code generated by resourcegeneration. DO NOT EDIT.
// Bindings: every attribute and subject binding this package declares. A box per
// declaring resource lists its bare bindings as ` + "`name: column`" + `; dashed boxes are
// the tables a path lands on or a foreign key points at; the subject ellipse is
// the requester, entering each anchor once with every name it yields there. One
// edge per hop, drawn once however many paths share it: solid for @attribute,
// dotted for @domain, dashed for a subject binding, labeled ` + "`name: column`" + ` on a
// continuing hop and ` + "`name: column ⇒ column`" + ` where the path terminates; a thin
// grey edge points a bare binding on a foreign key at its table. Facts only: no
// grant or condition is drawn.
// A subject set that writes a domain: part says where its rows' tenant is found,
// and in a request with a tenant the set holds that tenant's rows alone. The path
// is drawn dotted, as @domain's is, under the set's name: ` + "`name domain: column`" + ` on
// a continuing hop and ` + "`name domain: column ⇒ column`" + ` where it reads the tenant.
digraph Bindings {
	rankdir=LR;
	node [shape=box];
	"PoolMember" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>PoolMember</b></td></tr><tr><td align="left">subject.pools: PoolId</td></tr></table>>];
	"TeamMember" [label=<<table border="0" cellborder="0" cellspacing="0" cellpadding="1"><tr><td align="left"><b>TeamMember</b></td></tr><tr><td align="left">subject.teams: TeamId</td></tr></table>>];
	"Pool" [style=dashed];
	"subject" [shape=ellipse];
	"PoolMember" -> "Pool" [style=dotted, label="pools domain: PoolId ⇒ TenantId"];
	"subject" -> "PoolMember" [style=dashed, label="pools: UserId"];
	"subject" -> "TeamMember" [style=dashed, label="teams: UserId"];
	"PoolMember" -> "Pool" [color=gray55, fontcolor=gray35, arrowhead=vee, arrowsize=0.6, label="subject.pools"];
	subgraph cluster_legend {
		label="legend";
		"declares a binding" [shape=box];
		"landed on / pointed at" [shape=box, style=dashed];
		"requester" [shape=ellipse];
		"legend:attribute" [shape=point];
		"legend:attribute:to" [shape=point];
		"legend:domain" [shape=point];
		"legend:domain:to" [shape=point];
		"legend:subject" [shape=point];
		"legend:subject:to" [shape=point];
		"legend:reference" [shape=point];
		"legend:reference:to" [shape=point];
		"legend:attribute" -> "legend:attribute:to" [label="@attribute path"];
		"legend:domain" -> "legend:domain:to" [style=dotted, label="@domain path"];
		"legend:subject" -> "legend:subject:to" [style=dashed, label="@subjectSet / @subjectValue"];
		"legend:reference" -> "legend:reference:to" [color=gray55, fontcolor=gray35, arrowhead=vee, arrowsize=0.6, label="bare binding on a foreign key"];
	}
}
`
	if got := string(content); got != want {
		t.Errorf("bindings graph mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
}
