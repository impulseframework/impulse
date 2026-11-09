package access

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"go.uber.org/mock/gomock"
)

const ViewRolePermissions accesstypes.Permission = "ViewRolePermissions"

// TestHandlerClient_AddRole pins the request the handler decodes — the role's
// name and its description — and that it hands both to the manager and
// echoes both back; a role without a description is the manager's refusal,
// relayed as the request's error.
func TestHandlerClient_AddRole(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
		body   string
	}
	tests := []struct {
		name    string
		wantErr bool
		args    args
		prepare func(user *MockUserManager)
		want    string
	}{
		{
			name:    "Adds Viewer Role",
			wantErr: false,
			args:    args{domain: "tenant1", body: `{"roleName" : "Viewer", "description": "Reads the records of the tenant." }`},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRole(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Viewer"), "Reads the records of the tenant.").Return(nil).Times(1)
			},
			want: `{"role":"Viewer","description":"Reads the records of the tenant."}`,
		},
		{
			name:    "a name outside the grammar is refused",
			wantErr: true,
			args:    args{domain: "tenant1", body: `{"roleName" : "viewer", "description": "Reads the records of the tenant." }`},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRole(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("viewer"), "Reads the records of the tenant.").Return(httpio.NewBadRequestMessage("role viewer: a role name is letters and digits starting with a capital letter")).Times(1)
			},
		},
		{
			name:    "a role without a description is refused",
			wantErr: true,
			args:    args{domain: "tenant1", body: `{"roleName" : "Viewer" }`},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRole(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Viewer"), "").Return(httpio.NewBadRequestMessage("role \"Viewer\" carries no description")).Times(1)
			},
		},
		{
			name:    "fail to parse body",
			wantErr: true,
			args: args{
				domain: "tenant1",
			},
		},
		{
			name:    "fail to validate domain",
			args:    args{body: `{"roleName" : "Viewer", "description": "Reads the records of the tenant." }`},
			wantErr: true,
		},
		{
			name:    "fails to add a role",
			wantErr: true,
			args: args{
				domain: "tenant1",
				body:   `{"roleName" : "Viewer", "description": "Reads the records of the tenant." }`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRole(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Viewer"), "Reads the records of the tenant.").Return(errors.New("Failed to add the role")).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			req, err := createHTTPRequest(http.MethodPost, strings.NewReader(tt.args.body), map[httpio.ParamType]string{paramDomain: tt.args.domain})
			if err != nil {
				t.Error(err)
			}

			rr := httptest.NewRecorder()
			httpio.WithParams(h.AddRole()).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.AddRole() error = %v, wantErr = %v", got, tt.wantErr)
			}

			if got := strings.TrimSpace(rr.Body.String()); got != tt.want {
				t.Errorf("App.AddRole() body = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestHandlerClient_DeleteRole(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
		role   string
	}
	tests := []struct {
		name    string
		wantErr bool
		args    args
		prepare func(user *MockUserManager)
	}{
		{
			name:    "deletes Viewer Role",
			wantErr: false,
			args:    args{domain: "tenant1", role: "Viewer"},
			prepare: func(user *MockUserManager) {
				user.EXPECT().DeleteRole(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Viewer")).Return(true, nil).Times(1)
			},
		},
		{
			name:    "fail to validate domain",
			args:    args{domain: "", role: "Viewer"},
			wantErr: true,
		},
		{
			name:    "fail to validate role",
			args:    args{domain: "tenant1", role: ""},
			wantErr: true,
		},
		{
			name:    "fails to delete a role",
			wantErr: true,
			args:    args{domain: "tenant1", role: "Viewer"},
			prepare: func(user *MockUserManager) {
				user.EXPECT().DeleteRole(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Viewer")).Return(false, errors.New("Failed to add the role")).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			req, err := createHTTPRequest(http.MethodPost,
				http.NoBody,
				map[httpio.ParamType]string{paramDomain: tt.args.domain, paramRole: tt.args.role},
			)
			if err != nil {
				t.Error(err)
			}

			rr := httptest.NewRecorder()
			httpio.WithParams(h.DeleteRole()).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.AddRole() error = %v, wantErr = %v", got, tt.wantErr)
			}
		})
	}
}

func TestHandlerClient_AddRoleUsers(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
		role   string
		body   string
	}
	tests := []struct {
		name    string
		wantErr bool
		args    args
		prepare func(user *MockUserManager)
	}{
		{
			name:    "successfully adds users",
			wantErr: false,
			args: args{
				domain: "tenant1",
				role:   "Admin",
				body:   `{"users" : ["Daddy", "Bob"]}`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin"), accesstypes.User("Daddy"), accesstypes.User("Bob")).Return(nil).Times(1)
			},
		},
		{
			name:    "successfully adds users empty",
			wantErr: false,
			args: args{
				domain: "tenant1",
				role:   "Admin",
				body:   `{ "users" : [] }`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin")).Return(nil).Times(1)
			},
		},
		{
			name:    "fails to parse the request body",
			wantErr: true,
			args: args{
				domain: "",
				role:   "Admin",
				body:   `{"users": {abc}`,
			},
		},
		{
			name:    "fails on domain",
			wantErr: true,
			args: args{
				domain: "",
				role:   "Admin",
				body:   `{"users": []}`,
			},
		},
		{
			name:    "fails on role",
			wantErr: true,
			args: args{
				domain: "tenant1",
				role:   "",
				body:   `{"users": []}`,
			},
		},
		{
			name:    "fails to add the users",
			wantErr: true,
			args: args{
				domain: "tenant1",
				role:   "Admin",
				body:   `{"users": ["Johnny"]}`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().AddRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin"), accesstypes.User("Johnny")).Return(errors.New("failed to add the user to the role")).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			req, err := createHTTPRequest(http.MethodPost,
				strings.NewReader(tt.args.body),
				map[httpio.ParamType]string{paramDomain: tt.args.domain, paramRole: tt.args.role},
			)
			if err != nil {
				t.Error(err)
			}

			rr := httptest.NewRecorder()
			httpio.WithParams(h.AddRoleUsers()).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.AddRoleUsers() error = %v, wantErr = %v", got, tt.wantErr)
			}
		})
	}
}

func TestHandlerClient_DeleteRoleUsers(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
		role   string
		body   string
	}
	tests := []struct {
		name    string
		wantErr bool
		args    args
		prepare func(user *MockUserManager)
	}{
		{
			name:    "successfully adds users",
			wantErr: false,
			args: args{
				domain: "tenant1",
				role:   "Admin",
				body:   `{"users" : ["Daddy", "Bob"]}`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin"), accesstypes.User("Daddy"), accesstypes.User("Bob")).Return(nil).Times(1)
			},
		},
		{
			name:    "successfully adds users empty",
			wantErr: false,
			args: args{
				domain: "tenant1",
				role:   "Admin",
				body:   `{ "users" : [] }`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin")).Return(nil).Times(1)
			},
		},
		{
			name:    "fails to parse the request body",
			wantErr: true,
			args: args{
				domain: "",
				role:   "Admin",
				body:   `{"users": {abc}`,
			},
		},
		{
			name:    "fails on domain",
			wantErr: true,
			args: args{
				domain: "",
				role:   "Admin",
				body:   `{"users": []}`,
			},
		},
		{
			name:    "fails on role",
			wantErr: true,
			args: args{
				domain: "tenant1",
				role:   "",
				body:   `{"users": []}`,
			},
		},
		{
			name:    "fails to delete the users",
			wantErr: true,
			args: args{
				domain: "tenant1",
				role:   "Admin",
				body:   `{"users": ["Johnny"]}`,
			},
			prepare: func(user *MockUserManager) {
				user.EXPECT().DeleteRoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin"), accesstypes.User("Johnny")).Return(errors.New("failed to remove users from role")).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			req, err := createHTTPRequest(http.MethodPost,
				strings.NewReader(tt.args.body),
				map[httpio.ParamType]string{paramDomain: tt.args.domain, paramRole: tt.args.role},
			)
			if err != nil {
				t.Error(err)
			}

			rr := httptest.NewRecorder()
			httpio.WithParams(h.DeleteRoleUsers()).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.DeleteRoleUsers() error = %v, wantErr = %v", got, tt.wantErr)
			}
		})
	}
}

func TestHandlerClient_Roles(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
	}
	listed := []RoleInfo{
		{Name: "Editor", Description: "Edits the records of the tenant."},
		{Name: "Viewer", Description: "Reads the records of the tenant."},
	}
	tests := []struct {
		name    string
		args    args
		want    []RoleInfo
		prepare func(accessManager *MockUserManager)
		wantErr bool
	}{
		{
			name: "gets a list of roles, each with its name and description",
			want: listed,
			args: args{
				domain: "tenant1",
			},
			prepare: func(accessManager *MockUserManager) {
				accessManager.EXPECT().Roles(gomock.Any(), accesstypes.DomainPolicyScope("tenant1")).Return(listed, nil)
			},
		},
		{
			name: "fails to get roles and returns a 500",
			args: args{
				domain: "tenant1",
			},
			prepare: func(accessManager *MockUserManager) {
				accessManager.EXPECT().Roles(gomock.Any(), accesstypes.DomainPolicyScope("tenant1")).Return(nil, errors.New("Failed to get a list of roles")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "fails on domain",
			args: args{
				domain: "",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			req, err := createHTTPRequest(http.MethodGet, http.NoBody, map[httpio.ParamType]string{paramDomain: tt.args.domain})
			if err != nil {
				t.Error(err)
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			rr := httptest.NewRecorder()

			httpio.WithParams(h.Roles()).ServeHTTP(rr, req)

			// Check what the response code is. For 500 errors, execute this block
			if rr.Code == http.StatusInternalServerError {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.Roles() error = %v, wantErr = %v", got, tt.wantErr)
			}

			// parse the response body
			type response struct {
				Roles []RoleInfo `json:"roles"`
			}

			var got response
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Errorf("json.Unmarshal() error=%v", err)
			}

			// check if the response is what we expected by comparing the two
			if !reflect.DeepEqual(got.Roles, tt.want) {
				t.Errorf("App.Roles() = %v, want %v", &got, tt.want)
			}
		})
	}
}

func TestHandlerClient_RoleUsers(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
		role   string
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		prepare func(accessManager *MockUserManager)
		wantErr bool
	}{
		{
			name: "gets a list of users for role",
			want: []string{"daddy"},
			args: args{
				domain: "tenant1",
				role:   "Admin",
			},
			prepare: func(accessManager *MockUserManager) {
				accessManager.EXPECT().RoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), gomock.Any()).Return([]accesstypes.User{"daddy"}, nil)
			},
		},
		{
			name: "fails to get roles and returns a 500",
			args: args{
				domain: "tenant1",
				role:   "Admin",
			},
			prepare: func(accessManager *MockUserManager) {
				accessManager.EXPECT().RoleUsers(gomock.Any(), accesstypes.DomainPolicyScope("tenant1"), accesstypes.Role("Admin")).Return(nil, errors.New("Failed to get a list of roles")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "fails on domain",
			args: args{
				domain: "",
				role:   "Admin",
			},
			wantErr: true,
		},
		{
			name: "fails on role",
			args: args{
				domain: "tenant1",
				role:   "",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			req, err := createHTTPRequest(http.MethodGet, http.NoBody, map[httpio.ParamType]string{paramDomain: tt.args.domain, paramRole: tt.args.role})
			if err != nil {
				t.Error(err)
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			rr := httptest.NewRecorder()

			httpio.WithParams(h.RoleUsers()).ServeHTTP(rr, req)

			// Check what the response code is. For 500 errors, execute this block
			if rr.Code != http.StatusOK {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.RoleUsers() error = %v, wantErr = %v", got, tt.wantErr)
			}

			var got []string
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Errorf("json.Unmarshal() error=%v", err)
			}

			// check if the response is what we expected by comparing the two
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("App.RoleUsers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlerClient_RolePermissions(t *testing.T) {
	t.Parallel()

	type args struct {
		domain string
		role   string
	}
	tests := []struct {
		name    string
		args    args
		want    accesstypes.RolePermissionCollection
		prepare func(accessManager *MockUserManager)
		wantErr bool
	}{
		{
			name: "gets a list of permissions for role",
			want: accesstypes.RolePermissionCollection{"daddy": {Resources: []accesstypes.Resource{"widgets"}}},
			args: args{
				domain: "tenant1",
				role:   "Admin",
			},
			prepare: func(accessManager *MockUserManager) {
				accessManager.EXPECT().RolePermissions(gomock.Any(), accesstypes.DomainScope("tenant1"), gomock.Any()).Return(accesstypes.RolePermissionCollection{"daddy": {Resources: []accesstypes.Resource{"widgets"}}}, nil)
			},
		},
		{
			name: "fails to get permissions and returns a 500",
			args: args{
				domain: "tenant1",
				role:   "Admin",
			},
			prepare: func(accessManager *MockUserManager) {
				accessManager.EXPECT().RolePermissions(gomock.Any(), accesstypes.DomainScope("tenant1"), accesstypes.Role("Admin")).Return(nil, errors.New("Failed to get a list of permissions")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "fails on domain",
			args: args{
				domain: "",
				role:   "Admin",
			},
			wantErr: true,
		},
		{
			name: "fails on role",
			args: args{
				domain: "tenant1",
				role:   "",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			accessManager := NewMockUserManager(ctrl)

			h := &HandlerClient{
				manager: accessManager,
				handler: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if err := handler(w, r); err != nil {
							_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)
						}
					}
				},
			}

			req, err := createHTTPRequest(http.MethodGet, http.NoBody, map[httpio.ParamType]string{paramDomain: tt.args.domain, paramRole: tt.args.role})
			if err != nil {
				t.Error(err)
			}

			if tt.prepare != nil {
				tt.prepare(accessManager)
			}

			rr := httptest.NewRecorder()

			httpio.WithParams(h.RolePermissions()).ServeHTTP(rr, req)

			// Check what the response code is. For 500 errors, execute this block
			if rr.Code != http.StatusOK {
				if tt.wantErr {
					return
				}
				var got httpio.MessageResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Errorf("json.Unmarshal() error=%v", err)
				}
				t.Errorf("App.RolePermissions() error = %v, wantErr = %v", got, tt.wantErr)
			}

			var got accesstypes.RolePermissionCollection
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Errorf("json.Unmarshal() error=%v", err)
			}

			// check if the response is what we expected by comparing the two
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("App.RolePermissions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func createHTTPRequest(method string, body io.Reader, urlParams map[httpio.ParamType]string) (*http.Request, error) {
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, method, "", body)
	if err != nil {
		return nil, errors.Wrap(err, "http.NewRequestWithContext()")
	}
	rctx := chi.NewRouteContext()
	for key, val := range urlParams {
		rctx.URLParams.Add(string(key), val)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	return req, nil
}
