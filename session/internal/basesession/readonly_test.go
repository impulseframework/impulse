package basesession

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

func TestBaseSession_EnforceReadOnlyMask(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("92922509-82d2-4bc7-853a-d73b8926a55f"))
	withSession := func(imp *sessioninfo.Impersonation) context.Context {
		return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
			SessionInfo:   &sessioninfo.SessionInfo{ID: sessionID, Username: "bob", UpdatedAt: time.Now()},
			Impersonation: imp,
		})
	}
	impersonated := func(mask accesstypes.PermissionMask) *sessioninfo.Impersonation {
		return &sessioninfo.Impersonation{SessionID: sessionID, Actor: "alice", Principal: accesstypes.UserPrincipal("bob"), Mask: mask, ExpiresAt: time.Now().Add(time.Hour)}
	}
	readOnly := accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)

	type test struct {
		name        string
		ctx         context.Context
		method      string
		wantBlocked bool
	}
	tests := append(make([]test, 0, 16), []test{
		{name: "no session in context passes", ctx: context.Background(), method: http.MethodPost},
		{name: "an ordinary session writes", ctx: withSession(nil), method: http.MethodPost},
		{name: "an unmasked impersonation writes", ctx: withSession(impersonated(accesstypes.PermissionMask{})), method: http.MethodDelete},
		{name: "a mask including Execute is not read-only", ctx: withSession(impersonated(accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Execute))), method: http.MethodPost},
		{name: "a mask including Update is not read-only", ctx: withSession(impersonated(accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read, accesstypes.Update))), method: http.MethodPatch},
		{name: "the mask that allows nothing blocks writes", ctx: withSession(impersonated(accesstypes.DenyAll())), method: http.MethodPost, wantBlocked: true},
		// A mask that keeps every kind and refuses something inside them is the
		// permission check's to enforce: the guard is the read-only mask's alone.
		{name: "every kind kept, a class refused, is not read-only", ctx: withSession(impersonated(accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)))), method: http.MethodPost},
		{name: "every kind kept, an action refused, is not read-only", ctx: withSession(impersonated(accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("LaunchMission")))), method: http.MethodPost},
		{name: "a read-only mask that also refuses a class blocks writes", ctx: withSession(impersonated(readOnly.Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)))), method: http.MethodPost, wantBlocked: true},
	}...)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		tests = append(tests, test{name: "read-only reads with " + method, ctx: withSession(impersonated(readOnly)), method: method})
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		tests = append(tests, test{name: "read-only is refused " + method, ctx: withSession(impersonated(readOnly)), method: method, wantBlocked: true})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var events []sessioninfo.ImpersonationEvent
			s := &BaseSession{
				Handle: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
				ImpersonationAudit: func(_ context.Context, event sessioninfo.ImpersonationEvent) error {
					events = append(events, event)

					return nil
				},
			}

			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			})

			rr := httptest.NewRecorder()
			s.EnforceReadOnlyMask(next).ServeHTTP(rr, httptest.NewRequestWithContext(tt.ctx, tt.method, "/api/partners/1", http.NoBody))

			if reached == tt.wantBlocked {
				t.Fatalf("next reached = %v, wantBlocked %v", reached, tt.wantBlocked)
			}
			if !tt.wantBlocked {
				if rr.Code != http.StatusNoContent || len(events) != 0 {
					t.Errorf("status = %d, events = %v; want 204 and none", rr.Code, events)
				}

				return
			}
			if rr.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
			}
			if len(events) != 1 || events[0].Kind != sessioninfo.ImpersonationWriteBlocked || events[0].Operation != tt.method+" /api/partners/1" {
				t.Errorf("events = %+v, want one WriteBlocked for %s /api/partners/1", events, tt.method)
			}
		})
	}
}
