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

// changeBay is a row keyed by number that carries its yard.
type changeBay struct {
	Number int64  `spanner:"Number"`
	YardID string `spanner:"YardId"`
	Name   string `spanner:"Name"`
}

func (changeBay) Resource() accesstypes.Resource {
	return "Bays"
}

func (changeBay) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeBay) TenantBinding() TenantBinding {
	return TenantBinding{Field: "YardID"}
}

// changeCrate is a row keyed by number that reaches its yard through a bay, by the bay's
// number, which it may leave empty.
type changeCrate struct {
	Number    int64  `spanner:"Number"`
	BayNumber *int64 `spanner:"BayNumber"`
	Name      string `spanner:"Name"`
}

func (changeCrate) Resource() accesstypes.Resource {
	return "Crates"
}

func (changeCrate) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeCrate) TenantBinding() TenantBinding {
	return TenantBinding{Field: "BayNumber", Path: []BindingHop{{Table: "Bays", JoinColumn: "Number", Column: "YardId"}}}
}

// changeLabel is a row that reaches its yard through a crate and the crate's bay, both by
// number.
type changeLabel struct {
	ID          string `spanner:"Id"`
	CrateNumber int64  `spanner:"CrateNumber"`
	Name        string `spanner:"Name"`
}

func (changeLabel) Resource() accesstypes.Resource {
	return "Labels"
}

func (changeLabel) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeLabel) TenantBinding() TenantBinding {
	return TenantBinding{Field: "CrateNumber", Path: []BindingHop{
		{Table: "Crates", JoinColumn: "Number", Column: "BayNumber"},
		{Table: "Bays", JoinColumn: "Number", Column: "YardId"},
	}}
}

// changePier is a row that carries its yard and a code no other pier has.
type changePier struct {
	ID     string `spanner:"Id"`
	Code   string `spanner:"Code"`
	YardID string `spanner:"YardId"`
	Name   string `spanner:"Name"`
}

func (changePier) Resource() accesstypes.Resource {
	return "Piers"
}

func (changePier) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changePier) TenantBinding() TenantBinding {
	return TenantBinding{Field: "YardID"}
}

// changeSlip is a row that reaches its yard through a pier it names by the pier's code,
// which is not the pier's key.
type changeSlip struct {
	ID       string `spanner:"Id"`
	PierCode string `spanner:"PierCode"`
	Name     string `spanner:"Name"`
}

func (changeSlip) Resource() accesstypes.Resource {
	return "Slips"
}

func (changeSlip) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (changeSlip) TenantBinding() TenantBinding {
	return TenantBinding{Field: "PierCode", Path: []BindingHop{{Table: "Piers", JoinColumn: "Code", Column: "YardId"}}}
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

// numberPatch builds a patch of one fixture row keyed by number.
func numberPatch[R Resourcer](patchType PatchType, number int64, fields map[accesstypes.Field]any) *PatchSet[R] {
	p := NewPatchSet(NewMetadata[R]()).SetPatchType(patchType)
	p.SetKey("Number", number)
	for field, value := range fields {
		p.Set(field, value)
	}

	return p
}

// TestChangeRecordTenant_bufferedLookup pins how a point read settles the row it names
// from what the transaction's own patches buffered, and answers from it: the newest write
// that holds the column read by with the read's value names the row, or, where no write
// holds it and a write of the table by its key holds something else, the row the
// database names under the value; the row's newest write that says anything of the
// column answers. What the database holds is laid under the writes from the record of
// the reads the transaction made, so no case asks the database: the read by the column
// answers "yard-stored", the row the database names under a pier's code is pier-1 with
// "yard-stored-row", and pier-1 read by its key answers "yard-stored-key".
func TestChangeRecordTenant_bufferedLookup(t *testing.T) {
	t.Parallel()

	type write struct {
		patchType PatchType
		columns   map[string]any
	}
	dock := pointRead{table: "Docks", keyColumn: "Id", value: "dock-1", column: "YardId"}
	pier := pointRead{table: "Piers", keyColumn: "Code", value: "code-1", column: "YardId"}
	tests := []struct {
		name   string
		read   pointRead
		writes []write
		want   pointAnswer
	}{
		{
			name:   "a create that set the column answers its value",
			read:   dock,
			writes: []write{{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-1", "Name": "first"}}},
			want:   pointAnswer{value: "yard-1", found: true},
		},
		{
			name:   "a create that left the column out answers nil",
			read:   dock,
			writes: []write{{CreatePatchType, map[string]any{"Id": "dock-1", "Name": "first"}}},
			want:   pointAnswer{value: nil, found: true},
		},
		{
			name:   "a column set to NULL answers nil",
			read:   dock,
			writes: []write{{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": nil}}},
			want:   pointAnswer{value: nil, found: true},
		},
		{
			name: "an update that did not set the column is passed over for an older create",
			read: dock,
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "dock-1", "Name": "renamed"}},
			},
			want: pointAnswer{value: "yard-1", found: true},
		},
		{
			name: "the newest write that set the column answers",
			read: dock,
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-2"}},
			},
			want: pointAnswer{value: "yard-2", found: true},
		},
		{
			name:   "an update alone that did not set the column leaves the read to the database",
			read:   dock,
			writes: []write{{UpdatePatchType, map[string]any{"Id": "dock-1", "Name": "renamed"}}},
			want:   pointAnswer{value: "yard-stored", found: true},
		},
		{
			name:   "a write of another row leaves the read to the database",
			read:   dock,
			writes: []write{{CreatePatchType, map[string]any{"Id": "dock-2", "YardId": "yard-2"}}},
			want:   pointAnswer{value: "yard-stored", found: true},
		},
		{
			name:   "a write whose key cannot be spelled leaves the read to the database",
			read:   dock,
			writes: []write{{CreatePatchType, map[string]any{"Id": []string{"dock-1"}, "YardId": "yard-2"}}},
			want:   pointAnswer{value: "yard-stored", found: true},
		},
		{
			name:   "a delete is not noted",
			read:   dock,
			writes: []write{{DeletePatchType, map[string]any{"Id": "dock-1", "YardId": "yard-2"}}},
			want:   pointAnswer{value: "yard-stored", found: true},
		},
		{
			name: "a create that holds the code names its row, and the row's move by its key answers",
			read: pier,
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "pier-1", "Code": "code-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "pier-1", "YardId": "yard-2"}},
			},
			want: pointAnswer{value: "yard-2", found: true},
		},
		{
			name: "an update by key that did not set the column is passed over for the create that named the row",
			read: pier,
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "pier-1", "Code": "code-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "pier-1", "Name": "renamed"}},
			},
			want: pointAnswer{value: "yard-1", found: true},
		},
		{
			name: "the newest write that holds the code names the row",
			read: pier,
			writes: []write{
				{CreatePatchType, map[string]any{"Id": "pier-1", "Code": "code-1", "YardId": "yard-1"}},
				{UpdatePatchType, map[string]any{"Id": "pier-1", "Code": "code-9"}},
				{CreatePatchType, map[string]any{"Id": "pier-2", "Code": "code-1", "YardId": "yard-3"}},
			},
			want: pointAnswer{value: "yard-3", found: true},
		},
		{
			name:   "a row named by a write of its code that does not say the column: the row read by its key",
			read:   pier,
			writes: []write{{UpdatePatchType, map[string]any{"Id": "pier-1", "Code": "code-1"}}},
			want:   pointAnswer{value: "yard-stored-key", found: true},
		},
		{
			name:   "a row moved by its key, named by the database under the code: the move answers",
			read:   pier,
			writes: []write{{UpdatePatchType, map[string]any{"Id": "pier-1", "YardId": "yard-2"}}},
			want:   pointAnswer{value: "yard-2", found: true},
		},
		{
			name:   "a row renamed by its key, named by the database under the code: the stored column answers",
			read:   pier,
			writes: []write{{UpdatePatchType, map[string]any{"Id": "pier-1", "Name": "renamed"}}},
			want:   pointAnswer{value: "yard-stored-row", found: true},
		},
		{
			name:   "another row written by its key: the row the database names answers as stored",
			read:   pier,
			writes: []write{{UpdatePatchType, map[string]any{"Id": "pier-2", "YardId": "yard-2"}}},
			want:   pointAnswer{value: "yard-stored-row", found: true},
		},
		{
			name: "a row given the code and then another names no row: the read by the code",
			read: pier,
			writes: []write{
				{UpdatePatchType, map[string]any{"Id": "pier-2", "Code": "code-1", "YardId": "yard-2"}},
				{UpdatePatchType, map[string]any{"Id": "pier-2", "Code": "code-9"}},
			},
			want: pointAnswer{value: "yard-stored", found: true},
		},
		{
			name: "no write of the table: the read by the code",
			read: pier,
			want: pointAnswer{value: "yard-stored", found: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := newTransactionRows()
			for _, w := range tt.writes {
				key := map[string]any{"Id": w.columns["Id"]}
				rows.note(accesstypes.Resource(tt.read.table), w.patchType, rowMatchKey(KeySet{}.Add("ID", w.columns["Id"])), key, w.columns)
			}
			rows.note("Berths", CreatePatchType, rowMatchKey(KeySet{}.Add("ID", "dock-1")), map[string]any{"Id": "dock-1"}, map[string]any{"Id": "dock-1", "Code": "code-1", "YardId": "another table's"})
			rows.remember(tt.read, pointAnswer{value: "yard-stored", found: true})
			pierKey, err := keySpelling(map[string]any{"Id": "pier-1"})
			if err != nil {
				t.Fatalf("keySpelling() error = %v", err)
			}
			rows.rowReads[pier] = storedRow{found: true, key: pierKey, value: "yard-stored-row"}
			rows.keyReads[keyedRead{table: "Piers", key: pierKey, column: "YardId"}] = pointAnswer{value: "yard-stored-key", found: true}

			value, found, err := transactionPointReader(&rowsRecordingTxn{rows: rows}, changeRecordTenantNotImplemented)(context.Background(), tt.read.table, tt.read.keyColumn, tt.read.value, tt.read.column)
			if err != nil {
				t.Fatalf("read error = %v", err)
			}
			if got := (pointAnswer{value: value, found: found}); got != tt.want {
				t.Errorf("read = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestChangeRecordTenant_ownColumn pins how a row's own column is answered from what the
// transaction's patches buffered for that row: the newest write of the row that says
// anything of the column answers, a write of another row or table says nothing, and a
// row is the same row whichever order its key was given in.
func TestChangeRecordTenant_ownColumn(t *testing.T) {
	t.Parallel()

	berth := func(id string) string {
		return rowMatchKey(KeySet{}.Add("ID", id))
	}
	type write struct {
		table     accesstypes.Resource
		patchType PatchType
		row       string
		columns   map[string]any
	}
	tests := []struct {
		name      string
		writes    []write
		row       string
		want      any
		wantKnown bool
	}{
		{
			name:      "a write of the row that set the column answers its value",
			writes:    []write{{"Berths", UpdatePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "DockId": "dock-2"}}},
			row:       berth("berth-1"),
			want:      "dock-2",
			wantKnown: true,
		},
		{
			name:   "a write of another row says nothing",
			writes: []write{{"Berths", CreatePatchType, berth("berth-2"), map[string]any{"Id": "berth-2", "DockId": "dock-2"}}},
			row:    berth("berth-1"),
		},
		{
			name:   "a write of another table says nothing",
			writes: []write{{"Docks", CreatePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "DockId": "dock-2"}}},
			row:    berth("berth-1"),
		},
		{
			name: "a compound key keyed in another order is the same row",
			writes: []write{{
				"Berths", UpdatePatchType, rowMatchKey(KeySet{}.Add("YardID", "yard-1").Add("Number", 3)),
				map[string]any{"YardId": "yard-1", "Number": 3, "DockId": "dock-2"},
			}},
			row:       rowMatchKey(KeySet{}.Add("Number", 3).Add("YardID", "yard-1")),
			want:      "dock-2",
			wantKnown: true,
		},
		{
			name: "an update that did not set the column is passed over for an older create",
			writes: []write{
				{"Berths", CreatePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "DockId": "dock-1"}},
				{"Berths", UpdatePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "Name": "renamed"}},
			},
			row:       berth("berth-1"),
			want:      "dock-1",
			wantKnown: true,
		},
		{
			name:      "a create that left the column out answers empty",
			writes:    []write{{"Berths", CreatePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "Name": "first"}}},
			row:       berth("berth-1"),
			want:      nil,
			wantKnown: true,
		},
		{
			name:   "an update alone that did not set the column says nothing",
			writes: []write{{"Berths", CreateOrUpdatePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "Name": "renamed"}}},
			row:    berth("berth-1"),
		},
		{
			name:   "a delete is not noted",
			writes: []write{{"Berths", DeletePatchType, berth("berth-1"), map[string]any{"Id": "berth-1", "DockId": "dock-9"}}},
			row:    berth("berth-1"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := newTransactionRows()
			for _, w := range tt.writes {
				rows.note(w.table, w.patchType, w.row, nil, w.columns)
			}

			got, known := rows.ownColumn("Berths", tt.row, "DockId")
			if known != tt.wantKnown || got != tt.want {
				t.Errorf("ownColumn() = %v, known %t, want %v, known %t", got, known, tt.want, tt.wantKnown)
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

// TestChangeRecordTenant_mockNumberKey pins that a mocked transaction, which holds no
// tables to ask, buffers a tracked create of a row that reaches its tenant through a key
// that is a number, with an empty tenant and no error.
func TestChangeRecordTenant_mockNumberKey(t *testing.T) {
	t.Parallel()

	bay := int64(7)
	buffered := &bufferingTxn{}
	p := numberPatch[changeCrate](CreatePatchType, 70, map[accesstypes.Field]any{"BayNumber": &bay, "Name": "Seventieth"})
	if err := p.Buffer(context.Background(), NewMockReadWriteTransaction(buffered), "test"); err != nil {
		t.Fatalf("Buffer() error = %v", err)
	}
	if len(buffered.buffered) != 2 {
		t.Fatalf("the transaction buffered %d writes, want the create and its change record", len(buffered.buffered))
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
				rows.note("Docks", UpdatePatchType, rowMatchKey(KeySet{}.Add("ID", "dock-1")), map[string]any{"Id": "dock-1"}, map[string]any{"Id": "dock-1", "YardId": "yard-buffered"})
				rows.remember(read, pointAnswer{value: "yard-read", found: true})

				return &rowsRecordingTxn{rows: rows}
			},
			want: pointAnswer{value: "yard-buffered", found: true},
		},
		{
			name: "what the transaction read before answers with nothing buffered",
			txn: func() ReadWriteTransaction {
				rows := newTransactionRows()
				rows.remember(read, pointAnswer{value: "yard-read", found: true})

				return &rowsRecordingTxn{rows: rows}
			},
			want: pointAnswer{value: "yard-read", found: true},
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

			value, found, err := transactionPointReader(tt.txn(), changeRecordTenantNotImplemented)(context.Background(), read.table, read.keyColumn, read.value, read.column)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("read error = %v, want one containing %q", err, tt.wantError)
				}

				return
			}
			if err != nil {
				t.Fatalf("read error = %v", err)
			}
			if got := (pointAnswer{value: value, found: found}); got != tt.want {
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
