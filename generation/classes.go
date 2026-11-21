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
// constant of a registration made by hand, beside its @manualAddResource(Execute). A
// registration of another permission is a record's and is given none by an annotation
// (classOnNoAction). The generated collection carries them
// (resource.CollectionResource.Classes), which is where the masked check of an
// impersonated session reads them: a permission mask that refuses a class refuses every
// action that carries it (accesstypes.RefuseClass). The collection file declares a
// constant for each class in use, so a mask is written against a name the compiler
// knows.
//
// A record's write carries a class where the generator gives it one, and nowhere else:
// the class access, on the writes of every record that decides what a person may do
// and on the delete of the tenant record (writesThatDecideAccess,
// resource.CollectionResource.WriteClasses).

// identityCredentialClass is the @class line of the generated identity methods that
// change how a person signs in: a password set, required anew or removed, a sign-in
// identity unlinked, an address or a username changed. The template of each ends its
// annotations with it, so an application can grant "act as a person, except the
// credential actions" with a mask that refuses the class. A method that changes how a
// person signs in and lacks the line fails the test that lists them
// (Test_prepareIdentity_classedMethods).
const identityCredentialClass = "\n// @" + classKeyword + "(" + string(accesstypes.ClassCredential) + ")"

// identityAccessClass is the @class line of the generated identity methods that give or
// take away what someone may do: a role given or removed, a role a person takes
// themselves for a set time, a person put in a group or taken out of one, a role given to a group or taken from it, a group made or deleted,
// an invitation sent or withdrawn, an account made (it is made with the roles its
// person holds), a sign-in method opened to sign-up or made invite only, and the
// setting that lets people ask at the sign-in page for a link that resets their own
// password. The template of each ends its annotations with it, so an application can
// grant "act as a person, without changing who may do what" with a mask that refuses
// the class. A method that gives or takes away what someone may do and lacks the line
// fails the test that lists them (Test_prepareIdentity_classedMethods).
//
// An account disabled, enabled or deleted does not carry the class. That is the work a
// person who stands in for an administrator is there to do, and a mask that refuses the
// class leaves it to them. What is held instead is that nobody does it to themselves:
// the rule that the person acting is never the subject of what they do
// (identity.RefuseImpersonatorAsSubject) refuses each of the three on the real
// person's own account, whatever the mask says.
//
// The actions the generator declares with no struct to annotate carry the class by
// their registration, and a test lists those too (Test_generatorActions_classed): the
// two methods that open a tenant to its directory's people with a role and make it
// invite only again, the two of the switch that makes a tenant sign in with its
// directory alone (tenantOwnMethod.Classes), and the making of a tenant of one's own
// (tenantCreationAction).
const identityAccessClass = "\n// @" + classKeyword + "(" + string(accesstypes.ClassAccess) + ")"

// accessWrite is one write of a record the generator gives the class access: the
// record and the permission of the write.
type accessWrite struct {
	Record     *resourceInfo
	Permission accesstypes.Permission
}

// writesThatDecideAccess lists the writes of records that decide what a person may do,
// which the generator gives the class access (resource.CollectionResource.WriteClasses),
// in the order the resources are declared, each write once. They come in three shapes.
//
// A record whose rows the policy reads as a subject value or a subject set
// (@subjectValue, @subjectSet): a row says what the person it names, or the people of
// the set it names, may do, so its create, its change and its delete each give or take
// that away.
//
// A record a subject set's or a subject value's dotted value is read through (value:
// SquadID.DivisionID reads the division off the squad's row): the row says what every
// person who reaches it holds, so its change moves them all. Its create and its delete
// carry no class: a new row is reached by nobody yet, and a delete that takes with it
// the rows that reach it is a row removed with its parent, which a mask closes by
// refusing the parent's delete by name.
//
// The tenant record (@tenant), its delete alone: a tenant that goes takes with it the
// roles every person holds in it, its groups, the custom roles made in it and the
// invitations into it, and each account that is left with nothing
// (identity.Manager.ClearTenant, which the generated delete calls for every auth whose
// management the program generates). Each of those done one at a time is an action of
// the class, so the delete that does them all carries it, whether the library removes
// the rows or the application does. The record's change carries none: a tenant renamed
// holds what it held.
//
// Nobody lists these by hand: an application's mask refuses the class, and a record that
// gains a subject value, or a value that takes a new path, is covered when the
// application is generated again. An application does not put @class on a record
// (rejectClassAnnotation); a mask that is to keep one of these writes names it as an
// exception to its refusal of the class (accesstypes.RefuseClassExcept, with
// accesstypes.ExceptWrites for a record or accesstypes.ExceptPermission for one write).
func (r *resourceGenerator) writesThatDecideAccess() []accessWrite {
	byTable := make(map[string]*resourceInfo, len(r.resources))
	for _, res := range r.resources {
		byTable[r.pluralize(res.Name())] = res
	}

	var writes []accessWrite
	add := func(record *resourceInfo, permission accesstypes.Permission) {
		if !slices.ContainsFunc(writes, func(w accessWrite) bool {
			return w.Record == record && w.Permission == permission
		}) {
			writes = append(writes, accessWrite{Record: record, Permission: permission})
		}
	}
	for _, res := range r.resources {
		subjects := slices.Concat(res.SubjectValues, res.SubjectSets)
		if len(subjects) > 0 {
			for _, permission := range []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
				add(res, permission)
			}
		}
		if res.IsTenant {
			add(res, accesstypes.Delete)
		}
		for _, subject := range subjects {
			for _, hop := range subject.Path {
				// A hop onto a table no resource of this run declares has no record
				// for a write to be checked on: the run warns of it, naming the table
				// (SubjectPathWarning).
				if through, ok := byTable[hop.Table]; ok {
					add(through, accesstypes.Update)
				}
			}
		}
	}

	return writes
}

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

	return errors.Newf(
		"struct %s: @%s(%s) is carried by the action the generated routes register, and %s; put @%s on the constant that registers the action by hand, beside its @%s, so a mask that refuses the class refuses the action",
		method.Name(), classKeyword, classNames(method.Classes), why, classKeyword, manualAddResourceKeyword,
	)
}

// classOnNoAction refuses a class on a registration made by hand under a permission
// other than Execute; where names the declaration in the refusal. A class is carried by
// an action, and an action is what Execute is checked on. The other permissions are a
// record's: what a role grants on it, and what a mask refuses by the record's name. A
// class beside one of them would let a mask that refuses the class refuse a record's
// permission through a marking that says nothing of records, so the declaration is
// refused, where it is written.
func classOnNoAction(where string, permission accesstypes.Permission, classes []accesstypes.ActionClass) error {
	if len(classes) == 0 || permission == accesstypes.Execute {
		return nil
	}

	return errors.Newf(
		"%s: @%s(%s) marks an action, and the registration is of %s, which is no action's permission: a class is carried by a registration of %s alone. "+
			"Register the action with @%s(%s), or remove the @%s and have the mask name the record (accesstypes.RefusePermission, accesstypes.RefuseWrites)",
		where, classKeyword, classNames(classes), permission, accesstypes.Execute, manualAddResourceKeyword, accesstypes.Execute, classKeyword,
	)
}

// classesOnActionsAlone refuses a name that carries a class and registers a permission
// other than Execute, however the two came to share the name: a method that carries the
// class and a registration made by hand of another permission under the method's name,
// in the method's scope. One made in the other scope is refused before this is asked,
// since a name is of one scope (oneScopePerName). The masked check reads an action's
// classes by its name whichever scope registers it, so a class would otherwise ride the
// record's permission with the action's, in the action's scope and in the other.
func classesOnActionsAlone(data resource.CollectionData) error {
	classed := make(map[accesstypes.Resource][]accesstypes.ActionClass)
	for i := range data.Resources {
		res := &data.Resources[i]
		if len(res.Classes) > 0 {
			classed[res.Name] = append(classed[res.Name], res.Classes...)
		}
	}
	for i := range data.Resources {
		res := &data.Resources[i]
		classes, ok := classed[res.Name]
		if !ok {
			continue
		}
		for _, permission := range res.Permissions {
			if permission == accesstypes.Execute {
				continue
			}
			slices.Sort(classes)

			return errors.Newf(
				"%q carries the class %s and registers %s beside %s: a class is carried by an action alone, a name that registers %s and nothing else. "+
					"Register the %s permission under a name of its own, or remove the class",
				string(res.Name), classNames(slices.Compact(classes)), permission, accesstypes.Execute, accesstypes.Execute, permission,
			)
		}
	}

	return nil
}

// classNames lists classes for a refusal, comma separated.
func classNames(classes []accesstypes.ActionClass) string {
	names := make([]string, len(classes))
	for i, class := range classes {
		names[i] = string(class)
	}

	return strings.Join(names, ", ")
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

// actionClassConstants lists the classes the collection's actions and its records'
// writes carry, sorted, each with the constant the collection file declares for it. Two
// classes whose constants would share a name are refused, naming both.
func actionClassConstants(data resource.CollectionData) ([]actionClassConstant, error) {
	var classes []accesstypes.ActionClass
	for i := range data.Resources {
		classes = append(classes, data.Resources[i].Classes...)
		for _, write := range data.Resources[i].WriteClasses {
			classes = append(classes, write.Classes...)
		}
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
