package resource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

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

// TestChangeHistoryPage pins how the rows a read gives become a page: their order, each
// field under its JSON name in the order of the fields the reader may see, every other
// field left out, a record left empty still listed, a secret's change with no value, a
// value kept byte for byte, and next where a further row came back.
func TestChangeHistoryPage(t *testing.T) {
	t.Parallel()

	jsonNames := map[accesstypes.Field]string{"Title": "title", "Count": "count", "Code": "code", "Hidden": "hidden"}
	jsonName := func(field accesstypes.Field) string {
		return jsonNames[field]
	}
	visible := []accesstypes.Field{"Code", "Title", "Count"}
	newest := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	changeSet := func(text string) spanner.NullString {
		return spanner.NullString{StringVal: text, Valid: true}
	}
	rows := []changeRecordRow{
		{EventTime: newest, Sequence: 1, EventSource: "user b", ChangeSet: changeSet(`{"Count":{"Old":12345678901234567890,"New":98765432109876543210},"Title":{"Old":"a","New":"b"},"Gone":{"Old":1,"New":2}}`)},
		{EventTime: newest, Sequence: 0, EventSource: "user b", ChangeSet: changeSet(`{"Hidden":{"Old":"x","New":"y"}}`)},
		{EventTime: newest.Add(-time.Minute), Sequence: 0, EventSource: "process sync", ChangeSet: changeSet(`{"Code":{"Old":null,"New":null,"Secret":true},"Title":{"New":"a"}}`)},
		{EventTime: newest.Add(-2 * time.Minute), Sequence: 0, EventSource: "process sync"},
	}
	page, err := changeHistoryPage(rows, visible, jsonName)
	if err != nil {
		t.Fatalf("changeHistoryPage() error = %v", err)
	}
	null := json.RawMessage("null")
	want := &ChangeHistoryPage{Changes: []ChangeRecord{
		{At: newest, Sequence: 1, Source: "user b", Fields: []ChangedField{
			{Field: "title", Old: json.RawMessage(`"a"`), New: json.RawMessage(`"b"`)},
			{Field: "count", Old: json.RawMessage("12345678901234567890"), New: json.RawMessage("98765432109876543210")},
		}},
		{At: newest, Sequence: 0, Source: "user b", Fields: []ChangedField{}},
		{At: newest.Add(-time.Minute), Sequence: 0, Source: "process sync", Fields: []ChangedField{
			{Field: "code", Old: null, New: null, Secret: true},
			{Field: "title", Old: null, New: json.RawMessage(`"a"`)},
		}},
		{At: newest.Add(-2 * time.Minute), Sequence: 0, Source: "process sync", Fields: []ChangedField{}},
	}}
	if diff := cmp.Diff(want, page); diff != "" {
		t.Errorf("changeHistoryPage() mismatch (-want +got):\n%s", diff)
	}

	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	wantEncoded := `{"changes":[` +
		`{"at":"2026-11-23T18:00:00Z","sequence":1,"source":"user b","fields":[{"field":"title","old":"a","new":"b"},{"field":"count","old":12345678901234567890,"new":98765432109876543210}]},` +
		`{"at":"2026-11-23T18:00:00Z","sequence":0,"source":"user b","fields":[]},` +
		`{"at":"2026-11-23T17:59:00Z","sequence":0,"source":"process sync","fields":[{"field":"code","old":null,"new":null,"secret":true},{"field":"title","old":null,"new":"a"}]},` +
		`{"at":"2026-11-23T17:58:00Z","sequence":0,"source":"process sync","fields":[]}]}`
	if string(encoded) != wantEncoded {
		t.Errorf("the page encodes as\n%s\nwant\n%s", encoded, wantEncoded)
	}

	t.Run("next is there when a row beyond the page came back, and is the last record's", func(t *testing.T) {
		t.Parallel()

		many := make([]changeRecordRow, ChangeHistoryPageSize+1)
		for i := range many {
			many[i] = changeRecordRow{EventTime: newest.Add(-time.Duration(i) * time.Second), Sequence: int64(i % 2)}
		}
		page, err := changeHistoryPage(many, visible, jsonName)
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

		whole, err := changeHistoryPage(many[:ChangeHistoryPageSize], visible, jsonName)
		if err != nil {
			t.Fatalf("changeHistoryPage() error = %v", err)
		}
		if len(whole.Changes) != ChangeHistoryPageSize || whole.Next != "" {
			t.Errorf("a full page with nothing beyond it: %d records, next %q, want %d and none", len(whole.Changes), whole.Next, ChangeHistoryPageSize)
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
