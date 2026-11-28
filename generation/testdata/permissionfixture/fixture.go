// Package permissionfixture is the test fixture for a permission declared with no
// resource: accesstypes.Permission constants carrying @manualAddPermission, one that
// carries nothing and so declares nothing, and the declarations the generator refuses,
// each read alone by the test that names it. One resource's constant registers by hand
// in both scopes, which the generator refuses where it computes the collection. Constants
// of aliases of accesstypes.Resource and accesstypes.Permission are read as theirs, and
// a registration on a constant of any other type is refused. The const groups at the end
// carry annotations in the comment above the group, which nothing reads and the
// generator refuses, beside the groups it takes. The comments after them that a blank
// line parts from the declaration below, and the one no declaration follows, are no
// constant's: the generator refuses an annotation's line in one, and takes prose and a
// line that starts with another annotation's name.
package permissionfixture

import (
	"github.com/impulseframework/impulse/accesstypes"
)

const (
	// ExportReports is checked for the whole of a tenant.
	//
	// @manualAddPermission
	ExportReports accesstypes.Permission = "ExportReports"

	// Dormant carries no annotation and declares nothing.
	Dormant accesstypes.Permission = "Dormant"
)

// ApproveBudgets is declared in a trailing comment.
const ApproveBudgets accesstypes.Permission = "ApproveBudgets" // @manualAddPermission

// ExportAgain declares the permission another constant declares.
//
// @manualAddPermission
const ExportAgain accesstypes.Permission = "ExportReports"

// Nameless declares a permission with no name.
//
// @manualAddPermission
const Nameless accesstypes.Permission = ""

// Classed puts a class beside the declaration.
//
// @manualAddPermission
// @class(credential)
const Classed accesstypes.Permission = "Classed"

// Mounted puts an outlet beside the declaration.
//
// @manualAddPermission
// @outlet(portal)
const Mounted accesstypes.Permission = "Mounted"

// Registered registers a resource on a permission's constant.
//
// @manualAddPermission
// @manualAddResource(Execute)
const Registered accesstypes.Permission = "Registered"

// Strayed carries a resource's registration on a permission's constant that declares
// nothing.
//
// @manualAddResource(Execute)
const Strayed accesstypes.Permission = "Strayed"

// Outside names an outlet on a permission's constant that declares nothing.
//
// @outlet(portal)
const Outside accesstypes.Permission = "Outside"

// Marked puts a class on a permission's constant that declares nothing.
//
// @class(credential)
const Marked accesstypes.Permission = "Marked"

// Untyped carries the declaration on a constant that is no permission's.
//
// @manualAddPermission
const Untyped = "Untyped"

// Worded is an untyped constant whose comment only talks about the declaration: this
// sentence names @manualAddPermission in passing, and the line below starts with
// another word.
//
// @manualAddPermissions
const Worded = "Worded"

// Ledgers is a resource whose constant carries the declaration of a permission.
//
// @manualAddPermission
const Ledgers accesstypes.Resource = "Ledgers"

// Reports is a resource registered by hand, beside the permissions.
//
// @manualAddResource(Read)
const Reports accesstypes.Resource = "Reports"

// Divided is a resource registered by hand for the whole application and for a tenant.
//
// @manualAddResource(Read)
// @manualAddResource(Update, domain)
const Divided accesstypes.Resource = "Divided"

// ResourceAlias is another name for accesstypes.Resource: its constants are resources'.
type ResourceAlias = accesstypes.Resource

// PermissionAlias is another name for accesstypes.Permission: its constants are
// permissions'.
type PermissionAlias = accesstypes.Permission

// DeclaredResource is a type declared over accesstypes.Resource, which is another type.
type DeclaredResource accesstypes.Resource

// Aliased is a resource registered by hand on a constant of an alias of
// accesstypes.Resource.
//
// @manualAddResource(Read)
const Aliased ResourceAlias = "Aliased"

// AliasedExport declares a permission on a constant of an alias of
// accesstypes.Permission.
//
// @manualAddPermission
const AliasedExport PermissionAlias = "AliasedExport"

// Declared carries a registration on a constant of a type declared over
// accesstypes.Resource.
//
// @manualAddResource(Read)
const Declared DeclaredResource = "Declared"

// UntypedRegistered carries a registration on an untyped constant.
//
// @manualAddResource(Read)
const UntypedRegistered = "UntypedRegistered"

// SetWorded is an untyped constant whose comment holds a line that starts with another
// annotation's name.
//
// @manualAddResourceSet(Read)
const SetWorded = "SetWorded"

// Prosed is an untyped constant whose comment names @manualAddResource(Read) inside a
// line of prose.
const Prosed = "Prosed"

// GroupedExport and GroupedApprove carry the declaration in the comment above their
// group, where nothing reads it.
//
// @manualAddPermission
const (
	GroupedExport  accesstypes.Permission = "GroupedExport"
	GroupedApprove accesstypes.Permission = "GroupedApprove"
)

// GroupedLedgers and GroupedJournals carry a registration in the comment above their
// group, where nothing reads it.
//
// @manualAddResource(Read)
const (
	GroupedLedgers  accesstypes.Resource = "GroupedLedgers"
	GroupedJournals accesstypes.Resource = "GroupedJournals"
)

// MisspelledAccounts and MisspelledBudgets carry a misspelled registration in the comment
// above their group.
//
// @manualAddResurce(Read)
const (
	MisspelledAccounts accesstypes.Resource = "MisspelledAccounts"
	MisspelledBudgets  accesstypes.Resource = "MisspelledBudgets"
)

// UntypedFirst and UntypedSecond are untyped constants with the declaration in the
// comment above their group.
//
// @manualAddPermission
const (
	UntypedFirst  = "UntypedFirst"
	UntypedSecond = "UntypedSecond"
)

// UntypedLedgers and UntypedJournals are untyped constants with a registration in the
// comment above their group.
//
// @manualAddResource(Read)
const (
	UntypedLedgers  = "UntypedLedgers"
	UntypedJournals = "UntypedJournals"
)

// @manualAddPermission
const (
	// LoneExport is the one constant of its group and has a comment of its own.
	LoneExport accesstypes.Permission = "LoneExport"
)

// SettledExport and SettledApprove each carry the declaration above the constant, with
// prose above their group.
const (
	// @manualAddPermission
	SettledExport accesstypes.Permission = "SettledExport"
	// @manualAddPermission
	SettledApprove accesstypes.Permission = "SettledApprove"
)

// LabelFirst and LabelSecond are untyped constants with a line above their group that
// starts with another word after the @.
//
// @see the labels the screens show
const (
	LabelFirst  = "LabelFirst"
	LabelSecond = "LabelSecond"
)

// SingleExport is one constant with no parentheses: the comment above it is its own.
//
// @manualAddPermission
const SingleExport accesstypes.Permission = "SingleExport"

// @manualAddResource(Read)

// Parted is a resource whose registration a blank line parts from its constant.
const Parted accesstypes.Resource = "Parted"

const (
	// @manualAddPermission

	// PartedExport is a permission whose declaration a blank line parts from its
	// constant inside the group.
	PartedExport accesstypes.Permission = "PartedExport"
)

// This prose names @manualAddResource(Read) inside a line, and a blank line parts it
// from the constant below.

// PartedProse is an untyped constant below a comment of prose.
const PartedProse = "PartedProse"

// @manualAddResourceSet(Read)

// PartedSet is an untyped constant below a comment whose line starts with another
// annotation's name.
const PartedSet = "PartedSet"

// Audit is a function: its doc comment and the comment in its body are no constant's,
// and are not loose either.
//
// @manualAddResource(Read)
func Audit() {
	// @manualAddResource(Read)
}

// The fixture ends with a comment no declaration follows.
//
// @manualAddPermission
