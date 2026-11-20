package integration

// impersonation_ops_test (design plan §9): the watch desk lists a minted session with its
// two-hour cap, revocation refuses its next request, a forged write on a read-only
// session is refused by the middleware before any handler runs, and the desk itself is
// gated by the grants on what it reads and runs: the command staff hold them, so the
// marshal keeps the desk as the governor does, and a cadet does not. The desk is the
// library's list over the crew auth's generated impersonation records, and its Revoke
// the generated method that ends one, so the suite asks what the page asks. A revoke
// from the desk reaches the desk another person has open.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// The watch desk's two requests: the list of the impersonation records, and the method
// that ends the session of one.
const (
	watchDeskList   = consoleAPI + "/crew-session-impersonations"
	watchDeskRevoke = consoleAPI + "/end-crew-session-impersonation"
)

// watchDeskListChanged is the change document an open watch desk is sent when a record
// it lists is written: the list of the impersonation records, which no tenant holds.
const watchDeskListChanged = "list|CrewSessionImpersonations|||false"

// watchDeskRow finds the record of one session in the watch desk's list, as the given
// browser reads it.
func watchDeskRow(ctx context.Context, t *testing.T, operator *browser, sessionID string) map[string]any {
	t.Helper()

	status, body := operator.do(ctx, http.MethodGet, watchDeskList+"?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "sessionId") == sessionID {
			return row
		}
	}
	t.Fatalf("the watch desk does not list the session %s: %s", sessionID, body)

	return nil
}

// instant reads one cell of a row as the time it names.
func instant(t *testing.T, row map[string]any, key string) time.Time {
	t.Helper()

	at, err := time.Parse(time.RFC3339Nano, cell[string](t, row, key))
	if err != nil {
		t.Fatalf("cell %q of row %v is no time: %v", key, row, err)
	}

	return at
}

// Demonstrates: impersonation.active-list, impersonation.revoke, impersonation.max-duration, impersonation.read-only-backstop.
func TestImpersonationOperator(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	// The marshal views as Cass: the minted session replaces the marshal's cookie.
	maren := newBrowser(t, s, consoleAPI)
	maren.signIn(ctx, "marshal")
	status, body := maren.impersonate(ctx, viewAs(t, "cadet", "watch desk walkthrough"))
	assertStatus(t, status, http.StatusOK, body)
	minted := cell[string](t, decodeRow(t, body), "sessionId")
	revoke := fmt.Appendf(nil, `{"sessionId":%q}`, minted)

	// The governor runs the watch desk from a session of their own.
	greer := newBrowser(t, s, consoleAPI)
	greer.signIn(ctx, "governor")

	t.Run("the desk lists the live session with its actor, principal, and two-hour cap", func(t *testing.T) {
		found := watchDeskRow(ctx, t, greer, minted)
		// The record names the marshal by username and the cadet by the id of the
		// account, which the desk's As column reads the username from: the list of the
		// crew's accounts answers that id as the cadet's.
		if actor, kind := cell[string](t, found, "actorUsername"), cell[string](t, found, "principalKind"); actor != "marshal" || kind != "User" {
			t.Errorf("record = %v, want marshal holding a session as a user", found)
		}
		if principal, cadet := cell[string](t, found, "principalUserId"), crewAccountID(ctx, t, greer, "cadet"); principal != cadet {
			t.Errorf("principalUserId = %s, want the cadet's account %s", principal, cadet)
		}
		if reason := cell[string](t, found, "reason"); reason != "watch desk walkthrough" {
			t.Errorf("reason = %q, want the one the marshal gave", reason)
		}
		if found["endedAt"] != nil {
			t.Errorf("endedAt = %v, want none while the session stands", found["endedAt"])
		}
		// StartedAt is the database's commit time and ExpiresAt the mint route's own
		// deadline, two hours from its clock a moment before the commit, so the
		// lifetime falls short of the cap by the commit's latency and never exceeds it.
		if lifetime := instant(t, found, "expiresAt").Sub(instant(t, found, "startedAt")); lifetime > 2*time.Hour || lifetime < 2*time.Hour-5*time.Second {
			t.Errorf("lifetime = %v, want the two-hour cap the mint route sets, less the commit's latency", lifetime)
		}
	})

	t.Run("a forged write on the read-only session is stopped by the middleware", func(t *testing.T) {
		status, body := maren.do(ctx, http.MethodPost, sectorPath(anvil, "claim-mission"), fmt.Appendf(nil, `{"missionId":%q,"squadronId":%q}`, missionHaulerID, squadronTongsID))
		assertStatus(t, status, http.StatusForbidden, body)
		if want := "this session is read-only"; !strings.Contains(string(body), want) {
			t.Errorf("refusal = %s, want the backstop's %q", body, want)
		}
		// The read-only session still reads.
		status, body = maren.do(ctx, http.MethodGet, sectorPath(anvil, "missions"), nil)
		assertStatus(t, status, http.StatusOK, body)
	})

	t.Run("the marshal keeps the desk as the governor does, and reads who a session is held as", func(t *testing.T) {
		// The marshal's own session, in another browser than the one that views as Cass.
		mara := newBrowser(t, s, consoleAPI)
		mara.signIn(ctx, "marshal")
		found := watchDeskRow(ctx, t, mara, minted)
		if actor, reason := cell[string](t, found, "actorUsername"), cell[string](t, found, "reason"); actor != "marshal" || reason != "watch desk walkthrough" {
			t.Errorf("record as the marshal reads it = %v, want who holds the session and why", found)
		}
		// The command staff list the crew's accounts by username and by nothing else,
		// which is what the desk's As column reads.
		if principal, cadet := cell[string](t, found, "principalUserId"), crewAccountID(ctx, t, mara, "cadet"); principal != cadet {
			t.Errorf("principalUserId = %s, want the cadet's account %s", principal, cadet)
		}
		status, body := mara.do(ctx, http.MethodGet, consoleAPI+"/crew-session-users?limit=200", nil)
		assertStatus(t, status, http.StatusOK, body)
		for _, row := range decodeRows(t, body) {
			for field := range row {
				if field != "id" && field != "username" {
					t.Fatalf("the marshal's list of the crew's accounts carries %q: %v, want the username alone", field, row)
				}
			}
		}
	})

	t.Run("the cadet may not operate the desk", func(t *testing.T) {
		cass := newBrowser(t, s, consoleAPI)
		cass.signIn(ctx, "cadet")
		status, body := cass.do(ctx, http.MethodGet, watchDeskList, nil)
		assertStatus(t, status, http.StatusForbidden, body)
		status, body = cass.do(ctx, http.MethodPost, watchDeskRevoke, revoke)
		assertStatus(t, status, http.StatusForbidden, body)
	})

	t.Run("revocation refuses the revoked console's next request", func(t *testing.T) {
		status, body := greer.do(ctx, http.MethodPost, watchDeskRevoke, revoke)
		assertStatus(t, status, http.StatusNoContent, body)

		status, body = maren.do(ctx, http.MethodGet, sectorPath(anvil, "missions"), nil)
		assertStatus(t, status, http.StatusUnauthorized, body)

		// The desk keeps the record, now with its end: the page offers Revoke on a row
		// that has none.
		ended := watchDeskRow(ctx, t, greer, minted)
		if ended["endedAt"] == nil || ended["endReason"] != "Revoked" {
			t.Errorf("the revoked session's record: endedAt = %v, endReason = %v, want it ended as Revoked", ended["endedAt"], ended["endReason"])
		}
	})
}

// TestWatchDesk_revokeReachesAnOpenDesk pins what the desk shows without asking again:
// the governor has the desk open, the marshal ends a session from her own desk, and the
// governor's desk is sent the change. The revoke runs through the generated method, whose
// handler publishes the record it wrote.
//
// Demonstrates: impersonation.revoke, live.pages.
func TestWatchDesk_revokeReachesAnOpenDesk(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	// The governor's desk is open: its list subscribes as it reads.
	greer := newBrowser(t, s, consoleAPI)
	greer.signIn(ctx, "governor")
	resp := greer.doWith(ctx, http.MethodGet, watchDeskList+"?limit=50", nil, liveHeaders("tab-desk-1"))
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing the answer of the governor's desk: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the governor's desk: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// The marshal views as Cass in one browser, and keeps the desk in another.
	maren := newBrowser(t, s, consoleAPI)
	maren.signIn(ctx, "marshal")
	status, body := maren.impersonate(ctx, viewAs(t, "cadet", "a session the desk ends"))
	assertStatus(t, status, http.StatusOK, body)
	minted := cell[string](t, decodeRow(t, body), "sessionId")

	mara := newBrowser(t, s, consoleAPI)
	mara.signIn(ctx, "marshal")
	status, body = mara.do(ctx, http.MethodPost, watchDeskRevoke, fmt.Appendf(nil, `{"sessionId":%q}`, minted))
	assertStatus(t, status, http.StatusNoContent, body)

	if got := changeNames(s.live.Changes(principalID("governor"))); !slices.Contains(got, watchDeskListChanged) {
		t.Errorf("the governor's change set after the marshal's revoke = %v, want it to hold %s", got, watchDeskListChanged)
	}
	status, body = maren.do(ctx, http.MethodGet, sectorPath(anvil, "missions"), nil)
	assertStatus(t, status, http.StatusUnauthorized, body)
}
