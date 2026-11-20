package generation

import (
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// The classes of an action (@class). An action carries its classes where it is
// declared: on the @rpc struct of a generated method, or on the accesstypes.Resource
// constant of a registration made by hand, beside its @manualAddResource. The generated
// collection carries them (resource.CollectionResource.Classes), which is where the
// masked check of an impersonated session reads them: a permission mask that refuses a
// class refuses every action that carries it (accesstypes.RefuseClass). The collection
// file declares a constant for each class in use, so a mask is written against a name
// the compiler knows.

// identityCredentialClass is the @class line of the generated identity methods that
// change how a person signs in: a password set, required anew or removed, a sign-in
// identity unlinked, an address or a username changed. The template of each ends its
// annotations with it, so an application can grant "act as a person, except the
// credential actions" with a mask that refuses the class. A method that changes how a
// person signs in and lacks the line fails the test that lists them
// (Test_prepareIdentity_classedMethods).
const identityCredentialClass = "\n// @" + classKeyword + "(" + string(accesstypes.ClassCredential) + ")"

// identityAccessClass is the @class line of the generated identity methods that give or
// take away what someone may do: a role given or removed, a person put in a group or
// taken out of one, a role given to a group or taken from it, a group made or deleted,
// an invitation sent or withdrawn, an account made, disabled, enabled or deleted, a
// sign-in method opened to sign-up or made invite only, and the setting that lets
// people ask at the sign-in page for a link that resets their own password. The
// template of each ends its annotations with it, so an application can grant "act as a
// person, without changing who may do what" with a mask that refuses the class. A
// method that gives or takes away what someone may do and lacks the line fails the test
// that lists them (Test_prepareIdentity_classedMethods).
//
// The actions the generator declares with no struct to annotate carry the class by
// their registration, and a test lists those too (Test_generatorActions_classed): the
// two methods that open a tenant to its directory's people with a role and make it
// invite only again, the two of the switch that makes a tenant sign in with its
// directory alone (tenantOwnMethod.Classes), and the making of a tenant of one's own
// (tenantCreationAction).
const identityAccessClass = "\n// @" + classKeyword + "(" + string(accesstypes.ClassAccess) + ")"

// classConstantPrefix opens the name of the constant the collection file declares for
// a class: ClassCredential for the class credential.
const classConstantPrefix = "Class"

// resolveClasses reads an action's @class annotations: both comma lists and repeated
// annotations are accepted. where names the declaration in a refusal. A class is named
// by a lowercase letter, then lowercase letters, digits and underscores, and an action
// carries each of its classes once.
func resolveClasses(annotations genlang.ArgMap, where string) ([]accesstypes.ActionClass, error) {
	if !annotations.Has(classKeyword) {
		return nil, nil
	}

	var classes []accesstypes.ActionClass
	for arg := range annotations.Get(classKeyword).Seq() {
		for part := range strings.SplitSeq(arg, ",") {
			class := accesstypes.ActionClass(strings.TrimSpace(part))
			if !class.Valid() {
				return nil, errors.Newf("%s: @%s(%s) names the class %q; a class is named by a lowercase letter, then lowercase letters, digits and underscores, like @%s(%s)", where, classKeyword, arg, string(class), classKeyword, accesstypes.ClassCredential)
			}
			if slices.Contains(classes, class) {
				return nil, errors.Newf("%s: @%s names the class %s twice", where, classKeyword, class)
			}
			classes = append(classes, class)
		}
	}
	slices.Sort(classes)

	return classes, nil
}

// classWithoutRegistration refuses @class on a generated method the collection
// registers no action for; why says what leaves it unregistered. The collection is
// where a mask reads an action's classes, so a class on a method that registers nothing
// would be dropped without a word, and a session whose mask refuses the class would run
// the action once a registration made by hand mounts it. The class belongs where the
// action is registered: on that constant.
func classWithoutRegistration(method *rpcMethodInfo, why string) error {
	if len(method.Classes) == 0 {
		return nil
	}
	names := make([]string, len(method.Classes))
	for i, class := range method.Classes {
		names[i] = string(class)
	}

	return errors.Newf(
		"struct %s: @%s(%s) is carried by the action the generated routes register, and %s; put @%s on the constant that registers the action by hand, beside its @%s, so a mask that refuses the class refuses the action",
		method.Name(), classKeyword, strings.Join(names, ", "), why, classKeyword, manualAddResourceKeyword,
	)
}

// rejectClassAnnotation refuses @class on a struct that declares no action: a class is
// carried by an action, which a method or a registration made by hand declares.
func rejectClassAnnotation(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string) error {
	if !annotations.Struct.Has(classKeyword) {
		return nil
	}

	return errors.Newf("struct %s: @%s marks an action, an @%s struct or a constant carrying @%s; a %s is none. To hold an impersonated session off a record, the mask names the record (accesstypes.RefuseWrites)", pStruct.Name(), classKeyword, rpcKeyword, manualAddResourceKeyword, kind)
}

// actionClassConstant is one class the application's actions carry, as the collection
// file declares it: the constant's name and the class.
type actionClassConstant struct {
	Name  string
	Class accesstypes.ActionClass
}

// classConstantName is the name of the constant the collection file declares for a
// class: Class, then the class in PascalCase.
func classConstantName(class accesstypes.ActionClass) string {
	return classConstantPrefix + caser.ToPascal(string(class))
}

// actionClassConstants lists the classes the collection's actions carry, sorted, each
// with the constant the collection file declares for it. Two classes whose constants
// would share a name are refused, naming both.
func actionClassConstants(data resource.CollectionData) ([]actionClassConstant, error) {
	var classes []accesstypes.ActionClass
	for i := range data.Resources {
		classes = append(classes, data.Resources[i].Classes...)
	}
	slices.Sort(classes)
	classes = slices.Compact(classes)

	constants := make([]actionClassConstant, 0, len(classes))
	named := make(map[string]accesstypes.ActionClass, len(classes))
	for _, class := range classes {
		name := classConstantName(class)
		if other, taken := named[name]; taken {
			return nil, errors.Newf("the classes %s and %s would both be declared as the constant %s; rename one of them", other, class, name)
		}
		named[name] = class
		constants = append(constants, actionClassConstant{Name: name, Class: class})
	}

	return constants, nil
}
