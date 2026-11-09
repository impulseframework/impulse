package access

// These tests pin the EveryDomain handler forms: a membership held in every
// tenant domain has no domain to name, so the handlers take the role
// parameter alone and address the manager with
// accesstypes.EveryDomainPolicyScope(). Beside them, one table pins what the
// existing {domain} handler tests leave open: the {domain} parameter is data,
// so a domain spelled "global" or "every domain" is one tenant domain of that
// name — the manager is addressed with DomainPolicyScope (DomainScope for
// RolePermissions, a request scope), never the structural kind.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"go.uber.org/mock/gomock"
)

// everyDomainHandlerClient wires a HandlerClient over the mock manager with
// the log handler the handler tests share: a returned error is encoded as the
// client message.
func everyDomainHandlerClient(manager UserManager) *HandlerClient {
	return &HandlerClient{
		manager: manager,
		handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if err := handler(w, r); err != nil {
					_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
				}
			}
		},
	}
}

// serveEveryDomain runs one handler against a request carrying only the role
// parameter — no domain parameter exists for the EveryDomain forms, so a
// handler that read one would answer 400 — and returns the recorder.
func serveEveryDomain(t *testing.T, handler http.HandlerFunc, method, role string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req, err := createHTTPRequest(method, body, map[httpio.ParamType]string{paramRole: role})
	if err != nil {
		t.Fatalf("createHTTPRequest() error = %v", err)
	}
	rr := httptest.NewRecorder()
	httpio.WithParams(handler).ServeHTTP(rr, req)

	return rr
}

func TestHandlerClient_AddRoleUsersEveryDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    string
		body    string
		prepare func(manager *MockUserManager)
		wantErr bool
	}{
		{
			name: "assigns the memberships in every domain",
			role: "Admin",
			body: `{"users": ["Daddy", "Bob"]}`,
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin"), accesstypes.User("Daddy"), accesstypes.User("Bob")).Return(nil).Times(1)
			},
		},
		{
			name: "an empty list reaches the manager as no users",
			role: "Admin",
			body: `{"users": []}`,
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin")).Return(nil).Times(1)
			},
		},
		{
			name:    "a body that does not parse is refused before the manager is asked",
			role:    "Admin",
			body:    `{"users": {abc}`,
			wantErr: true,
		},
		{
			name:    "a missing role is refused",
			role:    "",
			body:    `{"users": ["Daddy"]}`,
			wantErr: true,
		},
		{
			name: "the manager's refusal is answered",
			role: "Admin",
			body: `{"users": ["Johnny"]}`,
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin"), accesstypes.User("Johnny")).Return(errors.New("failed to add the user to the role")).Times(1)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manager := NewMockUserManager(gomock.NewController(t))
			if tt.prepare != nil {
				tt.prepare(manager)
			}
			h := everyDomainHandlerClient(manager)

			rr := serveEveryDomain(t, h.AddRoleUsersEveryDomain(), http.MethodPost, tt.role, strings.NewReader(tt.body))
			if (rr.Code != http.StatusOK) != tt.wantErr {
				t.Errorf("AddRoleUsersEveryDomain() status = %d, body = %s, wantErr %v", rr.Code, rr.Body.String(), tt.wantErr)
			}
		})
	}
}

func TestHandlerClient_DeleteRoleUsersEveryDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    string
		body    string
		prepare func(manager *MockUserManager)
		wantErr bool
	}{
		{
			name: "removes the memberships held in every domain",
			role: "Admin",
			body: `{"users": ["Daddy", "Bob"]}`,
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin"), accesstypes.User("Daddy"), accesstypes.User("Bob")).Return(nil).Times(1)
			},
		},
		{
			name: "an empty list reaches the manager as no users",
			role: "Admin",
			body: `{"users": []}`,
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin")).Return(nil).Times(1)
			},
		},
		{
			name:    "a body that does not parse is refused before the manager is asked",
			role:    "Admin",
			body:    `{"users": {abc}`,
			wantErr: true,
		},
		{
			name:    "a missing role is refused",
			role:    "",
			body:    `{"users": ["Daddy"]}`,
			wantErr: true,
		},
		{
			name: "the manager's refusal is answered",
			role: "Admin",
			body: `{"users": ["Johnny"]}`,
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin"), accesstypes.User("Johnny")).Return(errors.New("failed to remove users from role")).Times(1)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manager := NewMockUserManager(gomock.NewController(t))
			if tt.prepare != nil {
				tt.prepare(manager)
			}
			h := everyDomainHandlerClient(manager)

			rr := serveEveryDomain(t, h.DeleteRoleUsersEveryDomain(), http.MethodPost, tt.role, strings.NewReader(tt.body))
			if (rr.Code != http.StatusOK) != tt.wantErr {
				t.Errorf("DeleteRoleUsersEveryDomain() status = %d, body = %s, wantErr %v", rr.Code, rr.Body.String(), tt.wantErr)
			}
		})
	}
}

func TestHandlerClient_RoleUsersEveryDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    string
		prepare func(manager *MockUserManager)
		want    []string
		wantErr bool
	}{
		{
			name: "lists the members whose membership is held in every domain",
			role: "Admin",
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().RoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin")).Return([]accesstypes.User{"daddy", "erin"}, nil).Times(1)
			},
			want: []string{"daddy", "erin"},
		},
		{
			name:    "a missing role is refused",
			role:    "",
			wantErr: true,
		},
		{
			name: "the manager's failure is answered",
			role: "Admin",
			prepare: func(manager *MockUserManager) {
				manager.EXPECT().RoleUsers(gomock.Any(), accesstypes.EveryDomainPolicyScope(), accesstypes.Role("Admin")).Return(nil, errors.New("failed to list the role's users")).Times(1)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manager := NewMockUserManager(gomock.NewController(t))
			if tt.prepare != nil {
				tt.prepare(manager)
			}
			h := everyDomainHandlerClient(manager)

			rr := serveEveryDomain(t, h.RoleUsersEveryDomain(), http.MethodGet, tt.role, http.NoBody)
			if (rr.Code != http.StatusOK) != tt.wantErr {
				t.Fatalf("RoleUsersEveryDomain() status = %d, body = %s, wantErr %v", rr.Code, rr.Body.String(), tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			var got []string
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RoleUsersEveryDomain() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHandlerClient_domainParamIsData pins that the {domain} forms address
// the manager with the domain the URL names as one tenant domain, whatever it
// is spelled: "global" and "every domain" are tenant names here, since the
// structural kinds have no URL spelling. RolePermissions, which asks what a
// role holds in a place a request is, takes the request scope of that domain.
func TestHandlerClient_domainParamIsData(t *testing.T) {
	t.Parallel()

	handlers := []struct {
		name   string
		method string
		body   string
		serve  func(h *HandlerClient) http.HandlerFunc
		expect func(manager *MockUserManager, domain accesstypes.Domain)
	}{
		{
			name: "AddRole", method: http.MethodPost, body: `{"roleName": "Viewer", "description": "Reads the records of the tenant."}`,
			serve: (*HandlerClient).AddRole,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().AddRole(gomock.Any(), accesstypes.DomainPolicyScope(domain), accesstypes.Role("Viewer"), "Reads the records of the tenant.").Return(nil).Times(1)
			},
		},
		{
			name: "DeleteRole", method: http.MethodPost,
			serve: (*HandlerClient).DeleteRole,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().DeleteRole(gomock.Any(), accesstypes.DomainPolicyScope(domain), accesstypes.Role("Viewer")).Return(true, nil).Times(1)
			},
		},
		{
			name: "AddRoleUsers", method: http.MethodPost, body: `{"users": ["dana"]}`,
			serve: (*HandlerClient).AddRoleUsers,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope(domain), accesstypes.Role("Viewer"), accesstypes.User("dana")).Return(nil).Times(1)
			},
		},
		{
			name: "DeleteRoleUsers", method: http.MethodPost, body: `{"users": ["dana"]}`,
			serve: (*HandlerClient).DeleteRoleUsers,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope(domain), accesstypes.Role("Viewer"), accesstypes.User("dana")).Return(nil).Times(1)
			},
		},
		{
			name: "Roles", method: http.MethodGet,
			serve: (*HandlerClient).Roles,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().Roles(gomock.Any(), accesstypes.DomainPolicyScope(domain)).Return([]RoleInfo{{Name: "Viewer", Description: "Reads the records of the tenant."}}, nil).Times(1)
			},
		},
		{
			name: "RoleUsers", method: http.MethodGet,
			serve: (*HandlerClient).RoleUsers,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().RoleUsers(gomock.Any(), accesstypes.DomainPolicyScope(domain), accesstypes.Role("Viewer")).Return([]accesstypes.User{"dana"}, nil).Times(1)
			},
		},
		{
			name: "RolePermissions", method: http.MethodGet,
			serve: (*HandlerClient).RolePermissions,
			expect: func(manager *MockUserManager, domain accesstypes.Domain) {
				manager.EXPECT().RolePermissions(gomock.Any(), accesstypes.DomainScope(domain), accesstypes.Role("Viewer")).Return(accesstypes.RolePermissionCollection{}, nil).Times(1)
			},
		},
	}
	domains := []accesstypes.Domain{"global", "every domain"}
	for _, handler := range handlers {
		t.Run(handler.name, func(t *testing.T) {
			t.Parallel()
			for _, domain := range domains {
				t.Run(string(domain), func(t *testing.T) {
					t.Parallel()
					manager := NewMockUserManager(gomock.NewController(t))
					handler.expect(manager, domain)
					h := everyDomainHandlerClient(manager)

					var body io.Reader = http.NoBody
					if handler.body != "" {
						body = strings.NewReader(handler.body)
					}
					req, err := createHTTPRequest(handler.method, body, map[httpio.ParamType]string{paramDomain: string(domain), paramRole: "Viewer"})
					if err != nil {
						t.Fatalf("createHTTPRequest() error = %v", err)
					}
					rr := httptest.NewRecorder()
					httpio.WithParams(handler.serve(h)).ServeHTTP(rr, req)

					if rr.Code != http.StatusOK {
						t.Errorf("%s() with domain %q status = %d, body = %s, want 200", handler.name, domain, rr.Code, rr.Body.String())
					}
				})
			}
		})
	}
}
