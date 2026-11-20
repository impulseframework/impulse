package session

import (
	"context"
	"go/types"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/internal/basesession"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"golang.org/x/tools/go/packages"
)

// insideSession is the type of the session inside the library, as the type checker
// prints it.
const insideSession = "basesession.BaseSession"

// TestOpaqueOption_noRouteToTheSessionCompiles type-checks the opaqueoption program,
// which tries each way to open a session option and get hold of the session the
// library builds, and pins that every one is refused before the program runs: the
// generic function that opened the option while it was a function over the session,
// the same function in the option's shape now, an option of the program's own making
// (a converted function, a struct written out), an option the library made, called or
// read, and the library's own constructor, whose package the go command lets the
// session library's directory import and nobody else.
func TestOpaqueOption_noRouteToTheSessionCompiles(t *testing.T) {
	t.Parallel()

	// A test runs in its package's directory. The program is kept beside it, outside the
	// session library's directory, where an application is.
	program := loadTypes(t, "../testdata/opaqueoption")

	const option = "session.BaseSessionOption"
	want := []struct {
		kind    packages.ErrorKind
		refusal []string
	}{
		{
			kind:    packages.ListError,
			refusal: []string{"use of internal package " + reflect.TypeFor[sessioncore.PasswordSettings]().PkgPath() + " not allowed"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{option, "does not satisfy ~func(P)"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{option, "does not satisfy ~struct{set func(P)}"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"cannot convert keep", "to type " + option},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"cannot refer to unexported field set in struct literal of type " + option},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"implicit assignment to unexported field set in struct literal of type " + option},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"cannot call made", option + " is not a function"},
		},
		{
			kind:    packages.TypeError,
			refusal: []string{"made.set undefined", "unexported field set"},
		},
	}
	if len(program.Errors) != len(want) {
		t.Fatalf("errors = %q, want exactly one for each route the program tries (%d)", program.Errors, len(want))
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

// TestOpaqueOption_shape pins the shape that keeps the option closed: it is a struct
// (not a function, whose parameter a type parameter would be matched to), no field of
// it is exported (so nobody outside writes one or reads one, and no constraint written
// outside matches it), and it has no exported method.
func TestOpaqueOption_shape(t *testing.T) {
	t.Parallel()

	option := reflect.TypeFor[BaseSessionOption]()
	if option.Kind() != reflect.Struct {
		t.Fatalf("BaseSessionOption is a %s, want a struct: a function type hands a generic function the type of the session it is called with", option.Kind())
	}
	if option.NumField() == 0 {
		t.Fatalf("BaseSessionOption is a struct with no field, want one that holds what the option sets")
	}
	for i := range option.NumField() {
		if field := option.Field(i); field.IsExported() {
			t.Errorf("BaseSessionOption.%s is exported: a program could read it, call it or write an option of its own", field.Name)
		}
	}
	for _, typ := range []reflect.Type{option, reflect.PointerTo(option)} {
		for method := range typ.Methods() {
			t.Errorf("%s has the exported method %s: an option is passed on and nothing else", typ, method.Name)
		}
	}
}

// TestOpaqueOption_zeroOptionIsRefused pins what a constructor does with the one option
// a program can still make without a With function, the zero value: it sets nothing,
// so the constructor refuses it and says what to pass.
func TestOpaqueOption_zeroOptionIsRefused(t *testing.T) {
	t.Parallel()

	const refusal = "a session option was made by no With function"
	var declared BaseSessionOption

	t.Run("Preauth", func(t *testing.T) {
		t.Parallel()
		_, err := NewPreauth[NoCustomData](sessionstorage.NewSpannerPreauth(nil), cookieKey, WithSessionTimeout(defaultSessionTimeout), declared)
		if err == nil || !strings.Contains(err.Error(), refusal) {
			t.Errorf("NewPreauth() with the zero option: error = %v, want it to say %q", err, refusal)
		}
	})
	t.Run("the option a With function made is taken", func(t *testing.T) {
		t.Parallel()
		if _, err := NewPreauth[NoCustomData](sessionstorage.NewSpannerPreauth(nil), cookieKey, WithSessionTimeout(defaultSessionTimeout)); err != nil {
			t.Errorf("NewPreauth() error = %v, want none", err)
		}
	})
}

// TestOpaqueOption_sessionMadeByReflectionWritesNothing follows the route reflection
// still has: it finds the type of the session inside the library through a session
// type's field, makes one of its own over the library's storage and starts an
// impersonated session on it with a record of its own writing. The session it made was
// not built by the library and holds no key, so the storage refuses the write before
// the database is reached (the storage here has none: reaching it fails the test).
func TestOpaqueOption_sessionMadeByReflectionWritesNothing(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeFor[Preauth[NoCustomData]]().FieldByName("baseSession")
	if !ok || field.Type.Kind() != reflect.Pointer {
		t.Fatalf("Preauth has no pointer field baseSession: the session inside the library moved, and this route is followed from there")
	}
	if field.IsExported() {
		t.Errorf("Preauth.%s is exported: a program reads the session the library built from it", field.Name)
	}

	made := reflect.New(field.Type.Elem())
	made.Elem().FieldByName("Storage").Set(reflect.ValueOf(sessionstorage.NewSpannerPreauth(nil)))
	start := made.MethodByName("StartImpersonatedSession")
	if !start.IsValid() {
		t.Fatalf("%s has no StartImpersonatedSession: the start moved, and this route is followed to it", field.Type)
	}

	imp := &sessioninfo.Impersonation{Actor: "mallory", ActorRealm: "elsewhere", Principal: accesstypes.RolePrincipal("Administrator")}
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "mallory"}
	answers := start.Call([]reflect.Value{
		reflect.ValueOf(context.Background()), reflect.ValueOf(httptest.NewRecorder()), reflect.ValueOf(req), reflect.ValueOf(imp),
	})
	err, _ := answers[1].Interface().(error)
	if !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
		t.Errorf("the start on a session reflection made: error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
	}
}

// TestOpaqueOption_nothingExportedHandsOutTheSession walks everything a program can
// reach from what the session library's importable packages export (every package
// under the session library's directory that is not internal, the mocks included) and
// pins that the session inside the library is nowhere in it. The walk follows what the
// compiler lets a program follow: the result and the arguments of a function, the
// exported fields and methods of a type wherever it is declared, the element of a
// pointer or a collection, the parameter of a function type (which is what a type
// parameter is matched to), and a type parameter's constraint. It does not follow a
// field that is not exported: a program cannot read one, and no constraint written
// outside the library matches a struct that has one.
func TestOpaqueOption_nothingExportedHandsOutTheSession(t *testing.T) {
	t.Parallel()

	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, "./...")
	if err != nil {
		t.Fatalf("packages.Load(./...) error = %v", err)
	}

	walk := reachWalk{seen: map[types.Type]bool{}, inside: reflect.TypeFor[basesession.BaseSession]().PkgPath()}
	walked := 0
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
				walk.reach(object.Type(), pkg.Name+"."+name)
				walked++
			}
		}
	}
	slices.Sort(walk.found)

	if len(walk.found) != 0 {
		t.Errorf("the session library hands out the session inside it (%s) through %v: a program that reaches it starts an impersonated session around the application's declaration", insideSession, slices.Compact(walk.found))
	}
	if !walk.seen[types.Unalias(lookupType(t, loaded, "BaseSessionOption"))] || walked == 0 {
		t.Errorf("the walk over %d exported names did not reach BaseSessionOption: it found nothing to hold, so it holds nothing", walked)
	}
}

// lookupType answers the type the session package exports under name.
func lookupType(t *testing.T, loaded []*packages.Package, name string) types.Type {
	t.Helper()

	root := reflect.TypeFor[Impersonations]().PkgPath()
	for _, pkg := range loaded {
		if pkg.PkgPath != root {
			continue
		}
		if object := pkg.Types.Scope().Lookup(name); object != nil {
			return object.Type()
		}
	}
	t.Fatalf("the package %s exports no %s", root, name)

	return nil
}

// reachWalk is the walk of TestOpaqueOption_nothingExportedHandsOutTheSession: the
// types it has been through, and each place it found the session inside the library.
type reachWalk struct {
	seen map[types.Type]bool
	// inside is the import path of the package the session inside the library is
	// declared in.
	inside string
	found  []string
}

// reach follows typ, reached at place, to everything a program can get from it.
func (w *reachWalk) reach(typ types.Type, place string) {
	typ = types.Unalias(typ)
	if w.seen[typ] {
		return
	}
	w.seen[typ] = true

	switch typ := typ.(type) {
	case *types.Named:
		object := typ.Obj()
		if object.Pkg() != nil && object.Pkg().Path() == w.inside && object.Name() == "BaseSession" {
			w.found = append(w.found, place)

			return
		}
		for argument := range typ.TypeArgs().Types() {
			w.reach(argument, place)
		}
		w.reach(typ.Underlying(), place)
		for _, methods := range []*types.MethodSet{types.NewMethodSet(typ), types.NewMethodSet(types.NewPointer(typ))} {
			for method := range methods.Methods() {
				if function := method.Obj(); function.Exported() {
					w.reach(function.Type(), place+"."+function.Name())
				}
			}
		}
	case *types.Pointer:
		w.reach(typ.Elem(), place)
	case *types.Slice:
		w.reach(typ.Elem(), place)
	case *types.Array:
		w.reach(typ.Elem(), place)
	case *types.Chan:
		w.reach(typ.Elem(), place)
	case *types.Map:
		w.reach(typ.Key(), place)
		w.reach(typ.Elem(), place)
	case *types.Signature:
		for parameter := range typ.TypeParams().TypeParams() {
			w.reach(parameter, place)
		}
		w.reach(typ.Params(), place)
		w.reach(typ.Results(), place)
	case *types.Tuple:
		for variable := range typ.Variables() {
			w.reach(variable.Type(), place)
		}
	case *types.Struct:
		for field := range typ.Fields() {
			if field.Exported() {
				w.reach(field.Type(), place+"."+field.Name())
			}
		}
	case *types.Interface:
		for method := range typ.Methods() {
			if method.Exported() {
				w.reach(method.Type(), place+"."+method.Name())
			}
		}
		for embedded := range typ.EmbeddedTypes() {
			w.reach(embedded, place)
		}
	case *types.Union:
		for term := range typ.Terms() {
			w.reach(term.Type(), place)
		}
	case *types.TypeParam:
		w.reach(typ.Constraint(), place)
	default:
	}
}
