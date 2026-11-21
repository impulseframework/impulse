package resource

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"golang.org/x/tools/go/packages"
)

// TestHeldDomains pins the list a rule on a person's tenants reads: every tenant the
// session's person holds a grant in, whatever the session shows of it. A mask that
// leaves nothing of the person's grants in their tenant takes the tenant off the
// session's own list (Domains) and not off this one, and so does a tenant that is closed
// to the way the session signed in. The first case is the one the create-your-tenant
// route met: a mask that keeps the creation, Execute alone, over a person whose tenant
// gives them reading, lists no tenant, and the person holds one.
func TestHeldDomains(t *testing.T) {
	t.Parallel()

	lakeside := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "tid-lakeside"}
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	directoryOnly := map[accesstypes.Domain]*signin.TenantDeclaration{
		"lakeside": {Directories: []signin.Directory{lakeside}, DirectoryOnly: true},
	}
	actingAsBob := func(mask accesstypes.PermissionMask) *sessioninfo.Impersonation {
		return &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.UserPrincipal("bob"), Mask: mask}
	}
	executeAlone := accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Execute)

	tests := []struct {
		name string
		// declared are the tenants' declarations, nil for an application that keeps none.
		declared map[accesstypes.Domain]*signin.TenantDeclaration
		// seated are the tenants the session's person holds a grant in: reading, in each.
		seated        []accesstypes.Domain
		established   sessioninfo.AuthEvent
		impersonation *sessioninfo.Impersonation
		// wantListed is the session's own list (Domains), and wantHeld the person's.
		wantListed []accesstypes.Domain
		wantHeld   []accesstypes.Domain
	}{
		{
			name:        "a person's own session lists what they hold",
			seated:      []accesstypes.Domain{"riverside", "lakeside"},
			established: password,
			wantListed:  []accesstypes.Domain{"lakeside", "riverside"},
			wantHeld:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a person who holds no tenant holds none",
			established: password,
			wantListed:  []accesstypes.Domain{},
			wantHeld:    []accesstypes.Domain{},
		},
		{
			name:          "a session that acts as a person under a mask that keeps Execute alone lists no tenant, and the person holds theirs",
			seated:        []accesstypes.Domain{"riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(executeAlone),
			wantListed:    []accesstypes.Domain{},
			wantHeld:      []accesstypes.Domain{"riverside"},
		},
		{
			name:          "the same in an application that keeps declarations",
			declared:      directoryOnly,
			seated:        []accesstypes.Domain{"riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(executeAlone),
			wantListed:    []accesstypes.Domain{},
			wantHeld:      []accesstypes.Domain{"riverside"},
		},
		{
			name:          "a session that acts as a person with no mask lists what the person holds",
			seated:        []accesstypes.Domain{"riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(accesstypes.AllowAll()),
			wantListed:    []accesstypes.Domain{"riverside"},
			wantHeld:      []accesstypes.Domain{"riverside"},
		},
		{
			name:        "a tenant closed to a password session is off the session's list, and the person holds it",
			declared:    directoryOnly,
			seated:      []accesstypes.Domain{"lakeside"},
			established: password,
			wantListed:  []accesstypes.Domain{},
			wantHeld:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:          "a tenant closed to a session that acts as its person, under a mask that hides the other: the person holds both",
			declared:      directoryOnly,
			seated:        []accesstypes.Domain{"lakeside", "riverside"},
			established:   sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"},
			impersonation: actingAsBob(executeAlone),
			wantListed:    []accesstypes.Domain{},
			wantHeld:      []accesstypes.Domain{"lakeside", "riverside"},
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"riverside", "lakeside", "unseated", "riverside"}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, digest: accesstypes.PermissionDigest{"documents": {accesstypes.Read: accesstypes.DigestGranted}}, domains: tt.seated}
			}
			var declarations TenantDeclarations
			if tt.declared != nil {
				declarations = (&declarationReader{declarations: tt.declared}).read
			}
			perms := SessionPermissions(sessionCtx("alice", tt.impersonation, tt.established), forUser, RolePrincipalsUnsupported, roster, declarations, nil)

			listed, err := perms.Domains(context.Background())
			if err != nil {
				t.Fatalf("Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantListed, listed); diff != "" {
				t.Errorf("Domains(), the session's own list, mismatch (-want +got):\n%s", diff)
			}
			held, err := perms.HeldDomains(context.Background())
			if err != nil {
				t.Fatalf("HeldDomains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantHeld, held); diff != "" {
				t.Errorf("HeldDomains(), every tenant the person holds, mismatch (-want +got):\n%s", diff)
			}

			// Permissions an application wrapped answer the same: the method is the
			// interface's, so the wrapper passes it through where it declares none.
			wrapped := audited{UserPermissions: perms, audit: new([]string)}
			listed, err = wrapped.Domains(context.Background())
			if err != nil {
				t.Fatalf("wrapped: Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantListed, listed); diff != "" {
				t.Errorf("wrapped: Domains(), the session's own list, mismatch (-want +got):\n%s", diff)
			}
			held, err = UserPermissions(wrapped).HeldDomains(context.Background())
			if err != nil {
				t.Fatalf("wrapped: HeldDomains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantHeld, held); diff != "" {
				t.Errorf("wrapped: HeldDomains(), every tenant the person holds, mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// audited is permissions an application wrapped to add a method of its own: the value
// SessionPermissions composed, embedded, as an application's UserPermissions(r) may
// return it.
type audited struct {
	UserPermissions
	audit *[]string
}

// Note is the wrapper's own method.
func (a audited) Note(line string) {
	*a.audit = append(*a.audit, line)
}

// TestHeldDomains_everyPermissions pins what the list is for the permissions that are
// not a person's session under a roster: a mask over permissions written by hand, which
// passes the question through; an application without tenants, which has none to hold;
// a roster that fails, whose error is the answer's; and a session that acts as a role
// the application runs no sessions as, which is refused the answer, since a rule that
// counts a person's tenants would pass on none.
func TestHeldDomains_everyPermissions(t *testing.T) {
	t.Parallel()

	forUser := func(user accesstypes.User) *stubPermissions {
		return &stubPermissions{user: user, domains: []accesstypes.Domain{"lakeside"}}
	}
	password := sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}
	failing := func(context.Context) ([]accesstypes.Domain, error) {
		return nil, errors.New("roster unavailable")
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"lakeside"}, nil
	}
	asAuditor := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Auditor"), Mask: accesstypes.AllowAll()}
	tests := []struct {
		name  string
		perms UserPermissions
		want  []accesstypes.Domain
		// wantErr says the answer is an error, and wantForbidden that it is a refusal.
		wantErr       bool
		wantForbidden bool
	}{
		{
			name:  "a mask over permissions written by hand asks them, and never lists for them",
			perms: Masked(&stubPermissions{user: "alice", domains: []accesstypes.Domain{"lakeside"}}, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Execute), nil),
			want:  []accesstypes.Domain{"lakeside"},
		},
		{
			name:  "an application without tenants",
			perms: SessionPermissions(sessionCtx("alice", nil, password), forUser, RolePrincipalsUnsupported, nil, nil, nil),
			want:  []accesstypes.Domain{},
		},
		{
			name:    "a roster that fails",
			perms:   SessionPermissions(sessionCtx("alice", nil, password), forUser, RolePrincipalsUnsupported, failing, nil, nil),
			wantErr: true,
		},
		{
			name: "a session that acts as a role the application runs no sessions as is refused the answer",
			perms: SessionPermissions(
				sessionCtx("alice", asAuditor, sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"}), forUser, RolePrincipalsUnsupported, roster, nil, nil,
			),
			wantErr:       true,
			wantForbidden: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held, err := tt.perms.HeldDomains(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("HeldDomains() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if held != nil {
					t.Errorf("HeldDomains() = %v beside its error, want no list", held)
				}
				if httpio.HasForbidden(err) != tt.wantForbidden {
					t.Errorf("HeldDomains() error = %v, want a refusal: %v", err, tt.wantForbidden)
				}
				if tt.wantForbidden {
					plainAnswer(t, err, httpio.HasForbidden, "You are acting as a role, and this session cannot do this.", "Auditor", "runs no sessions as")
				}

				return
			}
			if held == nil {
				t.Fatal("HeldDomains() = nil, want a list, never nil")
			}
			if diff := cmp.Diff(tt.want, held); diff != "" {
				t.Errorf("HeldDomains() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHeldDomains_roleSession pins whose tenants a session that runs with a role's
// grants holds: the real person's behind it, never the role's. The role's checker has a
// foothold in the tenant named tenant alone, so the session lists that one; the person
// holds riverside, and that is the list a rule on a person's tenants reads. A person
// another application authenticated has no account here: they hold none, and no checker
// is asked for a name another application spelled.
func TestHeldDomains_roleSession(t *testing.T) {
	t.Parallel()

	impersonation := sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"}
	tests := []struct {
		name string
		ctx  context.Context
		// wantListed is the session's own list, the role's footholds, and wantHeld the
		// person's. wantAsked are the people a checker was built for when the person's
		// tenants were asked.
		wantListed []accesstypes.Domain
		wantHeld   []accesstypes.Domain
		wantAsked  []accesstypes.User
	}{
		{
			name: "a session a person started to act as a role: the person's tenants, by the id of their account",
			ctx: sessionCtx("alice", &sessioninfo.Impersonation{
				Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}, impersonation),
			wantListed: []accesstypes.Domain{"tenant"},
			wantHeld:   []accesstypes.Domain{"riverside"},
			wantAsked:  []accesstypes.User{aliceUser},
		},
		{
			name: "a session a person of another application started to act as a role: nobody with an account here, who holds none",
			ctx: sessionCtx("alice", &sessioninfo.Impersonation{
				Actor: "alice", ActorRealm: "admin-portal", Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}, impersonation),
			wantListed: []accesstypes.Domain{"tenant"},
			wantHeld:   []accesstypes.Domain{},
		},
		{
			name:       "a person's own session that a resolver gave a role for the request: the person's tenants",
			ctx:        resolvedAs(accountCtx("alice", aliceID, nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}), accesstypes.RolePrincipal("PartnerViewer")),
			wantListed: []accesstypes.Domain{"tenant"},
			wantHeld:   []accesstypes.Domain{"riverside"},
			wantAsked:  []accesstypes.User{aliceUser},
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"tenant", "riverside", "lakeside"}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked []accesstypes.User
			forUser := func(user accesstypes.User) *stubPermissions {
				asked = append(asked, user)

				return &stubPermissions{user: user, domains: []accesstypes.Domain{"riverside"}}
			}
			forRole := func(role accesstypes.Role) *stubRolePermissions {
				return &stubRolePermissions{role: role}
			}
			perms := SessionPermissions(tt.ctx, forUser, forRole, roster, nil, nil)

			listed, err := perms.Domains(context.Background())
			if err != nil {
				t.Fatalf("Domains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantListed, listed); diff != "" {
				t.Errorf("Domains(), the session's own list, mismatch (-want +got):\n%s", diff)
			}
			if len(asked) != 0 {
				t.Errorf("a checker was built for %v before the person's tenants were asked, want none: the session runs with the role's", asked)
			}
			held, err := perms.HeldDomains(context.Background())
			if err != nil {
				t.Fatalf("HeldDomains() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantHeld, held); diff != "" {
				t.Errorf("HeldDomains(), every tenant the person holds, mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, asked); diff != "" {
				t.Errorf("the people a checker was built for mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHeldDomains_unansweredDoesNotCompile type-checks the heldunanswered program, an
// application's permissions written by hand that list the session's tenants and do not
// say what the person holds, and pins that the type checker refuses them as the
// request's permissions, in words that name the method. The rule that counts a
// person's tenants therefore never meets permissions that cannot answer it, and has no
// list to fall back to.
func TestHeldDomains_unansweredDoesNotCompile(t *testing.T) {
	t.Parallel()

	// A test runs in its package's directory. The program is kept outside the library's
	// packages, where an application is.
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, "../testdata/heldunanswered")
	if err != nil {
		t.Fatalf("packages.Load() error = %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("packages.Load() loaded %d packages, want 1", len(pkgs))
	}
	program := pkgs[0]
	if len(program.Errors) != 1 {
		t.Fatalf("errors = %q, want exactly one: the line that hands the permissions over", program.Errors)
	}
	got := program.Errors[0]
	if got.Kind != packages.TypeError {
		t.Errorf("error (%q) is of kind %d, want the type checker's (%d)", got.Msg, got.Kind, packages.TypeError)
	}
	for _, want := range []string{"listed does not implement ", ".UserPermissions (missing method HeldDomains)"} {
		if !strings.Contains(got.Msg, want) {
			t.Errorf("error = %q, want it to say %q", got.Msg, want)
		}
	}
}
