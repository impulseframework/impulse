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
