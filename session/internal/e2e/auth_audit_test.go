//go:build !skipAuth

// The audit log the session library writes for a person's own acts, through the real
// handlers and the real stores on both backends: each act's row is written with the act,
// in its own transaction, and names who acted.

package e2e

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/session"
)

func TestAuthSeams_AuditLog(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runAuditSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runAuditSeams(t, spannerBackend)
	})
}

func runAuditSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "a person's own password change, an administrator's reset link and the password chosen at it are each a row, by the person, by the application and by the link",
			run:  seamAuditPasswords[Tx],
		},
		{
			name: "an application token made is a row named by the token's name",
			run:  seamAuditToken[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t.Context(), t, be)
		})
	}
}

func seamAuditPasswords[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newResetApp(ctx, t, be)
	pat := a.seedConfirmed(ctx, t, "pat", "pat@lakeside.edu")

	// The person changes their own password in their session.
	b := a.browser(t).primed(ctx)
	if status, _, body := b.postLogin(ctx, "pat", seedPassword); status != http.StatusOK {
		t.Fatalf("login = %d %s, want 200", status, body)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/password", map[string]string{"oldPassword": seedPassword, "newPassword": "a-new-password-of-my-own"})
	// A wrong old password is refused, and writes no row.
	b.expect(ctx, http.StatusBadRequest, http.MethodPost, "/password", map[string]string{"oldPassword": "not the password", "newPassword": "another password of mine"})

	// The application's own job mails a reset link, and the person chooses a password at it.
	if err := a.api.SendPasswordReset(ctx, pat, session.AskedByApplication()); err != nil {
		t.Fatalf("SendPasswordReset() error = %v", err)
	}
	_, token := a.lastLink(t, "pat@lakeside.edu", "reset")
	a.browser(t).primed(ctx).expect(ctx, http.StatusOK, http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword})

	id := pat.String()
	want := []auditRow{
		{Act: string(auditlog.ActPasswordChanged), SubjectID: id, SubjectName: "pat", ActorUserID: id},
		{Act: string(auditlog.ActPasswordResetLinkSent), SubjectID: id, SubjectName: "pat", Process: auditlog.ProcessApplication},
		{Act: string(auditlog.ActPasswordChosen), SubjectID: id, SubjectName: "pat", Process: auditlog.ProcessResetLink},
	}
	if diff := cmp.Diff(want, a.db.auditRows(ctx, t)); diff != "" {
		t.Errorf("the audit log (-want +got):\n%s", diff)
	}
}

func seamAuditToken[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newTokenApp(ctx, t, be)
	pat := a.seed(ctx, t, "pat")
	if _, err := a.api.CreateApplicationToken(ctx, &session.ApplicationTokenRequest{UserID: pat, Outlet: "droids", Name: "laptop"}); err != nil {
		t.Fatalf("CreateApplicationToken() error = %v", err)
	}

	want := []auditRow{{Act: string(auditlog.ActTokenMade), SubjectID: pat.String(), SubjectName: "pat", Process: auditlog.ProcessApplication, NewValue: "laptop"}}
	if diff := cmp.Diff(want, a.db.auditRows(ctx, t)); diff != "" {
		t.Errorf("the audit log (-want +got):\n%s", diff)
	}
}
