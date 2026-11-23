package check

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// ruledAuth is an auth package whose New sets the sign-in rule spliced into the first
// slot, the second slot holding the package's rule functions. The construction is on line
// 23, the policy statement on line 28.
const ruledAuth = `package staff

import (
	"context"
	_ "embed"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

type Session = session.Auth[session.NoCustomData, session.NoCustomData, *cloudspanner.ReadWriteTransaction]

type Auth struct {
	session *Session
}

func New(ctx context.Context, db *cloudspanner.Client, key string, collection access.PermissionCollection, mail session.MailSettings) (*Auth, error) {
	_ = access.PermissionCollection(collection)
	s, err := session.NewAuth[session.NoCustomData, session.NoCustomData](sessionstorage.NewSpannerAuth(db), key, session.NoSuperAdministrators(), mail)
	if err != nil {
		return nil, err
	}
	session.PasswordSignIn(s)
%s
	return &Auth{session: s}, nil
}

func (a *Auth) Session() *Session { return a.session }

var _ = sessioninfo.MethodEmailCode

var _ = signin.Allow
%s`

const (
	secondFactorFunc = `
func requireSecondFactor(context.Context, *cloudspanner.ReadWriteTransaction, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
	return signin.RequireMFA(sessioninfo.MethodEmailCode), nil
}
`
	holdFunc = `
func holdEveryone(context.Context, *cloudspanner.ReadWriteTransaction, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
	return signin.RequireMFA(sessioninfo.MethodEmailCode), nil
}
`
)

// The findings for ruledAuth with a one-line policy statement and secondFactorFunc: the
// call on line 28, the function on line 38.
const (
	secondFactorCallFinding = "pkg/auth/staff/staff.go:28: the staff auth's sign-in rule still names requireSecondFactor, so everyone is asked for the code whatever the Sign-in page's setting says; take it off the rule (remove .Then(requireSecondFactor), or the whole SetSignInPolicy call where it is the only rule): the session library asks for the second factor by the auth's sign-in setting"
	secondFactorFuncFinding = "pkg/auth/staff/staff.go:38: requireSecondFactor is no longer the second factor's rule; remove it: whether everyone is asked is the auth's sign-in setting, changed on the Sign-in page"
)

func TestSecondFactor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:       "the function, and the call chaining it after the tenant policy",
			files:      map[string]string{"pkg/auth/staff/staff.go": fmt.Sprintf(ruledAuth, "\ts.SetSignInPolicy(signin.TenantPolicy(nil, nil).Then(requireSecondFactor))", secondFactorFunc)},
			wantStatus: Fail, wantSummary: "1 auth(s) still carry requireSecondFactor",
			wantDetails: []string{secondFactorCallFinding, secondFactorFuncFinding},
		},
		{
			name:       "the function, and the call giving it as the only rule",
			files:      map[string]string{"pkg/auth/staff/staff.go": fmt.Sprintf(ruledAuth, "\ts.SetSignInPolicy(requireSecondFactor)", secondFactorFunc)},
			wantStatus: Fail, wantSummary: "1 auth(s) still carry requireSecondFactor",
			wantDetails: []string{secondFactorCallFinding, secondFactorFuncFinding},
		},
		{
			name:       "the function alone",
			files:      map[string]string{"pkg/auth/staff/staff.go": fmt.Sprintf(ruledAuth, "", secondFactorFunc)},
			wantStatus: Fail, wantSummary: "1 auth(s) still carry requireSecondFactor",
			wantDetails: []string{secondFactorFuncFinding},
		},
		{
			name:       "a rule of the application's own returning signin.RequireMFA under another name",
			files:      map[string]string{"pkg/auth/staff/staff.go": fmt.Sprintf(ruledAuth, "\ts.SetSignInPolicy(signin.TenantPolicy(nil, nil).Then(holdEveryone))", holdFunc)},
			wantStatus: Pass, wantSummary: "1 auth(s) leave the second factor to the sign-in setting: staff",
		},
		{
			name:       "no rule",
			files:      map[string]string{"pkg/auth/staff/staff.go": fmt.Sprintf(ruledAuth, "", "")},
			wantStatus: Pass, wantSummary: "1 auth(s) leave the second factor to the sign-in setting: staff",
		},
		{
			name:       "no auth package",
			files:      map[string]string{"pkg/config/data.go": "package config\n"},
			wantStatus: Skip, wantSummary: "no auth package",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := discoverFiles(t, tt.files)
			got := secondFactor{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: "second-factor", Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSecondFactorFix: the check has no remedy to apply, so --fix leaves the package as it
// was and reports it as a run without --fix does.
func TestSecondFactorFix(t *testing.T) {
	t.Parallel()

	source := fmt.Sprintf(ruledAuth, "\ts.SetSignInPolicy(signin.TenantPolicy(nil, nil).Then(requireSecondFactor))", secondFactorFunc)
	a := discoverFiles(t, map[string]string{"pkg/auth/staff/staff.go": source})
	got := secondFactor{}.Run(context.Background(), &Env{App: a, Fix: true})
	want := Result{Name: "second-factor", Status: Fail, Summary: "1 auth(s) still carry requireSecondFactor", Details: []string{secondFactorCallFinding, secondFactorFuncFinding}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
	src, err := os.ReadFile(a.Abs("pkg/auth/staff/staff.go"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(source, string(src)); diff != "" {
		t.Errorf("--fix changed the file (-before +after):\n%s", diff)
	}
}

// discoverFiles writes the files under a fresh module root and discovers the application.
func discoverFiles(t *testing.T, files map[string]string) *app.App {
	t.Helper()

	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
	for rel, content := range files {
		write(rel, content)
	}
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}
