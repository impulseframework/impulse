package identity

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
)

func (f *fakeStore) AuditRecords(_ context.Context, _ resource.ReadOnlyTransaction, query *AuditQuery) iter.Seq2[*auditlog.Stored, error] {
	f.auditAsked = append(f.auditAsked, query)

	return func(yield func(*auditlog.Stored, error) bool) {
		if err := f.fail("AuditRecords"); err != nil {
			yield(nil, err)

			return
		}
		for i := range f.auditRows {
			if !yield(&f.auditRows[i], nil) {
				return
			}
		}
	}
}

// auditTable is the audit records table the statements of the tests read.
const auditTable = "CrewSessionAuditRecords"

// auditSelect is the start of every statement: every column of the table.
var auditSelect = "SELECT " + strings.Join(auditlog.Columns(), ", ") + " FROM "

// TestAuditStatement pins the statement a list of the audit log reads the table with:
// every column; each of the ten comparisons on a field and the two that find a row by
// any of its roles; the order; a first page and a page after a boundary in each
// direction, by the time and by the role, a NULL role among them; a tenant's rows; the
// index named in each of the three cases; and every value bound as a parameter.
func TestAuditStatement(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC)
	id := impulse.Must(impulse.UUIDFromString("e1111111-1111-4111-8111-111111111111"))
	byAt := []resource.SortField{{Field: "At", Direction: resource.SortDescending}, {Field: "ID", Direction: resource.SortDescending}}
	byAtBack := []resource.SortField{{Field: "At", Direction: resource.SortAscending}, {Field: "ID", Direction: resource.SortAscending}}
	byRole := []resource.SortField{{Field: "Role", Direction: resource.SortAscending}, {Field: "ID", Direction: resource.SortAscending}}
	byRoleBack := []resource.SortField{{Field: "Role", Direction: resource.SortDescending}, {Field: "ID", Direction: resource.SortDescending}}
	condition := func(field, operator string, value any) resource.Condition {
		return resource.Condition{Field: field, Operator: operator, Value: value}
	}
	list := func(field, operator string, values ...any) resource.Condition {
		return resource.Condition{Field: field, Operator: operator, Values: values}
	}
	null := func(field, operator string) resource.Condition {
		return resource.Condition{Field: field, Operator: operator, IsNullOp: true}
	}
	tests := []struct {
		name       string
		query      AuditQuery
		page       auditPage
		wantSQL    string
		wantParams map[string]any
	}{
		{name: "no filter and no page", wantSQL: auditSelect + auditTable, wantParams: map[string]any{}},
		{
			name: "equal", query: AuditQuery{Conditions: []resource.Condition{condition("Act", "eq", "roleGiven")}},
			wantSQL: auditSelect + auditTable + " WHERE Act = @f0", wantParams: map[string]any{"f0": "roleGiven"},
		},
		{
			name: "not equal", query: AuditQuery{Conditions: []resource.Condition{condition("ActorUsername", "ne", "alice")}},
			wantSQL: auditSelect + auditTable + " WHERE ActorUsername != @f0", wantParams: map[string]any{"f0": "alice"},
		},
		{
			name: "after and before a time", query: AuditQuery{Conditions: []resource.Condition{condition("At", "gt", at), condition("At", "lte", at)}},
			wantSQL: auditSelect + auditTable + " WHERE CreatedAt > @f0 AND CreatedAt <= @f1", wantParams: map[string]any{"f0": at, "f1": at},
		},
		{
			name: "at or after and before", query: AuditQuery{Conditions: []resource.Condition{condition("At", "gte", at), condition("At", "lt", at)}},
			wantSQL: auditSelect + auditTable + " WHERE CreatedAt >= @f0 AND CreatedAt < @f1", wantParams: map[string]any{"f0": at, "f1": at},
		},
		{
			name: "one of and none of", query: AuditQuery{Conditions: []resource.Condition{list("Act", "in", "roleGiven", "roleTaken"), list("SubjectName", "notin", "bob")}},
			wantSQL:    auditSelect + auditTable + " WHERE Act IN UNNEST(@f0) AND SubjectName NOT IN UNNEST(@f1)",
			wantParams: map[string]any{"f0": []string{"roleGiven", "roleTaken"}, "f1": []string{"bob"}},
		},
		{
			name: "none and some", query: AuditQuery{Conditions: []resource.Condition{null("SubjectName", "isnull"), null("ActorUsername", "isnotnull")}},
			wantSQL: auditSelect + auditTable + " WHERE SubjectName IS NULL AND ActorUsername IS NOT NULL", wantParams: map[string]any{},
		},
		{
			name: "a role, by any of a row's roles", query: AuditQuery{Conditions: []resource.Condition{condition("Role", "eq", "Editor")}},
			wantSQL: auditSelect + auditTable + " WHERE (RoleName = @f0 OR @f0 IN UNNEST(OtherRoleNames))", wantParams: map[string]any{"f0": "Editor"},
		},
		{
			name: "one of the roles, by any of a row's roles", query: AuditQuery{Conditions: []resource.Condition{list("Role", "in", "Editor", "Viewer")}},
			wantSQL:    auditSelect + auditTable + " WHERE (RoleName IN UNNEST(@f0) OR EXISTS (SELECT 1 FROM UNNEST(OtherRoleNames) AS n WHERE n IN UNNEST(@f0)))",
			wantParams: map[string]any{"f0": []string{"Editor", "Viewer"}},
		},
		{
			name: "none of the roles, by the first role", query: AuditQuery{Conditions: []resource.Condition{list("Role", "notin", "Editor"), null("Role", "isnotnull")}},
			wantSQL: auditSelect + auditTable + " WHERE RoleName NOT IN UNNEST(@f0) AND RoleName IS NOT NULL", wantParams: map[string]any{"f0": []string{"Editor"}},
		},
		{
			name: "the order by time, through the index by time", query: AuditQuery{Order: byAt},
			wantSQL: auditSelect + auditTable + "@{FORCE_INDEX=" + auditTable + "ByCreatedAt} ORDER BY CreatedAt DESC, Id DESC", wantParams: map[string]any{},
		},
		{
			name: "a first page by time", query: AuditQuery{Order: byAt}, page: auditPage{paged: true, boundary: nil, fetch: 26},
			wantSQL: auditSelect + auditTable + "@{FORCE_INDEX=" + auditTable + "ByCreatedAt} ORDER BY CreatedAt DESC, Id DESC LIMIT @fetch", wantParams: map[string]any{"fetch": int64(26)},
		},
		{
			name: "the next page by time", query: AuditQuery{Conditions: []resource.Condition{condition("Act", "eq", "roleGiven")}, Order: byAt}, page: auditPage{paged: true, boundary: []any{at, id}, fetch: 26},
			wantSQL: auditSelect + auditTable + "@{FORCE_INDEX=" + auditTable + "ByCreatedAt} WHERE Act = @f0 AND ((CreatedAt < @c0) OR (CreatedAt = @c0 AND Id < @c1))" +
				" ORDER BY CreatedAt DESC, Id DESC LIMIT @fetch",
			wantParams: map[string]any{"f0": "roleGiven", "c0": at, "c1": id, "fetch": int64(26)},
		},
		{
			name: "the page before, by time", query: AuditQuery{Order: byAtBack}, page: auditPage{paged: true, boundary: []any{at, id}, fetch: 26},
			wantSQL: auditSelect + auditTable + "@{FORCE_INDEX=" + auditTable + "ByCreatedAt} WHERE ((CreatedAt > @c0) OR (CreatedAt = @c0 AND Id > @c1))" +
				" ORDER BY CreatedAt ASC, Id ASC LIMIT @fetch",
			wantParams: map[string]any{"c0": at, "c1": id, "fetch": int64(26)},
		},
		{
			name: "a first page by role, with no hint", query: AuditQuery{Order: byRole}, page: auditPage{paged: true, boundary: nil, fetch: 2},
			wantSQL: auditSelect + auditTable + " ORDER BY RoleName ASC, Id ASC LIMIT @fetch", wantParams: map[string]any{"fetch": int64(2)},
		},
		{
			name: "the next page by role, after a role", query: AuditQuery{Order: byRole}, page: auditPage{paged: true, boundary: []any{"Editor", id}, fetch: 26},
			wantSQL:    auditSelect + auditTable + " WHERE ((RoleName > @c0) OR (RoleName = @c0 AND Id > @c1)) ORDER BY RoleName ASC, Id ASC LIMIT @fetch",
			wantParams: map[string]any{"c0": "Editor", "c1": id, "fetch": int64(26)},
		},
		{
			name: "the next page by role, after a row with none: every row with a role follows", query: AuditQuery{Order: byRole}, page: auditPage{paged: true, boundary: []any{nil, id}, fetch: 26},
			wantSQL:    auditSelect + auditTable + " WHERE ((RoleName IS NOT NULL) OR (RoleName IS NULL AND Id > @c1)) ORDER BY RoleName ASC, Id ASC LIMIT @fetch",
			wantParams: map[string]any{"c1": id, "fetch": int64(26)},
		},
		{
			name: "the page before by role, after a role: the rows with none follow", query: AuditQuery{Order: byRoleBack}, page: auditPage{paged: true, boundary: []any{"Editor", id}, fetch: 26},
			wantSQL:    auditSelect + auditTable + " WHERE (((RoleName < @c0 OR RoleName IS NULL)) OR (RoleName = @c0 AND Id < @c1)) ORDER BY RoleName DESC, Id DESC LIMIT @fetch",
			wantParams: map[string]any{"c0": "Editor", "c1": id, "fetch": int64(26)},
		},
		{
			name: "the page before by role, after a row with none", query: AuditQuery{Order: byRoleBack}, page: auditPage{paged: true, boundary: []any{nil, id}, fetch: 26},
			wantSQL:    auditSelect + auditTable + " WHERE ((RoleName IS NULL AND Id < @c1)) ORDER BY RoleName DESC, Id DESC LIMIT @fetch",
			wantParams: map[string]any{"c1": id, "fetch": int64(26)},
		},
		{
			name: "a tenant's rows, through the index by tenant", query: AuditQuery{Tenant: "anvil", Conditions: []resource.Condition{condition("Act", "eq", "roleGiven")}, Order: byAt},
			page:       auditPage{paged: true, boundary: nil, fetch: 26},
			wantSQL:    auditSelect + auditTable + "@{FORCE_INDEX=" + auditTable + "ByTenant} WHERE TenantId = @tenant AND Act = @f0 ORDER BY CreatedAt DESC, Id DESC LIMIT @fetch",
			wantParams: map[string]any{"tenant": "anvil", "f0": "roleGiven", "fetch": int64(26)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, params, err := auditStatement(auditTable, &tt.query, tt.page)
			if err != nil {
				t.Fatalf("auditStatement() error = %v", err)
			}
			if sql != tt.wantSQL {
				t.Errorf("auditStatement() sql =\n%s\nwant\n%s", sql, tt.wantSQL)
			}
			if diff := cmp.Diff(tt.wantParams, params); diff != "" {
				t.Errorf("auditStatement() params mismatch (-want +got):\n%s", diff)
			}
			for _, value := range []string{"roleGiven", "roleTaken", "alice", "bob", "Editor", "Viewer", "anvil", "2026"} {
				if strings.Contains(sql, value) {
					t.Errorf("auditStatement() writes the value %q into the text: %s", value, sql)
				}
			}
		})
	}
}

// TestAuditStatement_refusals pins what the statement refuses as an error for a
// developer: a field it does not filter or sort by, an operator it does not render, and
// a boundary that holds no time.
func TestAuditStatement_refusals(t *testing.T) {
	t.Parallel()

	id := impulse.Must(impulse.UUIDFromString("e1111111-1111-4111-8111-111111111111"))
	tests := []struct {
		name    string
		query   AuditQuery
		page    auditPage
		wantErr string
	}{
		{name: "a filter on who", query: AuditQuery{Conditions: []resource.Condition{{Field: "Who", Operator: "eq", Value: "alice"}}}, wantErr: "the audit log is not filtered by Who"},
		{name: "a filter on the id", query: AuditQuery{Conditions: []resource.Condition{{Field: "ID", Operator: "eq", Value: id}}}, wantErr: "the audit log is not filtered by ID"},
		{name: "a text match", query: AuditQuery{Conditions: []resource.Condition{{Field: "ActorUsername", Operator: "contains", Value: "ali"}}}, wantErr: "the operator contains is not rendered on ActorUsername"},
		{name: "an order by what", query: AuditQuery{Order: []resource.SortField{{Field: "What", Direction: resource.SortAscending}}}, wantErr: "the audit log is not sorted by What"},
		{
			name:    "a boundary with no time",
			query:   AuditQuery{Order: []resource.SortField{{Field: "At", Direction: resource.SortDescending}, {Field: "ID", Direction: resource.SortDescending}}},
			page:    auditPage{paged: true, boundary: []any{nil, id}, fetch: 26},
			wantErr: "the page's boundary holds no At, which every row holds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := auditStatement(auditTable, &tt.query, tt.page); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("auditStatement() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// auditListRow is a list of the audit log as the generated management declares it, as
// far as TakeAuditQuery reads it: the fields it filters and sorts by, and Who, which it
// does not.
type auditListRow struct {
	ID            impulse.UUID
	At            time.Time
	Act           string
	Who           string
	ActorUsername *string
	SubjectName   *string
	Role          *string
}

func (auditListRow) Resource() accesstypes.Resource { return "CrewListedAuditRecords" }

// auditListRequest is the list's request as the generated handler declares it: the
// filterable fields, the text ones searched in memory as every computed list's are.
type auditListRequest struct {
	ID            impulse.UUID `json:"id"            perm:"-"`
	At            time.Time    `json:"at"            allow_filter:"true"`
	Act           string       `json:"act"           allow_filter:"true" search:"memory"`
	Who           string       `json:"who"`
	ActorUsername *string      `json:"actorUsername" allow_filter:"true" search:"memory"`
	SubjectName   *string      `json:"subjectName"   allow_filter:"true" search:"memory"`
	Role          *string      `json:"role"          allow_filter:"true" search:"memory"`
}

// auditListQuery is the list's query set for a request's query string, decoded as the
// generated handler decodes one: newest first, 25 rows a page and at most 200.
func auditListQuery(t *testing.T, query string) *resource.QuerySet[auditListRow] {
	t.Helper()

	set, err := resource.NewSet[auditListRow, auditListRequest](accesstypes.List)
	if err != nil {
		t.Fatalf("resource.NewSet() error = %v", err)
	}
	decoder, err := resource.NewQueryDecoder[auditListRow, auditListRequest](set)
	if err != nil {
		t.Fatalf("resource.NewQueryDecoder() error = %v", err)
	}
	decoder = decoder.WithPaging(resource.Paging{Order: []resource.SortField{{Field: "At", Direction: resource.SortDescending}}, DefaultLimit: 25, MaxLimit: 200})
	qSet, err := decoder.DecodeWithoutPermissions(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/crew-listed-audit-records?"+query, http.NoBody))
	if err != nil {
		t.Fatalf("DecodeWithoutPermissions(%q) error = %v", query, err)
	}

	return qSet
}

// TestTakeAuditQuery pins what a list of the audit log takes from its query: the
// conditions on the fields the statement renders, where every condition on the field is
// one of the ten comparisons; the order where it names only the time, the role and the
// id; and then the page, where nothing is left to the handler and no total is asked.
func TestTakeAuditQuery(t *testing.T) {
	t.Parallel()

	byAt := []resource.SortField{{Field: "At", Direction: resource.SortDescending}, {Field: "ID", Direction: resource.SortAscending}}
	byRole := []resource.SortField{{Field: "Role", Direction: resource.SortAscending}, {Field: "ID", Direction: resource.SortAscending}}
	tests := []struct {
		name           string
		query          string
		tenant         accesstypes.Domain
		wantConditions []string
		wantOrder      []resource.SortField
		wantFetch      uint64 // 0 for no page taken
	}{
		{
			name: "a filter on the act and the role, newest first, one page", query: "filter=act:eq:roleGiven,role:eq:Editor",
			wantConditions: []string{"Act eq roleGiven", "Role eq Editor"}, wantOrder: byAt, wantFetch: 26,
		},
		{
			name: "the role filter's own question, taken whole with its page", query: "filter=role:notin:(Editor)&sort=role&limit=1",
			wantConditions: []string{"Role notin [Editor]"}, wantOrder: byRole, wantFetch: 2,
		},
		{
			name: "an order by who takes neither the order nor the page", query: "filter=act:eq:roleGiven&sort=who",
			wantConditions: []string{"Act eq roleGiven"},
		},
		{
			name: "a request for a total takes no page", query: "count=true", tenant: "anvil",
			wantOrder: byAt,
		},
		{
			name: "a text match on who is left to the handler, and so is the page", query: "filter=actorUsername:contains:ali,act:eq:roleGiven",
			wantConditions: []string{"Act eq roleGiven"}, wantOrder: byAt,
		},
		{
			name: "a field with a text match is left whole", query: "filter=subjectName:eq:bob,subjectName:startswith:b",
			wantOrder: byAt,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := TakeAuditQuery(auditListQuery(t, tt.query), tt.tenant)
			if got.Tenant != tt.tenant {
				t.Errorf("TakeAuditQuery().Tenant = %q, want %q", got.Tenant, tt.tenant)
			}
			if diff := cmp.Diff(tt.wantConditions, conditionsWords(got.Conditions)); diff != "" {
				t.Errorf("TakeAuditQuery().Conditions mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOrder, got.Order); diff != "" {
				t.Errorf("TakeAuditQuery().Order mismatch (-want +got):\n%s", diff)
			}
			switch {
			case tt.wantFetch == 0 && got.Page != nil:
				t.Errorf("TakeAuditQuery().Page fetches %d, want no page taken", got.Page.Fetch())
			case tt.wantFetch != 0 && (got.Page == nil || got.Page.Fetch() != tt.wantFetch || got.Page.Boundary() != nil):
				t.Errorf("TakeAuditQuery().Page = %+v, want a first page of %d", got.Page, tt.wantFetch)
			}
		})
	}
}

// conditionsWords writes each condition as its field, its operator and its value.
func conditionsWords(conditions []resource.Condition) []string {
	if len(conditions) == 0 {
		return nil
	}
	words := make([]string, 0, len(conditions))
	for _, c := range conditions {
		value := fmt.Sprint(c.Value)
		if c.Values != nil {
			value = fmt.Sprint(c.Values)
		}
		words = append(words, c.Field+" "+c.Operator+" "+value)
	}

	return words
}

// TestAuditStatement_firstPageTaken pins the statement of the first page a list takes
// through the library's own page: newest first, the page and one more.
func TestAuditStatement_firstPageTaken(t *testing.T) {
	t.Parallel()

	sql, params, err := AuditStatement(auditTable, TakeAuditQuery(auditListQuery(t, "limit=10"), ""))
	if err != nil {
		t.Fatalf("AuditStatement() error = %v", err)
	}
	if want := auditSelect + auditTable + "@{FORCE_INDEX=" + auditTable + "ByCreatedAt} ORDER BY CreatedAt DESC, Id ASC LIMIT @fetch"; sql != want {
		t.Errorf("AuditStatement() sql =\n%s\nwant\n%s", sql, want)
	}
	if diff := cmp.Diff(map[string]any{"fetch": int64(11)}, params); diff != "" {
		t.Errorf("AuditStatement() params mismatch (-want +got):\n%s", diff)
	}
}

// TestManager_AuditRecords pins the Manager's read of the log: it asks the store the
// query it was handed and yields each row the store answers with what a person reads of
// it, in the words the Manager holds for a tenant.
func TestManager_AuditRecords(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	store.auditRows = []auditlog.Stored{
		{
			Act: auditlog.ActRoleGiven, Source: eventsource.Stored{ActorUsername: "grace"}, SubjectKind: auditlog.SubjectAccount,
			SubjectID: "b0b00000-0000-4000-8000-000000000002", SubjectName: "bob", RoleNames: []string{"NightShiftLead"}, RoleScope: auditlog.RoleScopeEveryTenant,
		},
		{Act: auditlog.ActSecondFactorRequired, Source: eventsource.Stored{Process: auditlog.ProcessApplication}, SubjectKind: auditlog.SubjectSetting, SubjectID: auditlog.SettingSecondFactor},
	}
	m, _ := newManager(t, store, nil, WithTenantWord("sector"))
	query := &AuditQuery{Tenant: "anvil"}

	var got []auditlog.Sentences
	for line, err := range m.AuditRecords(t.Context(), nil, query) {
		if err != nil {
			t.Fatalf("AuditRecords() error = %v", err)
		}
		got = append(got, line.Reads)
	}
	want := []auditlog.Sentences{
		{Who: "grace", What: "Gave the role Night shift lead in every sector", DoneTo: "bob"},
		{Who: "the application", What: "Required a second factor of everyone", DoneTo: "Second factor"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("AuditRecords() mismatch (-want +got):\n%s", diff)
	}
	if len(store.auditAsked) != 1 || store.auditAsked[0] != query {
		t.Errorf("the store was asked %v, want the query once", store.auditAsked)
	}

	store.failOn = "AuditRecords"
	for _, err := range m.AuditRecords(t.Context(), nil, query) {
		if err == nil {
			t.Errorf("AuditRecords() of a store that fails yielded a row, want the error")
		}
	}
}
