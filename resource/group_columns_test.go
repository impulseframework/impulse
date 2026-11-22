package resource

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/protobuf/types/known/structpb"
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
	// groupUnread is a group the in-memory reader fails to read.
	groupUnread = "aaaaaaaa-0000-4000-8000-00000000000f"

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

// groupPennant is a row that names a group and reaches its yard through the share its
// own key names.
type groupPennant struct {
	ShareID string  `spanner:"ShareId"`
	GroupID *string `spanner:"GroupId"`
}

func (groupPennant) Resource() accesstypes.Resource {
	return "GroupPennants"
}

func (groupPennant) GroupColumns() []GroupColumn {
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

	stmt := pointTextStatement("CastGroups", "Id", "DomainId", groupOfNorth)
	if want := `SELECT CAST("DomainId" AS text) FROM "CastGroups" WHERE "Id" = @value`; stmt.SQL != want {
		t.Errorf("SQL = %s, want %s", stmt.SQL, want)
	}
	if got := stmt.Params["value"]; got != groupOfNorth {
		t.Errorf("the value bound = %v, want %s", got, groupOfNorth)
	}
}

// TestGroupColumn_check pins the rule's decision over a reader that holds the fixture
// in memory: the group's tenant against the row's, read on the row or through its
// parent, and each value handed to the reader as it is held, so a parent keyed by a
// number is read by the number.
func TestGroupColumn_check(t *testing.T) {
	t.Parallel()

	// A read is found by its table, column, key column and the value with its Go type.
	readKey := func(table, keyColumn string, value any, column string) string {
		return fmt.Sprintf("%s.%s where %s = %T %v", table, column, keyColumn, value, value)
	}
	held := map[string]any{}
	hold := func(table, keyColumn string, value any, column string, answer any) {
		held[readKey(table, keyColumn, value, column)] = answer
	}
	hold("CastGroups", "Id", groupOfNorth, "DomainId", groupYardNorth)
	hold("CastGroups", "Id", groupOfSouth, "DomainId", groupYardSouth)
	hold("CastGroups", "Id", groupOfEveryone, "DomainId", "")
	hold("GroupShares", "Id", "north-share", "Yard", groupYardNorth)
	hold("NumberedShares", "Number", int64(7), "Yard", groupYardNorth)
	errRead := errors.New("the database did not answer")
	read := func(_ context.Context, table, keyColumn string, value any, column string) (any, bool, error) {
		if value == groupUnread {
			return nil, false, errRead
		}
		answer, found := held[readKey(table, keyColumn, value, column)]

		return answer, found, nil
	}

	onRow := groupShare{}.GroupColumns()[0]
	cases := groupShareCases()
	cases = append(cases, groupCase{name: "a row with no yard names no group", anchor: "", group: groupOfEveryone, refused: true})
	for _, tt := range cases {
		t.Run("yard on the row: "+tt.name, func(t *testing.T) {
			t.Parallel()

			tt.judge(t, onRow.check(context.Background(), read, tt.anchor, tt.group))
		})
	}

	t.Run("yard on the row: a read that fails is returned and refuses nothing", func(t *testing.T) {
		t.Parallel()

		err := onRow.check(context.Background(), read, groupYardNorth, groupUnread)
		if !errors.Is(err, errRead) {
			t.Fatalf("error = %v, want the read's own error", err)
		}
		if httpio.HasBadRequest(err) {
			t.Errorf("error = %v, want no bad request: a read that failed says nothing about the group", err)
		}
	})

	throughParent := groupSeal{}.GroupColumns()[0]
	for _, tt := range []groupCase{
		{name: "a group kept in the parent's yard is written", anchor: "north-share", group: groupOfNorth},
		{name: "a group kept in another yard is refused", anchor: "north-share", group: groupOfSouth, refused: true},
		{name: "a group kept for the whole application is refused", anchor: "north-share", group: groupOfEveryone, refused: true},
		{name: "a parent that is not there is left to the foreign key", anchor: "no-share", group: groupOfSouth},
		{name: "a row with no parent names no group", anchor: "", group: groupOfNorth, refused: true},
	} {
		t.Run("yard through a parent: "+tt.name, func(t *testing.T) {
			t.Parallel()

			tt.judge(t, throughParent.check(context.Background(), read, tt.anchor, tt.group))
		})
	}

	// The same way through a parent keyed by a number: the row's anchor is an int64.
	throughNumber := throughParent
	throughNumber.Tenant = "ShareNumber"
	throughNumber.Path = []BindingHop{{Table: "NumberedShares", JoinColumn: "Number", Column: "Yard"}}
	for _, tt := range []groupCase{
		{name: "a group kept in the parent's yard is written", group: groupOfNorth},
		{name: "a group kept in another yard is refused", group: groupOfSouth, refused: true},
	} {
		t.Run("yard through a parent keyed by a number: "+tt.name, func(t *testing.T) {
			t.Parallel()

			tt.judge(t, throughNumber.check(context.Background(), read, int64(7), tt.group))
		})
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
	number := int64(7)
	var noNumber *int64
	returned := func(code sppb.TypeCode, value *structpb.Value) spanner.GenericColumnValue {
		return spanner.GenericColumnValue{Type: &sppb.Type{Code: code}, Value: value}
	}
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
		{name: "an int64", value: int64(7), want: "7"},
		{name: "a pointer to an int64", value: &number, want: "7"},
		{name: "a nil pointer to an int64", value: noNumber, want: ""},
		{name: "an int", value: 7, want: "7"},
		{name: "a uint32", value: uint32(7), want: "7"},
		{name: "a NullInt64 with a value", value: spanner.NullInt64{Int64: 7, Valid: true}, want: "7"},
		{name: "a NullInt64 with none", value: spanner.NullInt64{}, want: ""},
		{name: "a NullString with a value", value: spanner.NullString{StringVal: "north", Valid: true}, want: "north"},
		{name: "a NullString with none", value: spanner.NullString{}, want: ""},
		{name: "a STRING Spanner returned", value: returned(sppb.TypeCode_STRING, structpb.NewStringValue("north")), want: "north"},
		{name: "an INT64 Spanner returned", value: returned(sppb.TypeCode_INT64, structpb.NewStringValue("7")), want: "7"},
		{name: "a BOOL Spanner returned", value: returned(sppb.TypeCode_BOOL, structpb.NewBoolValue(true)), want: "true"},
		{name: "a NULL Spanner returned", value: returned(sppb.TypeCode_STRING, structpb.NewNullValue()), want: ""},
		{name: "a float, as the Spanner client encodes it", value: float64(1.5), want: "1.5"},
		{name: "bytes, as the Spanner client encodes them", value: []byte{1, 2}, want: "AQI="},
		{name: "a boolean, as the Spanner client encodes it", value: true, want: "true"},
		{name: "a list", value: []string{"north", "south"}, wantErr: true},
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
