package resource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// domainsStubPermissions scripts Domains; Check, PermissionDigest, and User
// exist only to satisfy UserPermissions.
type domainsStubPermissions struct {
	domains []accesstypes.Domain
	err     error
}

func (s *domainsStubPermissions) Check(context.Context, accesstypes.Environment, accesstypes.Scope, accesstypes.Permission, ...accesstypes.Resource) (accesstypes.Decisions, error) {
	return accesstypes.Decisions{}, nil
}

func (s *domainsStubPermissions) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{}, nil
}

func (s *domainsStubPermissions) HasGrants(context.Context, accesstypes.Scope) (bool, error) {
	return len(s.domains) > 0, s.err
}

func (s *domainsStubPermissions) Domains(context.Context) ([]accesstypes.Domain, error) {
	return s.domains, s.err
}

func (s *domainsStubPermissions) Reaches(context.Context, accesstypes.Domain) (bool, error) {
	return true, nil
}

func (s *domainsStubPermissions) User() accesstypes.User { return "dana" }

// stubNames names the domains it holds and no other.
type stubNames map[accesstypes.Domain]string

func (n stubNames) Name(domain accesstypes.Domain) string {
	return n[domain]
}

// TestUserDomainsHandler pins the user-domains wire contract: the checker's
// sorted list verbatim, each domain with its name (the namer's, the domain itself
// where it holds none or no namer is given), an empty membership as [] (never null,
// so a picker iterates without a null guard), and a checker error as an error response.
func TestUserDomainsHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stub       *domainsStubPermissions
		names      DomainNamer
		wantStatus int
		wantBody   string
	}{
		{
			name:       "membership lists verbatim, each domain its own name without a namer",
			stub:       &domainsStubPermissions{domains: []accesstypes.Domain{"tenant1", "tenant2"}},
			wantStatus: http.StatusOK,
			wantBody:   `[{"domain":"tenant1","name":"tenant1"},{"domain":"tenant2","name":"tenant2"}]`,
		},
		{
			name:       "the namer names each domain, the domain standing in where it holds no name",
			stub:       &domainsStubPermissions{domains: []accesstypes.Domain{"tenant1", "tenant2"}},
			names:      stubNames{"tenant1": "Ridgeline County"},
			wantStatus: http.StatusOK,
			wantBody:   `[{"domain":"tenant1","name":"Ridgeline County"},{"domain":"tenant2","name":"tenant2"}]`,
		},
		{
			name:       "no membership is an empty array, never null",
			stub:       &domainsStubPermissions{},
			wantStatus: http.StatusOK,
			wantBody:   `[]`,
		},
		{
			name:       "checker error is an error response",
			stub:       &domainsStubPermissions{err: errors.New("engine unavailable")},
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := UserDomainsHandler(func(*http.Request) UserPermissions { return tt.stub }, WithDomainNames(tt.names))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/user-domains", http.NoBody)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("response code = %d, want %d (body %q)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantBody != "" && strings.TrimSpace(rr.Body.String()) != tt.wantBody {
				t.Errorf("body = %q, want %q", strings.TrimSpace(rr.Body.String()), tt.wantBody)
			}
		})
	}
}

// TestUserDomainsHandler_admission pins the endpoint over the composed session
// permissions: the picker lists the tenants the session holds a grant in, and a
// tenant whose declaration does not admit the way the session was established
// is listed closed, with the reason in a sentence that names the tenant and says
// how the person signed in; a super administrator sees every tenant they hold a
// grant in open, and a person holding another global role does not. A tenant's own
// administrator whose password session carries no second factor is told the way in
// they hold, and nobody else is.
func TestUserDomainsHandler_admission(t *testing.T) {
	t.Parallel()

	const (
		anvil   accesstypes.Domain = "anvil"
		bastion accesstypes.Domain = "bastion"
	)
	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "acme"}
	declarations := map[accesstypes.Domain]*signin.TenantDeclaration{
		anvil:   {Directories: []signin.Directory{workspace}, DirectoryOnly: true},
		bastion: {Directories: []signin.Directory{workspace}},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{anvil, bastion}, nil
	}

	tests := []struct {
		name   string
		ctx    context.Context
		global bool
		super  bool
		// administers are the tenants the person administers.
		administers []accesstypes.Domain
		// namers say what the application's registrations call a directory.
		namers   []DirectoryNamer
		wantBody string
	}{
		{
			name:        "the tenant's own administrator, in a password session without the code, is told the code is what is missing",
			ctx:         sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}),
			administers: []accesstypes.Domain{anvil},
			wantBody:    `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in with a password. As one of its administrators you can still enter it: sign out, then sign in again with your password and the code we email you."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:        "the tenant's own administrator with the password and the code enters, and is told nothing",
			ctx:         sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}, sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode}),
			administers: []accesstypes.Domain{anvil},
			wantBody:    `[{"domain":"anvil","name":"Anvil Works"},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:        "the tenant's own administrator who came through another directory is not told of a code: the way in is the password",
			ctx:         sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodAzure, Connection: "tenant-id"}),
			administers: []accesstypes.Domain{anvil},
			wantBody:    `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in through your organization's Microsoft directory."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:        "a person who administers another tenant is told of no code in this one",
			ctx:         sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}),
			administers: []accesstypes.Domain{bastion},
			wantBody:    `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in with a password."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a password session lists the directory-only tenant closed, with the reason, beside the tenant it enters",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}),
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in with a password."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a session through the directory-only tenant's directory lists both open",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "acme"}),
			wantBody: `[{"domain":"anvil","name":"Anvil Works"},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name: "a session through another directory enters the tenant that is not directory only, and is told which directory it came through, by the name a registration gives it",
			ctx:  sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodAzure, Connection: "tenant-id"}),
			namers: []DirectoryNamer{func(directory signin.Directory) (string, string) {
				if directory.Connection == "tenant-id" {
					return "Lakeside", "Microsoft"
				}

				return "", ""
			}},
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in through Microsoft Entra tenant Lakeside."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a directory no registration names is said by the label of its button, never by its id",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodAzure, Connection: "tenant-id"}),
			namers:   []DirectoryNamer{func(signin.Directory) (string, string) { return "", "Staff sign-in" }},
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in through your organization's Staff sign-in directory."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "with nobody to ask, a tenant's directory is said by its kind, never by its id",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodAzure, Connection: "tenant-id"}),
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in through your organization's Microsoft directory."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a personal account is named as one",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection}),
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in with a personal Google account."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a session that views the application as someone else lists the tenants it reaches and none closed: the word is for a person's own sign-in",
			ctx:      sessionCtx("dana", &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.UserPrincipal("dana")}, sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"}),
			wantBody: `[{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a session that records no sign-in is told it did not come through the directory",
			ctx:      sessionCtx("dana", nil),
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. This session was not signed in through it."},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a super administrator lists every tenant they hold a grant in, none closed",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}),
			global:   true,
			super:    true,
			wantBody: `[{"domain":"anvil","name":"Anvil Works"},{"domain":"bastion","name":"bastion"}]`,
		},
		{
			name:     "a person holding another global role is held to the tenant's switch like anyone",
			ctx:      sessionCtx("dana", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}),
			global:   true,
			wantBody: `[{"domain":"anvil","name":"Anvil Works","closed":"Anvil Works admits only people who sign in through its own directory. You signed in with a password."},{"domain":"bastion","name":"bastion"}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, domains: []accesstypes.Domain{anvil, bastion}, global: tt.global, super: tt.super, administers: tt.administers}
			}
			reader := &declarationReader{declarations: declarations}
			handler := UserDomainsHandler(func(r *http.Request) UserPermissions {
				return SessionPermissions(r.Context(), forUser, RolePrincipalsUnsupported, roster, reader.read, nil)
			}, WithDomainNames(stubNames{anvil: "Anvil Works"}), WithDirectoryNames(tt.namers...))

			req := httptest.NewRequestWithContext(tt.ctx, http.MethodGet, "/api/user-domains", http.NoBody)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("response code = %d, want 200 (body %q)", rr.Code, rr.Body.String())
			}
			if got := strings.TrimSpace(rr.Body.String()); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}
