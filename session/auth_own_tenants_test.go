package session

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	gomock "go.uber.org/mock/gomock"
)

// ownTenantsHookFake is an own-tenants hook that answers what a test gives it and keeps
// what it was asked.
type ownTenantsHookFake struct {
	tenants  []OwnTenant
	deleted  bool
	leaveErr error

	listedFor []impulse.UUID
	left      []leftTenant
}

// leftTenant is one call of the hook's Leave.
type leftTenant struct {
	UserID         impulse.UUID
	Domain         accesstypes.Domain
	DeletesAccount bool
}

func (h *ownTenantsHookFake) hook() OwnTenantsHook {
	return OwnTenantsHook{
		List: func(_ context.Context, userID impulse.UUID) ([]OwnTenant, error) {
			h.listedFor = append(h.listedFor, userID)

			return h.tenants, nil
		},
		Leave: func(_ context.Context, userID impulse.UUID, domain accesstypes.Domain, deletesAccount bool) (bool, error) {
			h.left = append(h.left, leftTenant{UserID: userID, Domain: domain, DeletesAccount: deletesAccount})

			return h.deleted, h.leaveErr
		},
	}
}

// unaskedOwnTenants is an own-tenants hook for a test in which nothing may reach it: a
// request the handler refuses first, or a methods answer, which only asks whether the
// Auth holds one.
func unaskedOwnTenants() OwnTenantsHook {
	return OwnTenantsHook{
		List: func(context.Context, impulse.UUID) ([]OwnTenant, error) {
			panic("the own-tenants hook was asked to list")
		},
		Leave: func(context.Context, impulse.UUID, accesstypes.Domain, bool) (bool, error) {
			panic("the own-tenants hook was asked to leave")
		},
	}
}

// ownTenantsHandlerOf is handler on an Auth that holds a hook nothing may reach: the
// refusals' tables ask that each own-tenants handler refuses before its hook is asked.
func ownTenantsHandlerOf(handler func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc) func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc {
	return func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc {
		a.SetOwnTenants(unaskedOwnTenants())

		return handler(a)
	}
}

// TestAuth_OwnTenants proves the list of a person's tenants is the hook's, for the
// session's own user, each tenant named by the namer or, where it has no name, by its
// key, sorted by name without regard to case and then by key, with the roles never null
// and the refusal left out where there is none.
func TestAuth_OwnTenants(t *testing.T) {
	t.Parallel()

	t.Run("the hook's tenants are named, a tenant with no name by its key, and sorted by name without regard to case, then by key", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.f.auth.SetTenantNames(namedTenants{"t-acme-2": "acme", "t-acme-1": "Acme", "t-bay": "bayside", "t-zeta": "Zeta Docks"})
		fake := &ownTenantsHookFake{tenants: []OwnTenant{
			{Domain: "t-zeta", Roles: []string{"Viewer"}},
			{Domain: "t-acme-2", Roles: []string{"Editor", "Viewer"}, DeletesAccount: true},
			{Domain: "c-unnamed", Roles: nil},
			{Domain: "t-bay", Roles: []string{"Administrator"}, Refusal: "You are the last administrator of this tenant. Give another person the role before you leave."},
			{Domain: "t-acme-1", Roles: []string{"Viewer"}},
		}}
		c.f.auth.SetOwnTenants(fake.hook())
		c.signedIn()

		rr := c.serve(c.f.auth.OwnTenants(), c.ctx(), http.MethodGet, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("OwnTenants() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
		}
		want := map[string]any{"tenants": []any{
			map[string]any{"domain": "t-acme-1", "name": "Acme", "roles": []any{"Viewer"}, "deletesAccount": false},
			map[string]any{"domain": "t-acme-2", "name": "acme", "roles": []any{"Editor", "Viewer"}, "deletesAccount": true},
			map[string]any{
				"domain": "t-bay", "name": "bayside", "roles": []any{"Administrator"}, "deletesAccount": false,
				"refusal": "You are the last administrator of this tenant. Give another person the role before you leave.",
			},
			map[string]any{"domain": "c-unnamed", "name": "c-unnamed", "roles": []any{}, "deletesAccount": false},
			map[string]any{"domain": "t-zeta", "name": "Zeta Docks", "roles": []any{"Viewer"}, "deletesAccount": false},
		}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("OwnTenants() answer mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]impulse.UUID{c.userID}, fake.listedFor); diff != "" {
			t.Errorf("the hook was asked for (-want +got):\n%s", diff)
		}
	})

	t.Run("with no namer every tenant is named by its key", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.f.auth.SetOwnTenants((&ownTenantsHookFake{tenants: []OwnTenant{{Domain: "t-bay", Roles: []string{"Viewer"}}, {Domain: "t-acme", Roles: []string{"Viewer"}}}}).hook())
		c.signedIn()

		rr := c.serve(c.f.auth.OwnTenants(), c.ctx(), http.MethodGet, "")
		want := `{"tenants":[{"domain":"t-acme","name":"t-acme","roles":["Viewer"],"deletesAccount":false},{"domain":"t-bay","name":"t-bay","roles":["Viewer"],"deletesAccount":false}]}`
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != want {
			t.Errorf("OwnTenants() = %d %s, want 200 %s", rr.Code, rr.Body.String(), want)
		}
	})

	t.Run("a person who holds nothing is answered an empty list, not null", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.f.auth.SetOwnTenants((&ownTenantsHookFake{}).hook())
		c.signedIn()

		rr := c.serve(c.f.auth.OwnTenants(), c.ctx(), http.MethodGet, "")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"tenants":[]}` {
			t.Errorf("OwnTenants() = %d %s, want 200 {\"tenants\":[]}", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_LeaveTenant proves the leave hands the hook the session's own user, the
// tenant and deletesAccount as the page sent them, tells whether the account was
// deleted, and answers the hook's refusal as it is, with its status and words.
func TestAuth_LeaveTenant(t *testing.T) {
	t.Parallel()

	for _, deletes := range []bool{false, true} {
		t.Run("the hook is handed the user, the tenant and deletesAccount as sent, and its answer is told: deletesAccount "+map[bool]string{false: "false", true: "true"}[deletes], func(t *testing.T) {
			t.Parallel()
			c := newOwnSessionsCase(t)
			fake := &ownTenantsHookFake{deleted: deletes}
			c.f.auth.SetOwnTenants(fake.hook())
			c.signedIn()

			body := `{"domain":"t-acme","deletesAccount":false}`
			want := `{"accountDeleted":false}`
			if deletes {
				body = `{"domain":"t-acme","deletesAccount":true}`
				want = `{"accountDeleted":true}`
			}
			rr := c.serve(c.f.auth.LeaveTenant(), c.ctx(), http.MethodPost, body)
			if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != want {
				t.Errorf("LeaveTenant() = %d %s, want 200 %s", rr.Code, rr.Body.String(), want)
			}
			if diff := cmp.Diff([]leftTenant{{UserID: c.userID, Domain: "t-acme", DeletesAccount: deletes}}, fake.left); diff != "" {
				t.Errorf("the hook was asked to leave (-want +got):\n%s", diff)
			}
		})
	}

	refusals := []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{
			name: "a refusal", status: http.StatusForbidden,
			err:     httpio.NewForbiddenMessage("You are the last administrator of this tenant. Give another person the role before you leave."),
			message: "You are the last administrator of this tenant. Give another person the role before you leave.",
		},
		{
			name: "a changed list", status: http.StatusConflict,
			err:     httpio.NewConflictMessage("What you hold changed while this page was open. Look at the list again before you leave."),
			message: "What you hold changed while this page was open. Look at the list again before you leave.",
		},
		{name: "a tenant the person is not in", status: http.StatusNotFound, err: httpio.NewNotFoundMessagef("You hold no role in that %s.", "tenant"), message: "You hold no role in that tenant."},
		{name: "no tenant named", status: http.StatusBadRequest, err: httpio.NewBadRequestMessagef("Name the %s to leave.", "tenant"), message: "Name the tenant to leave."},
	}
	for _, tt := range refusals {
		t.Run("the hook's refusal is answered with its status and words: "+tt.name, func(t *testing.T) {
			t.Parallel()
			c := newOwnSessionsCase(t)
			c.f.auth.SetOwnTenants((&ownTenantsHookFake{leaveErr: tt.err}).hook())
			c.signedIn()

			rr := c.serve(c.f.auth.LeaveTenant(), c.ctx(), http.MethodPost, `{"domain":"t-acme","deletesAccount":false}`)
			if rr.Code != tt.status || messageOf(t, rr) != tt.message {
				t.Errorf("LeaveTenant() = %d %s, want %d %q", rr.Code, rr.Body.String(), tt.status, tt.message)
			}
		})
	}
}

// TestAuth_OwnTenants_noHook proves an Auth that holds no own-tenants hook serves
// neither handler: each answers 404 in words to a signed-in person.
func TestAuth_OwnTenants_noHook(t *testing.T) {
	t.Parallel()

	handlers := []struct {
		name    string
		handler func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc
		method  string
		body    string
	}{
		{name: "OwnTenants", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).OwnTenants, method: http.MethodGet},
		{name: "LeaveTenant", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).LeaveTenant, method: http.MethodPost, body: `{"domain":"t-acme","deletesAccount":false}`},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()
			c := newOwnSessionsCase(t)
			c.signedIn()

			rr := c.serve(h.handler(c.f.auth), c.ctx(), h.method, h.body)
			if rr.Code != http.StatusNotFound || messageOf(t, rr) != "This application does not serve a person's tenants." {
				t.Errorf("%s() = %d %s, want 404 saying the application does not serve a person's tenants", h.name, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestAuth_SetOwnTenants_Refused proves the own-tenants hook is set once, whole, before
// the Auth serves: anything else is a wiring mistake and panics in words.
func TestAuth_SetOwnTenants_Refused(t *testing.T) {
	t.Parallel()

	whole := unaskedOwnTenants()
	tests := []struct {
		name    string
		prepare func(f *authFixture)
		hook    OwnTenantsHook
		want    string
	}{
		{
			name: "a hook with no List",
			hook: OwnTenantsHook{Leave: whole.Leave},
			want: "session: SetOwnTenants was given a hook with no List or no Leave: pass the identity client's OwnTenants, or set none",
		},
		{
			name: "a hook with no Leave",
			hook: OwnTenantsHook{List: whole.List},
			want: "session: SetOwnTenants was given a hook with no List or no Leave: pass the identity client's OwnTenants, or set none",
		},
		{
			name:    "a second hook",
			prepare: func(f *authFixture) { f.auth.SetOwnTenants(unaskedOwnTenants()) },
			hook:    whole,
			want:    "session: the own-tenants hook was set twice: one Auth has one",
		},
		{
			name: "a hook set once the Auth is in use",
			prepare: func(f *authFixture) {
				f.serve(f.auth.Authenticated(), http.MethodGet, "/authenticated", nil, nil)
			},
			hook: whole,
			want: "session: the own-tenants hook was set after the Auth served a request: set it before serving",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), gomock.Any()).Return(nil, httpio.NewNotFoundMessage("no session")).AnyTimes()
			if tt.prepare != nil {
				tt.prepare(f)
			}

			recovered := registrationPanic(func() { f.auth.SetOwnTenants(tt.hook) })
			if msg, _ := recovered.(string); msg != tt.want {
				t.Errorf("SetOwnTenants() panic = %v, want %q", recovered, tt.want)
			}
		})
	}
}
