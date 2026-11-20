package session

import (
	"context"
	"go/types"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	"go.uber.org/mock/gomock"
	"golang.org/x/tools/go/packages"
)

// sealedWrite is the name of the write beneath an impersonated session: a method name
// that belongs to the session library's internal package.
const sealedWrite = "createImpersonatedSession"

// noSuchName is how the type checker refuses the write under the name the session
// library calls it by, wherever a program outside the library's internal package
// writes it.
const noSuchName = " undefined (cannot refer to unexported method " + sealedWrite + ")"

// typeRefusal is one error the type checker gives a program that is kept because it does
// not compile: its kind, and what it says.
type typeRefusal struct {
	kind packages.ErrorKind
	says []string
}

// wantRefusals pins the errors of a program that is kept because it does not compile:
// exactly the refusals in want, in the order of the lines they are for. Every other
// line of the program compiles.
func wantRefusals(t *testing.T, program *packages.Package, want []typeRefusal) {
	t.Helper()

	if len(program.Errors) != len(want) {
		t.Fatalf("errors = %q, want exactly one for each route the program tries (%d)", program.Errors, len(want))
	}
	for i, w := range want {
		got := program.Errors[i]
		if got.Kind != w.kind {
			t.Errorf("error %d (%q) is of kind %d, want kind %d", i, got.Msg, got.Kind, w.kind)
		}
		for _, says := range w.says {
			if !strings.Contains(got.Msg, says) {
				t.Errorf("error %d = %q, want it to say %q", i, got.Msg, says)
			}
		}
	}
}

// TestSealedStorage_directCallDoesNotCompile type-checks the sealedstorage program,
// which tries to make the write beneath an impersonated session itself, and pins that
// every try is refused before the program runs: the call as it was written before the
// storage was sealed (on the library's store and on the store's interface), the write
// under the name the session library calls it by (on both, and on the store an
// application writes for itself), and the library's own way to the write, whose package
// the go command lets the session library's directory import and nobody else.
func TestSealedStorage_directCallDoesNotCompile(t *testing.T) {
	t.Parallel()

	// A test runs in its package's directory. The program is kept beside it, outside the
	// session library's directory, where an application is.
	program := loadTypes(t, "../testdata/sealedstorage")

	wantRefusals(t, program, []typeRefusal{
		{
			kind: packages.ListError,
			says: []string{"use of internal package " + reflect.TypeFor[sealed.ImpersonatedSessionWrite]().PkgPath() + " not allowed"},
		},
		{
			kind: packages.TypeError,
			says: []string{"store.CreateImpersonatedSession undefined", "has no field or method CreateImpersonatedSession"},
		},
		{
			kind: packages.TypeError,
			says: []string{"behind.CreateImpersonatedSession undefined", "type sessionstorage.BaseStore has no field or method CreateImpersonatedSession"},
		},
		{
			kind: packages.TypeError,
			says: []string{"store." + sealedWrite + noSuchName},
		},
		{
			kind: packages.TypeError,
			says: []string{"behind." + sealedWrite + noSuchName},
		},
		{
			kind: packages.TypeError,
			says: []string{"sessionstorage.NoImpersonatedSessions{}." + sealedWrite + noSuchName},
		},
	})
}

// TestSealedStorage_wrapperDoesNotCompile type-checks the sealedwrapper program, an
// application's store that embeds the library's and tries to get between the library
// and the write beneath an impersonated session, and pins that every try is refused
// before the program runs: the wrapper that declares the write with a type parameter
// where the key went, built with the key's type taken by inference from the library's
// own store and handed over as a session store (which compiled, and was handed the key,
// the request and the record, while the write was an exported method), and the write of
// the wrapped store under the name the library calls it by, from inside the wrapper's
// own method of that name, on the store, and as a function.
//
// The line of the program that hands the wrapper over as a session store is not among
// the refusals: a wrapper is still a store.
func TestSealedStorage_wrapperDoesNotCompile(t *testing.T) {
	t.Parallel()

	program := loadTypes(t, "../testdata/sealedwrapper")

	wantRefusals(t, program, []typeRefusal{
		{
			kind: packages.TypeError,
			says: []string{"r.AuthStore." + sealedWrite + noSuchName},
		},
		{
			kind: packages.TypeError,
			says: []string{"store.CreateImpersonatedSession undefined", "has no field or method CreateImpersonatedSession"},
		},
		{
			kind: packages.TypeError,
			says: []string{"wrapped." + sealedWrite + noSuchName},
		},
		{
			kind: packages.TypeError,
			says: []string{"sessionstorage.BaseStore." + sealedWrite + noSuchName},
		},
	})
}

// TestSealedStorage_storeFromNothingDoesNotCompile type-checks the sealedfromnothing
// program, a session store an application writes with no store of the library's inside
// it, and pins that it is no session store: the store that declares the write with a
// type parameter where the key went, built with the key's type taken by inference from
// the method of the library's own interface and handed to the session library as its
// storage (which compiled, and was handed the key, while the write was an exported
// method), the store with a type of the program's own there, which exports every method
// the interface exports, and the same store with the write declared under the name the
// library calls it by, which is a method of the program's package and not the one the
// interface asks for.
func TestSealedStorage_storeFromNothingDoesNotCompile(t *testing.T) {
	t.Parallel()

	program := loadTypes(t, "../testdata/sealedfromnothing")

	wantRefusals(t, program, []typeRefusal{
		{
			kind: packages.TypeError,
			says: []string{"sessionstorage.BaseStore.CreateImpersonatedSession undefined", "type sessionstorage.BaseStore has no field or method CreateImpersonatedSession"},
		},
		{
			kind: packages.TypeError,
			says: []string{"*mine[struct{}] does not implement sessionstorage.PreauthStore (missing method " + sealedWrite + ")"},
		},
		{
			kind: packages.TypeError,
			says: []string{"*lookalike does not implement sessionstorage.PreauthStore (unexported method " + sealedWrite + ")"},
		},
	})
}

// TestSealedStorage_writeIsTheLibrarysAlone pins the shape that seals the storage. The
// store's interface has one method that is not exported, the write beneath an
// impersonated session, and the method's name belongs to an internal package of the
// session library: the go command lets no package outside the library's directory
// import that package, and Go lets no other package call or declare a method named
// there. No exported method stands beside it under the name it had. The type that
// implements the write, and the stores that hold one, export no field of it, so no
// program takes a write out of a store or puts one in. And the write nobody built,
// which is all a store written outside the library can hold, refuses.
func TestSealedStorage_writeIsTheLibrarysAlone(t *testing.T) {
	t.Parallel()

	inside := reflect.TypeFor[sealed.ImpersonatedSessionWrite]().PkgPath()
	if library := reflect.TypeFor[Impersonations]().PkgPath() + "/internal/"; !strings.HasPrefix(inside, library) {
		t.Errorf("the write is declared in %s, want a package under %s: outside it an application can import the package and call the write", inside, library)
	}

	store := reflect.TypeFor[sessionstorage.BaseStore]()
	var unexported []string
	for i := range store.NumMethod() {
		method := store.Method(i)
		switch {
		case !method.IsExported():
			unexported = append(unexported, method.PkgPath+"."+method.Name)
		case strings.EqualFold(method.Name, sealedWrite):
			t.Errorf("sessionstorage.BaseStore exports %s: any type can declare an exported method, so an application's store would be handed what the library writes, and any program could call it", method.Name)
		default:
		}
	}
	if want := []string{inside + "." + sealedWrite}; !slices.Equal(unexported, want) {
		t.Errorf("the methods of sessionstorage.BaseStore that are not exported = %v, want %v: the write beneath an impersonated session, named in the session library's internal package, and nothing else", unexported, want)
	}

	holders := map[string]reflect.Type{
		"sealed.ImpersonatedSessionWrite":       reflect.TypeFor[sealed.ImpersonatedSessionWrite](),
		"sessionstorage.NoImpersonatedSessions": reflect.TypeFor[sessionstorage.NoImpersonatedSessions](),
		"sessionstorage.Auth":                   reflect.TypeFor[sessionstorage.Auth[struct{}]](),
		"sessionstorage.Preauth":                reflect.TypeFor[sessionstorage.Preauth[struct{}]](),
		"mock_sessionstorage.MockBaseStore":     reflect.TypeFor[mock_sessionstorage.MockBaseStore](),
		"mock_sessionstorage.MockPreauthStore":  reflect.TypeFor[mock_sessionstorage.MockPreauthStore](),
		"mock_sessionstorage.MockAuthStore":     reflect.TypeFor[mock_sessionstorage.MockAuthStore[struct{}]](),
	}
	for name, holder := range holders {
		held := 0
		for _, field := range reflect.VisibleFields(holder) {
			if field.Type.PkgPath() != inside && holder.PkgPath() != inside {
				continue
			}
			held++
			if field.IsExported() {
				t.Errorf("%s exports the field %s of the write: a program that holds one could read the write out of it, or set another in its place", name, field.Name)
			}
		}
		if held == 0 {
			t.Errorf("%s holds no field of the write: the type moved, and the walk over its fields holds nothing", name)
		}
	}

	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "mallory"}
	imp := &sessioninfo.Impersonation{Actor: "mallory"}
	if _, err := sealed.CreateImpersonatedSession(context.Background(), sealed.ImpersonatedSessionWrite{}, req, imp); !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
		t.Errorf("the write nobody built: error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
	}
	if _, err := sealed.CreateImpersonatedSession(context.Background(), sessionstorage.NoImpersonatedSessions{}, req, imp); !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
		t.Errorf("the write of a store an application writes for itself: error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
	}
}

// TestSealedStorage_mocksWriteNothing pins what the mocks an application gets do with
// the write beneath an impersonated session: each refuses it and calls nothing. The
// generated mocks carry a method of the same name of their own, which is not the
// store's write: the controller fails the test if the library's call reached it, since
// no call is expected.
func TestSealedStorage_mocksWriteNothing(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mocks := map[string]sessionstorage.BaseStore{
		"MockBaseStore":    mock_sessionstorage.NewMockBaseStore(ctrl),
		"MockPreauthStore": mock_sessionstorage.NewMockPreauthStore(ctrl),
		"MockAuthStore":    mock_sessionstorage.NewMockAuthStore[struct{}](ctrl),
	}
	for name, mock := range mocks {
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "mallory"}
		imp := &sessioninfo.Impersonation{Actor: "mallory"}
		if _, err := sealed.CreateImpersonatedSession(context.Background(), mock, req, imp); !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
			t.Errorf("the write of mock_sessionstorage.%s: error = %v, want %v", name, err, sessionstorage.ErrImpersonationSealed)
		}
	}
}

// TestSealedStorage_nothingExportedReachesTheWrite walks everything the session
// library's importable packages export (every package under the session library's
// directory that is not internal, the mocks included) and pins that none of it is a way
// to the write beneath an impersonated session, with no exception:
//
//   - nothing exported names a type of the internal package the write is declared in:
//     no function or method takes or returns one, no variable or field holds one, and no
//     name stands for one, so a program has no value of the write and no type to embed;
//   - no exported function or method is handed a request for a session and an
//     impersonation record together, which is what the write takes, whether as arguments
//     of its own or inside one, and none has the write's name, however it is capitalized;
//   - the exported types that have the write are the ones listed here and no other: the
//     store's interfaces, the library's two stores (whose write is the library's), and
//     the store an application writes for itself and the mocks (whose write refuses).
func TestSealedStorage_nothingExportedReachesTheWrite(t *testing.T) {
	t.Parallel()

	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, "./...")
	if err != nil {
		t.Fatalf("packages.Load(./...) error = %v", err)
	}

	walk := newSealWalk()
	for _, pkg := range loaded {
		if strings.Contains(pkg.PkgPath+"/", "/internal/") {
			continue
		}
		if len(pkg.Errors) != 0 {
			t.Fatalf("the package %s does not type-check: %v", pkg.PkgPath, pkg.Errors)
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			if object := scope.Lookup(name); object.Exported() {
				walk.object(pkg.Name, object)
			}
		}
	}

	if names := sortedSet(walk.names); len(names) != 0 {
		t.Errorf("the session library exports %v, each of which names a type of the package the write beneath an impersonated session is declared in: an application would hold the write through it, and the storage beneath an impersonated session is the library's alone to write", names)
	}
	if takers := sortedSet(walk.takers); len(takers) != 0 {
		t.Errorf("the session library exports %v, each of which is handed a request for a session and an impersonation record together, or has the write's name: only the write beneath an impersonated session takes the two, and nothing exported may stand for it", takers)
	}
	wantHolders := []string{
		"mock_sessionstorage.MockAuthStore",
		"mock_sessionstorage.MockBaseStore",
		"mock_sessionstorage.MockPreauthStore",
		"sessionstorage.Auth",
		"sessionstorage.AuthStore",
		"sessionstorage.BaseStore",
		"sessionstorage.NoImpersonatedSessions",
		"sessionstorage.Preauth",
		"sessionstorage.PreauthStore",
	}
	if holders := sortedSet(walk.holders); !slices.Equal(holders, wantHolders) {
		t.Errorf("the exported types that have the write beneath an impersonated session = %v, want %v: a type added to them has a write of the library's making or one that refuses, and is listed here once that is seen", holders, wantHolders)
	}
}

// sealWalk is the walk of TestSealedStorage_nothingExportedReachesTheWrite: what it
// looks for, and each place it found it.
type sealWalk struct {
	// inside is the import path of the package the write is declared in.
	inside string
	// request and record match a request for a session and an impersonation record in a
	// type written out with full import paths.
	request, record *regexp.Regexp
	// names are the exported places that name a type of the package inside.
	names []string
	// takers are the exported functions and methods handed a request and a record
	// together, and those that have the write's name.
	takers []string
	// holders are the exported types that have the write.
	holders []string
}

// newSealWalk returns the walk, looking for the types the session library declares.
func newSealWalk() *sealWalk {
	word := func(typ reflect.Type) *regexp.Regexp {
		return regexp.MustCompile(regexp.QuoteMeta(typ.PkgPath()+"."+typ.Name()) + `\b`)
	}

	return &sealWalk{
		inside:  reflect.TypeFor[sealed.ImpersonatedSessionWrite]().PkgPath(),
		request: word(reflect.TypeFor[sessioninfo.NewSessionRequest]()),
		record:  word(reflect.TypeFor[sessioninfo.Impersonation]()),
	}
}

// object walks one exported name of the package named pkg.
func (w *sealWalk) object(pkg string, object types.Object) {
	place := pkg + "." + object.Name()
	typeName, isType := object.(*types.TypeName)
	if !isType {
		w.value(place, object.Name(), object.Type())

		return
	}

	if typeName.IsAlias() && w.namesInside(types.Unalias(typeName.Type())) {
		w.names = append(w.names, place)
	}
	if structure, ok := typeName.Type().Underlying().(*types.Struct); ok {
		for field := range structure.Fields() {
			if field.Exported() {
				w.value(place+"."+field.Name(), field.Name(), field.Type())
			}
		}
	}
	for _, methods := range []*types.MethodSet{types.NewMethodSet(typeName.Type()), types.NewMethodSet(types.NewPointer(typeName.Type()))} {
		for method := range methods.Methods() {
			function := method.Obj()
			switch {
			case function.Exported():
				w.value(place+"."+function.Name(), function.Name(), function.Type())
			case function.Pkg() != nil && function.Pkg().Path() == w.inside:
				w.holders = append(w.holders, place)
			default:
			}
		}
	}
}

// value walks one exported function, method, variable or field: its name and its type.
func (w *sealWalk) value(place, name string, typ types.Type) {
	if w.namesInside(typ) {
		w.names = append(w.names, place)
	}

	signature, ok := typ.(*types.Signature)
	if !ok {
		return
	}
	handed := written(signature.Params())
	if strings.EqualFold(name, sealedWrite) || (w.request.MatchString(handed) && w.record.MatchString(handed)) {
		w.takers = append(w.takers, place)
	}
}

// namesInside reports whether the type names a type of the package inside anywhere in
// it.
func (w *sealWalk) namesInside(typ types.Type) bool {
	return strings.Contains(written(typ), w.inside+".")
}

// written is the type written out, with the full import path of every package it names.
func written(typ types.Type) string {
	return types.TypeString(typ, func(pkg *types.Package) string {
		return pkg.Path()
	})
}

// sortedSet is the places sorted, each once.
func sortedSet(places []string) []string {
	slices.Sort(places)

	return slices.Compact(places)
}
