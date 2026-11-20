package session

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	gomock "go.uber.org/mock/gomock"
)

// TestImpersonationRequest_startsOneSession pins that a request the declaration made
// starts one session, on each session type. The first start is the session. A second
// start from the same request, and one from a copy of it, are refused with a plain
// error that says so, before the store is asked anything: each store's mock expects the
// one write and fails the test on another. A start that fails spends the request as
// well: its grant was checked for the call that followed it, and another try takes
// another request.
func TestImpersonationRequest_startsOneSession(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	// request is a request as the declaration makes one for a server-side caller, whose
	// own check allows every kind.
	request := func(t *testing.T, kind, principal string) *ImpersonationRequest {
		t.Helper()
		allowed := func(context.Context, accesstypes.Resource) (bool, error) {
			return true, nil
		}
		req, err := declaredImpersonations(t).RequestFor(context.Background(),
			ImpersonationActor{Name: "alice@example.com", Realm: "admin-portal"}, &ImpersonationAsk{Kind: kind, Principal: principal}, allowed)
		if err != nil {
			t.Fatalf("RequestFor() error = %v", err)
		}

		return req
	}
	// refusedAgain starts from the request again, and from a copy of it, and holds each
	// refusal to its words.
	refusedAgain := func(t *testing.T, req *ImpersonationRequest, start func(*ImpersonationRequest) (impulse.UUID, error)) {
		t.Helper()
		copied := *req
		for name, again := range map[string]*ImpersonationRequest{"the same request": req, "a copy of the request": &copied} {
			id, err := start(again)
			if err == nil || httpio.HasBadRequest(err) || httpio.HasForbidden(err) {
				t.Fatalf("a second StartImpersonatedSession() from %s = %v, error = %v; want a plain error: a mistake in the program, which no person can put right", name, id, err)
			}
			for _, want := range []string{
				`the impersonation request (kind "` + req.Kind() + `", alice@example.com acting as ` + req.Principal().String() + `) has been used to start a session already`,
				"a request starts one session", "Nothing was written", "Request or RequestFor",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("a second StartImpersonatedSession() from %s: error = %q, want it to carry %q", name, err, want)
				}
			}
		}
	}

	t.Run("Preauth: the first start is the session, a second is refused", func(t *testing.T) {
		t.Parallel()
		storage := newPreauthStoreMock(gomock.NewController(t))
		storage.EXPECT().ImpersonationEnabled().Return(true)
		storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)
		p, err := NewPreauth[NoCustomData](storage, cookieKey)
		if err != nil {
			t.Fatalf("NewPreauth() error = %v", err)
		}
		start := func(req *ImpersonationRequest) (impulse.UUID, error) {
			return p.API().StartImpersonatedSession(context.Background(), httptest.NewRecorder(), req)
		}

		req := request(t, "view", "bob")
		if got, err := start(req); err != nil || got != sessionID {
			t.Fatalf("the first StartImpersonatedSession() = %v, error = %v; want the session %v", got, err, sessionID)
		}
		refusedAgain(t, req, start)
	})
	t.Run("Auth: the first start is the session, a second is refused", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().ImpersonationEnabled().Return(true)
		// The name of an actor from elsewhere must be no user's here.
		f.store.EXPECT().UserByUserName(gomock.Any(), "alice@example.com").Return(nil, httpio.NewNotFoundMessage("no such user"))
		f.store.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)
		start := func(req *ImpersonationRequest) (impulse.UUID, error) {
			return f.auth.API().StartImpersonatedSession(context.Background(), httptest.NewRecorder(), req)
		}

		req := request(t, "role", "Dispatcher")
		if got, err := start(req); err != nil || got != sessionID {
			t.Fatalf("the first StartImpersonatedSession() = %v, error = %v; want the session %v", got, err, sessionID)
		}
		refusedAgain(t, req, start)
	})
	t.Run("a start that fails spends the request as well", func(t *testing.T) {
		t.Parallel()
		storage := newPreauthStoreMock(gomock.NewController(t))
		// The storage keeps no impersonation records, so the first start is refused.
		storage.EXPECT().ImpersonationEnabled().Return(false)
		p, err := NewPreauth[NoCustomData](storage, cookieKey)
		if err != nil {
			t.Fatalf("NewPreauth() error = %v", err)
		}
		start := func(req *ImpersonationRequest) (impulse.UUID, error) {
			return p.API().StartImpersonatedSession(context.Background(), httptest.NewRecorder(), req)
		}

		req := request(t, "view", "bob")
		if _, err := start(req); err == nil || strings.Contains(err.Error(), "a request starts one session") {
			t.Fatalf("the first StartImpersonatedSession() error = %v, want the storage's refusal", err)
		}
		refusedAgain(t, req, start)
	})
	t.Run("a second request from the declaration starts a second session", func(t *testing.T) {
		t.Parallel()
		storage := newPreauthStoreMock(gomock.NewController(t))
		storage.EXPECT().ImpersonationEnabled().Return(true).Times(2)
		storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil).Times(2)
		p, err := NewPreauth[NoCustomData](storage, cookieKey)
		if err != nil {
			t.Fatalf("NewPreauth() error = %v", err)
		}
		for range 2 {
			if _, err := p.API().StartImpersonatedSession(context.Background(), httptest.NewRecorder(), request(t, "view", "bob")); err != nil {
				t.Errorf("StartImpersonatedSession() from a request of its own: error = %v, want none", err)
			}
		}
	})
}
