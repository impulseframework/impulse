// Demonstrates: rpc.dry-run.
package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// TestDryRun drives HoldMission and InspectShip under X-Dry-Run: the whole frame and
// the body run, then the transaction rolls back. A caller the real call would admit
// gets 200 with no body and nothing changes; a caller the real call would refuse gets
// the same refusal, here the Flight Lead's notes refusal from inside the transaction.
func TestDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		user       accesstypes.User
		method     string
		body       string
		wantStatus int
		// column and key name the row the dry run must leave as seeded.
		table, column string
		key           string
		wantRefusal   string
	}{
		{name: "the marshal's hold would commit", user: "marshal", method: "hold-mission", body: `{"missionId":%q,"reason":"debris"}`, wantStatus: http.StatusOK, table: "Missions", column: "StatusId", key: missionConvoyID},
		{name: "the flight lead's hold would be refused on the notes", user: "lead", method: "hold-mission", body: `{"missionId":%q,"reason":"debris"}`, wantStatus: http.StatusForbidden, table: "Missions", column: "StatusId", key: missionConvoyID, wantRefusal: "does not have (Update) on [Missions]"},
		{name: "an inspection would commit and answers with no body", user: "engineer", method: "inspect-ship", body: `{"refitId":%q}`, wantStatus: http.StatusOK, table: "Refits", column: "StatusId", key: refitLanternID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, db, h := demoWorld(t)
			before := readColumn[string](ctx, t, db, tt.table, spanner.Key{tt.key}, tt.column)

			status, body := doDryRunAs(t, h, tt.user, sectorPath(anvil, tt.method), fmt.Sprintf(tt.body, tt.key))
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantRefusal != "" && !strings.Contains(string(body), tt.wantRefusal) {
				t.Errorf("refusal = %s, want it to contain %q", body, tt.wantRefusal)
			}
			if status == http.StatusOK && strings.TrimSpace(string(body)) != "null" && strings.TrimSpace(string(body)) != "" {
				t.Errorf("a dry run that would succeed answers with no body, got %s", body)
			}
			if after := readColumn[string](ctx, t, db, tt.table, spanner.Key{tt.key}, tt.column); after != before {
				t.Errorf("%s.%s = %q after the dry run, want %q: nothing commits", tt.table, tt.column, after, before)
			}
		})
	}
}

// doDryRunAs posts the body under the dry-run header as the user.
func doDryRunAs(t *testing.T, h http.Handler, user accesstypes.User, target, body string) (statusCode int, respBody []byte) {
	t.Helper()

	ctx := sessionContext(t, user)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(resource.DryRunHeader, "true")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	return rr.Code, rr.Body.Bytes()
}

// TestHeadersTakeTrueOrAreAbsent sends each kind of generated method frame a request
// whose X-Dry-Run or X-Warning header says something other than true. The two headers
// ask a frame for something in place of the call, so a value a frame does not take is
// refused, 400 naming the header and the value it takes, and the call is not run: a
// client that sends "1" to rehearse a write is told so and writes nothing.
func TestHeadersTakeTrueOrAreAbsent(t *testing.T) {
	t.Parallel()

	brief := uploadFile{name: "brief.pdf", contentType: "application/pdf", content: []byte("%PDF-1.7 escort brief")}
	headers := []string{resource.DryRunHeader, resource.WarningHeader}
	values := []string{"1", "false", "yes", ""}

	t.Run("a method that runs in a transaction is refused and writes nothing", func(t *testing.T) {
		t.Parallel()

		ctx, db, h := demoWorld(t)
		before := readColumn[string](ctx, t, db, "Missions", spanner.Key{missionConvoyID}, "StatusId")
		for _, header := range headers {
			for _, value := range values {
				status, body := doHeaderAs(t, h, "marshal", sectorPath(anvil, "hold-mission"), "application/json", strings.NewReader(fmt.Sprintf(`{"missionId":%q,"reason":"debris"}`, missionConvoyID)), header, value)
				assertHeaderRefused(t, status, body, header, value)
			}
		}
		if after := readColumn[string](ctx, t, db, "Missions", spanner.Key{missionConvoyID}, "StatusId"); after != before {
			t.Errorf("Missions.StatusId = %q after the refused calls, want %q: a refused call writes nothing", after, before)
		}
	})

	t.Run("a method that runs outside a transaction is refused by the header's own rule", func(t *testing.T) {
		t.Parallel()

		_, h, _ := sharedWorld(t)
		for _, header := range headers {
			for _, value := range values {
				status, body := doHeaderAs(t, h, "marshal", sectorPath(anvil, "compile-briefing"), "application/json", strings.NewReader(`{}`), header, value)
				assertHeaderRefused(t, status, body, header, value)
			}
		}
	})

	t.Run("an upload is refused and stores nothing", func(t *testing.T) {
		t.Parallel()

		h, stores := documentWorld(t)
		for _, header := range headers {
			for _, value := range values {
				form, contentType := multipartBody(t, fmt.Sprintf(`{"missionId":%q,"title":"Escort brief"}`, missionConvoyID), brief)
				status, body := doHeaderAs(t, h, "marshal", sectorPath(anvil, "attach-mission-document"), contentType, form, header, value)
				assertHeaderRefused(t, status, body, header, value)
			}
		}
		if stored := stores.documents.Keys(); len(stored) != 0 {
			t.Errorf("the Documents store holds %v after the refused uploads, want nothing", stored)
		}
		if rows := readRows(t, h, "marshal", sectorPath(anvil, "mission-documents")); len(rows) != 0 {
			t.Errorf("the mission has %d documents after the refused uploads, want none", len(rows))
		}
	})
}

// doHeaderAs posts the body as the user with one header set to the value.
func doHeaderAs(t *testing.T, h http.Handler, user accesstypes.User, target, contentType string, body io.Reader, header, value string) (statusCode int, respBody []byte) {
	t.Helper()

	ctx := sessionContext(t, user)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(header, value)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	return rr.Code, rr.Body.Bytes()
}

// assertHeaderRefused fails unless the answer is the refusal of a header value a frame
// does not take: 400, naming the header, the value it takes and what was sent.
func assertHeaderRefused(t *testing.T, status int, body []byte, header, value string) {
	t.Helper()

	want := fmt.Sprintf("the %s header takes the value true, or is left off the request: %q is not a value it takes, so the call was not run", header, value)
	if status != http.StatusBadRequest || !strings.Contains(string(body), strings.ReplaceAll(want, `"`, `\"`)) {
		t.Errorf("%s: %q answered %d %s, want 400 saying %q", header, value, status, body, want)
	}
}
