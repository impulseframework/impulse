package app

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestScan_hiddenCalls pins that the scan refuses the two forms of Go that would hide a
// call it reads by its package's name from every check, in an ordinary file and in a
// test file alike, with the file and the line: a dot import of a package of the
// framework or of the application's own module, and one of the functions the checks
// read calls of named without being called. A dot import of another module's package,
// a blank or a named import, a call written with its package's name (in parentheses, or
// with type arguments, too), another function of a listed package named as a value,
// and a file the scan does not read are taken.
func TestScan_hiddenCalls(t *testing.T) {
	t.Parallel()

	const dotAccess = `package config

import (
	. "github.com/impulseframework/impulse/access"
)

func engine(store Store, collection PermissionCollection) (*Client, error) {
	return New(store, collection)
}
`
	const dotOwnInTest = `package config

import (
	"testing"

	. "example.com/harbor/pkg/auth/staff"
)

func TestRoles(t *testing.T) {
	if Roles() == nil {
		t.Fatal("no role file")
	}
}
`
	const dotOtherModule = `package config

import (
	"testing"

	. "github.com/google/go-cmp/cmp"
)

func TestSame(t *testing.T) {
	if Diff(1, 1) != "" {
		t.Fatal("not the same")
	}
}
`
	const blankAndNamed = `package config

import (
	_ "embed"

	acc "github.com/impulseframework/impulse/access"
	staffauth "example.com/harbor/pkg/auth/staff"
)

func engine(store acc.Store, collection acc.PermissionCollection) (*acc.Client, error) {
	return acc.New(store, collection, acc.WithDefaultRoles(staffauth.Roles(), staffauth.Name))
}
`
	const engineValue = `package config

import (
	"github.com/impulseframework/impulse/access"
)

func engine(store access.Store, collection access.PermissionCollection) (*access.Client, error) {
	newEngine := access.New

	return newEngine(store, collection)
}
`
	const optionValue = `package config

import (
	acc "github.com/impulseframework/impulse/access"
)

func options(roles acc.RoleFile) []acc.Option {
	give := acc.WithDefaultRoles

	return []acc.Option{give(roles, "staff")}
}
`
	const validationValue = `package config

import (
	"testing"

	"github.com/impulseframework/impulse/access"
)

func TestRoles(t *testing.T) {
	validate := access.ValidateRoles
	_ = validate
}
`
	const called = `package config

import (
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
)

func engine(store access.Store, collection access.PermissionCollection) (*access.Client, error) {
	if _, err := (access.New)(store, collection); err != nil {
		return nil, err
	}
	_, _ = session.NewAuth[session.NoCustomData, session.NoCustomData](nil)

	return access.New(store, collection, access.WithHeartbeatInterval(0))
}
`
	const otherValue = `package config

import (
	"time"

	"github.com/impulseframework/impulse/access"
)

func heartbeat() func(time.Duration) access.Option {
	return access.WithHeartbeatInterval
}
`
	const bothForms = `package scratch

import (
	. "github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access"
)

var newEngine = access.New

func engine(store Store, collection PermissionCollection) (*Client, error) {
	return New(store, collection)
}
`

	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:  "a dot import of the framework's package in an ordinary file",
			files: map[string]string{"pkg/config/data.go": dotAccess},
			wantErr: `pkg/config/data.go:4: import . "github.com/impulseframework/impulse/access" brings the package's names in without the package's name, ` +
				"and impulse finds a call by the name of its package, so it would see none of this file's calls into it: import the package by its name and write each call with it",
		},
		{
			name:  "a dot import of a package of the application's own module in a test file",
			files: map[string]string{"pkg/config/data_test.go": dotOwnInTest},
			wantErr: `pkg/config/data_test.go:6: import . "example.com/harbor/pkg/auth/staff" brings the package's names in without the package's name, ` +
				"and impulse finds a call by the name of its package, so it would see none of this file's calls into it: import the package by its name and write each call with it",
		},
		{
			name:  "a dot import of another module's package",
			files: map[string]string{"pkg/config/data_test.go": dotOtherModule},
		},
		{
			name:  "a blank import and named imports",
			files: map[string]string{"pkg/config/data.go": blankAndNamed},
		},
		{
			name:  "the engine's constructor named without being called",
			files: map[string]string{"pkg/config/data.go": engineValue},
			wantErr: "pkg/config/data.go:8: access.New is named here without being called, and impulse reads a call where the source writes it, " +
				"so it would not see a call made through this value: call access.New where the call is made",
		},
		{
			name:  "the role file's option named without being called, the package imported under another name",
			files: map[string]string{"pkg/config/data.go": optionValue},
			wantErr: "pkg/config/data.go:8: acc.WithDefaultRoles is named here without being called, and impulse reads a call where the source writes it, " +
				"so it would not see a call made through this value: call acc.WithDefaultRoles where the call is made",
		},
		{
			name:  "the validation of a role file named without being called in a test file",
			files: map[string]string{"pkg/config/data_test.go": validationValue},
			wantErr: "pkg/config/data_test.go:10: access.ValidateRoles is named here without being called, and impulse reads a call where the source writes it, " +
				"so it would not see a call made through this value: call access.ValidateRoles where the call is made",
		},
		{
			name:  "calls written with their package's name, in parentheses and with type arguments",
			files: map[string]string{"pkg/config/data.go": called},
		},
		{
			name:  "a function the checks read no call of named as a value",
			files: map[string]string{"pkg/config/data.go": otherValue},
		},
		{
			name: "files the scan does not read",
			files: map[string]string{
				"pkg/config/_scratch.go":           bothForms,
				"pkg/config/testdata/scratch/s.go": bothForms,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			files := map[string]string{"go.mod": "module example.com/harbor\n\ngo 1.26\n"}
			for rel, content := range tt.files {
				files[rel] = content
			}
			writeTree(t, root, files)
			_, err := Discover(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Discover() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Discover() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestScan_callsInParentheses pins that the scan reads a call whose function is written in
// parentheses, or with a comment before its parenthesis, as the call it is: the engine, the
// role file handed to it, the validation of a role file, the retirement of secret versions
// and the construction of an Auth.
func TestScan_callsInParentheses(t *testing.T) {
	t.Parallel()

	const staff = `package staff

import (
	_ "embed"

	"github.com/impulseframework/impulse/access"
)

//go:embed roles.json
var roleFile access.RoleFile

// Name is the auth's name.
const Name = "staff"

func Roles() access.RoleFile {
	return roleFile
}

func engine(store access.Store, collection access.PermissionCollection) (*access.Client, error) {
	return (access.New)(store, collection, (access.WithDefaultRoles)(Roles(), Name))
}
`
	const members = `package staff

import (
	"github.com/impulseframework/impulse/access"

	"example.com/harbor/pkg/auth/members"
)

func membersEngine(store access.Store, collection access.PermissionCollection) (*access.Client, error) {
	return access.New(store, collection, access.WithDefaultRoles /* the roles */ (members.Roles(), members.Name))
}
`
	const validation = `package staff_test

import (
	"testing"

	"github.com/impulseframework/impulse/access"

	"example.com/harbor/pkg/auth/staff"
)

func TestRoles(t *testing.T) {
	if err := access.ValidateRoles /* here */ (staff.Roles(), staff.Name, nil); err != nil {
		t.Fatal(err)
	}
}
`
	const retirement = `package deploy

import (
	"context"

	"github.com/impulseframework/impulse/resource"

	"example.com/harbor/pkg/auth/staff"
)

func retire(ctx context.Context, client resource.Client) error {
	_, err := (resource.RetireSecretVersion)(ctx, client, "SESSION_KEY", staff.SecretHolders()...)

	return err
}
`
	const auth = `package staff

import "github.com/impulseframework/impulse/session"

func New() {
	_, _ = (session.NewAuth)(nil)
}
`

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                       "module example.com/harbor\n\ngo 1.26\n",
		"pkg/auth/staff/staff.go":      staff,
		"pkg/auth/staff/members.go":    members,
		"pkg/auth/staff/roles_test.go": validation,
		"pkg/auth/staff/auth.go":       auth,
		"pkg/deploy/deploy.go":         retirement,
	})
	a, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	var engines []string
	for _, e := range a.Engines {
		engines = append(engines, e.File+":"+strconv.Itoa(e.Line))
	}
	slices.Sort(engines)
	if diff := cmp.Diff([]string{"pkg/auth/staff/members.go:10", "pkg/auth/staff/staff.go:20"}, engines); diff != "" {
		t.Errorf("Engines mismatch (-want +got):\n%s", diff)
	}
	wantRoles := []DefaultRoles{
		{File: "pkg/auth/staff/members.go", Line: 10, RolesPackage: "example.com/harbor/pkg/auth/members", NamePackage: "example.com/harbor/pkg/auth/members", NameText: "members.Name", InEngine: true},
		{File: "pkg/auth/staff/staff.go", Line: 20, RolesPackage: "example.com/harbor/pkg/auth/staff", NamePackage: "example.com/harbor/pkg/auth/staff", NameText: "Name", InEngine: true},
	}
	roles := slices.Clone(a.DefaultRoles)
	slices.SortFunc(roles, func(x, y DefaultRoles) int { return strings.Compare(x.File, y.File) })
	if diff := cmp.Diff(wantRoles, roles); diff != "" {
		t.Errorf("DefaultRoles mismatch (-want +got):\n%s", diff)
	}
	wantValidations := []RoleValidation{{File: "pkg/auth/staff/roles_test.go", Line: 12, RolesPackages: []string{"example.com/harbor/pkg/auth/staff"}}}
	if diff := cmp.Diff(wantValidations, a.RoleValidations); diff != "" {
		t.Errorf("RoleValidations mismatch (-want +got):\n%s", diff)
	}
	wantRetirements := []SecretRetirement{{File: "pkg/deploy/deploy.go", Line: 12, Packages: []string{"example.com/harbor/pkg/auth/staff"}}}
	if diff := cmp.Diff(wantRetirements, a.SecretRetirements); diff != "" {
		t.Errorf("SecretRetirements mismatch (-want +got):\n%s", diff)
	}
	var auths []string
	for _, au := range a.Auths {
		auths = append(auths, au.File+":"+strconv.Itoa(au.Line))
	}
	if diff := cmp.Diff([]string{"pkg/auth/staff/auth.go:6"}, auths); diff != "" {
		t.Errorf("Auths mismatch (-want +got):\n%s", diff)
	}
}
