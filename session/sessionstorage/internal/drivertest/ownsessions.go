package drivertest

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// A person's account page lists where they are signed in: their own live sessions, each
// with the name of the browser it was signed in from, and how many browsers the second
// factor remembers for them. These cases pin, on both backends, that a session keeps the
// browser's name it was made with, that the list holds the person's own live sessions
// alone, newest use first, and that the count holds the live devices alone.

// ownSession is a listed session as the cases compare it: its id and browser.
type ownSession struct {
	ID      impulse.UUID
	Browser string
}

// ownSessionsOf lists userID's own sessions used at activeSince or later.
func ownSessionsOf(ctx context.Context, t *testing.T, d AuthDriver, userID impulse.UUID, activeSince time.Time) []ownSession {
	t.Helper()

	sessions, err := d.OwnSessions(ctx, userID, activeSince)
	if err != nil {
		t.Fatalf("OwnSessions() error = %v", err)
	}
	if sessions == nil {
		t.Fatal("OwnSessions() = nil, want a list, empty for none")
	}
	got := make([]ownSession, 0, len(sessions))
	for _, s := range sessions {
		o := ownSession{ID: s.ID}
		if s.Browser != nil {
			o.Browser = *s.Browser
		}
		got = append(got, o)
	}

	return got
}

func testOwnSessions(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Impersonation: true})
	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	sam := createUser(ctx, t, in.Driver, "sam@lakeside.edu")

	start := func(user *dbtype.SessionUser, browser string) impulse.UUID {
		t.Helper()
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID, Browser: browser}
		id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), req)
		if err != nil {
			t.Fatalf("InsertSession() error = %v", err)
		}

		return id
	}

	if got := ownSessionsOf(ctx, t, in.Driver, pat.ID, time.Time{}); len(got) != 0 {
		t.Errorf("OwnSessions() of a person signed in nowhere = %v, want none", got)
	}

	// The session last used before the cut. The cut lies between its use and the next
	// session's, both the database's times, so the host's clock decides nothing.
	stale := start(pat, "Firefox on Windows")
	unnamed := start(pat, "")
	staleAt := mustSession(ctx, t, in.Driver, stale).UpdatedAt
	unnamedAt := mustSession(ctx, t, in.Driver, unnamed).UpdatedAt
	if !unnamedAt.After(staleAt) {
		t.Fatalf("a later session was used at %v, not after the earlier one's %v", unnamedAt, staleAt)
	}
	activeSince := staleAt.Add(unnamedAt.Sub(staleAt) / 2)

	laptop := start(pat, "Chrome on Linux")
	ended := start(pat, "Safari on iPhone")
	if err := in.Driver.DestroySession(ctx, ended); err != nil {
		t.Fatalf("DestroySession() error = %v", err)
	}
	_ = start(sam, "Edge on Windows")
	imp := dbtype.NewInsertImpersonation(&sessioninfo.Impersonation{Actor: "admin@lakeside.edu", Principal: accesstypes.UserPrincipal(accesstypes.User(pat.ID.String())), ExpiresAt: time.Now().Add(time.Hour)})
	if _, err := in.Driver.InsertImpersonatedSession(ctx, newInsertSession(pat.Username),
		&sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: pat.Username, UserID: pat.ID, Browser: "Chrome on macOS"}, imp); err != nil {
		t.Fatalf("InsertImpersonatedSession() error = %v", err)
	}

	want := []ownSession{{ID: laptop, Browser: "Chrome on Linux"}, {ID: unnamed}}
	if diff := cmp.Diff(want, ownSessionsOf(ctx, t, in.Driver, pat.ID, activeSince)); diff != "" {
		t.Errorf("OwnSessions() mismatch (-want +got): the person's own live sessions, newest use first; no session expired, used before the cut, another person's or established acting as them\n%s", diff)
	}

	// The unnamed session is used again, so it is the newest use though it was made
	// before the laptop's.
	if err := in.Driver.UpdateSessionActivity(ctx, unnamed); err != nil {
		t.Fatalf("UpdateSessionActivity() error = %v", err)
	}
	want = []ownSession{{ID: unnamed}, {ID: laptop, Browser: "Chrome on Linux"}}
	if diff := cmp.Diff(want, ownSessionsOf(ctx, t, in.Driver, pat.ID, activeSince)); diff != "" {
		t.Errorf("OwnSessions() after a use mismatch (-want +got): newest use first\n%s", diff)
	}

	sessions, err := in.Driver.OwnSessions(ctx, pat.ID, activeSince)
	if err != nil {
		t.Fatalf("OwnSessions() error = %v", err)
	}
	unnamedNow := mustSession(ctx, t, in.Driver, unnamed)
	if s := sessions[0]; !s.CreatedAt.Equal(unnamedNow.CreatedAt) || !s.UpdatedAt.Equal(unnamedNow.UpdatedAt) {
		t.Errorf("OwnSessions()[0] = signed in %v, last used %v; want the session's %v and %v", s.CreatedAt, s.UpdatedAt, unnamedNow.CreatedAt, unnamedNow.UpdatedAt)
	}

	wantAll := []ownSession{{ID: unnamed}, {ID: laptop, Browser: "Chrome on Linux"}, {ID: stale, Browser: "Firefox on Windows"}}
	if diff := cmp.Diff(wantAll, ownSessionsOf(ctx, t, in.Driver, pat.ID, time.Time{})); diff != "" {
		t.Errorf("OwnSessions() with no cut mismatch (-want +got):\n%s", diff)
	}

	without := h.New(ctx, t, WithoutAuth, AuthConfig{Impersonation: true})
	if _, err := without.Driver.OwnSessions(ctx, pat.ID, time.Time{}); err == nil {
		t.Error("OwnSessions() without the auth schema error = nil, want an error naming it")
	}
}

// testRenameLeavesLastUse pins that a rename is no use of a session: each row keeps when
// it was last used, so a session that timed out stays out of the person's live list, and
// a session of another account keeps its name whatever name its row carries.
func testRenameLeavesLastUse(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Impersonation: true})
	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	sam := createUser(ctx, t, in.Driver, "sam@lakeside.edu")

	start := func(username string, userID impulse.UUID) impulse.UUID {
		t.Helper()
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: username, UserID: userID}
		id, err := in.Driver.InsertSession(ctx, newInsertSession(username), req)
		if err != nil {
			t.Fatalf("InsertSession() error = %v", err)
		}

		return id
	}

	// Two sessions of pat's with a cut between their uses, both the database's times.
	older := start(pat.Username, pat.ID)
	newer := start(pat.Username, pat.ID)
	olderAt := mustSession(ctx, t, in.Driver, older).UpdatedAt
	newerAt := mustSession(ctx, t, in.Driver, newer).UpdatedAt
	if !newerAt.After(olderAt) {
		t.Fatalf("a later session was used at %v, not after the earlier one's %v", newerAt, olderAt)
	}
	activeSince := olderAt.Add(newerAt.Sub(olderAt) / 2)

	// A session of sam's whose row carries pat's name: a sign-in that waited on its code
	// while its person's account changed names.
	borrowed := start(pat.Username, sam.ID)

	const renamed = "patricia@lakeside.edu"
	if err := in.Driver.SetUserUsername(ctx, pat.ID, renamed); err != nil {
		t.Fatalf("SetUserUsername() error = %v", err)
	}

	for _, s := range []struct {
		id     impulse.UUID
		usedAt time.Time
	}{{older, olderAt}, {newer, newerAt}} {
		got := mustSession(ctx, t, in.Driver, s.id)
		if !got.UpdatedAt.Equal(s.usedAt) {
			t.Errorf("Session(%s).UpdatedAt after the rename = %v, want %v: a rename is no use of a session", s.id, got.UpdatedAt, s.usedAt)
		}
		if got.Username != renamed {
			t.Errorf("Session(%s).Username after the rename = %q, want %q", s.id, got.Username, renamed)
		}
	}

	want := []ownSession{{ID: newer}}
	if diff := cmp.Diff(want, ownSessionsOf(ctx, t, in.Driver, pat.ID, activeSince)); diff != "" {
		t.Errorf("OwnSessions() at the cut after the rename mismatch (-want +got): the session used before the cut stays out\n%s", diff)
	}

	if got := mustSession(ctx, t, in.Driver, borrowed).Username; got != pat.Username {
		t.Errorf("Session().Username of another account's session after the rename = %q, want %q: it is not pat's to rename", got, pat.Username)
	}
}

func testTrustedDeviceCount(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	sam := createUser(ctx, t, in.Driver, "sam@lakeside.edu")

	count := func(userID impulse.UUID) int {
		t.Helper()
		n, err := in.Driver.TrustedDeviceCount(ctx, userID, time.Now())
		if err != nil {
			t.Fatalf("TrustedDeviceCount() error = %v", err)
		}

		return n
	}

	if got := count(pat.ID); got != 0 {
		t.Errorf("TrustedDeviceCount() of a person with no device = %d, want 0", got)
	}

	live := newTrustedDevice(pat.ID, tokenHash(1))
	revoked := newTrustedDevice(pat.ID, tokenHash(2))
	lapsed := newTrustedDevice(pat.ID, tokenHash(3))
	lapsed.ExpiresAt = time.Now().Add(-time.Second)
	samsDevice := newTrustedDevice(sam.ID, tokenHash(4))
	for _, d := range []*dbtype.TrustedDevice{live, revoked, lapsed, samsDevice} {
		if err := in.Driver.InsertTrustedDevice(ctx, d); err != nil {
			t.Fatalf("InsertTrustedDevice() error = %v", err)
		}
	}
	if err := in.Driver.RevokeTrustedDevice(ctx, revoked.TokenHash, time.Now()); err != nil {
		t.Fatalf("RevokeTrustedDevice() error = %v", err)
	}

	if got := count(pat.ID); got != 1 {
		t.Errorf("TrustedDeviceCount() = %d, want 1: the live device, not the revoked or the lapsed one, nor another person's", got)
	}
}

// testDestroyOtherOwnSessions pins that signing out everywhere else ends, in one write,
// every session of the person's own but the one kept, whenever it was last used, and
// counts the ones used at the cut or later: the ones the list showed. No other person's
// session, no session someone else established acting as the person, and no session
// already ended is written.
func testDestroyOtherOwnSessions(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Impersonation: true})
	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	sam := createUser(ctx, t, in.Driver, "sam@lakeside.edu")

	start := func(user *dbtype.SessionUser) impulse.UUID {
		t.Helper()
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID}
		id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), req)
		if err != nil {
			t.Fatalf("InsertSession() error = %v", err)
		}

		return id
	}

	// The session last used before the cut has timed out. The cut lies between its use
	// and the next session's, both the database's times, so the host's clock decides
	// nothing.
	timedOut := start(pat)
	keep := start(pat)
	timedOutAt := mustSession(ctx, t, in.Driver, timedOut).UpdatedAt
	keepAt := mustSession(ctx, t, in.Driver, keep).UpdatedAt
	if !keepAt.After(timedOutAt) {
		t.Fatalf("a later session was used at %v, not after the earlier one's %v", keepAt, timedOutAt)
	}
	activeSince := timedOutAt.Add(keepAt.Sub(timedOutAt) / 2)

	live := start(pat)
	ended := start(pat)
	if err := in.Driver.DestroySession(ctx, ended); err != nil {
		t.Fatalf("DestroySession() error = %v", err)
	}
	sams := start(sam)
	imp := dbtype.NewInsertImpersonation(&sessioninfo.Impersonation{Actor: "admin@lakeside.edu", Principal: accesstypes.UserPrincipal(accesstypes.User(pat.ID.String())), ExpiresAt: time.Now().Add(time.Hour)})
	impersonated, err := in.Driver.InsertImpersonatedSession(ctx, newInsertSession(pat.Username),
		&sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: pat.Username, UserID: pat.ID}, imp)
	if err != nil {
		t.Fatalf("InsertImpersonatedSession() error = %v", err)
	}

	untouched := map[impulse.UUID]*dbtype.SessionData{}
	for _, id := range []impulse.UUID{keep, ended, sams, impersonated} {
		untouched[id] = mustSession(ctx, t, in.Driver, id)
	}

	count, err := in.Driver.DestroyOtherOwnSessions(ctx, pat.ID, keep, activeSince)
	if err != nil {
		t.Fatalf("DestroyOtherOwnSessions() error = %v", err)
	}
	if count != 1 {
		t.Errorf("DestroyOtherOwnSessions() = %d, want 1: the live other alone is counted, not the one that timed out", count)
	}
	for _, id := range []impulse.UUID{live, timedOut} {
		if !mustSession(ctx, t, in.Driver, id).Expired {
			t.Errorf("Session(%s).Expired after DestroyOtherOwnSessions() = false, want true: every other session of the person's own ends, the live one and the one that timed out", id)
		}
	}
	unchanged := func(when string) {
		t.Helper()
		for id, before := range untouched {
			after := mustSession(ctx, t, in.Driver, id)
			if after.Expired != before.Expired || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Errorf("%s: Session(%s) is expired %v, last used %v; want as it was, %v and %v", when, id, after.Expired, after.UpdatedAt, before.Expired, before.UpdatedAt)
			}
			if before.Impersonation != nil && (after.Impersonation == nil || after.Impersonation.EndedAt != nil) {
				t.Errorf("%s: the impersonation record of Session(%s) = %+v, want it live as it was", when, id, after.Impersonation)
			}
		}
	}
	unchanged("after DestroyOtherOwnSessions()")

	count, err = in.Driver.DestroyOtherOwnSessions(ctx, pat.ID, keep, activeSince)
	if err != nil {
		t.Fatalf("DestroyOtherOwnSessions() with no other session error = %v", err)
	}
	if count != 0 {
		t.Errorf("DestroyOtherOwnSessions() with no other session = %d, want 0", count)
	}
	unchanged("after DestroyOtherOwnSessions() with no other session")

	without := h.New(ctx, t, WithoutAuth, AuthConfig{Impersonation: true})
	if _, err := without.Driver.DestroyOtherOwnSessions(ctx, pat.ID, keep, time.Time{}); err == nil {
		t.Error("DestroyOtherOwnSessions() without the auth schema error = nil, want an error naming it")
	}
}
