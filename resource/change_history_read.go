package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan"
)

// The history of one record, as its route reads it.
const (
	// ChangeHistoryRoute is the segment under a record's read route that the record's
	// history is read at: GET <the read route>/changes.
	ChangeHistoryRoute = "changes"
	// ChangeHistoryPageSize is the most change records one page of a record's history
	// holds: the newest ones, or the newest older than the record a page continues from.
	ChangeHistoryPageSize = 50
)

// The query parameters a record's history takes: where to continue from, and the one
// field whose changes are kept.
const (
	changeHistoryAfterParam = "after"
	changeHistoryFieldParam = "field"
)

var errChangeHistoryUnsupportedDatabase = errors.New("resource: a record's change history is read on Spanner only")

// DecodeChangeHistory decodes the request for one record's history, which the generated
// history handler serves under the record's read route. It reads two query parameters
// and no body: after, the value of next a page before this one gave, and field, the JSON
// name of the one field whose changes are kept. Any other parameter, a field the
// resource does not have (or hides behind a flag that is off), and a value of after the
// read did not give are refused 400. The record itself is read as a request for it with
// no parameters reads it, under the caller's grant in scope, so the history sees every
// field the grant covers. neverReturned names the fields the record's own read never
// returns although a grant covers them (a field declared input_only, a file's key): they
// are in no change record, and field naming one is refused as a field the resource does
// not have.
func (d *QueryDecoder[Resource, Request]) DecodeChangeHistory(request *http.Request, userPermissions UserPermissions, scope accesstypes.Scope, neverReturned ...accesstypes.Field) (*ChangeHistoryQuery[Resource], error) {
	params := request.URL.Query()
	var others []string
	for name := range params {
		if name != changeHistoryAfterParam && name != changeHistoryFieldParam {
			others = append(others, name)
		}
	}
	if len(others) > 0 {
		slices.Sort(others)

		return nil, httpio.NewBadRequestMessagef("A record's history takes two parameters, after and field. Take %s out of the request.", strings.Join(others, ", "))
	}

	history := &ChangeHistoryQuery[Resource]{neverReturned: slices.Clone(neverReturned)}
	if name := params.Get(changeHistoryFieldParam); name != "" {
		field, found := d.structFieldName(name, d.hiddenFields())
		if !found || slices.Contains(neverReturned, field) {
			return nil, httpio.NewBadRequestMessagef("This record has no field named %q.", name)
		}
		history.field, history.fieldName = field, name
	}
	if token := params.Get(changeHistoryAfterParam); token != "" {
		after, err := parseChangeHistoryToken(token)
		if err != nil {
			return nil, err
		}
		history.after = &after
	}

	record := request.Clone(request.Context())
	record.URL.RawQuery = ""
	querySet, err := d.Decode(record, userPermissions, scope)
	if err != nil {
		return nil, err
	}
	history.querySet = querySet

	return history, nil
}

// ChangeHistoryQuery is a decoded request for one record's history
// (QueryDecoder.DecodeChangeHistory): the record, named by its key on QuerySet, and a page
// of its change records, read by Read.
type ChangeHistoryQuery[Resource Resourcer] struct {
	querySet *QuerySet[Resource]
	// after is where the page continues from, nil for the newest page.
	after *changeHistoryToken
	// field is the one field whose changes are kept, and fieldName its JSON name as the
	// request named it; both empty where every change is kept.
	field     accesstypes.Field
	fieldName string
	// neverReturned are the fields the record's own read never returns, which no change
	// record lists.
	neverReturned []accesstypes.Field
}

// QuerySet returns the read of the record whose history is asked for: the generated
// handler names the record by setting each key field on it (SetKey).
func (h *ChangeHistoryQuery[Resource]) QuerySet() *QuerySet[Resource] {
	return h.querySet
}

// Read answers a page of the record's change records, newest first. It reads on Spanner
// alone, and on another database answers an error. It first reads the record as the
// record's own route reads it: a caller that route refuses is refused here in its words,
// and any error but the record's not being there is answered as it is. A resource that
// does not track its changes is answered 404 next, so that a caller without the grant is
// told 403 on every resource, tracked or not. Then it decides the fields the reader may
// see: where the record was read, every field the read returned whose cell is not masked
// on the row; where it is not there, none if the grant on the resource carries a
// condition (the not-found of the record's own read is answered, since a condition is
// judged on the row and the row is gone), and otherwise every field the grant covers
// without a condition. A field the record's read never returns is not among them. A
// request that keeps one field's changes is refused 403 where that field is not among
// them. In a tenant's request, on a resource that belongs to tenants, only the records
// written while the row was that tenant's are read; a record that is not there, of a
// resource that belongs to no tenant, is answered as not found in a tenant's request,
// since nothing ties its records to the tenant. Then it reads at most one more record
// than a page holds, older than where the page continues from and naming the field kept
// where one is; a record that is not there and has no record on the newest page is
// answered as not found. Last it shapes the page (ChangeHistoryPage).
func (h *ChangeHistoryQuery[Resource]) Read(ctx context.Context, client Client) (*ChangeHistoryPage, error) {
	if client.DBType() != SpannerDBType {
		return nil, errChangeHistoryUnsupportedDatabase
	}

	q := h.querySet
	row, err := q.Read(ctx, client)
	var notFound error
	if err != nil {
		if !httpio.HasNotFound(err) {
			return nil, err
		}
		notFound = err
	}
	if !q.rMeta.trackChanges {
		return nil, recordNotFound[Resource](string(q.Resource()) + " keeps no change history: its Config does not set TrackChanges")
	}

	visible, err := h.visibleFields(row, notFound)
	if err != nil {
		return nil, err
	}
	if h.field != "" && !slices.Contains(visible, h.field) {
		return nil, NotPermitted(
			errors.Newf("scope (%s), user (%s) may not read %s on this record, so its changes are not listed", q.scope, q.User(), h.fieldName),
			accesstypes.Read, q.resourceSet.Resource(h.field),
		)
	}

	return h.readPage(ctx, client, visible, notFound)
}

// visibleFields answers the fields of the record the reader may see in its history, as
// Go field names in the order the record's read returns them. row is the record as it
// was read, and notFound the error of a record that is not there, nil where it was read.
func (h *ChangeHistoryQuery[Resource]) visibleFields(row *Row[Resource], notFound error) ([]accesstypes.Field, error) {
	q := h.querySet
	if notFound != nil {
		if _, conditional := q.conditionalDecisions[q.resourceSet.BaseResource()]; conditional {
			return nil, notFound
		}
	}

	visible := make([]accesstypes.Field, 0, len(q.Fields()))
	for _, field := range q.Fields() {
		if slices.Contains(h.neverReturned, field) {
			continue
		}
		if notFound == nil {
			if row.Masked(q.jsonName(field)) {
				continue
			}
		} else if _, conditional := q.conditionalDecisions[q.resourceSet.Resource(field)]; conditional {
			continue
		}
		visible = append(visible, field)
	}

	return visible, nil
}

// readPage reads and shapes the page of the record's history that the request asks for,
// with visible the fields the reader may see (visibleFields) and notFound as there.
func (h *ChangeHistoryQuery[Resource]) readPage(ctx context.Context, client Client, visible []accesstypes.Field, notFound error) (*ChangeHistoryPage, error) {
	q := h.querySet
	records := changeRecordsQuery{
		table: string(q.Resource()),
		rowID: q.KeySet().RowID(),
		after: h.after,
		field: h.field,
	}
	if domain, ok := q.scope.Domain(); ok && !q.scope.IsZero() {
		if q.rMeta.tenantBinding != nil {
			records.tenant = string(domain)
		} else if notFound != nil {
			return nil, notFound
		}
	}

	txn := client.ReadOnlyTransaction()
	defer txn.Close()

	rows, err := readChangeRecords(ctx, txn, records)
	if err != nil {
		return nil, err
	}
	if notFound != nil && h.after == nil && len(rows) == 0 {
		return nil, notFound
	}

	return changeHistoryPage(rows, visible, q.jsonName)
}

// ChangeHistoryPage is a page of one record's history, as its route answers it: the
// record's change records, newest first, and where the next page continues from. A
// record's history outlives the record: the change records of a record that was deleted
// are kept, and read as those of one that is there.
type ChangeHistoryPage struct {
	// Changes are at most ChangeHistoryPageSize change records, newest first; never nil.
	Changes []ChangeRecord `json:"changes"`
	// Next is the value of after that reads the page of older records, empty where there
	// are none.
	Next string `json:"next,omitempty"`
}

// ChangeRecord is one change record of a record's history: when the change was made,
// who made it, and each field it changed that the reader may see.
type ChangeRecord struct {
	// At is the time the change was committed.
	At time.Time `json:"at"`
	// Sequence numbers the change records one commit wrote for the record.
	Sequence int64 `json:"sequence"`
	// Source says who made the change.
	Source string `json:"source"`
	// Fields are the changed fields the reader may see; never nil, and empty for a change
	// of fields the reader may not see, which is listed all the same.
	Fields []ChangedField `json:"fields"`
}

// ChangedField is one field a change record says was changed, with its value before and
// after the change as the record keeps them.
type ChangedField struct {
	// Field is the field's JSON name.
	Field string `json:"field"`
	// Old is the value before the change, JSON null where there was none.
	Old json.RawMessage `json:"old"`
	// New is the value after the change, JSON null where there is none.
	New json.RawMessage `json:"new"`
	// Secret says the field holds a secret: it was changed, and neither value is kept.
	Secret bool `json:"secret,omitempty"`
}

// changeHistoryToken is where a page of a record's history continues from: the time and
// the sequence of the last record of the page before, whose older records it reads.
type changeHistoryToken struct {
	at       time.Time
	sequence int64
}

// parseChangeHistoryToken reads the value of after, which a page gave as its next: the
// time in time.RFC3339Nano in UTC and the sequence in decimal, joined by an underscore.
// Anything else is refused 400.
func parseChangeHistoryToken(token string) (changeHistoryToken, error) {
	refused := httpio.NewBadRequestMessagef("The history cannot be continued from %q. Use the value of next from the page before.", token)

	split := strings.LastIndex(token, "_")
	if split < 0 {
		return changeHistoryToken{}, refused
	}
	stamp, digits := token[:split], token[split+1:]
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || at.UTC().Format(time.RFC3339Nano) != stamp {
		return changeHistoryToken{}, refused
	}
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return changeHistoryToken{}, refused
	}
	sequence, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return changeHistoryToken{}, refused
	}

	return changeHistoryToken{at: at, sequence: sequence}, nil
}

// String renders the token as the value of after reads it.
func (t changeHistoryToken) String() string {
	return t.at.UTC().Format(time.RFC3339Nano) + "_" + strconv.FormatInt(t.sequence, 10)
}

// changeRecordsQuery says which change records a page of a record's history reads: the
// record's table and RowId, the tenant they are held to (empty for none), where the page
// continues from (nil for the newest) and the one field they must name (empty for any).
type changeRecordsQuery struct {
	table  string
	rowID  string
	tenant string
	after  *changeHistoryToken
	field  accesstypes.Field
}

// changeRecordRow is a row of the DataChangeEvents table as a record's history reads it,
// the change set as JSON text.
type changeRecordRow struct {
	EventTime   time.Time          `spanner:"EventTime"`
	Sequence    int64              `spanner:"Sequence"`
	EventSource string             `spanner:"EventSource"`
	ChangeSet   spanner.NullString `spanner:"ChangeSet"`
}

// readChangeRecords reads the change records of one record, newest first, one more than
// a page holds so the page knows whether older ones exist. The place a page continues
// from is a range on the time first, so the table's key serves it as a range. The field
// kept is named in the statement by its Go field name, which is how the change set keys
// its entries; nothing from the request is written into the statement's text.
func readChangeRecords(ctx context.Context, txn ReadOnlyTransaction, records changeRecordsQuery) ([]changeRecordRow, error) {
	var sql strings.Builder
	sql.WriteString("SELECT EventTime, Sequence, EventSource, TO_JSON_STRING(ChangeSet) AS ChangeSet\n")
	sql.WriteString("  FROM DataChangeEvents\n")
	sql.WriteString(" WHERE TableName = @table AND RowId = @rowId\n")
	params := map[string]any{"table": records.table, "rowId": records.rowID}
	if records.tenant != "" {
		sql.WriteString("   AND TenantId = @tenant\n")
		params["tenant"] = records.tenant
	}
	if records.after != nil {
		sql.WriteString("   AND EventTime <= @afterTime AND (EventTime < @afterTime OR Sequence < @afterSequence)\n")
		params["afterTime"] = records.after.at
		params["afterSequence"] = records.after.sequence
	}
	if records.field != "" {
		sql.WriteString("   AND JSON_QUERY(ChangeSet, '$." + string(records.field) + "') IS NOT NULL\n")
	}
	sql.WriteString(" ORDER BY EventTime DESC, Sequence DESC\n")
	sql.WriteString(" LIMIT " + strconv.Itoa(ChangeHistoryPageSize+1))

	var rows []changeRecordRow
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, spanner.Statement{SQL: sql.String(), Params: params}); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}

	return rows, nil
}

// changeSetEntry is one field's entry in a change set as the history reads it: each
// value kept as the change set holds it, byte for byte.
type changeSetEntry struct {
	Old    json.RawMessage
	New    json.RawMessage
	Secret bool
}

// changeHistoryPage shapes the rows a read gave, newest first, into a page: at most
// ChangeHistoryPageSize records, Next the token of the last where a further row came
// back, and in each record the entries of the fields in visible, in its order, under
// their JSON names (jsonName). An entry of any other field is left out, and a record
// left with none is listed all the same.
func changeHistoryPage(rows []changeRecordRow, visible []accesstypes.Field, jsonName func(accesstypes.Field) string) (*ChangeHistoryPage, error) {
	page := &ChangeHistoryPage{Changes: make([]ChangeRecord, 0, min(len(rows), ChangeHistoryPageSize))}
	if len(rows) > ChangeHistoryPageSize {
		rows = rows[:ChangeHistoryPageSize]
		last := rows[len(rows)-1]
		page.Next = changeHistoryToken{at: last.EventTime, sequence: last.Sequence}.String()
	}

	for _, row := range rows {
		fields, err := changedFields(row.ChangeSet, visible, jsonName)
		if err != nil {
			return nil, errors.Wrapf(err, "the change record of %s at sequence %d", row.EventTime.UTC().Format(time.RFC3339Nano), row.Sequence)
		}
		page.Changes = append(page.Changes, ChangeRecord{At: row.EventTime, Sequence: row.Sequence, Source: row.EventSource, Fields: fields})
	}

	return page, nil
}

// changedFields answers the entries of one change set for the fields in visible, in its
// order; a value the entry does not hold, or holds as JSON null, is JSON null.
func changedFields(changeSet spanner.NullString, visible []accesstypes.Field, jsonName func(accesstypes.Field) string) ([]ChangedField, error) {
	fields := []ChangedField{}
	if !changeSet.Valid {
		return fields, nil
	}

	var entries map[accesstypes.Field]*changeSetEntry
	if err := json.Unmarshal([]byte(changeSet.StringVal), &entries); err != nil {
		return nil, errors.Wrap(err, "json.Unmarshal()")
	}
	for _, field := range visible {
		entry := entries[field]
		if entry == nil {
			continue
		}
		fields = append(fields, ChangedField{Field: jsonName(field), Old: jsonValue(entry.Old), New: jsonValue(entry.New), Secret: entry.Secret})
	}

	return fields, nil
}

// jsonValue answers a value of a change set as it is kept, JSON null where there is none.
func jsonValue(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return json.RawMessage("null")
	}

	return value
}
