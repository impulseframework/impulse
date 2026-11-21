package access

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestNew_refusesANilCollection pins that no value stands for "no collection": a nil one
// is refused, with or without options, and the refusal says what to pass. A nil pointer
// of a collection's type is a nil collection too: the interface that holds it is not
// nil, and the first question the engine asked it used to panic.
func TestNew_refusesANilCollection(t *testing.T) {
	t.Parallel()

	const want = "access.New(): the collection must not be nil: pass the permission collection the release declares, which every grant is held to: the generated router's Collection(), or for an application with several sites the union of theirs (access.UnionCollection)"
	var unassigned *stubCollection
	tests := []struct {
		name       string
		collection PermissionCollection
		opts       []Option
	}{
		{name: "nothing beside it"},
		{name: "beside a role file", opts: []Option{WithDefaultRoles(grammarRoleFile)}},
		{name: "beside the super administrator's methods", opts: []Option{WithSuperAdministratorMethods("Restore")}},
		{name: "a nil pointer of a collection's type", collection: unassigned},
		{name: "a nil pointer of a collection's type beside a role file", collection: unassigned, opts: []Option{WithDefaultRoles(grammarRoleFile)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), tt.collection, tt.opts...)
			if err == nil || !strings.HasSuffix(err.Error(), want) {
				t.Fatalf("New() error = %v, want one ending %q", err, want)
			}
			if client != nil {
				t.Errorf("New() = %v beside an error, want nil", client)
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
			construction: "access.New(store, collection, access.WithDefaultRoles(collection, roles))",
			refusal:      []string{"too many arguments in call to access.WithDefaultRoles", "want (access.RoleFile)"},
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
