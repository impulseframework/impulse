// Package notonownaccountfixture holds the structs the @notOnOwnAccount resolution reads:
// rows that are accounts, a table's and a view's, and methods that name them, one per
// shape the resolution accepts or refuses.
package notonownaccountfixture

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Keeper is an account: its ID holds the account's id. It also says which tenants a
// caller must enter, in the one form.
type Keeper struct {
	ID   impulse.UUID `spanner:"Id"`
	Name string       `spanner:"Name"`
}

func (Keeper) TenantsToEnter() resource.TenantsToEnter {
	return resource.TenantsToEnter{Query: "SELECT KeeperId AS RowKey, Tenant FROM KeeperPosts", Match: map[string]string{"RowKey": "Id"}}
}

// Lookout is a view of accounts, read as a table's rows are.
type Lookout struct {
	ID impulse.UUID `spanner:"Id"`
}

type (
	// DisableKeeper is kept off the Keeper of the caller's own account.
	//
	// @rpc
	// @notOnOwnAccount(ID)
	DisableKeeper struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// GreetKeeper names a Keeper and is offered as the grant says.
	//
	// @rpc
	GreetKeeper struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// RetireKeeper is offered on a Keeper only to a caller who enters its tenants, and is
	// kept off the Keeper of the caller's own account.
	//
	// @rpc
	// @callerEnters
	// @notOnOwnAccount(ID)
	RetireKeeper struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// DisableLookout is kept off the Lookout of the caller's own account.
	//
	// @rpc
	// @notOnOwnAccount(ID)
	DisableLookout struct {
		// @target(Lookout)
		LookoutID impulse.UUID
	}

	// NotOnOwnAccountNoRow names no row.
	//
	// @rpc
	// @notOnOwnAccount(ID)
	NotOnOwnAccountNoRow struct {
		Reason string
	}

	// NotOnOwnAccountTwoNames names two fields.
	//
	// @rpc
	// @notOnOwnAccount(ID, Name)
	NotOnOwnAccountTwoNames struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// NotOnOwnAccountEmptyName names a field and an empty name.
	//
	// @rpc
	// @notOnOwnAccount(ID, )
	NotOnOwnAccountEmptyName struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// NotOnOwnAccountNoField names a field the row's struct does not have.
	//
	// @rpc
	// @notOnOwnAccount(AccountID)
	NotOnOwnAccountNoField struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// NotOnOwnAccountString names a field that is not an id.
	//
	// @rpc
	// @notOnOwnAccount(Name)
	NotOnOwnAccountString struct {
		// @target(Keeper)
		KeeperID impulse.UUID
	}

	// Tower is a resource, which declares no method to offer.
	//
	// @resource
	// @notOnOwnAccount(ID)
	Tower struct {
		ID impulse.UUID `spanner:"Id"`
	}
)
