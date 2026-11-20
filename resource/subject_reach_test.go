package resource

// These tests pin the two statements a rule about a named person asks the
// application's subject sets with (subject_reach.go): whether a value is in one
// of the person's sets, and whether one of the person's facts is read through a
// row's column. Both are built from the set's own declaration, so the text here is
// the text a condition's membership test renders, with the person bound by name
// and no tenant named.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestSubjectSetHoldsStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		set       string
		want      string
		wantNames []string
		wantErr   string
	}{
		{
			name:      "a value in a plain set, in no one tenant",
			set:       "teams",
			want:      "SELECT EXISTS (SELECT 1 FROM `Memberships` `ca1` WHERE `ca1`.`UserId` = @subject AND `ca1`.`Team` = @_c1) AS `fact`",
			wantNames: []string{"subject"},
		},
		{
			name:      "a value in a set read through a hop",
			set:       "regions",
			want:      "SELECT EXISTS (SELECT 1 FROM `Memberships` `ca1` WHERE `ca1`.`UserId` = @subject AND EXISTS (SELECT 1 FROM `Hubs` `ca2` WHERE `ca2`.`Id` = `ca1`.`HubId` AND `ca2`.`Region` = @_c1)) AS `fact`",
			wantNames: []string{"subject"},
		},
		{
			name:      "a set's where: goes with it, and reads the instant asked at",
			set:       "circles",
			want:      "SELECT EXISTS (SELECT 1 FROM `Circles` `ca1` WHERE `ca1`.`UserId` = @subject AND (`ca1`.`ValidUntil` IS NULL OR `ca1`.`ValidUntil` > @now) AND `ca1`.`Circle` = @_c1) AS `fact`",
			wantNames: []string{"now", "subject"},
		},
		{
			name:      "a set whose rows follow another set asks that set by its own definition",
			set:       "widerCircles",
			want:      "SELECT EXISTS (SELECT 1 FROM `Circles` `ca1` WHERE EXISTS (SELECT 1 FROM `Circles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`ValidUntil` IS NULL OR `ca2`.`ValidUntil` > @now) AND `ca2`.`Circle` = `ca1`.`InnerCircle`) AND `ca1`.`Circle` = @_c1) AS `fact`",
			wantNames: []string{"now", "subject"},
		},
		{name: "a set the application does not declare", set: "nope", wantErr: "subject.nope is not a declared subject set"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, registry, err := subjectSetHoldsStatement(semanticCollection(t), SpannerDBType, tt.set, "north")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("subjectSetHoldsStatement() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("subjectSetHoldsStatement() error = %v", err)
			}
			if sql != tt.want {
				t.Errorf("subjectSetHoldsStatement() =\n%s\nwant\n%s", sql, tt.want)
			}
			if names := slices.Sorted(slices.Values(registry.referencedNames())); !slices.Equal(names, tt.wantNames) {
				t.Errorf("subjectSetHoldsStatement() names %v, want %v", names, tt.wantNames)
			}
			if bound := registry.boundParams(); len(bound) != 1 || bound[0].Value != "north" {
				t.Errorf("subjectSetHoldsStatement() bound %v, want the one value asked about", bound)
			}
		})
	}
}

// reachCollection is a collection whose facts are read through other records: a
// set one hop away, a set two hops away, a set whose rows follow another set and a
// subject value, all landing on Hubs or Zones.
func reachCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	return semanticCollectionOf(t, []CollectionResource{
		{
			Name:  "Memberships",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{
				{Name: "hubs", UserColumn: "UserId", Column: "HubId", Type: AttributeTypeString},
				{Name: "regions", UserColumn: "UserId", Column: "HubId", Type: AttributeTypeString, Path: []BindingHop{{Table: "Hubs", JoinColumn: "Id", Column: "Region"}}},
				{Name: "climates", UserColumn: "UserId", Column: "HubId", Type: AttributeTypeString, Path: []BindingHop{
					{Table: "Hubs", JoinColumn: "Id", Column: "ZoneId"},
					{Table: "Zones", JoinColumn: "Id", Column: "Climate"},
				}},
			},
		},
		{
			Name:  "Loans",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{
				{
					Name: "lentRegions", UserColumn: "UserId", Column: "LentHubId", Type: AttributeTypeString,
					Who:  []SubjectWhoData{{Column: "UserId"}, {Column: "HubId", Set: "hubs"}},
					Path: []BindingHop{{Table: "Hubs", JoinColumn: "Id", Column: "Region"}},
				},
			},
		},
		{
			Name:  "Profiles",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectValues: []SubjectBindingData{
				{Name: "homeRegion", UserColumn: "UserId", Column: "HomeHubId", Type: AttributeTypeString, Path: []BindingHop{{Table: "Hubs", JoinColumn: "Id", Column: "Region"}}},
			},
		},
	})
}

func TestSubjectReadThroughStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fact    string
		table   string
		column  string
		want    string
		wantErr string
	}{
		{
			name: "a set one hop away is read through the row a row of the person's points at", fact: "regions", table: "Hubs", column: "Region",
			want: "SELECT EXISTS (SELECT 1 FROM `Hubs` `ca1` WHERE `ca1`.`Id` = @_c1 AND EXISTS (SELECT 1 FROM `Memberships` `ca2` WHERE `ca2`.`UserId` = @subject AND `ca2`.`HubId` = `ca1`.`Id`)) AS `fact`",
		},
		{
			name: "the first hop of two is read through its own column", fact: "climates", table: "Hubs", column: "ZoneId",
			want: "SELECT EXISTS (SELECT 1 FROM `Hubs` `ca1` WHERE `ca1`.`Id` = @_c1 AND EXISTS (SELECT 1 FROM `Memberships` `ca2` WHERE `ca2`.`UserId` = @subject AND `ca2`.`HubId` = `ca1`.`Id`)) AS `fact`",
		},
		{
			name: "the second hop of two is reached through the first", fact: "climates", table: "Zones", column: "Climate",
			want: "SELECT EXISTS (SELECT 1 FROM `Zones` `ca1` WHERE `ca1`.`Id` = @_c1 AND EXISTS (SELECT 1 FROM `Memberships` `ca2` WHERE `ca2`.`UserId` = @subject AND EXISTS (SELECT 1 FROM `Hubs` `ca3` WHERE `ca3`.`Id` = `ca2`.`HubId` AND `ca3`.`ZoneId` = `ca1`.`Id`))) AS `fact`",
		},
		{
			name: "a set whose rows follow another set counts those rows as the person's", fact: "lentRegions", table: "Hubs", column: "Region",
			want: "SELECT EXISTS (SELECT 1 FROM `Hubs` `ca1` WHERE `ca1`.`Id` = @_c1 AND EXISTS (SELECT 1 FROM `Loans` `ca2` WHERE (`ca2`.`UserId` = @subject OR EXISTS (SELECT 1 FROM `Memberships` `ca3` WHERE `ca3`.`UserId` = @subject AND `ca3`.`HubId` = `ca2`.`HubId`)) AND `ca2`.`LentHubId` = `ca1`.`Id`)) AS `fact`",
		},
		{
			name: "a subject value is read through its hop as a set is", fact: "homeRegion", table: "Hubs", column: "Region",
			want: "SELECT EXISTS (SELECT 1 FROM `Hubs` `ca1` WHERE `ca1`.`Id` = @_c1 AND EXISTS (SELECT 1 FROM `Profiles` `ca2` WHERE `ca2`.`UserId` = @subject AND `ca2`.`HomeHubId` = `ca1`.`Id`)) AS `fact`",
		},
		{
			name: "a fact that does not read the column", fact: "regions", table: "Hubs", column: "Name",
			wantErr: "subject.regions is not read through Hubs.Name: no hop of its value's path lands on that column",
		},
		{
			name: "a fact with no path is read through no record", fact: "hubs", table: "Hubs", column: "Region",
			wantErr: "subject.hubs is not read through Hubs.Region: no hop of its value's path lands on that column",
		},
		{
			name: "a fact the application does not declare", fact: "nope", table: "Hubs", column: "Region",
			wantErr: "subject.nope is not a declared subject set or subject value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, registry, err := subjectReadThroughStatement(reachCollection(t), SpannerDBType, tt.fact, tt.table, tt.column, []reachKeyColumn{{column: "Id", value: "row-1"}})
			if tt.wantErr != "" {
				if err == nil || !strings.HasSuffix(err.Error(), "error="+tt.wantErr) {
					t.Fatalf("subjectReadThroughStatement() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("subjectReadThroughStatement() error = %v", err)
			}
			if sql != tt.want {
				t.Errorf("subjectReadThroughStatement() =\n%s\nwant\n%s", sql, tt.want)
			}
			if names := registry.referencedNames(); !slices.Equal(names, []string{"subject"}) {
				t.Errorf("subjectReadThroughStatement() names %v, want the person alone", names)
			}
			if bound := registry.boundParams(); len(bound) != 1 || bound[0].Value != "row-1" {
				t.Errorf("subjectReadThroughStatement() bound %v, want the row's key alone", bound)
			}
		})
	}
}

// TestPatchSet_subjectReach_refusesWhatItCannotAsk pins that neither question is
// ever answered "no" by a patch that cannot ask it: a patch with no collection is
// an error, and so are a PostgreSQL transaction that holds no connection to run the
// statement on and a database the library knows no dialect for.
func TestPatchSet_subjectReach_refusesWhatItCannotAsk(t *testing.T) {
	t.Parallel()

	bare := NewPatchSet(NewMetadata[toStructTestResource]())
	if _, err := bare.SubjectSetHolds(context.Background(), reachTxn{dbType: SpannerDBType}, "teams", "user-1", "north"); err == nil || !strings.Contains(err.Error(), "carries no collection") {
		t.Errorf("SubjectSetHolds() with no collection error = %v, want the refusal that names the missing collection", err)
	}
	if _, err := bare.SubjectReadThrough(context.Background(), reachTxn{dbType: SpannerDBType}, "regions", "Region", "user-1", KeySet{}.Add("ID", "row-1")); err == nil || !strings.Contains(err.Error(), "carries no collection") {
		t.Errorf("SubjectReadThrough() with no collection error = %v, want the refusal that names the missing collection", err)
	}

	known := NewPatchSet(NewMetadata[toStructTestResource]())
	known.querySet.collection = semanticCollection(t)
	if _, err := known.SubjectSetHolds(context.Background(), newPostgresPostgresReadWriteTransaction(nil), "teams", "user-1", "north"); err == nil || !strings.Contains(err.Error(), "holds no connection a statement can be run on") {
		t.Errorf("SubjectSetHolds() on a PostgreSQL transaction with no connection error = %v, want the refusal that says so", err)
	}
	if _, err := known.SubjectSetHolds(context.Background(), reachTxn{dbType: "ledger"}, "teams", "user-1", "north"); err == nil || !strings.Contains(err.Error(), "unsupported dbType: ledger") {
		t.Errorf("SubjectSetHolds() on a database the library does not know error = %v, want the refusal that names the database", err)
	}
}

// reachTxn is a transaction that answers its database type and nothing else: the
// refusals above are made before any statement runs.
type reachTxn struct {
	ReadWriteTransaction
	dbType DBType
}

func (r reachTxn) DBType() DBType {
	return r.dbType
}
