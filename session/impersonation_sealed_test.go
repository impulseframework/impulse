package session

import (
	"go/types"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"golang.org/x/tools/go/packages"
)

// sealedKey is the type of the key the write beneath an impersonated session takes, as
// the type checker prints it.
const sealedKey = "sealed.ImpersonationKey"

// TestSealedStorage_directCallDoesNotCompile type-checks the sealedstorage program,
// which calls the write beneath an impersonated session itself, and pins that every
// call is refused before the program runs: the call as it was written before the
// storage was sealed, a key of the program's own making (nil, an empty struct, a struct
// shaped like the key), and the library's own key, whose package the go command lets
// the session library's directory import and nobody else.
func TestSealedStorage_directCallDoesNotCompile(t *testing.T) {
	t.Parallel()

	// A test runs in its package's directory. The program is kept beside it, outside the
	// session library's directory, where an application is.
	program := loadTypes(t, "../testdata/sealedstorage")

	want := []struct {
		kind    packages.ErrorKind
		refusal []string
	}{
		{
			kind:    packages.ListError,
			refusal: []string{"use of internal package " + reflect.TypeFor[sealed.ImpersonationKey]().PkgPath() + " not allowed"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"not enough arguments in call to store.CreateImpersonatedSession", "want (context.Context, " + sealedKey + ","},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"cannot use nil as " + sealedKey + " value in argument to store.CreateImpersonatedSession"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"cannot use struct{}{}", "as " + sealedKey + " value in argument to store.CreateImpersonatedSession"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"cannot use lookalike{", "as " + sealedKey + " value in argument to store.CreateImpersonatedSession"},
		},
	}
	if len(program.Errors) != len(want) {
		t.Fatalf("errors = %q, want exactly one for each call the program makes (%d)", program.Errors, len(want))
	}
	for i, w := range want {
		got := program.Errors[i]
		if got.Kind != w.kind {
			t.Errorf("error %d (%q) is of kind %d, want kind %d", i, got.Msg, got.Kind, w.kind)
		}
		for _, refusal := range w.refusal {
			if !strings.Contains(got.Msg, refusal) {
				t.Errorf("error %d = %q, want it to say %q", i, got.Msg, refusal)
			}
		}
	}
}

// TestSealedStorage_keyIsTheLibrarysAlone pins the shape that seals the storage: the
// write beneath an impersonated session takes a key, the key's type lives in an
// internal package of the session library (so the go command lets nobody else import
// it), no field of it is exported (so nobody outside fills one in), and a key nobody
// made (the zero key, which reflection or a type parameter can reach without naming the
// type) is not a held one.
func TestSealedStorage_keyIsTheLibrarysAlone(t *testing.T) {
	t.Parallel()

	key := reflect.TypeFor[sealed.ImpersonationKey]()

	create, ok := reflect.TypeFor[sessionstorage.BaseStore]().MethodByName("CreateImpersonatedSession")
	if !ok {
		t.Fatal("sessionstorage.BaseStore has no CreateImpersonatedSession: the write beneath an impersonated session moved, and its seal is pinned here")
	}
	if takes := slices.Collect(create.Type.Ins()); !slices.Contains(takes, key) {
		t.Errorf("sessionstorage.BaseStore.CreateImpersonatedSession takes %v, want the key (%s) among them: without it any application can call the write", takes, key)
	}

	if library := reflect.TypeFor[Impersonations]().PkgPath() + "/internal/"; !strings.HasPrefix(key.PkgPath(), library) {
		t.Errorf("the key lives in %s, want a package under %s: outside it an application can import the key and make one", key.PkgPath(), library)
	}
	if key.Kind() != reflect.Struct || key.NumField() == 0 {
		t.Fatalf("the key is a %s with %d fields, want a struct with a field of its own: a key with none is the same as struct{}{}, which any program can write", key.Kind(), key.NumField())
	}
	for i := range key.NumField() {
		if field := key.Field(i); field.IsExported() {
			t.Errorf("the key's field %s is exported: a program that reaches the type could fill it in", field.Name)
		}
	}

	if (sealed.ImpersonationKey{}).Held() {
		t.Error("the zero key is held, want it refused: reflection and a type parameter reach the zero key without naming its type")
	}
	if !sealed.NewImpersonationKey().Held() {
		t.Error("the key the session library makes is not held, so the store would refuse the library itself")
	}
}

// TestSealedStorage_nothingExportedHandsOutTheKey walks everything the session
// library's importable packages export (every package under the session library's
// directory that is not internal, the mocks included) and pins the one place the key
// appears: as an argument of a method named CreateImpersonatedSession. A function or
// method that answers a key, a variable or a field that holds one, a name for its type
// or a callback that is handed one would each give an application what the internal
// package keeps from it, and each fails here.
func TestSealedStorage_nothingExportedHandsOutTheKey(t *testing.T) {
	t.Parallel()

	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, "./...")
	if err != nil {
		t.Fatalf("packages.Load(./...) error = %v", err)
	}

	var takers, others []string
	for _, pkg := range loaded {
		if strings.Contains(pkg.PkgPath+"/", "/internal/") {
			continue
		}
		if len(pkg.Errors) != 0 {
			t.Fatalf("the package %s does not type-check: %v", pkg.PkgPath, pkg.Errors)
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			object := scope.Lookup(name)
			if !object.Exported() {
				continue
			}
			typeName, isType := object.(*types.TypeName)
			if !isType {
				if namesTheKey(object.Type()) {
					others = append(others, pkg.Name+"."+name)
				}

				continue
			}
			if typeName.IsAlias() && namesTheKey(types.Unalias(typeName.Type())) {
				others = append(others, pkg.Name+"."+name)
			}
			if structure, ok := typeName.Type().Underlying().(*types.Struct); ok {
				for field := range structure.Fields() {
					if field.Exported() && namesTheKey(field.Type()) {
						others = append(others, pkg.Name+"."+name+"."+field.Name())
					}
				}
			}
			for _, methods := range []*types.MethodSet{types.NewMethodSet(typeName.Type()), types.NewMethodSet(types.NewPointer(typeName.Type()))} {
				for method := range methods.Methods() {
					function := method.Obj()
					signature, ok := function.Type().(*types.Signature)
					if !ok || !function.Exported() || !namesTheKey(signature) {
						continue
					}
					place := pkg.Name + "." + name + "." + function.Name()
					if function.Name() == "CreateImpersonatedSession" && takesTheKeyAndNoMore(signature) {
						takers = append(takers, place)
					} else {
						others = append(others, place)
					}
				}
			}
		}
	}
	slices.Sort(takers)
	takers = slices.Compact(takers)
	slices.Sort(others)
	others = slices.Compact(others)

	if len(others) != 0 {
		t.Errorf("the session library exports %v, each of which names the key outside an argument of CreateImpersonatedSession: an application would reach the key through it, and the storage beneath an impersonated session is the library's alone to write", others)
	}
	if !slices.Contains(takers, "sessionstorage.BaseStore.CreateImpersonatedSession") {
		t.Errorf("the methods that take the key = %v, want sessionstorage.BaseStore.CreateImpersonatedSession among them: the walk found nothing to hold, so it holds nothing", takers)
	}
}

// namesTheKey reports whether the type names the key anywhere in it.
func namesTheKey(typ types.Type) bool {
	return strings.Contains(types.TypeString(typ, nil), sealedKey)
}

// takesTheKeyAndNoMore reports whether the method takes the key as an argument of its
// own (not inside a function, a pointer or a collection it is handed) and answers
// nothing that names it.
func takesTheKeyAndNoMore(signature *types.Signature) bool {
	if namesTheKey(signature.Results()) {
		return false
	}
	taken := false
	for param := range signature.Params().Variables() {
		switch {
		case types.TypeString(param.Type(), (*types.Package).Name) == sealedKey:
			taken = true
		case namesTheKey(param.Type()):
			return false
		default:
		}
	}

	return taken
}
