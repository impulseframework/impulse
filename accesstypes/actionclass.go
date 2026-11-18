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

// actionClassName is what a class may be named: a lowercase letter, then
// lowercase letters, digits and underscores, 64 characters at most.
var actionClassName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Valid reports whether the class is named as a class may be: a lowercase
// letter, then lowercase letters, digits and underscores, 64 characters at most.
func (c ActionClass) Valid() bool {
	return actionClassName.MatchString(string(c))
}
