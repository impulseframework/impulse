package resource

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/securehash"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -destination mock_resource_iface_test.go -package=resource . Reader

// SortTestResource is used for testing sorting functionality.
type SortTestResource struct {
	ID   string  `spanner:"Id"   postgres:"Id"`
	Name string  `spanner:"Name" postgres:"Name"`
	Date string  `spanner:"Date" postgres:"Date"`
	Note *string `spanner:"Note" postgres:"Note"`
}

func (SortTestResource) Resource() accesstypes.Resource {
	return "SortTestResources"
}

func (s SortTestResource) DefaultConfig() Config {
	return Config{}
}

func TestQuerySet_Stmt_OrderBy_Limit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		sortFields           []SortField
		limit                *uint64
		wantQueryContains    string
		wantErrorMsgContains string
		wantErr              bool
		assertFunc           func(t *testing.T, sql string, _ string)
	}{
		{
			name:              "with limit",
			limit:             new(uint64(10)),
			wantQueryContains: "LIMIT 10",
		},
		{
			name:       "no sort fields or limit",
			sortFields: []SortField{},
			assertFunc: func(t *testing.T, sql string, _ string) {
				if strings.Contains(sql, "ORDER BY") {
					t.Errorf("Expected SQL NOT to contain 'ORDER BY', but got: %s", sql)
				}
				if strings.Contains(sql, "LIMIT") {
					t.Errorf("Expected SQL NOT to contain 'LIMIT', but got: %s", sql)
				}
			},
		},
		{
			name:              "single field ascending",
			sortFields:        []SortField{{Field: "Name", Direction: SortAscending}},
			wantQueryContains: "ORDER BY `Name` ASC",
		},
		{
			name:              "single field descending",
			sortFields:        []SortField{{Field: "Date", Direction: SortDescending}},
			wantQueryContains: "ORDER BY `Date` DESC",
		},
		{
			name:              "multiple fields mixed directions",
			sortFields:        []SortField{{Field: "Name", Direction: SortAscending}, {Field: "Date", Direction: SortDescending}},
			wantQueryContains: "ORDER BY `Name` ASC, `Date` DESC",
		},
		{
			name:              "sorting by ID field",
			sortFields:        []SortField{{Field: "ID", Direction: SortDescending}},
			wantQueryContains: "ORDER BY `Id` DESC",
		},
		{
			name:                 "invalid sort field",
			sortFields:           []SortField{{Field: "InvalidField", Direction: SortAscending}},
			wantErr:              true,                             // Expect error from buildOrderByClause
			wantErrorMsgContains: "not found in resource metadata", // buildOrderByClause error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rMeta := NewMetadata[SortTestResource]()

			qSet := NewQuerySet(rMeta)
			qSet.AddField("ID")

			qSet.SetSortFields(tt.sortFields)
			if tt.limit != nil {
				qSet.SetLimit(tt.limit)
			}
			stmt, err := qSet.stmt(SpannerDBType)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected an error, but got none")
				} else if tt.wantErrorMsgContains != "" && !strings.Contains(err.Error(), tt.wantErrorMsgContains) {
					t.Errorf("SpannerStmt() error = %v, want error message containing %q", err, tt.wantErrorMsgContains)
				}

				return // Test finished if error was expected
			}
			if err != nil {
				t.Fatalf("SpannerStmt() error = %v, wantErr %v", err, tt.wantErr)
			}

			sql := stmt.SQL // Access as a field, not a function
			if tt.assertFunc != nil {
				tt.assertFunc(t, sql, tt.wantQueryContains)
			} else if !strings.Contains(sql, tt.wantQueryContains) {
				t.Errorf("SpannerStmt() SQL = \n%s\nWant to contain:\n%s", sql, tt.wantQueryContains)
			}
		})
	}
}

func TestQuerySet_BatchList(t *testing.T) {
	t.Parallel()

	sourceResources := []*SortTestResource{
		{ID: "1", Name: "Resource 1"},
		{ID: "2", Name: "Resource 2"},
		{ID: "3", Name: "Resource 3"},
		{ID: "4", Name: "Resource 4"},
		{ID: "5", Name: "Resource 5"},
		{ID: "6", Name: "Resource 6"},
		{ID: "7", Name: "Resource 7"},
		{ID: "8", Name: "Resource 8"},
	}

	tests := []struct {
		name    string
		prepare func(ctrl *gomock.Controller) Client
		qSet    *QuerySet[SortTestResource]

		batchSize             int
		expectError           bool
		expectedErrorContains string
	}{
		{
			name: "batch size (3) not evenly divisable with without loss",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)
				sortTestResource.EXPECT().DBType().MinTimes(1).Return(SpannerDBType)
				sortTestResource.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					MockIterSeq2(nil, sourceResources...),
				)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)
				qSet.AddField("ID")
				qSet.AddField("Name")

				return qSet
			}(),
			batchSize: 3,
		},
		{
			name: "batch size (1) evenly divisable without loss",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)
				sortTestResource.EXPECT().DBType().MinTimes(1).Return(SpannerDBType)
				sortTestResource.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					MockIterSeq2(nil, sourceResources...),
				)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)
				qSet.AddField("ID")
				qSet.AddField("Name")

				return qSet
			}(),
			batchSize: 1,
		},
		{
			name: "batch size (2) evenly divisable without loss",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)
				sortTestResource.EXPECT().DBType().MinTimes(1).Return(SpannerDBType)
				sortTestResource.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					MockIterSeq2(nil, sourceResources...),
				)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)
				qSet.AddField("ID")
				qSet.AddField("Name")

				return qSet
			}(),
			batchSize: 1,
		},
		{
			name: "batch size (4) evenly divisable without loss",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)
				sortTestResource.EXPECT().DBType().MinTimes(1).Return(SpannerDBType)
				sortTestResource.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					MockIterSeq2(nil, sourceResources...),
				)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)
				qSet.AddField("ID")
				qSet.AddField("Name")

				return qSet
			}(),
			batchSize: 1,
		},
		{
			name: "batch size (8) evenly divisable without loss",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)
				sortTestResource.EXPECT().DBType().MinTimes(1).Return(SpannerDBType)
				sortTestResource.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					MockIterSeq2(nil, sourceResources...),
				)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)
				qSet.AddField("ID")
				qSet.AddField("Name")

				return qSet
			}(),
			batchSize: 1,
		},
		{
			name: "batch size (10) larger then data without loss",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)
				sortTestResource.EXPECT().DBType().MinTimes(1).Return(SpannerDBType)
				sortTestResource.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					MockIterSeq2(nil, sourceResources...),
				)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)
				qSet.AddField("ID")
				qSet.AddField("Name")

				return qSet
			}(),
			batchSize: 1,
		},
		{
			name: "returns an error for invalid batch size",
			prepare: func(ctrl *gomock.Controller) Client {
				sortTestResource := NewMockReader[SortTestResource](ctrl)

				return NewMockClient(nil, []any{sortTestResource}, nil)
			},
			qSet: func() *QuerySet[SortTestResource] {
				rMeta := NewMetadata[SortTestResource]()
				qSet := NewQuerySet(rMeta)

				return qSet
			}(),
			batchSize:             0,
			expectError:           true,
			expectedErrorContains: "invalid batch size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			txn := tt.prepare(ctrl)

			var collectedResources []*SortTestResource
			for batch := range tt.qSet.BatchList(t.Context(), txn, tt.batchSize) {
				for row, err := range batch {
					if tt.expectError {
						if err == nil {
							t.Fatal("Expected an error but got nil")
						}
						if !strings.Contains(err.Error(), tt.expectedErrorContains) {
							t.Errorf("Expected error message to contain '%s', but got: %v", tt.expectedErrorContains, err)
						}

						return // Stop processing after finding the expected error
					}
					if err != nil {
						t.Fatalf("Unexpected error while iterating a batch: %v", err)
					}
					collectedResources = append(collectedResources, &row.Data)
				}
			}

			if !tt.expectError {
				if len(collectedResources) != len(sourceResources) {
					t.Fatalf("Expected %d resources, but got %d", len(sourceResources), len(collectedResources))
				}
				for i, res := range collectedResources {
					if *res != *sourceResources[i] {
						t.Errorf("Resource at index %d does not match. Got %+v, want %+v", i, *res, *sourceResources[i])
					}
				}
			}
		})
	}
}

func TestQuerySetCompare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        QuerySetComparer
		b        QuerySetComparer
		wantDiff bool
	}{
		{
			name: "equal query sets",
			a: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			b: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			wantDiff: false,
		},
		{
			name: "equal query sets with different field order",
			a: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.AddField("field2")
				qs.SetKey("id", 1)

				return qs
			}(),
			b: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field2")
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			wantDiff: false,
		},
		{
			name: "equal patch sets with different primary key order",
			a: func() QuerySetComparer {
				ps := NewQuerySet(NewMetadata[resourcer]())
				ps.SetKey("id", 1)
				ps.SetKey("name", "test")

				return ps
			}(),
			b: func() QuerySetComparer {
				ps := NewQuerySet(NewMetadata[resourcer]())
				ps.SetKey("name", "test")
				ps.SetKey("id", 1)

				return ps
			}(),
			wantDiff: false,
		},
		{
			name: "different fields",
			a: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			b: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field2")
				qs.SetKey("id", 1)

				return qs
			}(),
			wantDiff: true,
		},
		{
			name: "different key set",
			a: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			b: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.SetKey("id", 2)

				return qs
			}(),
			wantDiff: true,
		},
		{
			name: "different resource",
			a: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[resourcer]())
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			b: func() QuerySetComparer {
				qs := NewQuerySet(NewMetadata[nilResource]())
				qs.AddField("field1")
				qs.SetKey("id", 1)

				return qs
			}(),
			wantDiff: true,
		},
		{
			name: "resource with securehash.Hash key",
			a: func() QuerySetComparer {
				ps := NewQuerySet(NewMetadata[nilResource]())
				ps.SetKey("field1", func() securehash.Hash {
					h := securehash.Hash{}
					_ = h.UnmarshalText([]byte("1$12288$3$1$oGwawstCMOWozw2vbJgyyQ==.TwIukshFIMhe8brmzjO21FBjB/OeMiHHEEVVVRliDIc="))

					return h
				}())

				return ps
			}(),
			b: func() QuerySetComparer {
				ps := NewQuerySet(NewMetadata[nilResource]())
				ps.SetKey("field1", func() securehash.Hash {
					h := securehash.Hash{}
					_ = h.UnmarshalText([]byte("1$12288$3$1$oGwawstCMOWozw2vbJgyyQ==.TwIukshFIMhe8brmzjO21FBjB/OeMiHHEEVVVRliDIc="))

					return h
				}())

				return ps
			}(),
			wantDiff: false,
		},
		{
			name: "resource with different securehash.Hash key",
			a: func() QuerySetComparer {
				ps := NewQuerySet(NewMetadata[nilResource]())
				ps.SetKey("field1", func() *securehash.Hash {
					h := new(securehash.Hash)
					_ = h.UnmarshalText([]byte("1$12288$3$1$oGwawstCMOWozw2vbJgyyQ==.TwIukshFIMhe8brmzjO21FBjB/OeMiHHEEVVVRliDIc="))

					return h
				}())

				return ps
			}(),
			b: func() QuerySetComparer {
				ps := NewQuerySet(NewMetadata[nilResource]())
				ps.SetKey("field1", func() *securehash.Hash {
					h := new(securehash.Hash)
					_ = h.UnmarshalText([]byte("1$12288$3$1$nlP2592Ld9cIt2wwhyW7xw==.Cfghih+wTsPz00Fp4PraYRorRJim1RjxFXykoxtJjBM="))

					return h
				}())

				return ps
			}(),
			wantDiff: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := QuerySetDiff(cmp.Comparer(hashCompare))(tt.a, tt.b); (diff != "") != tt.wantDiff {
				t.Errorf("QuerySetCompare() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestQuerySet_Scope pins that the QuerySet hands application query logic the scope
// its permission check ran in, so a computed resource partitions on the checked value.
func TestQuerySet_Scope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope accesstypes.Scope
	}{
		{name: "domain scope", scope: accesstypes.DomainScope("anvil")},
		{name: "global scope", scope: accesstypes.GlobalScope()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rSet, err := NewSet[SortTestResource, struct{}](accesstypes.List)
			if err != nil {
				t.Fatalf("NewSet() error = %v", err)
			}
			qSet := NewQuerySet(NewMetadata[SortTestResource]()).EnableUserPermissionEnforcement(rSet, nil, tt.scope, accesstypes.List)

			if got := qSet.Scope(); got != tt.scope {
				t.Errorf("Scope() = %v, want %v", got, tt.scope)
			}
		})
	}
}

// TestQuerySet_Caller pins the caller a bound query answers: who the permission check
// ran as, in the scope and the environment it ran in, so a computed resource's List
// function can ask what that caller may read of another resource; a query no check was
// bound to answers none, which reads as the zero caller.
func TestQuerySet_Caller(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[SortTestResource, struct{}](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}

	unbound := NewQuerySet(NewMetadata[SortTestResource]())
	if caller := unbound.Caller(); caller != nil || !caller.IsZero() {
		t.Errorf("Caller() of a query no check was bound to = %+v, want nil, which reads as the zero caller", caller)
	}

	permissions := &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {"Sectors.name"}}}
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	scope := accesstypes.DomainScope("anvil")
	bound := NewQuerySet(NewMetadata[SortTestResource]())
	bound.arm(&Caller{Permissions: permissions, Scope: scope, Env: accesstypes.EnvironmentAt(at)}, rSet, accesstypes.List)

	caller := bound.Caller()
	if caller.IsZero() {
		t.Fatal("Caller() of a bound query is the zero caller")
	}
	if caller.Permissions != UserPermissions(permissions) || caller.Scope != scope {
		t.Errorf("Caller() = %+v, want the checker and the scope the query was bound to", caller)
	}
	if now, ok := caller.Env.Now(); !ok || !now.Equal(at) {
		t.Errorf("Caller().Env.Now() = %v, %v; want the environment the query was bound in (%v)", now, ok, at)
	}

	// What the caller may read of another resource is asked in that scope and that
	// environment.
	decisions, err := caller.Check(t.Context(), accesstypes.List, "Sectors.name", "Sectors.region")
	if err != nil {
		t.Fatalf("Caller().Check() error = %v", err)
	}
	if !decisions["Sectors.name"].IsGranted() || decisions["Sectors.region"].IsGranted() {
		t.Errorf("Caller().Check() = %v, want Sectors.name granted and Sectors.region not", decisions)
	}
	if len(permissions.gotScopes) != 1 || permissions.gotScopes[0] != scope {
		t.Errorf("the check ran in %v, want %v", permissions.gotScopes, scope)
	}
	if now, ok := permissions.gotEnvs[0].Now(); !ok || !now.Equal(at) {
		t.Errorf("the check ran at %v, %v; want %v", now, ok, at)
	}
}
