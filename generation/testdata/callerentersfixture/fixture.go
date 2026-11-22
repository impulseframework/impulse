// Package callerentersfixture holds the structs the @callerEnters resolution reads: rows
// whose struct says which tenants a caller must enter, in its one form and in others,
// and methods that name them, one per shape the resolution accepts or refuses.
package callerentersfixture

import (
	"github.com/impulseframework/impulse/resource"
)

// Keeper says which tenants a caller must enter, in the one form.
type Keeper struct {
	ID string `spanner:"Id"`
}

func (Keeper) TenantsToEnter() resource.TenantsToEnter {
	return resource.TenantsToEnter{Query: "SELECT KeeperId AS RowKey, Tenant FROM KeeperPosts", Column: "Id"}
}

// Dock says nothing.
type Dock struct {
	ID string `spanner:"Id"`
}

// Crane says it on a pointer receiver, which a value of the row does not carry.
type Crane struct {
	ID string `spanner:"Id"`
}

func (*Crane) TenantsToEnter() resource.TenantsToEnter {
	return resource.TenantsToEnter{}
}

// Hoist says it given a parameter.
type Hoist struct {
	ID string `spanner:"Id"`
}

func (Hoist) TenantsToEnter(column string) resource.TenantsToEnter {
	return resource.TenantsToEnter{Column: column}
}

// Winch says it as a string.
type Winch struct {
	ID string `spanner:"Id"`
}

func (Winch) TenantsToEnter() string {
	return ""
}

type (
	// RetireKeeper is offered on a Keeper only to a caller who enters its tenants.
	//
	// @rpc
	// @callerEnters
	RetireKeeper struct {
		// @target(Keeper)
		KeeperID string
	}

	// GreetKeeper names a Keeper and is offered as the grant says.
	//
	// @rpc
	GreetKeeper struct {
		// @target(Keeper)
		KeeperID string
	}

	// @rpc
	// @callerEnters(Keeper)
	CallerEntersWithArgument struct {
		// @target(Keeper)
		KeeperID string
	}

	// @rpc
	// @callerEnters
	CallerEntersNoRow struct {
		KeeperID string
	}

	// @rpc
	// @callerEnters
	CallerEntersUndeclared struct {
		// @target(Dock)
		DockID string
	}

	// @rpc
	// @callerEnters
	CallerEntersPointer struct {
		// @target(Crane)
		CraneID string
	}

	// @rpc
	// @callerEnters
	CallerEntersParameter struct {
		// @target(Hoist)
		HoistID string
	}

	// @rpc
	// @callerEnters
	CallerEntersString struct {
		// @target(Winch)
		WinchID string
	}

	// Tower is a resource that marks itself as if it were a method.
	//
	// @resource
	// @callerEnters
	Tower struct {
		ID string `spanner:"Id"`
	}
)
