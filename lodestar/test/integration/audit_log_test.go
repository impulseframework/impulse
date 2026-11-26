package integration

// audit_log_test: the crew auth's audit log as its two generated lists serve it, on the
// served stack over the Spanner emulator. Each act on an account, on who holds what or
// on how people sign in is one row of the auth's audit records table, written in the
// transaction of the act, so an act that did not happen has none. The lists read the
// rows back newest first with who acted (the person and whom they acted as; the RPC of
// the generated method that made the act stays in its column), the act and whom it was
// done to in words, which the library writes as each row is read, beside the names the row keeps as they were when the act happened. The whole application's list is Headquarters', and a
// sector's list holds the acts done in the sector and is its marshal's. Both are read
// only: nothing a request sends changes a row.
//
// The acts made through a directory's sign-in, and a directory's own acts, are proven
// beside this file (audit_log_skipauth_test).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/resource"
	"google.golang.org/api/iterator"
)

// The routes of the crew's two lists of its audit log, and the table they read.
const (
	// crewAuditPath is the whole application's list.
	crewAuditPath = "crew-listed-audit-records"
	// crewSectorAuditPath is a sector's list, under the sector.
	crewSectorAuditPath = "crew-sector-audit-records"
	// crewAuditTable is the crew auth's audit records table.
	crewAuditTable = "CrewSessionAuditRecords"
)

// auditLine is what a row of the log says, as a person reads it: the act by its fixed
// name, who acted, the act and whom it was done to in words, and the names the row keeps
// of whom it was done to and of the sector it was done in, with the reason the act
// carried.
type auditLine struct {
	Act, Who, What, DoneTo, SubjectName, TenantName, Reason string
}

// lineOf is what the row of a list says.
func lineOf(row map[string]any) auditLine {
	return auditLine{
		Act: text(row, "act"), Who: text(row, "who"), What: text(row, "what"), DoneTo: text(row, "doneTo"),
		SubjectName: text(row, "subjectName"), TenantName: text(row, "tenantName"), Reason: text(row, "reason"),
	}
}

// auditListPath is the route of the whole application's list where sector is "", and
// of the sector's list otherwise.
func auditListPath(sector string) string {
	if sector == "" {
		return consoleAPI + "/" + crewAuditPath
	}

	return sectorPath(sector, crewSectorAuditPath)
}

// auditRows reads a list of the crew's audit log as the browser's user, filtered by the
// filter where there is one, in the order served.
func auditRows(ctx context.Context, t *testing.T, b *browser, sector, filter string) []map[string]any {
	t.Helper()

	query := "?limit=200"
	if filter != "" {
		query += "&filter=" + url.QueryEscape(filter)
	}
	status, body := b.do(ctx, http.MethodGet, auditListPath(sector)+query, nil)
	assertStatus(t, status, http.StatusOK, body)

	return decodeRows(t, body)
}

// auditRowsOf is the rows of the act done to the subject the rows name by subjectName.
func auditRowsOf(rows []map[string]any, act, subjectName string) []map[string]any {
	var kept []map[string]any
	for _, row := range rows {
		if text(row, "act") == act && text(row, "subjectName") == subjectName {
			kept = append(kept, row)
		}
	}

	return kept
}

// oneAuditRow is the one row of the whole application's list of the act done to the
// subject named subjectName, as the browser's user reads it, and fails the test unless
// the list holds exactly one.
func oneAuditRow(ctx context.Context, t *testing.T, b *browser, act, subjectName string) map[string]any {
	t.Helper()

	rows := auditRowsOf(auditRows(ctx, t, b, "", "act:eq:"+act), act, subjectName)
	if len(rows) != 1 {
		t.Fatalf("the rows of the log of %s done to %s = %v, want one", act, subjectName, rows)
	}

	return rows[0]
}

// auditIDs is the ids of the rows, in their order.
func auditIDs(t *testing.T, rows []map[string]any) []string {
	t.Helper()

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, cell[string](t, row, "id"))
	}

	return ids
}

// storedAuditRow reads the row of the crew's audit records table by its id, straight
// from the database: each column by its name, as Spanner holds it.
func storedAuditRow(ctx context.Context, t *testing.T, s *served, id string) map[string]spanner.GenericColumnValue {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{SQL: "SELECT * FROM " + crewAuditTable + " WHERE Id = @id", Params: map[string]any{"id": id}})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the audit record %s from %s: %v", id, crewAuditTable, err)
	}
	columns := make(map[string]spanner.GenericColumnValue, row.Size())
	for i, name := range row.ColumnNames() {
		var value spanner.GenericColumnValue
		if err := row.Column(i, &value); err != nil {
			t.Fatalf("reading the column %s of the audit record %s: %v", name, id, err)
		}
		columns[name] = value
	}

	return columns
}

// storedText is what a column of a row of the table that holds text holds, "" where it
// holds nothing.
func storedText(t *testing.T, row map[string]spanner.GenericColumnValue, column string) string {
	t.Helper()

	value, ok := row[column]
	if !ok {
		t.Fatalf("the audit records table has no column %s", column)
	}
	var held spanner.NullString
	if err := value.Decode(&held); err != nil {
		t.Fatalf("the column %s of the audit record as text: %v", column, err)
	}

	return held.StringVal
}

// storedAuditRows is every row of the crew's audit records table, straight from the
// database, one line a row of the columns an act writes, by id: two readings that are
// equal mean no row was written, changed or removed between them.
func storedAuditRows(ctx context.Context, t *testing.T, s *served) []string {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{SQL: "SELECT CONCAT(Id, ' ', Act, ' ', IFNULL(Form, ''), ' ', SubjectKind, ' ', SubjectId, ' ', IFNULL(SubjectName, ''), ' ', " +
		"IFNULL(TenantId, ''), ' ', IFNULL(RoleName, ''), ' ', IFNULL(Reason, ''), ' ', CAST(CreatedAt AS STRING)) FROM " + crewAuditTable + " ORDER BY Id"})
	defer iter.Stop()
	var rows []string
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return rows
		}
		if err != nil {
			t.Fatalf("reading %s: %v", crewAuditTable, err)
		}
		var line string
		if err := row.Columns(&line); err != nil {
			t.Fatalf("reading a row of %s: %v", crewAuditTable, err)
		}
		rows = append(rows, line)
	}
}

// auditRequest is the JSON of the request of one of the methods the acts are made by.
func auditRequest(t *testing.T, request any) []byte {
	t.Helper()

	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal(%v): %v", request, err)
	}

	return body
}

// giveAuditedRole gives the person the role in the sector, or for the whole application
// where sector is "", as the browser's user, through the whole application's method,
// and fails the test unless it is given.
func giveAuditedRole(ctx context.Context, t *testing.T, b *browser, userID, role, sector string) {
	t.Helper()

	request := map[string]any{"userId": userID, "role": role}
	if sector != "" {
		request["domain"] = sector
	}
	if status, body := b.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), auditRequest(t, request)); status != http.StatusNoContent {
		t.Fatalf("giving %s the role %s in %q: status %d, want 204: %s", userID, role, sector, status, body)
	}
}

// TestAuditLog_actingAsSomeoneNamesBoth holds who acted in a session that acts as
// another person: the row's who names both, the real person first, and leaves the
// method's RPC to its column, and the table's actor columns hold the real person, as
// every change record of the library does. A
// session that only views writes nothing, so the act is made in the kind that does what
// the person may do. The cadet's own roles hold no act of the log, so the governor first
// makes the cadet Anvil's marshal; he then acts as the cadet and disables an account
// Anvil manages, an act no class of the session's mask refuses.
func TestAuditLog_actingAsSomeoneNamesBoth(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	giveAuditedRole(ctx, t, hq, string(account(t, "cadet")), "SectorMarshal", anvil)
	// The stowage hand holds a role in Anvil and nowhere else, so Anvil manages the account.
	stowageID := s.seedCrewAccount(ctx, t, "stowage", nil).String()
	giveAuditedRole(ctx, t, hq, stowageID, "Cadet", anvil)

	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "governor")
	if status, body := acting.impersonate(ctx, actAs(t, "cadet", "covering the marshal's desk", "")); status != http.StatusOK {
		t.Fatalf("the governor acting as the cadet: status %d, want 200: %s", status, body)
	}
	if status, body := acting.do(ctx, http.MethodPost, sectorPath(anvil, "disable-crew-sector-user"), auditRequest(t, map[string]string{"userId": stowageID})); status != http.StatusNoContent {
		t.Fatalf("the governor, acting as the cadet, disabling the stowage hand at Anvil: status %d, want 204: %s", status, body)
	}

	row := oneAuditRow(ctx, t, hq, "accountDisabled", "stowage")
	want := auditLine{Act: "accountDisabled", Who: "governor, acting as cadet", What: "Disabled the account", DoneTo: "stowage", SubjectName: "stowage", TenantName: "Anvil"}
	if got := lineOf(row); got != want {
		t.Errorf("the row of the account disabled while acting as the cadet = %+v, want %+v", got, want)
	}
	if got := text(row, "actorUsername"); got != "governor" {
		t.Errorf("the row's actorUsername = %q, want governor, the real person", got)
	}
	stored := storedAuditRow(ctx, t, s, cell[string](t, row, "id"))
	for column, want := range map[string]string{"ActorUserId": string(account(t, "governor")), "ActorUsername": "governor", "PrincipalUsername": "cadet", "Rpc": "DisableCrewSectorUser"} {
		if got := storedText(t, stored, column); got != want {
			t.Errorf("the table's %s of the row = %q, want %q", column, got, want)
		}
	}
}

// TestAuditLog_aSectorReadsItsOwnRowsAndNothingChangesOne holds who reads which rows,
// and that the lists are read only. Anvil's marshal reads in Anvil's list exactly the
// rows of the whole log done in Anvil: none of Bastion's and none of an act done in no
// sector. The marshal does not read the whole log, and Bastion's is answered to her as a
// sector that is not there; the cadet, whose roles hold neither list, reads neither. POST on a list's route is the query of the
// list the generator serves by POST, and answers it; PATCH and DELETE on the routes, and
// POST, PATCH and DELETE on a row's address, are answered as a route that is not there.
// None of them writes, changes or removes a row.
func TestAuditLog_aSectorReadsItsOwnRowsAndNothingChangesOne(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	cadetID := string(account(t, "cadet"))
	giveAuditedRole(ctx, t, hq, cadetID, "Pilot", anvil)
	giveAuditedRole(ctx, t, hq, string(account(t, "dispatcher")), "Pilot", bastion)
	giveAuditedRole(ctx, t, hq, cadetID, "BriefingClerk", "")

	whole := auditRows(ctx, t, hq, "", "")
	var inAnvil []string
	for _, row := range whole {
		if text(row, "tenantId") == anvil {
			inAnvil = append(inAnvil, cell[string](t, row, "id"))
		}
	}
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	anvilRows := auditRows(ctx, t, marshal, anvil, "")
	if got := auditIDs(t, anvilRows); !slices.Equal(got, inAnvil) {
		t.Errorf("the rows of Anvil's list as its marshal reads them = %v, want the whole log's rows done in Anvil, %v", got, inAnvil)
	}
	if len(auditRowsOf(anvilRows, "roleGiven", "cadet")) != 1 || len(auditRowsOf(anvilRows, "roleGiven", "dispatcher")) != 0 {
		t.Errorf("Anvil's list = %v, want the cadet's role given in Anvil, and neither the dispatcher's in Bastion nor the cadet's for the whole application", anvilRows)
	}

	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	// A sector the reader holds nothing in is answered as one that is not there.
	for _, refused := range []struct {
		who    string
		by     *browser
		sector string
		want   int
	}{
		{who: "Anvil's marshal", by: marshal, sector: "", want: http.StatusForbidden},
		{who: "Anvil's marshal", by: marshal, sector: bastion, want: http.StatusNotFound},
		{who: "the cadet", by: cadet, sector: "", want: http.StatusForbidden},
		{who: "the cadet", by: cadet, sector: anvil, want: http.StatusForbidden},
	} {
		if status, body := refused.by.do(ctx, http.MethodGet, auditListPath(refused.sector)+"?limit=200", nil); status != refused.want {
			t.Errorf("%s reading the list of the log of %q: status %d, want %d: %s", refused.who, refused.sector, status, refused.want, body)
		}
	}

	before := storedAuditRows(ctx, t, s)
	for _, sector := range []string{"", anvil} {
		reader := hq
		if sector != "" {
			reader = marshal
		}
		path := auditListPath(sector)
		status, body := reader.do(ctx, http.MethodPost, path+"?limit=200", []byte(`{}`))
		assertStatus(t, status, http.StatusOK, body)
		if got, want := auditIDs(t, decodeRows(t, body)), auditIDs(t, auditRows(ctx, t, reader, sector, "")); !slices.Equal(got, want) {
			t.Errorf("POST %s answers the rows %v, want the list's, %v", path, got, want)
		}
		row := path + "/" + auditIDs(t, anvilRows)[0]
		for _, request := range []struct{ method, target string }{
			{method: http.MethodPatch, target: path},
			{method: http.MethodDelete, target: path},
			{method: http.MethodPost, target: row},
			{method: http.MethodPatch, target: row},
			{method: http.MethodDelete, target: row},
		} {
			status, body := reader.do(ctx, request.method, request.target, []byte(`{"reason":"changed"}`))
			if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want no such route: %s", request.method, request.target, status, body)
			}
		}
	}
	if after := storedAuditRows(ctx, t, s); !slices.Equal(after, before) {
		t.Errorf("the audit records table after the requests to the lists' routes = %v, want it as it was, %v", after, before)
	}
}

// TestAuditLog_aRowKeepsItsPartsAndTheNamesOfItsDay holds what a row is made of. A dry
// run of a role given, whose transaction does not commit, leaves no row. The role given
// is one row, whose sentence the list writes from its parts: the table holds the role,
// its place and its period, and no column of it holds the sentence. A name is kept as it
// was: once the person's username changes, the row of the role still says the old one,
// and the change is a row of its own, done to the new username.
func TestAuditLog_aRowKeepsItsPartsAndTheNamesOfItsDay(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	veteranID := string(account(t, "veteran"))
	end := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Minute)
	const reason = "Flying the convoy escort"
	giving := auditRequest(t, map[string]any{"userId": veteranID, "role": "Pilot", "domain": anvil, "endsAt": end.Format(time.RFC3339), "reason": reason})

	// A dry run.
	before := storedAuditRows(ctx, t, s)
	dry := hq.doWith(ctx, http.MethodPost, crewMethod("assign-crew-role"), giving, map[string]string{resource.DryRunHeader: "true"})
	if err := dry.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if dry.StatusCode != http.StatusOK && dry.StatusCode != http.StatusNoContent {
		t.Fatalf("a dry run of the veteran given Pilot at Anvil: status %d, want it answered", dry.StatusCode)
	}
	if after := storedAuditRows(ctx, t, s); !slices.Equal(after, before) {
		t.Errorf("the audit records table after a dry run of a role given = %v, want it as it was, %v", after, before)
	}
	var held int64
	stmt := spanner.Statement{SQL: "SELECT COUNT(*) FROM CrewUserRoles WHERE UserId = @user AND Role = 'Pilot'", Params: map[string]any{"user": veteranID}}
	if err := s.db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		return row.Columns(&held)
	}); err != nil {
		t.Fatalf("counting the veteran's Pilot roles: %v", err)
	}
	if held != 0 {
		t.Fatalf("the veteran holds Pilot %d times after the dry run, want none", held)
	}

	// The role given.
	status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), giving)
	assertStatus(t, status, http.StatusNoContent, body)
	given := oneAuditRow(ctx, t, hq, "roleGiven", "veteran")
	what := "Gave the role Pilot until " + end.Format("2006-01-02 15:04") + " UTC"
	want := auditLine{Act: "roleGiven", Who: "governor", What: what, DoneTo: "veteran", SubjectName: "veteran", TenantName: "Anvil", Reason: reason}
	if got := lineOf(given); got != want {
		t.Errorf("the row of the role given = %+v, want %+v", got, want)
	}
	stored := storedAuditRow(ctx, t, s, cell[string](t, given, "id"))
	for column, want := range map[string]string{"RoleName": "Pilot", "RoleScope": string(auditlog.RoleScopeTenant), "TenantId": anvil, "SubjectName": "veteran", "Reason": reason} {
		if got := storedText(t, stored, column); got != want {
			t.Errorf("the table's %s of the role given = %q, want %q", column, got, want)
		}
	}
	var periodEnd spanner.NullTime
	if err := stored["PeriodEnd"].Decode(&periodEnd); err != nil {
		t.Fatalf("the table's PeriodEnd of the role given: %v", err)
	}
	if !periodEnd.Valid || !periodEnd.Time.Equal(end) {
		t.Errorf("the table's PeriodEnd of the role given = %v, want %v", periodEnd, end)
	}
	for column, value := range stored {
		var single spanner.NullString
		if value.Decode(&single) == nil && strings.Contains(single.StringVal, what) {
			t.Errorf("the table's %s of the role given holds the sentence %q: %q", column, what, single.StringVal)
		}
		var many []spanner.NullString
		if value.Decode(&many) != nil {
			continue
		}
		for _, one := range many {
			if strings.Contains(one.StringVal, what) {
				t.Errorf("the table's %s of the role given holds the sentence %q: %v", column, what, many)
			}
		}
	}

	// The username changes.
	status, body = hq.do(ctx, http.MethodPost, crewMethod("rename-crew-user"), auditRequest(t, map[string]string{"userId": veteranID, "username": "veteran-renamed"}))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := lineOf(oneAuditRow(ctx, t, hq, "roleGiven", "veteran")); got != want {
		t.Errorf("the row of the role given after the username changed = %+v, want it as it was, %+v", got, want)
	}
	renamed := auditLine{Act: "usernameChanged", Who: "governor", What: "Changed the username from veteran to veteran-renamed", DoneTo: "veteran-renamed", SubjectName: "veteran-renamed"}
	if got := lineOf(oneAuditRow(ctx, t, hq, "usernameChanged", "veteran-renamed")); got != renamed {
		t.Errorf("the row of the username changed = %+v, want %+v", got, renamed)
	}
}

// TestAuditLog_filtersAndPages holds the list's filters, each answering exactly the rows
// of the whole log it admits, in the same order: by the act, by the username of the
// person who acted, by the name of whom it was done to, by the role and by a range of
// the time, and by a text match on the name, which the handler applies to the rows. The
// rows a person did include the address their own sign-in wrote. A page of the list links
// the next, which goes on where the first stopped.
func TestAuditLog_filtersAndPages(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	cadetID := string(account(t, "cadet"))
	giveAuditedRole(ctx, t, hq, cadetID, "Pilot", anvil)
	giveAuditedRole(ctx, t, hq, string(account(t, "veteran")), "Engineer", anvil)
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	if status, body := marshal.do(ctx, http.MethodPost, sectorPath(anvil, "assign-crew-sector-role"), auditRequest(t, map[string]string{"userId": cadetID, "role": "Engineer"})); status != http.StatusNoContent {
		t.Fatalf("Anvil's marshal giving the cadet Engineer: status %d, want 204: %s", status, body)
	}
	giveAuditedRole(ctx, t, hq, string(account(t, "dispatcher")), "Pilot", bastion)

	whole := auditRows(ctx, t, hq, "", "")
	given := auditRows(ctx, t, hq, "", "act:eq:roleGiven")
	if len(given) < 4 {
		t.Fatalf("the roles given in the log = %v, want the four", given)
	}
	// at is when the row's act happened.
	at := func(row map[string]any) time.Time {
		t.Helper()

		when, err := time.Parse(time.RFC3339Nano, cell[string](t, row, "at"))
		if err != nil {
			t.Fatalf("the row's at: %v", err)
		}

		return when
	}
	// equal admits a row whose field holds the value, and containing one whose field
	// holds it as a part.
	equal := func(field, value string) func(row map[string]any) bool {
		return func(row map[string]any) bool {
			return text(row, field) == value
		}
	}
	containing := func(field, part string) func(row map[string]any) bool {
		return func(row map[string]any) bool {
			return strings.Contains(text(row, field), part)
		}
	}
	// The range runs from the cadet's Pilot, the first of the four, up to the
	// dispatcher's, the last, which it leaves out.
	from, to := at(auditRowsOf(whole, "roleGiven", "cadet")[1]), at(auditRowsOf(whole, "roleGiven", "dispatcher")[0])
	within := func(row map[string]any) bool {
		return !at(row).Before(from) && at(row).Before(to)
	}
	for _, filter := range []struct {
		filter string
		admits func(row map[string]any) bool
		least  int
	}{
		{filter: "act:eq:roleGiven", admits: equal("act", "roleGiven"), least: 4},
		{filter: "actorUsername:eq:marshal", admits: equal("actorUsername", "marshal"), least: 1},
		{filter: "subjectName:eq:cadet", admits: equal("subjectName", "cadet"), least: 2},
		{filter: "role:eq:Engineer", admits: equal("role", "Engineer"), least: 2},
		{filter: fmt.Sprintf(`at:gte:%q,at:lt:%q`, from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano)), admits: within, least: 3},
		{filter: "subjectName:contains:spatch", admits: containing("subjectName", "spatch"), least: 1},
	} {
		var want []string
		for _, row := range whole {
			if filter.admits(row) {
				want = append(want, cell[string](t, row, "id"))
			}
		}
		got := auditIDs(t, auditRows(ctx, t, hq, "", filter.filter))
		if !slices.Equal(got, want) || len(got) < filter.least {
			t.Errorf("the log filtered by %s = %v, want the %d or more rows of the whole log it admits, %v", filter.filter, got, filter.least, want)
		}
	}
	matched := auditRows(ctx, t, hq, "", "subjectName:contains:spatch")
	dispatched := auditLine{Act: "roleGiven", Who: "governor", What: "Gave the role Pilot", DoneTo: "dispatcher", SubjectName: "dispatcher", TenantName: "Bastion"}
	if len(matched) != 1 || lineOf(matched[0]) != dispatched {
		t.Errorf("the log filtered by a part of the dispatcher's username = %v, want the one row %+v", matched, dispatched)
	}
	// The marshal's own acts, newest first: the role she gave, and the address her sign-in
	// wrote onto her account, which a code proved.
	byMarshal := auditRows(ctx, t, hq, "", "actorUsername:eq:marshal")
	byHer := []auditLine{
		{Act: "roleGiven", Who: "marshal", What: "Gave the role Engineer", DoneTo: "cadet", SubjectName: "cadet", TenantName: "Anvil"},
		{Act: "addressChanged", Who: "marshal, through a sign-in", What: "Set the email address to marshal@mail.example", DoneTo: "marshal", SubjectName: "marshal"},
	}
	got := make([]auditLine, 0, len(byMarshal))
	for _, row := range byMarshal {
		got = append(got, lineOf(row))
	}
	if !slices.Equal(got, byHer) {
		t.Errorf("the log filtered by the marshal's username = %+v, want %+v", got, byHer)
	}

	// A page of two links the next, which goes on where the first stopped.
	first := hq.doResponse(ctx, http.MethodGet, auditListPath("")+"?limit=2", nil)
	firstBody, err := io.ReadAll(first.Body)
	if closeErr := first.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("reading the first page: %v", err)
	}
	assertStatus(t, first.StatusCode, http.StatusOK, firstBody)
	next, ok := linkRelations(t, first.Header.Get(resource.LinkHeader))["next"]
	if !ok {
		t.Fatalf("the first page of two carries no next relation: %q", first.Header.Get(resource.LinkHeader))
	}
	status, secondBody := hq.do(ctx, http.MethodGet, next, nil)
	assertStatus(t, status, http.StatusOK, secondBody)
	paged := append(auditIDs(t, decodeRows(t, firstBody)), auditIDs(t, decodeRows(t, secondBody))...)
	if want := auditIDs(t, whole[:4]); !slices.Equal(paged, want) {
		t.Errorf("the rows of the first two pages of two = %v, want the whole log's first four, %v", paged, want)
	}
}
