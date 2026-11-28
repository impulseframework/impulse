package resource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// sessionColumn is the session every test context of the package carries (sessionCtx,
// accountCtx), as its column holds it.
const sessionColumn = "de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"

// whoColumnsOf answers the who columns a value sets as a row of a table that keeps them
// holds them, each NULL where the value leaves it empty.
func whoColumnsOf(source eventsource.EventSource) eventSourceColumns {
	columns := source.Columns()
	column := func(name string) spanner.NullString {
		value, ok := columns[name].(string)

		return spanner.NullString{StringVal: value, Valid: ok}
	}

	return eventSourceColumns{
		Auth: column("Auth"), ActorUserID: column("ActorUserId"), ActorUsername: column("ActorUsername"),
		PrincipalUserID: column("PrincipalUserId"), PrincipalUsername: column("PrincipalUsername"), PrincipalRole: column("PrincipalRole"),
		SessionID: column("SessionId"), Process: column("Process"), RPC: column("Rpc"),
	}
}

// TestEventSourceColumns pins the row a statement scans who made a change into to the
// names the value's package gives the nine columns: each field's tag is the name
// eventsource.ColumnNames answers in its place.
func TestEventSourceColumns(t *testing.T) {
	t.Parallel()

	fields := reflect.VisibleFields(reflect.TypeFor[eventSourceColumns]())
	tags := make([]string, 0, len(fields))
	for _, field := range fields {
		tags = append(tags, field.Tag.Get("spanner"))
	}
	if diff := cmp.Diff(eventsource.ColumnNames(), tags); diff != "" {
		t.Errorf("eventSourceColumns' spanner tags mismatch (-eventsource.ColumnNames() +tags):\n%s", diff)
	}
}

// TestEventSourceColumns_stored pins that a row's who columns answer the sentence of the
// value they were written from.
func TestEventSourceColumns_stored(t *testing.T) {
	t.Parallel()

	aliceAsSupport := sessionCtx("alice", &sessioninfo.Impersonation{Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("Support")})
	for _, source := range []eventsource.EventSource{
		eventsource.UserEvent(WithAuth(accountCtx("bob", bobID, nil), "crew")),
		eventsource.UserProcessEvent(WithRPC(aliceAsSupport, "ImportOrders"), "Import"),
		eventsource.ProcessEvent("MigrateFeatures"),
	} {
		columns := whoColumnsOf(source)
		if got, want := columns.stored().String(), source.String(); got != want {
			t.Errorf("the row's sentence = %q, want the value's, %q", got, want)
		}
	}
}

// trackedForkRowRead is a read route's request type of trackedForkRow with the key and
// the title each under a grant of its own, so that a row whose title a condition masks
// still shows a field and is read.
type trackedForkRowRead struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// trackedForkRowHistoryDecoder is the decoder a generated history handler of
// trackedForkRow builds, with collection to render a grant's condition from.
func trackedForkRowHistoryDecoder(collection *GeneratedCollection) *QueryDecoder[trackedForkRow, trackedForkRowRead] {
	return MustNewQueryDecoder[trackedForkRow, trackedForkRowRead](collection, accesstypes.Read)
}

// TestChangeHistoryToken pins the value of after: a page's next reads back as the place
// it was made from, and every other shape is refused 400 in one sentence.
func TestChangeHistoryToken(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 23, 18, 4, 5, 123456000, time.UTC)
	token := changeHistoryToken{at: at, sequence: 7}
	if got, want := token.String(), "2026-11-23T18:04:05.123456Z_7"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	got, err := parseChangeHistoryToken(token.String())
	if err != nil {
		t.Fatalf("parseChangeHistoryToken(%q) error = %v", token.String(), err)
	}
	if !got.at.Equal(at) || got.sequence != 7 {
		t.Errorf("parseChangeHistoryToken(%q) = %v %d, want %v 7", token.String(), got.at, got.sequence, at)
	}

	for _, malformed := range []string{
		"no-underscore",
		"_7",
		"yesterday_7",
		"2026-11-23T18:04:05.123456+01:00_7",
		"2026-11-23T18:04:05.1234560Z_7",
		"2026-11-23T18:04:05.123456Z_",
		"2026-11-23T18:04:05.123456Z_-1",
		"2026-11-23T18:04:05.123456Z_+1",
		"2026-11-23T18:04:05.123456Z_1e3",
		"2026-11-23T18:04:05.123456Z_seven",
		"2026-11-23T18:04:05.123456Z_99999999999999999999",
	} {
		t.Run(malformed, func(t *testing.T) {
			t.Parallel()

			_, err := parseChangeHistoryToken(malformed)
			if !httpio.HasBadRequest(err) {
				t.Fatalf("parseChangeHistoryToken(%q) error = %v, want a 400", malformed, err)
			}
			want := "The history cannot be continued from \"" + malformed + "\". Use the value of next from the page before."
			if got := httpio.Message(err); got != want {
				t.Errorf("the refusal says %q, want %q", got, want)
			}
		})
	}
}

// TestChangeHistoryDecode pins what a request for a record's history may carry: after
// and field, and nothing else; field a JSON name of the resource that its read returns.
func TestChangeHistoryDecode(t *testing.T) {
	t.Parallel()

	decoder := trackedForkRowHistoryDecoder(nil)
	perms := &fakeUserPermissions{}
	decode := func(target string, neverReturned ...accesstypes.Field) (*ChangeHistoryQuery[trackedForkRow], error) {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)

		return decoder.DecodeChangeHistory(request, perms, accesstypes.GlobalScope(), neverReturned...)
	}

	t.Run("no parameter: the newest page of every change", func(t *testing.T) {
		t.Parallel()

		history, err := decode("/rows/a/changes")
		if err != nil {
			t.Fatalf("DecodeChangeHistory() error = %v", err)
		}
		if history.after != nil || history.field != "" {
			t.Errorf("DecodeChangeHistory() = after %v field %q, want neither", history.after, history.field)
		}
		if history.QuerySet() == nil {
			t.Fatal("DecodeChangeHistory() gave no query set to name the record on")
		}
	})
	t.Run("after and field are read", func(t *testing.T) {
		t.Parallel()

		history, err := decode("/rows/a/changes?after=2026-11-23T18:04:05.5Z_3&field=title")
		if err != nil {
			t.Fatalf("DecodeChangeHistory() error = %v", err)
		}
		if history.after == nil || history.after.String() != "2026-11-23T18:04:05.5Z_3" {
			t.Errorf("after = %v, want 2026-11-23T18:04:05.5Z_3", history.after)
		}
		if history.field != "Title" {
			t.Errorf("field = %q, want Title", history.field)
		}
	})

	refusals := []struct {
		name          string
		target        string
		neverReturned []accesstypes.Field
		want          string
	}{
		{
			name:   "any other parameter, named in order",
			target: "/rows/a/changes?limit=5&field=title&v=3&columns=title",
			want:   "A record's history takes two parameters, after and field. Take columns, limit, v out of the request.",
		},
		{
			name:   "a field the resource does not have",
			target: "/rows/a/changes?field=shade",
			want:   `This record has no field named "shade".`,
		},
		{
			name:   "a field asked by its Go name",
			target: "/rows/a/changes?field=Title",
			want:   `This record has no field named "Title".`,
		},
		{
			name:          "a field the record's read never returns, in the words of one it does not have",
			target:        "/rows/a/changes?field=title",
			neverReturned: []accesstypes.Field{"Title"},
			want:          `This record has no field named "title".`,
		},
		{
			name:   "a place to continue from that no page gave",
			target: "/rows/a/changes?after=yesterday",
			want:   `The history cannot be continued from "yesterday". Use the value of next from the page before.`,
		},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decode(tt.target, tt.neverReturned...)
			if !httpio.HasBadRequest(err) {
				t.Fatalf("DecodeChangeHistory() error = %v, want a 400", err)
			}
			if got := httpio.Message(err); got != tt.want {
				t.Errorf("the refusal says %q, want %q", got, tt.want)
			}
		})
	}
}

// TestChangeHistoryVisibleFields pins the fields a record's history shows of a record
// that is there: those its read returned whose cell is not masked, less those the read
// never returns.
func TestChangeHistoryVisibleFields(t *testing.T) {
	t.Parallel()

	read := func(neverReturned ...accesstypes.Field) *ChangeHistoryQuery[trackedForkRow] {
		q := NewQuerySet(NewMetadata[trackedForkRow]())
		q.AddField("ID")
		q.AddField("Title")
		q.jsonNames = map[accesstypes.Field]string{"ID": "id", "Title": "title"}

		return &ChangeHistoryQuery[trackedForkRow]{querySet: q, neverReturned: neverReturned}
	}
	tests := []struct {
		name          string
		masked        []string
		neverReturned []accesstypes.Field
		want          []accesstypes.Field
	}{
		{name: "every field the read returned", want: []accesstypes.Field{"ID", "Title"}},
		{name: "a masked cell is left out", masked: []string{"title"}, want: []accesstypes.Field{"ID"}},
		{name: "a field the read never returns is left out", neverReturned: []accesstypes.Field{"Title"}, want: []accesstypes.Field{"ID"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := read(tt.neverReturned...).visibleFields(&Row[trackedForkRow]{masked: tt.masked}, nil)
			if err != nil {
				t.Fatalf("visibleFields() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("visibleFields() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestChangeHistoryPage pins how the rows a read gives become a page: their order, what
// kind of write each was, who made it as one sentence with no id, each field under its
// JSON name in the order of the fields the reader may see, every other
// field left out, a record left empty still listed, a secret's change with no value, a
// value kept byte for byte, a field set to the commit's time answered with the record's
// own time, the names kept beside a pointing field's values where the reader may read
// them, and next where a further row came back.
func TestChangeHistoryPage(t *testing.T) {
	t.Parallel()

	jsonNames := map[accesstypes.Field]string{"Title": "title", "Count": "count", "Code": "code", "Hidden": "hidden", "Key": "key"}
	jsonName := func(field accesstypes.Field) string {
		return jsonNames[field]
	}
	// Key is a secret by the resource's declaration; no other field is.
	secret := func(field accesstypes.Field) bool {
		return field == "Key"
	}
	visible := []accesstypes.Field{"Code", "Title", "Count"}
	newest := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	changeSet := func(text string) spanner.NullString {
		return spanner.NullString{StringVal: text, Valid: true}
	}
	text := func(value string) spanner.NullString {
		return spanner.NullString{StringVal: value, Valid: true}
	}
	// Who made each change, in the columns the table keeps: a person, with the ids the
	// route never serves, and a process.
	userB := eventSourceColumns{Auth: text("crew"), ActorUserID: text(bobID.String()), ActorUsername: text("b"), SessionID: text(sessionColumn)}
	sync := eventSourceColumns{Process: text("sync")}
	rows := []changeRecordRow{
		{EventTime: newest, Sequence: 1, Operation: "update", eventSourceColumns: userB, ChangeSet: changeSet(`{"Count":{"Old":12345678901234567890,"New":98765432109876543210},"Title":{"Old":"a","New":"b"},"Gone":{"Old":1,"New":2}}`)},
		{EventTime: newest, Sequence: 0, Operation: "update", eventSourceColumns: userB, ChangeSet: changeSet(`{"Hidden":{"Old":"x","New":"y"}}`)},
		{EventTime: newest.Add(-time.Minute), Sequence: 0, Operation: "create", eventSourceColumns: sync, ChangeSet: changeSet(`{"Code":{"Old":null,"New":null,"Secret":true},"Title":{"New":"a"}}`)},
		{EventTime: newest.Add(-2 * time.Minute), Sequence: 0, Operation: "delete", eventSourceColumns: sync},
	}
	page, err := changeHistoryPage(rows, visible, nil, jsonName, secret)
	if err != nil {
		t.Fatalf("changeHistoryPage() error = %v", err)
	}
	null := json.RawMessage("null")
	want := &ChangeHistoryPage{Changes: []ChangeRecord{
		{At: newest, Sequence: 1, Operation: ChangeUpdate, Source: "b", Fields: []ChangedField{
			{Field: "title", Old: json.RawMessage(`"a"`), New: json.RawMessage(`"b"`)},
			{Field: "count", Old: json.RawMessage("12345678901234567890"), New: json.RawMessage("98765432109876543210")},
		}},
		{At: newest, Sequence: 0, Operation: ChangeUpdate, Source: "b", Fields: []ChangedField{}},
		{At: newest.Add(-time.Minute), Sequence: 0, Operation: ChangeCreate, Source: "the process sync", Fields: []ChangedField{
			{Field: "code", Old: null, New: null, Secret: true},
			{Field: "title", Old: null, New: json.RawMessage(`"a"`)},
		}},
		{At: newest.Add(-2 * time.Minute), Sequence: 0, Operation: ChangeDelete, Source: "the process sync", Fields: []ChangedField{}},
	}}
	if diff := cmp.Diff(want, page); diff != "" {
		t.Errorf("changeHistoryPage() mismatch (-want +got):\n%s", diff)
	}

	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	wantEncoded := `{"changes":[` +
		`{"at":"2026-11-23T18:00:00Z","sequence":1,"operation":"update","source":"b","fields":[{"field":"title","old":"a","new":"b"},{"field":"count","old":12345678901234567890,"new":98765432109876543210}]},` +
		`{"at":"2026-11-23T18:00:00Z","sequence":0,"operation":"update","source":"b","fields":[]},` +
		`{"at":"2026-11-23T17:59:00Z","sequence":0,"operation":"create","source":"the process sync","fields":[{"field":"code","old":null,"new":null,"secret":true},{"field":"title","old":null,"new":"a"}]},` +
		`{"at":"2026-11-23T17:58:00Z","sequence":0,"operation":"delete","source":"the process sync","fields":[]}]}`
	if string(encoded) != wantEncoded {
		t.Errorf("the page encodes as\n%s\nwant\n%s", encoded, wantEncoded)
	}

	t.Run("next is there when a row beyond the page came back, and is the last record's", func(t *testing.T) {
		t.Parallel()

		many := make([]changeRecordRow, ChangeHistoryPageSize+1)
		for i := range many {
			many[i] = changeRecordRow{EventTime: newest.Add(-time.Duration(i) * time.Second), Sequence: int64(i % 2)}
		}
		page, err := changeHistoryPage(many, visible, nil, jsonName, secret)
		if err != nil {
			t.Fatalf("changeHistoryPage() error = %v", err)
		}
		if len(page.Changes) != ChangeHistoryPageSize {
			t.Errorf("the page holds %d records, want %d", len(page.Changes), ChangeHistoryPageSize)
		}
		last := many[ChangeHistoryPageSize-1]
		if want := (changeHistoryToken{at: last.EventTime, sequence: last.Sequence}).String(); page.Next != want {
			t.Errorf("next = %q, want %q", page.Next, want)
		}

		whole, err := changeHistoryPage(many[:ChangeHistoryPageSize], visible, nil, jsonName, secret)
		if err != nil {
			t.Fatalf("changeHistoryPage() error = %v", err)
		}
		if len(whole.Changes) != ChangeHistoryPageSize || whole.Next != "" {
			t.Errorf("a full page with nothing beyond it: %d records, next %q, want %d and none", len(whole.Changes), whole.Next, ChangeHistoryPageSize)
		}
	})
	t.Run("a field declared a secret is answered with no value, whatever its record holds", func(t *testing.T) {
		t.Parallel()

		// A record written before the field was declared a secret: both values, no mark.
		before := []changeRecordRow{{EventTime: newest, Sequence: 0, Operation: "update", eventSourceColumns: userB, ChangeSet: changeSet(`{"Key":{"Old":"old key","New":"new key"},"Title":{"Old":"a","New":"b"}}`)}}
		page, err := changeHistoryPage(before, []accesstypes.Field{"Key", "Title"}, nil, jsonName, secret)
		if err != nil {
			t.Fatalf("changeHistoryPage() error = %v", err)
		}
		want := []ChangedField{
			{Field: "key", Old: null, New: null, Secret: true},
			{Field: "title", Old: json.RawMessage(`"a"`), New: json.RawMessage(`"b"`)},
		}
		if diff := cmp.Diff(want, page.Changes[0].Fields); diff != "" {
			t.Errorf("changeHistoryPage() fields mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("a field that points at another row: the kept names of a field the reader may read them for, none for another or a secret, and no key where none was kept", func(t *testing.T) {
		t.Parallel()

		pointing := []changeRecordRow{{EventTime: newest, Sequence: 0, Operation: "update", eventSourceColumns: userB, ChangeSet: changeSet(
			`{"Code":{"Old":"r1","New":"r2","OldName":"One","NewName":"Two"},` +
				`"Title":{"Old":"r1","New":"r2","OldName":"One","NewName":"Two"},` +
				`"Count":{"Old":null,"New":"r3","NewName":"Three"},` +
				`"Hidden":{"Old":"r1","New":"r4"},` +
				`"Key":{"Old":null,"New":null,"OldName":"One","NewName":"Two","Secret":true}}`)}}
		named := map[accesstypes.Field]bool{"Code": true, "Count": true, "Hidden": true, "Key": true}
		page, err := changeHistoryPage(pointing, []accesstypes.Field{"Code", "Title", "Count", "Hidden", "Key"}, named, jsonName, secret)
		if err != nil {
			t.Fatalf("changeHistoryPage() error = %v", err)
		}
		want := []ChangedField{
			{Field: "code", Old: json.RawMessage(`"r1"`), New: json.RawMessage(`"r2"`), OldName: "One", NewName: "Two"},
			{Field: "title", Old: json.RawMessage(`"r1"`), New: json.RawMessage(`"r2"`)},
			{Field: "count", Old: null, New: json.RawMessage(`"r3"`), NewName: "Three"},
			{Field: "hidden", Old: json.RawMessage(`"r1"`), New: json.RawMessage(`"r4"`)},
			{Field: "key", Old: null, New: null, Secret: true},
		}
		if diff := cmp.Diff(want, page.Changes[0].Fields); diff != "" {
			t.Errorf("changeHistoryPage() fields mismatch (-want +got):\n%s", diff)
		}
		encoded, err := json.Marshal(page.Changes[0].Fields)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		wantEncoded := `[{"field":"code","old":"r1","new":"r2","oldName":"One","newName":"Two"},` +
			`{"field":"title","old":"r1","new":"r2"},` +
			`{"field":"count","old":null,"new":"r3","newName":"Three"},` +
			`{"field":"hidden","old":"r1","new":"r4"},` +
			`{"field":"key","old":null,"new":null,"secret":true}]`
		if string(encoded) != wantEncoded {
			t.Errorf("the fields encode as\n%s\nwant\n%s", encoded, wantEncoded)
		}
	})
	t.Run("a field set to the commit's time is answered with the record's own time as its new value", func(t *testing.T) {
		t.Parallel()

		// The record's time in another zone, to nanoseconds: the value is answered in UTC.
		at := time.Date(2026, 11, 23, 11, 0, 0, 123456789, time.FixedZone("MST", -7*60*60))
		stamped := []changeRecordRow{{EventTime: at, Sequence: 0, Operation: "update", eventSourceColumns: userB, ChangeSet: changeSet(`{"Code":{"Old":"2026-11-20T10:00:00Z","New":null,"CommitTime":true},"Key":{"Old":null,"New":null,"CommitTime":true},"Title":{"Old":"a","New":"b"}}`)}}
		page, err := changeHistoryPage(stamped, []accesstypes.Field{"Code", "Key", "Title"}, nil, jsonName, secret)
		if err != nil {
			t.Fatalf("changeHistoryPage() error = %v", err)
		}
		want := []ChangedField{
			{Field: "code", Old: json.RawMessage(`"2026-11-20T10:00:00Z"`), New: json.RawMessage(`"2026-11-23T18:00:00.123456789Z"`)},
			{Field: "key", Old: null, New: null, Secret: true},
			{Field: "title", Old: json.RawMessage(`"a"`), New: json.RawMessage(`"b"`)},
		}
		if diff := cmp.Diff(want, page.Changes[0].Fields); diff != "" {
			t.Errorf("changeHistoryPage() fields mismatch (-want +got):\n%s", diff)
		}
	})
}

// postgresClient is a client of a database that is not Spanner.
type postgresClient struct {
	Client
}

func (postgresClient) DBType() DBType {
	return PostgresDBType
}

// TestChangeHistoryRead_spannerOnly pins that a record's history is read on Spanner
// alone: on another database the read answers an error before it reads anything.
func TestChangeHistoryRead_spannerOnly(t *testing.T) {
	t.Parallel()

	history := &ChangeHistoryQuery[trackedForkRow]{querySet: NewQuerySet(NewMetadata[trackedForkRow]())}
	if _, err := history.Read(t.Context(), postgresClient{}); !errors.Is(err, errChangeHistoryUnsupportedDatabase) {
		t.Errorf("Read() error = %v, want %v", err, errChangeHistoryUnsupportedDatabase)
	}
}
