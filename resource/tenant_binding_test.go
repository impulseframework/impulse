package resource

import (
	"context"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"go.uber.org/mock/gomock"
)

// The fixture of the change record tenant tests: two yards (the tenants), a dock and a
// berth in the north yard, and a mooring at that berth.
const (
	changeYardNorth = "yard-north"
	changeYardSouth = "yard-south"
)

// changeYard is the fixture's tenant record: its way to its tenant is its own key.
type changeYard struct {
	ID   string `spanner:"Id"`
	Name string `spanner:"Name"`
}

func (changeYard) Resource() accesstypes.Resource {
	return "Yards"
}

func (changeYard) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeYard) TenantBinding() TenantBinding {
	return TenantBinding{Field: "ID"}
}

// changeDock is a row that carries its yard.
type changeDock struct {
	ID     string `spanner:"Id"`
	YardID string `spanner:"YardId"`
	Name   string `spanner:"Name"`
}

func (changeDock) Resource() accesstypes.Resource {
	return "Docks"
}

func (changeDock) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeDock) TenantBinding() TenantBinding {
	return TenantBinding{Field: "YardID"}
}

// changeBerth is a row that reaches its yard through a dock it may leave empty.
type changeBerth struct {
	ID     string  `spanner:"Id"`
	DockID *string `spanner:"DockId"`
	Name   string  `spanner:"Name"`
}

func (changeBerth) Resource() accesstypes.Resource {
	return "Berths"
}

func (changeBerth) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeBerth) TenantBinding() TenantBinding {
	return TenantBinding{Field: "DockID", Path: []BindingHop{{Table: "Docks", JoinColumn: "Id", Column: "YardId"}}}
}

// changeBerthRequest is what a request to change a berth carries.
type changeBerthRequest struct {
	ID     string  `json:"id"     perm:"-"`
	DockID *string `json:"dockId"`
	Name   string  `json:"name"`
}

// changeMooring is a row that reaches its yard through its berth and the berth's dock.
type changeMooring struct {
	ID      string `spanner:"Id"`
	BerthID string `spanner:"BerthId"`
	Name    string `spanner:"Name"`
}

func (changeMooring) Resource() accesstypes.Resource {
	return "Moorings"
}

func (changeMooring) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeMooring) TenantBinding() TenantBinding {
	return TenantBinding{Field: "BerthID", Path: []BindingHop{
		{Table: "Berths", JoinColumn: "Id", Column: "DockId"},
		{Table: "Docks", JoinColumn: "Id", Column: "YardId"},
	}}
}

// changeNotice is a row of a table that belongs to no tenant.
type changeNotice struct {
	ID   string `spanner:"Id"`
	Body string `spanner:"Body"`
}

func (changeNotice) Resource() accesstypes.Resource {
	return "Notices"
}

func (changeNotice) Config() Config {
	return Config{}.SetTrackChanges(true)
}

// changePatch builds a patch of one fixture row, as a method's own code builds one.
func changePatch[R Resourcer](patchType PatchType, id string, fields map[accesstypes.Field]any) *PatchSet[R] {
	p := NewPatchSet(NewMetadata[R]()).SetPatchType(patchType)
	p.SetKey("ID", id)
	for field, value := range fields {
		p.Set(field, value)
	}

	return p
}

// TestChangeRecordTenant_bufferedLookup pins how a point read is answered from what the
// transaction's own patches buffered: the newest write of the row that says anything of
// the column answers, and a write that says nothing is passed over.
func TestChangeRecordTenant_bufferedLookup(t *testing.T) {
	t.Parallel()

	type write struct {
		patchType PatchType
		columns   map[string]any
	}
	read := pointRead{table: "Docks", keyColumn: "Id", value: "dock-1", column: "YardId"}
	tests := []struct {
		name      string
		writes    []write
		want      pointAnswer
		wantKnown bool
	}{
		{
			name:      "a create that set the column answers its value",
			writes:    []write{{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-1", "Name": "first"}}},
			want:      pointAnswer{text: "yard-1", found: true},
			wantKnown: true,
		},
		{
			name:      "a create that left the column out answers the empty string",
			writes:    []write{{CreatePatchType, map[string]any{"Id": "dock-1", "Name": "first"}}},
			want:      pointAnswer{text: "", found: true},
			wantKnown: true,
		},
		{
			name:      "a column set to NULL is the empty string",
			writes:    []write{{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": nil}}},
			want:      pointAnswer{text: "", found: true},
			wantKnown: true,
		},
		{
			name: "an update that did not set the column is passed over for an older create",
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "dock-1", "Name": "renamed"}},
			},
			want:      pointAnswer{text: "yard-1", found: true},
			wantKnown: true,
		},
		{
			name: "the newest write that set the column answers",
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-2"}},
			},
			want:      pointAnswer{text: "yard-2", found: true},
			wantKnown: true,
		},
		{
			name:   "an update alone that did not set the column says nothing",
			writes: []write{{UpdatePatchType, map[string]any{"Id": "dock-1", "Name": "renamed"}}},
		},
		{
			name:   "a write of another row says nothing",
			writes: []write{{CreatePatchType, map[string]any{"Id": "dock-2", "YardId": "yard-2"}}},
		},
		{
			name:   "a write whose key is not text says nothing",
			writes: []write{{CreatePatchType, map[string]any{"Id": 7, "YardId": "yard-2"}}},
		},
		{
			name:   "a delete is not noted",
			writes: []write{{DeletePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-2"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := newTransactionRows()
			for _, w := range tt.writes {
				rows.note("Docks", w.patchType, w.columns)
			}
			rows.note("Berths", CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "another table's"})

			got, known, err := rows.buffered(read)
			if err != nil {
				t.Fatalf("buffered() error = %v", err)
			}
			if known != tt.wantKnown || got != tt.want {
				t.Errorf("buffered() = %+v, known %t, want %+v, known %t", got, known, tt.want, tt.wantKnown)
			}
		})
	}
}

// TestChangeRecordTenant_mock pins that a mocked transaction, which holds no
// tables to ask, records an update of a row that reaches its tenant through a parent
// with an empty tenant, and reads nothing more than the update reads for its change set.
func TestChangeRecordTenant_mock(t *testing.T) {
	t.Parallel()

	dock := "dock-1"
	reader := NewMockReader[changeBerth](gomock.NewController(t))
	reader.EXPECT().Read(gomock.Any(), gomock.Any()).Return(&Row[changeBerth]{Data: changeBerth{ID: "berth-1", DockID: &dock, Name: "before"}}, nil)
	buffered := &bufferingTxn{}

	p := changePatch[changeBerth](UpdatePatchType, "berth-1", map[accesstypes.Field]any{"Name": "after"})
	if err := p.Buffer(context.Background(), NewMockReadWriteTransaction(buffered, reader), "test"); err != nil {
		t.Fatalf("Buffer() error = %v", err)
	}
	if len(buffered.buffered) != 2 {
		t.Fatalf("the transaction buffered %d writes, want the update and its change record", len(buffered.buffered))
	}
	record, ok := buffered.buffered[1].(*DataChangeEvent)
	if !ok {
		t.Fatalf("the second write is a %T, want the change record", buffered.buffered[1])
	}
	if record.TenantID != "" {
		t.Errorf("the change record's tenant = %q, want empty", record.TenantID)
	}
}

// TestChangeRecordTenant_armed pins that a write armed in a tenant's request
// takes the request's tenant, which the tenancy check has held the row to, and reads
// nothing for it: the mocked transaction has no row to read, and the berth's dock is
// not on the patch.
func TestChangeRecordTenant_armed(t *testing.T) {
	t.Parallel()

	collection, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        "Berths",
		Scope:       accesstypes.DomainPermissionScope,
		Permissions: []accesstypes.Permission{accesstypes.Update, accesstypes.Delete},
		Domain:      &DomainBindingData{Column: "DockId", Path: []BindingHop{{Table: "Docks", JoinColumn: "Id", Column: "YardId"}}},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	rSet, err := NewSet[changeBerth, changeBerthRequest](accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}

	for _, tt := range []struct {
		name      string
		patchType PatchType
		fields    map[accesstypes.Field]any
	}{
		{name: "an update", patchType: UpdatePatchType, fields: map[accesstypes.Field]any{"Name": "after"}},
		{name: "a delete", patchType: DeletePatchType},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := changePatch[changeBerth](tt.patchType, "berth-1", tt.fields)
			p.querySet.collection = collection
			p.EnableUserPermissionEnforcement(rSet, renderStubPermissions{}, accesstypes.DomainScope(changeYardNorth), accesstypes.Update)

			got, err := p.changeRecordTenant(context.Background(), NewMockReadWriteTransaction(&bufferingTxn{}), nil)
			if err != nil {
				t.Fatalf("changeRecordTenant() error = %v", err)
			}
			if got != changeYardNorth {
				t.Errorf("changeRecordTenant() = %q, want the request's tenant %q", got, changeYardNorth)
			}
		})
	}
}

// rowsRecordingTxn is a transaction that keeps the record of the columns its patches
// buffered and the point reads it made, and has no tables to ask.
type rowsRecordingTxn struct {
	bufferingTxn
	rows *transactionRows
}

func (r *rowsRecordingTxn) transactionRows() *transactionRows {
	return r.rows
}

// postgresTypedTxn is a PostgreSQL transaction with no tables to ask.
type postgresTypedTxn struct {
	bufferingTxn
}

func (*postgresTypedTxn) DBType() DBType {
	return PostgresDBType
}

// TestChangeRecordTenant_pointReader pins the order a transaction's point read is
// answered in: what its patches buffered, then what it read before, then the database,
// which a transaction with no tables to ask leaves unasked and one of another database
// refuses.
func TestChangeRecordTenant_pointReader(t *testing.T) {
	t.Parallel()

	read := pointRead{table: "Docks", keyColumn: "Id", value: "dock-1", column: "YardId"}
	tests := []struct {
		name      string
		txn       func() ReadWriteTransaction
		want      pointAnswer
		wantError string
	}{
		{
			name: "what the transaction buffered answers before what it read",
			txn: func() ReadWriteTransaction {
				rows := newTransactionRows()
				rows.note("Docks", UpdatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-buffered"})
				rows.remember(read, pointAnswer{text: "yard-read", found: true})

				return &rowsRecordingTxn{rows: rows}
			},
			want: pointAnswer{text: "yard-buffered", found: true},
		},
		{
			name: "what the transaction read before answers with nothing buffered",
			txn: func() ReadWriteTransaction {
				rows := newTransactionRows()
				rows.remember(read, pointAnswer{text: "yard-read", found: true})

				return &rowsRecordingTxn{rows: rows}
			},
			want: pointAnswer{text: "yard-read", found: true},
		},
		{
			name: "a transaction with no tables to ask answers that there is no row",
			txn: func() ReadWriteTransaction {
				return &rowsRecordingTxn{rows: newTransactionRows()}
			},
			want: pointAnswer{},
		},
		{
			name: "a transaction of another database refuses the read",
			txn: func() ReadWriteTransaction {
				return &postgresTypedTxn{}
			},
			wantError: "finding the tenant of a changed row is not implemented for postgres",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text, found, err := transactionPointReader(tt.txn(), changeRecordTenantNotImplemented)(context.Background(), read.table, read.keyColumn, read.value, read.column)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("read error = %v, want one containing %q", err, tt.wantError)
				}

				return
			}
			if err != nil {
				t.Fatalf("read error = %v", err)
			}
			if got := (pointAnswer{text: text, found: found}); got != tt.want {
				t.Errorf("read = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestChangeRecordTenant_postgres pins that a change record whose tenant needs a read is
// refused on PostgreSQL, where the reads are not written, at the first read it needs:
// the stored row, or a parent.
func TestChangeRecordTenant_postgres(t *testing.T) {
	t.Parallel()

	dock := "dock-1"
	tests := []struct {
		name  string
		patch *PatchSet[changeBerth]
	}{
		{name: "an update that leaves the dock as it is", patch: changePatch[changeBerth](UpdatePatchType, "berth-1", map[accesstypes.Field]any{"Name": "after"})},
		{name: "a create under a dock", patch: changePatch[changeBerth](CreatePatchType, "berth-1", map[accesstypes.Field]any{"DockID": &dock})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.patch.changeRecordTenant(context.Background(), &postgresTypedTxn{}, nil)
			want := "finding the tenant of a changed row is not implemented for postgres"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("changeRecordTenant() error = %v, want one containing %q", err, want)
			}
		})
	}
}
