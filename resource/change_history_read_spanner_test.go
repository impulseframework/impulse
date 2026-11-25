package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// forkRowRead is the read route's request type of forkRow, a resource that keeps no
// change records.
type forkRowRead struct {
	ID        string `json:"id"        perm:"-"`
	Title     string `json:"title"`
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
}

// trackedForkRowGeneratedRead is trackedForkRow's read type as a generated one is
// written: the key asks no grant (perm:"-"), so only the title's grant decides which rows
// the read answers.
type trackedForkRowGeneratedRead struct {
	ID    string `json:"id"    perm:"-"`
	Title string `json:"title"`
}

// snapshotClient wraps a client: it counts the read-only transactions it is asked for, and
// fails the test when anything reads through the client itself rather than through one of
// them.
type snapshotClient struct {
	Client
	t            *testing.T
	transactions int
}

func (c *snapshotClient) ReadOnlyTransaction() ReadOnlyTransactionCloser {
	c.transactions++

	return c.Client.ReadOnlyTransaction()
}

func (c *snapshotClient) SpannerReadOnlyTransaction() spxapi.Querier {
	c.t.Error("something read through the client itself, not through a read-only transaction")

	return c.Client.SpannerReadOnlyTransaction()
}

// historyWriter writes trackedForkRow rows through the patch set, each write one change
// record, the way a generated handler writes them.
type historyWriter struct {
	t      *testing.T
	client *SpannerClient
}

// patch is a patch of one trackedForkRow row that sets its title; a create sets its key
// as a field too, so its change record names it.
func (w historyWriter) patch(patchType PatchType, id, title string) *PatchSet[trackedForkRow] {
	p := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(patchType)
	p.SetKey("ID", id)
	if patchType == CreatePatchType {
		p.Set("ID", id)
	}
	if patchType != DeletePatchType {
		p.Set("Title", title)
	}

	return p
}

// write applies one patch, in a transaction of its own.
func (w historyWriter) write(patchType PatchType, id, title string) {
	w.t.Helper()

	if err := w.patch(patchType, id, title).Apply(w.t.Context(), w.client, ProcessEvent("test")); err != nil {
		w.t.Fatalf("Apply(%s %s %q) error = %v", patchType, id, title, err)
	}
}

// writeTogether applies two updates of one row in one transaction: two change records
// of one commit, one time and two sequences.
func (w historyWriter) writeTogether(id, first, second string) {
	w.t.Helper()

	if err := w.client.ExecuteFunc(w.t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		if err := w.patch(UpdatePatchType, id, first).Buffer(ctx, txn, ProcessEvent("test")); err != nil {
			return err
		}

		return w.patch(UpdatePatchType, id, second).Buffer(ctx, txn, ProcessEvent("test"))
	}); err != nil {
		w.t.Fatalf("ExecuteFunc() of two updates of %s error = %v", id, err)
	}
}

// storedChangeRecord is a change record of one row as the table holds it, read past the
// library.
type storedChangeRecord struct {
	at        time.Time
	sequence  int64
	changeSet map[string]json.RawMessage
}

// storedChangeRecords reads every change record of one TrackedForkRows row, newest
// first.
func storedChangeRecords(t *testing.T, db *spanner.Client, id string) []storedChangeRecord {
	t.Helper()

	stmt := spanner.NewStatement(`SELECT EventTime, Sequence, TO_JSON_STRING(ChangeSet) FROM DataChangeEvents
		WHERE TableName = 'TrackedForkRows' AND RowId = @id ORDER BY EventTime DESC, Sequence DESC`)
	stmt.Params["id"] = id
	var records []storedChangeRecord
	if err := db.Single().Query(t.Context(), stmt).Do(func(row *spanner.Row) error {
		var record storedChangeRecord
		var changeSet string
		if err := row.Columns(&record.at, &record.sequence, &changeSet); err != nil {
			return errors.Wrap(err, "row.Columns()")
		}
		if err := json.Unmarshal([]byte(changeSet), &record.changeSet); err != nil {
			return errors.Wrap(err, "json.Unmarshal()")
		}
		records = append(records, record)

		return nil
	}); err != nil {
		t.Fatalf("reading the change records of %s: %v", id, err)
	}

	return records
}

// keyedHistory is the history of one trackedForkRow row as a generated handler names it,
// read with no grant: the tests of the statement give the fields the reader may see.
func keyedHistory(id string) *ChangeHistoryQuery[trackedForkRow] {
	q := NewQuerySet(NewMetadata[trackedForkRow]())
	q.SetKey("ID", id)

	return &ChangeHistoryQuery[trackedForkRow]{querySet: q}
}

// readWholeHistory reads every page of a history by next, and the size of each page.
func readWholeHistory(t *testing.T, client Client, history *ChangeHistoryQuery[trackedForkRow], visible []accesstypes.Field) (records []ChangeRecord, sizes []int) {
	t.Helper()

	for {
		page, err := history.readPage(t.Context(), client, visible, nil)
		if err != nil {
			t.Fatalf("readPage() error = %v", err)
		}
		records = append(records, page.Changes...)
		sizes = append(sizes, len(page.Changes))
		if page.Next == "" {
			return records, sizes
		}
		after, err := parseChangeHistoryToken(page.Next)
		if err != nil {
			t.Fatalf("parseChangeHistoryToken(%q) error = %v", page.Next, err)
		}
		history.after = &after
	}
}

// TestChangeHistoryRead pins the read of one record's change records over the emulator:
// a page at a time by next, newest first, with no record twice and none missed, two
// records of one commit on either side of a page's end; the records of one field; never
// another row's; the records of one tenant; and a record that is not there.
func TestChangeHistoryRead(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "change-history-read")
	client := NewSpannerClient(db)
	w := historyWriter{t: t, client: client}
	visible := []accesstypes.Field{"ID", "Title"}

	// 120 records of one row: written oldest first, the 70th and 71st in one commit, so
	// that newest first they are the 50th and the 51st, the last of the first page and
	// the first of the second.
	w.write(CreatePatchType, "paged", "title 0")
	for i := 1; i <= 68; i++ {
		w.write(UpdatePatchType, "paged", fmt.Sprintf("title %d", i))
	}
	w.writeTogether("paged", "title 69", "title 70")
	for i := 71; i <= 119; i++ {
		w.write(UpdatePatchType, "paged", fmt.Sprintf("title %d", i))
	}
	// Another row's records, written among them.
	w.write(CreatePatchType, "neighbor", "neighbor 0")
	w.write(UpdatePatchType, "neighbor", "neighbor 1")

	t.Run("a page at a time, newest first, each record once", func(t *testing.T) {
		t.Parallel()

		records, sizes := readWholeHistory(t, client, keyedHistory("paged"), visible)
		if diff := cmp.Diff([]int{50, 50, 20}, sizes); diff != "" {
			t.Errorf("page sizes mismatch (-want +got):\n%s", diff)
		}
		stored := storedChangeRecords(t, db, "paged")
		if len(stored) != 120 {
			t.Fatalf("the row has %d change records, want 120", len(stored))
		}
		if len(records) != len(stored) {
			t.Fatalf("the pages hold %d records, want the %d the row has", len(records), len(stored))
		}
		for i, record := range records {
			if !record.At.Equal(stored[i].at) || record.Sequence != stored[i].sequence {
				t.Errorf("record %d is %s/%d, want %s/%d", i, record.At, record.Sequence, stored[i].at, stored[i].sequence)
			}
			if want := fmt.Sprintf("%q", fmt.Sprintf("title %d", 119-i)); len(record.Fields) == 0 || string(record.Fields[len(record.Fields)-1].New) != want {
				t.Errorf("record %d holds %v, want the title set to %s", i, record.Fields, want)
			}
		}
		last, first := records[49], records[50]
		if !last.At.Equal(first.At) || last.Sequence != first.Sequence+1 {
			t.Errorf("the first page ends at %s/%d and the second starts at %s/%d, want one commit's two records, the later first", last.At, last.Sequence, first.At, first.Sequence)
		}
	})

	t.Run("the records of one field", func(t *testing.T) {
		t.Parallel()

		w.write(CreatePatchType, "filtered", "first")
		w.write(UpdatePatchType, "filtered", "second")
		w.write(UpdatePatchType, "filtered", "third")
		stored := storedChangeRecords(t, db, "filtered")
		for _, field := range []accesstypes.Field{"ID", "Title"} {
			var want []int64
			for _, record := range stored {
				if _, named := record.changeSet[string(field)]; named {
					want = append(want, record.at.UnixMicro())
				}
			}
			history := keyedHistory("filtered")
			history.field = field
			records, _ := readWholeHistory(t, client, history, visible)
			var got []int64
			for _, record := range records {
				got = append(got, record.At.UnixMicro())
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("the records of %s mismatch (-want +got):\n%s", field, diff)
			}
		}
		history := keyedHistory("filtered")
		history.field = "ID"
		if records, _ := readWholeHistory(t, client, history, visible); len(records) != 1 || len(stored) != 3 {
			t.Errorf("the records of the key: %d of %d, want the create's alone of 3", len(records), len(stored))
		}
	})

	t.Run("one tenant's records", func(t *testing.T) {
		t.Parallel()

		// Two records of one row under two tenants, as a row moved between them leaves.
		for _, tenant := range []string{"north", "south"} {
			if _, err := db.Apply(t.Context(), []*spanner.Mutation{spanner.Insert("DataChangeEvents",
				[]string{"TableName", "RowId", "Sequence", "EventTime", "Operation", "Process", "ChangeSet", "TenantId"},
				[]any{"TrackedForkRows", "moved", int64(0), spanner.CommitTimestamp, string(ChangeUpdate), "test", spanner.NullJSON{Valid: true, Value: map[string]any{"Title": map[string]any{"Old": nil, "New": tenant}}}, tenant},
			)}); err != nil {
				t.Fatalf("writing a record of tenant %s: %v", tenant, err)
			}
		}
		txn := client.ReadOnlyTransaction()
		defer txn.Close()
		for _, tt := range []struct {
			tenant string
			want   []string
		}{
			{tenant: "north", want: []string{`{"Title":{"New":"north","Old":null}}`}},
			{tenant: "south", want: []string{`{"Title":{"New":"south","Old":null}}`}},
			{tenant: "", want: []string{`{"Title":{"New":"south","Old":null}}`, `{"Title":{"New":"north","Old":null}}`}},
		} {
			rows, err := readChangeRecords(t.Context(), txn, &changeRecordsQuery{table: "TrackedForkRows", rowID: "moved", tenant: tt.tenant})
			if err != nil {
				t.Fatalf("readChangeRecords(%q) error = %v", tt.tenant, err)
			}
			var got []string
			for _, row := range rows {
				got = append(got, row.ChangeSet.StringVal)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the records held to tenant %q mismatch (-want +got):\n%s", tt.tenant, diff)
			}
		}

		// A resource that belongs to no tenant is not held to the request's tenant where
		// its record is there, and where it is not, nothing ties its records to the
		// tenant: it is not found.
		history := keyedHistory("moved")
		history.querySet.scope = accesstypes.DomainScope("north")
		page, err := history.readPage(t.Context(), client, visible, nil)
		if err != nil {
			t.Fatalf("readPage() in a tenant's request error = %v", err)
		}
		if len(page.Changes) != 2 {
			t.Errorf("a record of no tenant, there, in a tenant's request: %d records, want both", len(page.Changes))
		}
		notFound := recordNotFound[trackedForkRow]("moved")
		if _, err := history.readPage(t.Context(), client, visible, notFound); !errors.Is(err, notFound) {
			t.Errorf("a record of no tenant, not there, in a tenant's request: readPage() error = %v, want its not found", err)
		}
	})

	t.Run("what kind of write each record was, and who made it", func(t *testing.T) {
		t.Parallel()

		person := accountCtx("alice", aliceID, nil)
		for _, write := range []struct {
			patchType PatchType
			title     string
			source    EventSource
		}{
			{patchType: CreatePatchType, title: "made", source: UserEvent(WithRPC(person, "CloseOrder"))},
			{patchType: UpdatePatchType, title: "changed", source: UserEvent(person)},
			{patchType: DeletePatchType, source: ProcessEvent("test")},
		} {
			if err := w.patch(write.patchType, "sourced", write.title).Apply(t.Context(), client, write.source); err != nil {
				t.Fatalf("Apply(%s) error = %v", write.patchType, err)
			}
		}
		page, err := keyedHistory("sourced").readPage(t.Context(), client, visible, nil)
		if err != nil {
			t.Fatalf("readPage() error = %v", err)
		}
		type said struct {
			Operation ChangeOperation
			Source    string
		}
		var got []said
		for _, record := range page.Changes {
			got = append(got, said{Operation: record.Operation, Source: record.Source})
		}
		want := []said{
			{Operation: ChangeDelete, Source: "the process test"},
			{Operation: ChangeUpdate, Source: "alice"},
			{Operation: ChangeCreate, Source: "alice, through the RPC CloseOrder"},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the records' operations and sources mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a record that is not there", func(t *testing.T) {
		t.Parallel()

		w.write(CreatePatchType, "gone", "here")
		w.write(DeletePatchType, "gone", "")
		notFound := recordNotFound[trackedForkRow]("gone")

		if _, err := keyedHistory("never").readPage(t.Context(), client, visible, notFound); !errors.Is(err, notFound) {
			t.Errorf("a record with no change record: readPage() error = %v, want its not found", err)
		}
		page, err := keyedHistory("gone").readPage(t.Context(), client, visible, notFound)
		if err != nil {
			t.Fatalf("a deleted record: readPage() error = %v", err)
		}
		if len(page.Changes) != 2 {
			t.Errorf("a deleted record: %d records, want its create and its delete", len(page.Changes))
		}
		older := keyedHistory("gone")
		older.after = &changeHistoryToken{at: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		page, err = older.readPage(t.Context(), client, visible, notFound)
		if err != nil {
			t.Fatalf("a deleted record, past its oldest record: readPage() error = %v", err)
		}
		if len(page.Changes) != 0 {
			t.Errorf("a deleted record, past its oldest record: %d records, want none", len(page.Changes))
		}
	})
}

// trackedForkRowCollection is the collection a generated application declares
// trackedForkRow in, for a grant's condition on its title to be rendered from.
func trackedForkRowCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	collection, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        "TrackedForkRows",
		Scope:       accesstypes.GlobalPermissionScope,
		Permissions: []accesstypes.Permission{accesstypes.Read},
		Attributes:  []AttributeData{{Name: "title", Column: "Title", Type: AttributeTypeString}},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return collection
}

// TestChangeHistoryRead_grants pins what the read answers each caller over the emulator:
// a caller the record's read refuses is refused; a resource that keeps no change records
// is not found to every caller the read admits; a masked cell's field is left out of a
// record that is there and asking for its changes is refused; and a record that is not
// there is read under a grant whose condition stands on a field alone, without that
// field, and not found under one whose condition stands on the resource.
func TestChangeHistoryRead_grants(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "change-history-grants")
	client := NewSpannerClient(db)
	w := historyWriter{t: t, client: client}
	w.write(CreatePatchType, "plain", "draft")
	w.write(UpdatePatchType, "plain", "plain")
	w.write(CreatePatchType, "secretive", "secretive")
	w.write(CreatePatchType, "gone", "plain")
	w.write(DeletePatchType, "gone", "")
	// A row of a resource that keeps no change records, created and then updated so that
	// every column holds a value the read scans.
	for _, patchType := range []PatchType{CreatePatchType, UpdatePatchType} {
		if err := forkPatchOf(patchType, "untracked", string(patchType)).Apply(t.Context(), client, ProcessEvent("test")); err != nil {
			t.Fatalf("Apply() of an untracked row error = %v", err)
		}
	}

	collection := trackedForkRowCollection(t)
	every := &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: {"TrackedForkRows", "TrackedForkRows.id", "TrackedForkRows.title", "ForkRows", "ForkRows.title", "ForkRows.createdBy", "ForkRows.updatedBy"}}}
	// The key granted outright and the title on the rows whose title is plain: the
	// title's cell is masked on every other row, which is read for its key.
	titleOfPlain := &fakeUserPermissions{
		granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: {"TrackedForkRows", "TrackedForkRows.id"}},
		conditions: map[accesstypes.Resource]string{"TrackedForkRows.title": "title = 'plain'"},
	}
	plainAlone := &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"TrackedForkRows": "title = 'plain'", "TrackedForkRows.id": "title = 'plain'", "TrackedForkRows.title": "title = 'plain'"}}

	read := func(perms UserPermissions, target string) (*ChangeHistoryPage, error) {
		t.Helper()

		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
		history, err := trackedForkRowHistoryDecoder(collection).DecodeChangeHistory(request, perms, accesstypes.GlobalScope())
		if err != nil {
			t.Fatalf("DecodeChangeHistory() error = %v", err)
		}
		history.QuerySet().SetKey("ID", request.URL.Path[1:])

		return history.Read(t.Context(), client)
	}
	fieldsOf := func(page *ChangeHistoryPage) [][]string {
		fields := [][]string{}
		for _, record := range page.Changes {
			names := []string{}
			for _, field := range record.Fields {
				names = append(names, field.Field)
			}
			fields = append(fields, names)
		}

		return fields
	}

	t.Run("a caller the record's read refuses is refused", func(t *testing.T) {
		t.Parallel()

		if _, err := read(&fakeUserPermissions{}, "/plain"); !httpio.HasForbidden(err) {
			t.Errorf("Read() error = %v, want a 403", err)
		}
	})
	t.Run("a resource that keeps no change records is not found, after the grant", func(t *testing.T) {
		t.Parallel()

		decoder := MustNewQueryDecoder[forkRow, forkRowRead](nil, accesstypes.Read)
		for _, tt := range []struct {
			perms UserPermissions
			want  func(error) bool
		}{
			{perms: every, want: httpio.HasNotFound},
			{perms: &fakeUserPermissions{}, want: httpio.HasForbidden},
		} {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/untracked", http.NoBody)
			history, err := decoder.DecodeChangeHistory(request, tt.perms, accesstypes.GlobalScope())
			if err != nil {
				t.Fatalf("DecodeChangeHistory() error = %v", err)
			}
			history.QuerySet().SetKey("ID", "untracked")
			if _, err := history.Read(t.Context(), client); !tt.want(err) {
				t.Errorf("Read() error = %v", err)
			}
		}
	})
	t.Run("a record that is there: every field the reader sees", func(t *testing.T) {
		t.Parallel()

		page, err := read(every, "/plain")
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if diff := cmp.Diff([][]string{{"title"}, {"id", "title"}}, fieldsOf(page)); diff != "" {
			t.Errorf("fields mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("a record that is there: a masked cell's field is left out, and its changes are refused", func(t *testing.T) {
		t.Parallel()

		page, err := read(titleOfPlain, "/secretive")
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if diff := cmp.Diff([][]string{{"id"}}, fieldsOf(page)); diff != "" {
			t.Errorf("fields mismatch (-want +got):\n%s", diff)
		}
		if _, err := read(titleOfPlain, "/secretive?field=title"); !httpio.HasForbidden(err) {
			t.Errorf("the changes of a masked field: Read() error = %v, want a 403", err)
		}
		page, err = read(titleOfPlain, "/plain?field=title")
		if err != nil {
			t.Fatalf("the changes of a field the reader sees: Read() error = %v", err)
		}
		if diff := cmp.Diff([][]string{{"title"}, {"id", "title"}}, fieldsOf(page)); diff != "" {
			t.Errorf("fields mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("a record that is not there: the fields granted without a condition", func(t *testing.T) {
		t.Parallel()

		page, err := read(every, "/gone")
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if diff := cmp.Diff([][]string{{"id", "title"}, {"id", "title"}}, fieldsOf(page)); diff != "" {
			t.Errorf("fields under a grant with no condition mismatch (-want +got):\n%s", diff)
		}
		page, err = read(titleOfPlain, "/gone")
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if diff := cmp.Diff([][]string{{"id"}, {"id"}}, fieldsOf(page)); diff != "" {
			t.Errorf("fields under a condition on the title mismatch (-want +got):\n%s", diff)
		}
		if _, err := read(titleOfPlain, "/gone?field=title"); !httpio.HasForbidden(err) {
			t.Errorf("the changes of a field granted under a condition: Read() error = %v, want a 403", err)
		}
		if _, err := read(plainAlone, "/gone"); !httpio.HasNotFound(err) {
			t.Errorf("a grant whose condition stands on the resource: Read() error = %v, want a 404", err)
		}
		if page, err := read(plainAlone, "/plain"); err != nil || len(page.Changes) != 2 {
			t.Errorf("the same grant, on a record it reaches: Read() = %v, %v, want both records", page, err)
		}
	})
	t.Run("the record and its change records are read in one read-only transaction", func(t *testing.T) {
		t.Parallel()

		snapshot := &snapshotClient{Client: client, t: t}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/plain", http.NoBody)
		history, err := trackedForkRowHistoryDecoder(collection).DecodeChangeHistory(request, every, accesstypes.GlobalScope())
		if err != nil {
			t.Fatalf("DecodeChangeHistory() error = %v", err)
		}
		history.QuerySet().SetKey("ID", "plain")
		page, err := history.Read(t.Context(), snapshot)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if len(page.Changes) != 2 {
			t.Errorf("Read() answers %d records, want the record's 2", len(page.Changes))
		}
		if snapshot.transactions != 1 {
			t.Errorf("the read asked for %d read-only transactions, want 1", snapshot.transactions)
		}
	})
	t.Run("a record the read may hide from the caller: not found, as the read answers, whether hidden, deleted or never there", func(t *testing.T) {
		t.Parallel()

		// The resource is granted outright and the title under a condition; the key asks
		// no grant. Every field the read selects under a grant stands under the condition,
		// so the read holds its rows to it and answers a row that fails it as not there.
		decoder := MustNewQueryDecoder[trackedForkRow, trackedForkRowGeneratedRead](collection, accesstypes.Read)
		readHidden := func(target string) (*ChangeHistoryPage, error) {
			t.Helper()

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
			history, err := decoder.DecodeChangeHistory(request, titleOfPlain, accesstypes.GlobalScope())
			if err != nil {
				t.Fatalf("DecodeChangeHistory() error = %v", err)
			}
			history.QuerySet().SetKey("ID", request.URL.Path[1:])

			return history.Read(t.Context(), client)
		}
		for _, target := range []string{"/secretive", "/gone", "/never"} {
			if page, err := readHidden(target); !httpio.HasNotFound(err) {
				t.Errorf("%s: Read() = %v, %v, want a 404", target, page, err)
			}
		}
		page, err := readHidden("/plain")
		if err != nil {
			t.Fatalf("/plain: Read() error = %v", err)
		}
		if diff := cmp.Diff([][]string{{"title"}, {"id", "title"}}, fieldsOf(page)); diff != "" {
			t.Errorf("/plain: fields mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestChangeHistoryRead_presentRow pins, over the emulator, the records a reader under a
// condition reads of a key that was deleted and made again: those of the row that is there
// now alone, which end above the earlier row's delete, however the page is asked for and
// wherever it is asked to continue from. A reader with no condition reads every record of
// the key, and a reader under a condition pages a row that was never deleted to its end.
func TestChangeHistoryRead_presentRow(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "change-history-present-row")
	client := NewSpannerClient(db)
	w := historyWriter{t: t, client: client}
	// A row created classified, deleted, and created again under its key, plain.
	w.write(CreatePatchType, "reborn", "classified")
	w.write(DeletePatchType, "reborn", "")
	w.write(CreatePatchType, "reborn", "plain")
	// A row whose earlier row under its key had its title emptied before the delete, so
	// that the delete's record does not name the title.
	w.write(CreatePatchType, "reborn-emptied", "classified")
	w.write(UpdatePatchType, "reborn-emptied", "")
	w.write(DeletePatchType, "reborn-emptied", "")
	w.write(CreatePatchType, "reborn-emptied", "plain")
	// A row whose earlier life under its key was longer than a page: created, updated
	// fifty-four times, deleted, and created again, plain.
	w.write(CreatePatchType, "reborn-long", "classified 0")
	for i := 1; i <= 54; i++ {
		w.write(UpdatePatchType, "reborn-long", fmt.Sprintf("classified %d", i))
	}
	w.write(DeletePatchType, "reborn-long", "")
	w.write(CreatePatchType, "reborn-long", "plain")
	// A row never deleted, of more than a page of records, plain now.
	w.write(CreatePatchType, "long", "draft 0")
	for i := 1; i <= 58; i++ {
		w.write(UpdatePatchType, "long", fmt.Sprintf("draft %d", i))
	}
	w.write(UpdatePatchType, "long", "plain")

	collection := trackedForkRowCollection(t)
	every := &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: {"TrackedForkRows", "TrackedForkRows.id", "TrackedForkRows.title"}}}
	plainAlone := &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"TrackedForkRows": "title = 'plain'", "TrackedForkRows.id": "title = 'plain'", "TrackedForkRows.title": "title = 'plain'"}}
	// The resource and the key granted outright, and the title on the rows whose title is
	// plain: a condition on a field alone.
	titleOfPlain := &fakeUserPermissions{
		granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: {"TrackedForkRows", "TrackedForkRows.id"}},
		conditions: map[accesstypes.Resource]string{"TrackedForkRows.title": "title = 'plain'"},
	}
	read := func(perms UserPermissions, target string) *ChangeHistoryPage {
		t.Helper()

		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
		history, err := trackedForkRowHistoryDecoder(collection).DecodeChangeHistory(request, perms, accesstypes.GlobalScope())
		if err != nil {
			t.Fatalf("DecodeChangeHistory(%s) error = %v", target, err)
		}
		history.QuerySet().SetKey("ID", request.URL.Path[1:])
		page, err := history.Read(t.Context(), client)
		if err != nil {
			t.Fatalf("Read(%s) error = %v", target, err)
		}

		return page
	}
	answer := func(page *ChangeHistoryPage) string {
		t.Helper()

		body, err := json.Marshal(page)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		return string(body)
	}
	// The key's records, newest first: the later create, the delete, the earlier create.
	stored := storedChangeRecords(t, db, "reborn")
	if len(stored) != 3 {
		t.Fatalf("the key has %d change records, want 3", len(stored))
	}
	placeOf := func(record storedChangeRecord) string {
		return url.QueryEscape(changeHistoryToken{at: record.at, sequence: record.sequence}.String())
	}

	t.Run("a reader under a condition reads the row that is there now alone", func(t *testing.T) {
		t.Parallel()

		for _, reader := range []struct {
			name  string
			perms UserPermissions
		}{
			{name: "every grant under the condition", perms: plainAlone},
			{name: "the title's grant alone under the condition", perms: titleOfPlain},
		} {
			for _, target := range []string{"/reborn", "/reborn?field=title", "/reborn-emptied?field=title"} {
				page := read(reader.perms, target)
				if len(page.Changes) != 1 || page.Changes[0].Operation != ChangeCreate || page.Next != "" {
					t.Errorf("%s, %s: %s, want the later create alone, with no next", reader.name, target, answer(page))
				}
				if strings.Contains(answer(page), "classified") {
					t.Errorf("%s, %s: %s, want no word of the earlier row", reader.name, target, answer(page))
				}
			}
		}
	})

	t.Run("a reader under a condition is answered no next where the page was cut, though more came back", func(t *testing.T) {
		t.Parallel()

		page := read(plainAlone, "/reborn-long")
		if len(page.Changes) != 1 || page.Changes[0].Operation != ChangeCreate || page.Next != "" {
			t.Errorf("%s, want the later create alone, with no next", answer(page))
		}
	})

	t.Run("a reader under a condition continued from at or below the row's first record reads nothing", func(t *testing.T) {
		t.Parallel()

		for _, place := range []struct {
			name, after string
		}{
			{name: "the later create's own place", after: placeOf(stored[0])},
			{name: "the delete's own place", after: placeOf(stored[1])},
			{name: "a time years before", after: url.QueryEscape(changeHistoryToken{at: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}.String())},
		} {
			if got := answer(read(plainAlone, "/reborn?after="+place.after)); got != `{"changes":[]}` {
				t.Errorf("after %s: %s, want {\"changes\":[]}", place.name, got)
			}
		}
	})

	t.Run("a reader with no condition reads every record of the key", func(t *testing.T) {
		t.Parallel()

		page := read(every, "/reborn")
		var operations []ChangeOperation
		for _, record := range page.Changes {
			operations = append(operations, record.Operation)
		}
		if diff := cmp.Diff([]ChangeOperation{ChangeCreate, ChangeDelete, ChangeCreate}, operations); diff != "" {
			t.Fatalf("the records' operations mismatch (-want +got):\n%s", diff)
		}
		if deleted := answer(&ChangeHistoryPage{Changes: page.Changes[1:2]}); !strings.Contains(deleted, `{"field":"title","old":"classified","new":null}`) {
			t.Errorf("the delete's record = %s, want the title it took", deleted)
		}
	})

	t.Run("a reader under a condition pages a row never deleted to its end", func(t *testing.T) {
		t.Parallel()

		seen := map[string]bool{}
		var sizes []int
		target := "/long"
		for {
			page := read(plainAlone, target)
			sizes = append(sizes, len(page.Changes))
			for _, record := range page.Changes {
				place := changeHistoryToken{at: record.At, sequence: record.Sequence}.String()
				if seen[place] {
					t.Errorf("the record at %s is answered twice", place)
				}
				seen[place] = true
			}
			if page.Next == "" {
				break
			}
			target = "/long?after=" + url.QueryEscape(page.Next)
		}
		if diff := cmp.Diff([]int{ChangeHistoryPageSize, 60 - ChangeHistoryPageSize}, sizes); diff != "" {
			t.Errorf("the pages' sizes mismatch (-want +got):\n%s", diff)
		}
		if len(seen) != 60 {
			t.Errorf("%d records answered, want the row's 60", len(seen))
		}
	})
}

// TestChangeHistoryRead_presentRowTenant pins, over the emulator, that in a tenant's
// request a delete's record written under another tenant still cuts the page of a reader
// under a condition: of this tenant's two records on either side of it, the newer alone is
// read. A reader with no condition reads both, and not the other tenant's record.
func TestChangeHistoryRead_presentRowTenant(t *testing.T) {
	t.Parallel()

	db := changeDatabase(t, "change-history-tenant-cut")
	client := NewSpannerClient(db)
	applyEach(t, client, changePatch[changeDock](CreatePatchType, "dock-reborn", map[accesstypes.Field]any{"YardID": changeYardNorth, "Name": "First"}))
	// The delete's record of an earlier row of the key, under the other tenant, written past
	// the library between the row's two records.
	seedChangeRows(t, db, spanner.Insert("DataChangeEvents",
		[]string{"TableName", "RowId", "EventTime", "Sequence", "Operation", "Process", "ChangeSet", "TenantId"},
		[]any{"Docks", "dock-reborn", spanner.CommitTimestamp, int64(1), string(ChangeDelete), "test", spanner.NullJSON{Valid: true, Value: map[string]any{"Name": map[string]any{"Old": "Gone", "New": nil}}}, changeYardSouth},
	))
	applyEach(t, client, changePatch[changeDock](UpdatePatchType, "dock-reborn", map[accesstypes.Field]any{"Name": "Second"}))

	visible := []accesstypes.Field{"ID", "YardID", "Name"}
	for _, tt := range []struct {
		name        string
		conditional bool
		want        []ChangeOperation
	}{
		{name: "a reader with no condition reads the tenant's two records", want: []ChangeOperation{ChangeUpdate, ChangeCreate}},
		{name: "a reader under a condition reads the newer alone", conditional: true, want: []ChangeOperation{ChangeUpdate}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			q := NewQuerySet(NewMetadata[changeDock]())
			q.SetKey("ID", "dock-reborn")
			q.scope = accesstypes.DomainScope(changeYardNorth)
			if tt.conditional {
				docks := q.resourceSet.BaseResource()
				q.conditionalDecisions = accesstypes.Decisions{docks: accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{docks}, Condition: testCondition})}
			}
			page, err := (&ChangeHistoryQuery[changeDock]{querySet: q}).readPage(t.Context(), client, visible, nil)
			if err != nil {
				t.Fatalf("readPage() error = %v", err)
			}
			var operations []ChangeOperation
			for _, record := range page.Changes {
				operations = append(operations, record.Operation)
			}
			if diff := cmp.Diff(tt.want, operations); diff != "" || page.Next != "" {
				t.Errorf("the records' operations mismatch (-want +got):\n%s; next = %q, want none", diff, page.Next)
			}
		})
	}
}
