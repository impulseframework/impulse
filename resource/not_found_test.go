package resource

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// goneRow is a row both databases know: its key and one column.
type goneRow struct {
	ID    string `spanner:"Id"    postgres:"Id"`
	Title string `spanner:"Title" postgres:"Title"`
}

func (goneRow) Resource() accesstypes.Resource {
	return "GoneRows"
}

func (goneRow) DefaultConfig() Config {
	return Config{}
}

// plainAnswer holds one of the package's answers to what a caller may be told. The error
// is the kind of answer wanted (is); the caller is sent the plain sentence and nothing
// else; no message carries a mark of a query or any of the words kept from the caller
// (the statement's WHERE clause, a table's name, a column's, a key's value); and the
// error's own text, which the server's log prints, still carries each of those words.
func plainAnswer(t *testing.T, err error, is func(error) bool, sentence string, kept ...string) {
	t.Helper()

	if err == nil || !is(err) {
		t.Fatalf("error = %v, want the answer that says %q", err, sentence)
	}
	messages := httpio.Messages(err)
	if len(messages) != 1 || messages[0] != sentence {
		t.Errorf("the caller is told %q, want only %q", messages, sentence)
	}
	for _, message := range messages {
		for _, word := range append([]string{"WHERE", "`", "\"", "@p", "$1"}, kept...) {
			if strings.Contains(message, word) {
				t.Errorf("the caller is told %q, which carries %q", message, word)
			}
		}
	}
	for _, word := range kept {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error's text, which the server's log prints, is %q: it does not carry %q", err.Error(), word)
		}
	}
}

// missingRowStatement is the statement that reads one row of the resource by its key, as
// the given database is asked for it.
func missingRowStatement[Resource Resourcer](t *testing.T, dbType DBType, id string) *Statement {
	t.Helper()

	q := NewQuerySet(NewMetadata[Resource]())
	q.SetKey("ID", id)
	q.AddField("ID")
	q.AddField("Title")
	if err := q.checkPermissions(t.Context(), dbType); err != nil {
		t.Fatalf("QuerySet.checkPermissions() error = %v", err)
	}
	stmt, err := q.stmt(dbType)
	if err != nil {
		t.Fatalf("QuerySet.stmt() error = %v", err)
	}
	if !strings.Contains(stmt.resolvedWhereClause, "WHERE") || !strings.Contains(stmt.resolvedWhereClause, "Id") || !strings.Contains(stmt.resolvedWhereClause, id) {
		t.Fatalf("the statement's WHERE clause is %q, want it to name the key's column and its value", stmt.resolvedWhereClause)
	}

	return stmt
}

// TestAnswers_sayNoQuery proves what a caller is told, on each database the package
// speaks, where the package finds no record or nothing to change: a plain sentence, with
// no query, no table's name, no column's and no key. What the server looked for (the
// WHERE clause as that database is asked it, the table) stays in the error, for the
// server's log. For PostgreSQL this holds the answers to the statement alone: the package
// renders the statement for that database and reads nothing through it yet, so no read
// is proven there, as TestSpannerClient_notFound_saysNoQuery proves one over Spanner.
func TestAnswers_sayNoQuery(t *testing.T) {
	t.Parallel()

	for _, dbType := range dbTypes() {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			var res goneRow
			where := missingRowStatement[goneRow](t, dbType, "row-9").resolvedWhereClause

			t.Run("a record that is not there", func(t *testing.T) {
				t.Parallel()

				plainAnswer(t, recordNotFound(res.Resource(), where), httpio.HasNotFound, "This record was not found.", where, "GoneRows", "Id", "row-9")
			})
			t.Run("a write that names a record that is not there", func(t *testing.T) {
				t.Parallel()

				plainAnswer(t, referencedNotFound(res.Resource(), "ParentRows"), httpio.HasNotFound, "A record this one refers to was not found.", "ParentRows", "GoneRows")
			})
			t.Run("a write that changes nothing", func(t *testing.T) {
				t.Parallel()

				plainAnswer(
					t, nothingToChange(res.Resource(), where), httpio.HasBadRequest,
					"There is nothing to change: the record already holds these values.", where, "GoneRows", "Id", "row-9",
				)
			})
		})
	}
}

// TestRecordNotFound_theLogKeepsTheWhereClause serves the answer for a record that is
// not there as a handler does, and reads both sides of it: the caller's body says the
// plain sentence and nothing of the query, and the line the server logs for the request
// carries the WHERE clause. It is not parallel: the standard logger, which a request
// with no logger of its own writes through, is the process's own.
func TestRecordNotFound_theLogKeepsTheWhereClause(t *testing.T) { //nolint:paralleltest // it reads the standard logger's output, which is the whole process's
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	var res goneRow
	where := missingRowStatement[goneRow](t, SpannerDBType, "row-9").resolvedWhereClause
	handler := httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		return httpio.NewEncoder(w).ClientMessage(r.Context(), recordNotFound(res.Resource(), where))
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/gone-rows/row-9", http.NoBody))

	if recorder.Code != http.StatusNotFound {
		t.Errorf("the answer's status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "This record was not found.") {
		t.Errorf("the caller's body is %q, want it to say %q", body, "This record was not found.")
	}
	for _, word := range []string{"WHERE", "`", "Id", "row-9", "GoneRows"} {
		if strings.Contains(body, word) {
			t.Errorf("the caller's body is %q, which carries %q", body, word)
		}
	}
	if !strings.Contains(logged.String(), where) {
		t.Errorf("the server's log for the request is %q, want it to carry the WHERE clause %q", logged.String(), where)
	}
}
