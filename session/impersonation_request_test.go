package session

import (
	"go/types"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// loadTypes type-checks the one package pattern names, from the session package's
// directory, and returns it with whatever errors the type checker found.
func loadTypes(t *testing.T, pattern string) *packages.Package {
	t.Helper()

	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, pattern)
	if err != nil {
		t.Fatalf("packages.Load(%s) error = %v", pattern, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("packages.Load(%s) loaded %d packages, want 1", pattern, len(pkgs))
	}

	return pkgs[0]
}

// TestImpersonationRequest_noFieldAnApplicationCanSet pins the shape that makes the
// declaration the only way to a request: the request an impersonated session starts
// from has fields, and none of them is exported, so no application names one in a
// literal or assigns to one.
func TestImpersonationRequest_noFieldAnApplicationCanSet(t *testing.T) {
	t.Parallel()

	request := reflect.TypeFor[ImpersonationRequest]()
	if request.Kind() != reflect.Struct || request.NumField() == 0 {
		t.Fatalf("ImpersonationRequest is a %s with %d fields, want a struct that carries the request", request.Kind(), request.NumField())
	}
	for i := range request.NumField() {
		if field := request.Field(i); field.IsExported() {
			t.Errorf("ImpersonationRequest.%s is exported: an application could set it and start a session no declaration allowed", field.Name)
		}
	}
}

// TestImpersonationRequest_builtByHandDoesNotCompile type-checks the handbuilt program,
// which writes a request by hand with an actor, a principal and a mask, and pins that
// the Go type checker refuses each field: a request is made by the declaration alone
// (Impersonations.Request, Impersonations.RequestFor), and a program that builds one
// itself does not compile.
func TestImpersonationRequest_builtByHandDoesNotCompile(t *testing.T) {
	t.Parallel()

	// A test runs in its package's directory, which is where the program is kept.
	program := loadTypes(t, "./testdata/handbuilt")
	messages := make([]string, 0, len(program.Errors))
	for _, e := range program.Errors {
		messages = append(messages, e.Msg)
	}
	want := []string{"Actor", "Principal", "Mask"}
	if len(messages) != len(want) {
		t.Fatalf("type errors = %q, want exactly one for each field the program sets: %v", messages, want)
	}
	for i, field := range want {
		if refusal := "unknown field " + field + " in struct literal of type"; !strings.Contains(messages[i], refusal) || !strings.Contains(messages[i], "session.ImpersonationRequest") {
			t.Errorf("type error %q, want the refusal of the field %s of session.ImpersonationRequest (%q)", messages[i], field, refusal)
		}
	}
}

// TestImpersonationRequest_readOnly pins what an application can do with a request it
// holds: read it. The request's methods are listed here, each takes nothing and answers
// a value, so none hands out the request's inside to be written to. A method added to
// the request is added to this list, by someone who has looked at what it returns.
func TestImpersonationRequest_readOnly(t *testing.T) {
	t.Parallel()

	want := []string{"Actor", "ActorRealm", "ActorSessionID", "Kind", "Mask", "MaxDuration", "Principal", "Reason"}

	request := reflect.TypeFor[*ImpersonationRequest]()
	got := make([]string, 0, request.NumMethod())
	for i := range request.NumMethod() {
		method := request.Method(i)
		got = append(got, method.Name)
		if method.Type.NumIn() != 1 || method.Type.NumOut() != 1 {
			t.Errorf("ImpersonationRequest.%s takes %d arguments and answers %d values, want a reader: nothing in, one value out", method.Name, method.Type.NumIn()-1, method.Type.NumOut())

			continue
		}
		switch out := method.Type.Out(0); out.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Interface:
			t.Errorf("ImpersonationRequest.%s answers a %s (%s), through which the request could be written to: answer a value", method.Name, out.Kind(), out)
		default:
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("the exported methods of ImpersonationRequest = %v, want %v: a new one is listed here once it is known to hand out nothing that can be written to", got, want)
	}
}

// TestImpersonationRequest_onlyTheDeclarationMakesOne lists every exported function and
// method of the session package that answers a request an impersonated session starts
// from, the library's own form of it included, and pins that there are two: the
// declaration's Request and RequestFor. A third way to a request, a constructor or a
// method that answers one, fails here.
func TestImpersonationRequest_onlyTheDeclarationMakesOne(t *testing.T) {
	t.Parallel()

	session := loadTypes(t, ".")
	if len(session.Errors) != 0 {
		t.Fatalf("the session package does not type-check: %v", session.Errors)
	}
	answersARequest := func(signature *types.Signature) bool {
		for i := range signature.Results().Len() {
			if strings.Contains(types.TypeString(signature.Results().At(i).Type(), nil), "ImpersonationRequest") {
				return true
			}
		}

		return false
	}

	var makers []string
	scope := session.Types.Scope()
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		if !object.Exported() {
			continue
		}
		switch object := object.(type) {
		case *types.Func:
			if signature, ok := object.Type().(*types.Signature); ok && answersARequest(signature) {
				makers = append(makers, name)
			}
		case *types.TypeName:
			methods := types.NewMethodSet(types.NewPointer(object.Type()))
			for i := range methods.Len() {
				method := methods.At(i).Obj()
				if signature, ok := method.Type().(*types.Signature); ok && method.Exported() && answersARequest(signature) {
					makers = append(makers, name+"."+method.Name())
				}
			}
		default:
		}
	}
	slices.Sort(makers)

	if want := []string{"Impersonations.Request", "Impersonations.RequestFor"}; !slices.Equal(makers, want) {
		t.Errorf("the exported functions and methods of the session package that answer a request = %v, want %v: the declaration is the only way to one", makers, want)
	}
}
