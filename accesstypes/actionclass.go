package accesstypes

import "regexp"

// ActionClass names a class of actions: a marking an action carries where it is
// declared (an @class annotation on a generated method, or on the constant that
// registers the action by hand, with Execute), which a permission mask refers
// to. A mask that refuses a class refuses every action that carries it, the ones
// declared after the mask was written among them, so nobody keeps a list of
// actions in step with the application. An application puts a class on an action
// alone. A record's write carries a class where the generator gives it one: the
// class access, on the writes of every record that decides what a person may do
// (ClassAccess). A mask holds a session off any other record by naming it
// (RefusePermission, RefuseWrites).
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
// do: a role given or removed, a role a person takes themselves for a set time, a
// person put in a group or taken out of one, a role given to a group or taken from
// it, a group made or deleted, an invitation sent or withdrawn, an account made (it
// is made with the roles its person holds), what a sign-in method or a tenant's
// directory gives everyone who arrives through it, the settings that say who may
// sign in at all (the switch that makes a tenant sign in with its directory alone,
// and the one that lets people ask at the sign-in page for a link that resets their
// own password), and the making of a tenant of one's own, which makes its maker the
// tenant's administrator. The library marks its own generated actions of that kind
// with it, so an application can grant "act as a person, without changing who may
// do what" with a mask that refuses the class.
//
// The generator gives the class to the writes of the application's own records that
// decide what a person may do: the create, the change and the delete of every record
// the policy reads as a subject value or a subject set (@subjectValue, @subjectSet),
// and the change of a record such a value is read through (a membership's team gives
// the division, so the team's change is in the class). Nobody lists those records by
// hand, and a record that gains a subject value is covered when the application is
// generated again.
//
// An account disabled, enabled or deleted is not in the class. That is the work a
// person who stands in for an administrator is there to do, and a mask that refuses
// the class leaves it to them.
//
// The class is the second of two controls. The first needs no mask: in an
// impersonated session nobody is the subject of what they do, so the person who
// impersonates never changes their own account, whatever the mask keeps
// (identity.RefuseImpersonatorAsSubject): they give it no role, and they do not
// disable, enable or delete it. The class refuses what that rule cannot see: a role
// given to a group the person is in, to another account of theirs, or to a
// colleague. A row of the application's own that reaches the person without naming
// them (a pass given to a team they are in) is beside those: the record's writes
// carry the class, so a mask that refuses the class refuses the row, and a session
// whose mask does not refuse it writes such a row. A row removed with
// its parent, which may name the person, is a limit of its own: no write of the row
// is made, and a mask closes it by refusing each request that removes a row above
// it. Those are the parent's delete (RefusePermission(Delete, parent)), the delete
// of each record further up while the cascade goes on, and an action that removes
// one of them in its own body (RefuseAction). Where a foreign key changes the row
// instead of removing it (PostgreSQL's ON DELETE SET NULL and SET DEFAULT, and its
// ON UPDATE CASCADE, which follows a change of the parent's key), the same refusals
// and the refusal of the parent's update close it. A column that records who made
// a row, which a grant compares to the requester, is another. While the
// application stamps it from the session and people cannot write it, no write
// moves a row onto the real person, and where people can write it, a mask refuses
// the record's writes. A row made in a session that acts as a role is the stated
// case: a role is not anyone, so the row is stamped with the person who started
// the session, and what the grants give its author is theirs after the session
// too. A role kind whose mask refuses the record's create
// (RefusePermission(Create, record)) closes that, as does a column the application
// stamps with nobody in such a session. Each of these is a stated limit of the
// rule, which a mask that does not refuse it leaves open.
//
// One limit the class does not close. The rule knows the real person by the account
// they signed in with, so a second account of their own is someone else's to it:
// a person who acts as an administrator can enable that account again, or disable
// or delete it, as the administrator can, and no class refuses it. An application
// that must hold this refuses the three actions by name in the kind's mask
// (RefuseAction).
const ClassAccess ActionClass = "access"

// actionClassName is what a class may be named: a lowercase letter, then
// lowercase letters, digits and underscores, 64 characters at most.
var actionClassName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Valid reports whether the class is named as a class may be: a lowercase
// letter, then lowercase letters, digits and underscores, 64 characters at most.
func (c ActionClass) Valid() bool {
	return actionClassName.MatchString(string(c))
}
