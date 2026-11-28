package access

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

// TestNew_refusesANilCollection pins that no value stands for "no collection": a nil one
// is refused, with or without options, and the refusal says what to pass. A nil pointer
// of a collection's type is a nil collection too: the interface that holds it is not
// nil, and the first question the engine asked it used to panic. The refusal is the
// sentence alone: New does not name itself in it, since the caller that wraps New's
// errors names it, and a person would read the name twice.
func TestNew_refusesANilCollection(t *testing.T) {
	t.Parallel()

	const want = "the collection must not be nil: pass the permission collection the release declares, which every grant is held to: the generated router's Collection(), or for an application with several sites the union of theirs (access.UnionCollection)"
	var unassigned *stubCollection
	tests := []struct {
		name       string
		collection PermissionCollection
		opts       []Option
	}{
		{name: "nothing beside it"},
		{name: "beside a role file", opts: []Option{WithDefaultRoles(grammarRoleFile, fixtureAuth)}},
		{name: "beside the super administrator's methods", opts: []Option{WithSuperAdministratorMethods("Restore")}},
		{name: "a nil pointer of a collection's type", collection: unassigned},
		{name: "a nil pointer of a collection's type beside a role file", collection: unassigned, opts: []Option{WithDefaultRoles(grammarRoleFile, "")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), tt.collection, tt.opts...)
			if err == nil {
				t.Fatalf("New() error = nil, want %q", want)
			}
			if got := plainText(err); got != want {
				t.Errorf("New() error = %q, want %q", got, want)
			}
			if client != nil {
				t.Errorf("New() = %v beside an error, want nil", client)
			}
		})
	}
}

// TestNew_refusalsDoNotNameNew pins that New's own refusals are their sentences alone,
// as the refusal of a nil collection is: the caller that wraps New's errors names New,
// and a person would read the name twice.
func TestNew_refusalsDoNotNameNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		collection PermissionCollection
		opts       []Option
		// wantPrefix is the start of the refusal; the whole of it when whole is set.
		wantPrefix string
		whole      bool
	}{
		{
			name:       "no role file over a collection that knows several auths",
			collection: setsCollection{auths: []string{"members", "staff"}},
			wantPrefix: "the collection knows several auths (members, staff), and the engine is handed no role file, so it is told none of them. " +
				"An engine holds the roles of one auth and holds each role's grants to that auth's sets of people: open it with the auth's role file under the auth's name, access.WithDefaultRoles(file, name)",
			whole: true,
		},
		{
			name:       "a super administrator's method the collection does not declare",
			collection: grammarCollection{},
			opts:       []Option{WithSuperAdministratorMethods("Restore")},
			wantPrefix: "WithSuperAdministratorMethods names",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), tt.collection, tt.opts...)
			if err == nil {
				t.Fatalf("New() = %v, want a refusal", client)
			}
			switch got := plainText(err); {
			case tt.whole && got != tt.wantPrefix:
				t.Errorf("New() error = %q, want %q", got, tt.wantPrefix)
			case !strings.HasPrefix(got, tt.wantPrefix):
				t.Errorf("New() error = %q, want it to start %q", got, tt.wantPrefix)
			}
		})
	}
}

// TestNew_oneRoleFile pins that an engine takes one role file: WithDefaultRoles among
// New's options more than once is refused, the same file given twice as two files are,
// with the number of times it was given, since an engine holds the roles of one auth and
// the last file given used to replace the others without a word. One file opens.
func TestNew_oneRoleFile(t *testing.T) {
	t.Parallel()

	lookouts := RoleFile(`{
  "auth": "members",
  "roles": {
    "domain": [
      {"name": "Lookout", "description": "Reads widgets and their names.", "permissions": {"Read": [{"resource": "Widgets", "fields": ["name"]}]}}
    ]
  }
}`)
	refusal := func(times int) string {
		return fmt.Sprintf("access.WithDefaultRoles() is among the options %d times, and an engine holds the roles of one auth, so it takes one role file: "+
			"hand it its own auth's, once (another auth has an engine of its own)", times)
	}
	tests := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{
			name:    "the same file twice",
			opts:    []Option{WithDefaultRoles(grammarRoleFile, fixtureAuth), WithDefaultRoles(grammarRoleFile, fixtureAuth)},
			wantErr: refusal(2),
		},
		{
			name:    "two files",
			opts:    []Option{WithDefaultRoles(grammarRoleFile, fixtureAuth), WithHeartbeatInterval(time.Minute), WithDefaultRoles(lookouts, "members")},
			wantErr: refusal(2),
		},
		{
			name:    "three files",
			opts:    []Option{WithDefaultRoles(grammarRoleFile, fixtureAuth), WithDefaultRoles(lookouts, "members"), WithDefaultRoles(lookouts, "members")},
			wantErr: refusal(3),
		},
		{
			name: "one file",
			opts: []Option{WithDefaultRoles(grammarRoleFile, fixtureAuth), WithHeartbeatInterval(time.Minute)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.wantErr == "" {
				newPolicyClient(t, newFakeStore(), grammarCollection{}, tt.opts...)

				return
			}
			client, err := New(newFakeStore(), grammarCollection{}, tt.opts...)
			if err == nil {
				t.Fatalf("New() = %v, want the refusal %q", client, tt.wantErr)
			}
			if got := plainText(err); got != tt.wantErr {
				t.Errorf("New() error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

// TestNew_withoutTheCollectionDoesNotCompile type-checks the nocollection program, which
// builds a Client in each way an application could before the collection became an
// argument of New, and pins that the Go type checker refuses each: a Client is built
// over what its release declares, and a program that leaves the collection out, or hands
// it to an option, does not compile.
func TestNew_withoutTheCollectionDoesNotCompile(t *testing.T) {
	t.Parallel()

	// A test runs in its package's directory, which is where the program is kept.
	const pattern = "./testdata/nocollection"
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, pattern)
	if err != nil {
		t.Fatalf("packages.Load(%s) error = %v", pattern, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("packages.Load(%s) loaded %d packages, want 1", pattern, len(pkgs))
	}
	messages := make([]string, 0, len(pkgs[0].Errors))
	for _, e := range pkgs[0].Errors {
		messages = append(messages, e.Msg)
	}

	// One refusal for each construction, in the program's order.
	want := []struct {
		construction string
		refusal      []string
	}{
		{
			construction: "access.New(store)",
			refusal:      []string{"not enough arguments in call to access.New", "want (access.Store, access.PermissionCollection, ...access.Option)"},
		},
		{
			construction: "access.New(store, access.WithHeartbeatInterval(time.Minute))",
			refusal:      []string{"cannot use access.WithHeartbeatInterval(time.Minute)", "as access.PermissionCollection value in argument to access.New"},
		},
		{
			construction: "access.New(store, access.WithPermissionCollection(collection))",
			refusal:      []string{"undefined: access.WithPermissionCollection"},
		},
		{
			construction: "access.New(store, collection, access.WithDefaultRoles(collection, roles, \"staff\"))",
			refusal:      []string{"too many arguments in call to access.WithDefaultRoles", "want (access.RoleFile, string)"},
		},
	}
	if len(messages) != len(want) {
		t.Fatalf("type errors = %q, want exactly one for each of the program's %d constructions", messages, len(want))
	}
	for i, w := range want {
		for _, part := range w.refusal {
			if !strings.Contains(messages[i], part) {
				t.Errorf("type error %q, want the refusal of %s (containing %q)", messages[i], w.construction, part)
			}
		}
	}
}
