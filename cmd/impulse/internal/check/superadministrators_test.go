package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

func TestSuperAdministratorsTold(t *testing.T) {
	t.Parallel()

	const (
		ownForm  = "pass session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))) over the engine %s.New opens with access.WithDefaultRoles(Roles(), Name)"
		staffOwn = "pass session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))) over the engine staff.New opens with access.WithDefaultRoles(Roles(), Name)"
	)
	membersOwn := strings.Replace(ownForm, "%s", "members", 1)
	// The members auth beside the staff auth: the same package under its own name, which
	// imports the staff auth's package.
	membersAuth := strings.NewReplacer(
		"package staff", "package members",
		`"staff"`, `"members"`,
		`"Staff"`, `"Members"`,
		"\t\"github.com/impulseframework/impulse/session/signin\"\n", "\t\"github.com/impulseframework/impulse/session/signin\"\n\n\t\"example.com/harbor/pkg/auth/staff\"\n",
	).Replace(staffAuth)
	one := func(auth string) map[string]string {
		return map[string]string{
			"pkg/auth/staff/staff.go":   auth,
			"pkg/auth/staff/roles.json": rolesJSON,
		}
	}
	two := func(members string) map[string]string {
		files := one(staffAuth)
		files["pkg/auth/members/members.go"] = members
		files["pkg/auth/members/roles.json"] = rolesJSON

		return files
	}
	told := func(auth, argument string) string {
		return strings.Replace(auth, staffSuperAdministrators, argument, 1)
	}
	// The members auth told of nobody, beside a staff auth whose argument does not read.
	both := two(told(membersAuth, "session.NoSuperAdministrators()"))
	both["pkg/auth/staff/staff.go"] = told(staffAuth, "superAdministrators")

	tests := []struct {
		name        string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "a check over the engine the package opens with its own role file", files: one(staffAuth),
			wantStatus: Pass, wantSummary: "1 Auth(s) told their super administrators by the engine their package opens with its own role file: staff",
		},
		{
			name: "two auths, each by its own engine", files: two(membersAuth),
			wantStatus: Pass, wantSummary: "2 Auth(s) told their super administrators by the engine their package opens with its own role file: members, staff",
		},
		{
			name: "an auth with a role file told it has no super administrators", files: one(told(staffAuth, "session.NoSuperAdministrators()")),
			wantStatus: Fail, wantSummary: "1 Auth(s) not told who their own auth's super administrators are",
			wantDetails: []string{"pkg/auth/staff/staff.go:37: the staff auth has a role file (pkg/auth/staff/roles.json), so its people hold roles and the Super Administrator role is among them, and its Auth is built with session.NoSuperAdministrators(), which leaves a holder to the sign-in rule alone, with no second factor of the library's own; " + staffOwn},
		},
		{
			name:       "a check over an engine handed another auth's role file",
			files:      two(strings.Replace(membersAuth, "access.WithDefaultRoles(Roles(), Name)", "access.WithDefaultRoles(staff.Roles(), staff.Name)", 1)),
			wantStatus: Fail, wantSummary: "1 Auth(s) not told who their own auth's super administrators are",
			wantDetails: []string{"pkg/auth/members/members.go:39: the members auth's Auth takes its super administrators from the engine opened at pkg/auth/members/members.go:35, which is handed the staff auth's role file and is that auth's engine, so it holds that auth's holders to the second factor and none of its own; " + membersOwn},
		},
		{
			name:       "a check taken from another auth's package",
			files:      two(told(membersAuth, "session.SuperAdministratorsBy(staff.SuperAdministrators())")),
			wantStatus: Fail, wantSummary: "1 Auth(s) not told who their own auth's super administrators are",
			wantDetails: []string{"pkg/auth/members/members.go:39: the members auth's Auth takes its super administrators from the staff auth's package (example.com/harbor/pkg/auth/staff), so it holds that auth's holders to the second factor and none of its own; " + membersOwn},
		},
		{
			name:       "a check over an engine the constructor is handed is not judged",
			files:      one(told(staffAuth, "session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(users)))")),
			wantStatus: Warn, wantSummary: "1 Auth(s) whose super administrators cannot be followed to an engine",
			wantDetails: []string{"pkg/auth/staff/staff.go:37: the check the staff auth's Auth is handed does not lead to a permission engine opened in the same function (access.New), so whose roles it reads is not judged here; " + staffOwn},
		},
		{
			name:       "a check over an engine whose role file does not read is not judged",
			files:      one(strings.Replace(staffAuth, "access.WithDefaultRoles(Roles(), Name)", "options...", 1)),
			wantStatus: Warn, wantSummary: "1 Auth(s) whose super administrators cannot be followed to an engine",
			wantDetails: []string{"pkg/auth/staff/staff.go:37: the check the staff auth's Auth is handed is built over the engine opened at pkg/auth/staff/staff.go:33, and the role file that engine is handed does not read there (no access.WithDefaultRoles(Roles(), Name) among its arguments), so whose engine it is is not judged here; " + staffOwn},
		},
		{
			name:       "a check over an engine handed a role file that is no auth package's is not judged",
			files:      one(strings.Replace(staffAuth, "access.WithDefaultRoles(Roles(), Name)", "access.WithDefaultRoles(signin.Roles(), signin.Name)", 1)),
			wantStatus: Warn, wantSummary: "1 Auth(s) whose super administrators cannot be followed to an engine",
			wantDetails: []string{"pkg/auth/staff/staff.go:37: the check the staff auth's Auth is handed is built over the engine opened at pkg/auth/staff/staff.go:33, which is handed the Roles() of github.com/impulseframework/impulse/session/signin, no auth package of the application, and not the package's own, so whose engine it is is not judged here; " + staffOwn},
		},
		{
			name:       "a check written by hand as a function is not judged",
			files:      one(told(staffAuth, "session.SuperAdministratorsBy(func(context.Context, string) (bool, error) { _ = engine; return false, nil })")),
			wantStatus: Warn, wantSummary: "1 Auth(s) whose super administrators cannot be followed to an engine",
			wantDetails: []string{"pkg/auth/staff/staff.go:37: the check the staff auth's Auth is handed does not lead to a permission engine opened in the same function (access.New), so whose roles it reads is not judged here; " + staffOwn},
		},
		{
			name: "an argument the constructor is handed is not judged", files: one(told(staffAuth, "superAdministrators")),
			wantStatus: Warn, wantSummary: "1 Auth(s) whose super administrators cannot be followed to an engine",
			wantDetails: []string{"pkg/auth/staff/staff.go:37: what the staff auth's Auth is told about its super administrators does not read here: the argument after the cookie key is neither session.SuperAdministratorsBy(...) nor session.NoSuperAdministrators(), written in the call or assigned to the name the call takes (name := ... or name = ...) earlier in the same function, so it is not judged; " + staffOwn},
		},
		{
			name: "a finding beside a warning fails, the finding first", files: both,
			wantStatus: Fail, wantSummary: "1 Auth(s) not told who their own auth's super administrators are",
			wantDetails: []string{
				"pkg/auth/members/members.go:39: the members auth has a role file (pkg/auth/members/roles.json), so its people hold roles and the Super Administrator role is among them, and its Auth is built with session.NoSuperAdministrators(), which leaves a holder to the sign-in rule alone, with no second factor of the library's own; " + membersOwn,
				"pkg/auth/staff/staff.go:37: what the staff auth's Auth is told about its super administrators does not read here: the argument after the cookie key is neither session.SuperAdministratorsBy(...) nor session.NoSuperAdministrators(), written in the call or assigned to the name the call takes (name := ... or name = ...) earlier in the same function, so it is not judged; " + staffOwn,
			},
		},
		{
			name: "an auth package without its role file is the other checks' finding", files: map[string]string{"pkg/auth/staff/staff.go": told(staffAuth, "session.NoSuperAdministrators()")},
			wantStatus: Skip, wantSummary: "no auth package holds a role file",
		},
		{
			name: "no auth package", files: map[string]string{"pkg/config/doc.go": "package config\n"},
			wantStatus: Skip, wantSummary: "no auth package (pkg/auth/<name>) constructs a session Auth",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
			for rel, content := range tt.files {
				write(rel, content)
			}
			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			got := superAdministratorsTold{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: "super-administrators", Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
