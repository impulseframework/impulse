// Package permissionfixture is the test fixture for a permission declared with no
// resource: accesstypes.Permission constants carrying @manualAddPermission, one that
// carries nothing and so declares nothing, and the declarations the generator refuses,
// each read alone by the test that names it. One resource's constant registers by hand
// in both scopes, which the generator refuses where it computes the collection.
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
