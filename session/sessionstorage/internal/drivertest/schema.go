package drivertest

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The shipped auth schema holds its own integrity: a session belongs to an existing user
// by foreign key, and deleting a user takes its sessions, their events and its links
// with it. These cases prove the DDL as shipped on both backends.

func testDeleteUserCascades(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true})
	gone := createUser(ctx, t, in.Driver, "gone@lakeside.edu")
	stays := createUser(ctx, t, in.Driver, "stays@lakeside.edu")
	mustLink(ctx, t, in.Driver, gone.ID, lakeside("idp_gone"))
	keptLink := mustLink(ctx, t, in.Driver, stays.ID, lakeside("idp_stays"))

	goneSession, _, err := signIn(ctx, in, lakeside("idp_gone"), sessioninfo.ReasonLogin)
	if err != nil {
		t.Fatalf("sign-in of the user to delete error = %v", err)
	}
	staysSession, _, err := signIn(ctx, in, lakeside("idp_stays"), sessioninfo.ReasonLogin)
	if err != nil {
		t.Fatalf("sign-in of the other user error = %v", err)
	}
	if n := h.CountAuthEvents(ctx, t, in.Raw, goneSession); n != 1 {
		t.Fatalf("auth events of the session to delete = %d, want 1", n)
	}

	if err := in.Driver.DeleteUser(ctx, gone.ID); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}

	if _, err := in.Driver.Session(ctx, goneSession); !httpio.HasNotFound(err) {
		t.Errorf("Session() of the deleted user's session error = %v, want NotFound: the session row goes with its user", err)
	}
	if n := h.CountAuthEvents(ctx, t, in.Raw, goneSession); n != 0 {
		t.Errorf("auth events of the deleted user's session = %d, want 0: they go with the session row", n)
	}
	assertNoLink(ctx, t, in.Driver, lakeside("idp_gone"))
	if sess := mustSession(ctx, t, in.Driver, staysSession); sess.UserID != impulse.NullUUIDFromUUID(stays.ID) {
		t.Errorf("the other user's session = %+v, want it kept under %s", sess, stays.ID)
	}
	if link, err := in.Driver.Identity(ctx, sessioninfo.MethodWorkOS, "conn_lakeside", "idp_stays"); err != nil || link.ID != keptLink.ID {
		t.Errorf("Identity() of the other user's link = %v, %v; want it kept", link, err)
	}
}

func testSessionUserForeignKey(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	user := createUser(ctx, t, in.Driver, "pat@lakeside.edu")

	unknown := impulse.Must(impulse.NewUUID())
	if _, err := in.Driver.InsertSession(ctx, newInsertSession("ghost"), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "ghost", UserID: unknown}); err == nil {
		t.Error("InsertSession() for a user id that does not exist error = nil, want the schema's refusal")
	}

	// A session with a user, and one with none (a preauth stepping stone), are both inserted.
	if _, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID}); err != nil {
		t.Errorf("InsertSession() for an existing user error = %v", err)
	}
	if _, err := in.Driver.InsertSession(ctx, newInsertSession("stepping-stone"), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonPreauth, Username: "stepping-stone"}); err != nil {
		t.Errorf("InsertSession() of a preauth stepping stone error = %v", err)
	}
}
