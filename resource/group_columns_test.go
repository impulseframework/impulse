package resource

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// The fixture of the group column tests: two yards (the tenants), a group kept in each
// and a group kept for the whole application, as an auth's groups table holds them.
const (
	groupYardNorth = "11111111-1111-4111-8111-111111111111"
	groupYardSouth = "22222222-2222-4222-8222-222222222222"

	groupOfNorth    = "aaaaaaaa-0000-4000-8000-000000000001"
	groupOfSouth    = "aaaaaaaa-0000-4000-8000-000000000002"
	groupOfEveryone = "aaaaaaaa-0000-4000-8000-000000000003"
	groupOfNobody   = "aaaaaaaa-0000-4000-8000-000000000009"

	// groupRefusal is the answer to a row that names a group its yard does not keep.
	groupRefusal = "groupId names a group that is not one of this yard's own. Choose a group kept in this yard: a group kept in another yard, or for the whole application, cannot be named here."
)

// groupShare is a row of a yard that names a group, with its yard on the row.
type groupShare struct {
	ID      string  `spanner:"Id"`
	Yard    string  `spanner:"Yard"`
	GroupID *string `spanner:"GroupId"`
	Note    string  `spanner:"Note"`
}

func (groupShare) Resource() accesstypes.Resource {
	return "GroupShares"
}

func (groupShare) GroupColumns() []GroupColumn {
	return []GroupColumn{
		{Field: "GroupID", Name: "groupId", Tenant: "Yard", Groups: "CastGroups", Key: "Id", Kept: "DomainId", TenantWord: "yard"},
	}
}

// groupSeal is a row that names a group and reaches its yard through its share.
type groupSeal struct {
	ID      string  `spanner:"Id"`
	ShareID string  `spanner:"ShareId"`
	GroupID *string `spanner:"GroupId"`
}

func (groupSeal) Resource() accesstypes.Resource {
	return "GroupSeals"
}

func (groupSeal) GroupColumns() []GroupColumn {
	return []GroupColumn{
		{
			Field: "GroupID", Name: "groupId", Tenant: "ShareID", Path: []BindingHop{{Table: "GroupShares", JoinColumn: "Id", Column: "Yard"}},
			Groups: "CastGroups", Key: "Id", Kept: "DomainId", TenantWord: "yard",
		},
	}
}

// groupCase is one row a writer proposes: the value of the field its tenant is found
// through, the group it names, and whether the library writes it.
type groupCase struct {
	name    string
	anchor  string
	group   string
	refused bool
}

// groupShareCases are the cases of a row that carries its yard, the same on both
// databases.
func groupShareCases() []groupCase {
	return []groupCase{
		{name: "a group kept in the row's own yard is written", anchor: groupYardNorth, group: groupOfNorth},
		{name: "a group kept in another yard is refused", anchor: groupYardNorth, group: groupOfSouth, refused: true},
		{name: "a group kept for the whole application is refused", anchor: groupYardNorth, group: groupOfEveryone, refused: true},
		{name: "an id that is no group's is refused in the same words", anchor: groupYardNorth, group: groupOfNobody, refused: true},
		{name: "the other yard's own group is written there", anchor: groupYardSouth, group: groupOfSouth},
	}
}

// judge holds a check's answer to its case: nothing for a row that is written, and
// for a refused one a bad request in the refusal's exact words.
func (c *groupCase) judge(t *testing.T, err error) {
	t.Helper()

	if !c.refused {
		if err != nil {
			t.Fatalf("error = %v, want the row written", err)
		}

		return
	}
	if err == nil {
		t.Fatal("the row was written, want it refused")
	}
	if !httpio.HasBadRequest(err) {
		t.Errorf("error = %v, want a bad request", err)
	}
	if got := httpio.Message(err); got != groupRefusal {
		t.Errorf("the refusal reads\n  %s\nwant\n  %s", got, groupRefusal)
	}
}

func Test_pointTextStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dbType DBType
		want   string
	}{
		{name: "Spanner", dbType: SpannerDBType, want: "SELECT CAST(`DomainId` AS STRING) FROM `CastGroups` WHERE `Id` = @value"},
		{name: "PostgreSQL", dbType: PostgresDBType, want: `SELECT CAST("DomainId" AS text) FROM "CastGroups" WHERE "Id" = @value`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stmt := pointTextStatement(tt.dbType, "CastGroups", "Id", "DomainId", groupOfNorth)
			if stmt.SQL != tt.want {
				t.Errorf("SQL = %s, want %s", stmt.SQL, tt.want)
			}
			if got := stmt.Params["value"]; got != groupOfNorth {
				t.Errorf("the value bound = %v, want %s", got, groupOfNorth)
			}
		})
	}
}

// TestGroupColumn_check pins the rule's decision over a reader that holds the fixture
// in memory, in both dialects' statements: the group's tenant against the row's, read
// on the row or through its parent.
func TestGroupColumn_check(t *testing.T) {
	t.Parallel()

	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		held := map[string]string{}
		hold := func(table, key, column, value, text string) {
			stmt := pointTextStatement(dbType, table, key, column, value)
			held[stmt.SQL+" "+value] = text
		}
		hold("CastGroups", "Id", "DomainId", groupOfNorth, groupYardNorth)
		hold("CastGroups", "Id", "DomainId", groupOfSouth, groupYardSouth)
		hold("CastGroups", "Id", "DomainId", groupOfEveryone, "")
		hold("GroupShares", "Id", "Yard", "north-share", groupYardNorth)
		read := func(_ context.Context, stmt *Statement) (string, bool, error) {
			text, found := held[stmt.SQL+" "+stmt.Params["value"].(string)]

			return text, found, nil
		}

		onRow := groupShare{}.GroupColumns()[0]
		cases := groupShareCases()
		cases = append(cases, groupCase{name: "a row with no yard names no group", anchor: "", group: groupOfEveryone, refused: true})
		for _, tt := range cases {
			t.Run(string(dbType)+", yard on the row: "+tt.name, func(t *testing.T) {
				t.Parallel()

				tt.judge(t, onRow.check(context.Background(), dbType, read, tt.anchor, tt.group))
			})
		}

		throughParent := groupSeal{}.GroupColumns()[0]
		for _, tt := range []groupCase{
			{name: "a group kept in the parent's yard is written", anchor: "north-share", group: groupOfNorth},
			{name: "a group kept in another yard is refused", anchor: "north-share", group: groupOfSouth, refused: true},
			{name: "a group kept for the whole application is refused", anchor: "north-share", group: groupOfEveryone, refused: true},
			{name: "a parent that is not there is left to the foreign key", anchor: "no-share", group: groupOfSouth},
		} {
			t.Run(string(dbType)+", yard through a parent: "+tt.name, func(t *testing.T) {
				t.Parallel()

				tt.judge(t, throughParent.check(context.Background(), dbType, read, tt.anchor, tt.group))
			})
		}
	}
}

// TestGroupColumn_refusalWords pins the answer where the application gives the library
// no word for a tenant.
func TestGroupColumn_refusalWords(t *testing.T) {
	t.Parallel()

	column := GroupColumn{Name: "teamId"}
	want := "teamId names a group that is not one of this tenant's own. Choose a group kept in this tenant: a group kept in another tenant, or for the whole application, cannot be named here."
	if got := httpio.Message(column.refusal()); got != want {
		t.Errorf("the refusal reads\n  %s\nwant\n  %s", got, want)
	}
}

// TestPatchSet_checkGroupColumns_mock pins that a mocked transaction, which holds no
// tables to ask, buffers a row that names a group.
func TestPatchSet_checkGroupColumns_mock(t *testing.T) {
	t.Parallel()

	group := groupOfSouth
	p := NewPatchSet(NewMetadata[groupShare]()).SetPatchType(CreatePatchType)
	p.SetKey("ID", "share-1")
	p.Set("Yard", groupYardNorth)
	p.Set("GroupID", &group)
	p.Set("Note", "a note")
	if err := p.checkGroupColumns(context.Background(), NewMockReadWriteTransaction(&bufferingTxn{})); err != nil {
		t.Fatalf("checkGroupColumns() error = %v, want none on a mocked transaction", err)
	}
}

func Test_columnText(t *testing.T) {
	t.Parallel()

	text := "north"
	var none *string
	tests := []struct {
		name    string
		value   any
		want    string
		wantErr bool
	}{
		{name: "nothing", value: nil, want: ""},
		{name: "a string", value: "north", want: "north"},
		{name: "a pointer to a string", value: &text, want: "north"},
		{name: "a nil pointer", value: none, want: ""},
		{name: "a domain", value: accesstypes.Domain("north"), want: "north"},
		{name: "a number", value: 7, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := columnText(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("columnText() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("columnText() = %q, want %q", got, tt.want)
			}
		})
	}
}
