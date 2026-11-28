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
	return resource.TenantsToEnter{Query: "SELECT KeeperId AS RowKey, Tenant FROM KeeperPosts", Match: map[string]string{"RowKey": "Id"}}
}

// Bollard says which tenants a caller must enter, in the one form, as Keeper does.
type Bollard struct {
	ID string `spanner:"Id"`
}

func (Bollard) TenantsToEnter() resource.TenantsToEnter {
	return resource.TenantsToEnter{Query: "SELECT BollardId AS RowKey, Tenant FROM BollardPosts", Match: map[string]string{"RowKey": "Id"}}
}

// Mooring is a struct of the package that is no resource.
type Mooring struct {
	ID string `spanner:"Id"`
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
	return resource.TenantsToEnter{Match: map[string]string{"RowKey": column}}
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

	// SweepPosts names no row and is offered on the rows of Keeper and Bollard, in the
	// order written, only to a caller who enters a row's tenants.
	//
	// @rpc
	// @callerEnters(Keeper, Bollard)
	SweepPosts struct {
		KeeperID string
	}

	// @rpc
	// @callerEnters(Keeper, , Bollard)
	CallerEntersEmptyName struct {
		KeeperID string
	}

	// @rpc
	// @callerEnters(Keeper, Bollard, Keeper)
	CallerEntersTwice struct {
		KeeperID string
	}

	// @rpc
	// @callerEnters(Keeper, Mooring)
	CallerEntersNotResource struct {
		KeeperID string
	}

	// @rpc
	// @callerEnters(Keeper)
	CallerEntersOtherScope struct {
		KeeperID string
	}

	// @rpc
	// @callerEnters(Keeper, Dock)
	CallerEntersOfferedUndeclared struct {
		KeeperID string
	}

	// Tower is a resource that marks itself as if it were a method.
	//
	// @resource
	// @callerEnters
	Tower struct {
		ID string `spanner:"Id"`
	}
)
