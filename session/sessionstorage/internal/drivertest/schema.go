package drivertest

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The shipped auth schema holds a user's identity links to the user by a cascading
// foreign key, and a session's auth events to the session. A session names its user and
// is not held to it by a foreign key: a deleted user's sessions are ended in code, and
// their rows stay until the timed removal takes them. These cases prove the DDL as
// shipped on both backends.

// testDeleteUserKeepsSessionRows deletes a user as the session API's DeleteSessionUser
// does, the user's sessions ended first (DestroyUserSessions) and then the user
// (DeleteUser): the user and its identity links are gone, and the session's row stays
// with its auth events, ended, so a request that carries it is refused as one carrying
// any ended session is. Another user's session and link are untouched.
func testDeleteUserKeepsSessionRows(ctx context.Context, t *testing.T, h *AuthHarness) {
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
		t.Fatalf("auth events of the session of the user to delete = %d, want 1", n)
	}

	if err := in.Driver.DestroyUserSessions(ctx, gone.ID); err != nil {
		t.Fatalf("DestroyUserSessions() error = %v", err)
	}
	if err := in.Driver.DeleteUser(ctx, gone.ID, nil); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}

	if _, err := in.Driver.User(ctx, gone.ID); !httpio.HasNotFound(err) {
		t.Errorf("User() of the deleted user error = %v, want NotFound", err)
	}
	assertNoLink(ctx, t, in.Driver, lakeside("idp_gone"))
	if links, err := in.Driver.IdentitiesByUser(ctx, gone.ID); err != nil || len(links) != 0 {
		t.Errorf("IdentitiesByUser() of the deleted user = %v, %v; want none", links, err)
	}
	sess, err := in.Driver.Session(ctx, goneSession)
	if err != nil {
		t.Fatalf("Session() of the deleted user's session error = %v, want its row kept", err)
	}
	if !sess.Expired || sess.UserID != impulse.NullUUIDFromUUID(gone.ID) {
		t.Errorf("the deleted user's session = %+v, want its row kept, ended, naming %s", sess, gone.ID)
	}
	if n := h.CountAuthEvents(ctx, t, in.Raw, goneSession); n != 1 {
		t.Errorf("auth events of the deleted user's session = %d, want 1: they stay with the session row", n)
	}
	if sess := mustSession(ctx, t, in.Driver, staysSession); sess.Expired || sess.UserID != impulse.NullUUIDFromUUID(stays.ID) {
		t.Errorf("the other user's session = %+v, want it live under %s", sess, stays.ID)
	}
	if link, err := in.Driver.Identity(ctx, sessioninfo.MethodWorkOS, "conn_lakeside", "idp_stays"); err != nil || link.ID != keptLink.ID {
		t.Errorf("Identity() of the other user's link = %v, %v; want it kept", link, err)
	}
}
