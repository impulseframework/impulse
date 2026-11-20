package accesstypes

import "regexp"

// ActionClass names a class of actions: a marking an action carries where it is
// declared (an @class annotation on a generated method, or on the constant of a
// registration made by hand), which a permission mask refers to. A mask that
// refuses a class refuses every action that carries it, the ones declared after
// the mask was written among them, so nobody keeps a list of actions in step
// with the application.
//
// An application names its own classes and marks its own actions with them, or
// with one the library names.
type ActionClass string

// ClassCredential is the class of the actions that change how a person signs in:
// a password set, required anew or removed, a sign-in identity unlinked, an
// address or a username changed. The library marks its own generated identity
// methods of that kind with it, so an application can grant "act as a person,
// except the credential actions" with a mask that refuses the class.
const ClassCredential ActionClass = "credential"

// ClassAccess is the class of the actions that give or take away what someone may
// do: a role given or removed, a person put in a group or taken out of one, a role
// given to a group or taken from it, a group made or deleted, an invitation sent or
// withdrawn, an account made, disabled, enabled or deleted, what a sign-in method
// or a tenant's directory gives everyone who arrives through it, the settings that
// say who may sign in at all (the switch that makes a tenant sign in with its
// directory alone, and the one that lets people ask at the sign-in page for a link
// that resets their own password), and the making of a tenant of one's own, which
// makes its maker the tenant's administrator. The library marks its own generated
// actions of that kind with it, so an application can grant "act as a person,
// without changing who may do what" with a mask that refuses the class.
//
// The class is the second of two controls. The first needs no mask: in an
// impersonated session nobody is the subject of what they do, so the person who
// impersonates never changes their own account, whatever the mask keeps
// (identity.RefuseImpersonatorAsSubject). The class refuses what that rule cannot
// see: a role given to a group the person is in, to another account of theirs, or
// to a colleague.
const ClassAccess ActionClass = "access"

// actionClassName is what a class may be named: a lowercase letter, then
// lowercase letters, digits and underscores, 64 characters at most.
var actionClassName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Valid reports whether the class is named as a class may be: a lowercase
// letter, then lowercase letters, digits and underscores, 64 characters at most.
func (c ActionClass) Valid() bool {
	return actionClassName.MatchString(string(c))
}
