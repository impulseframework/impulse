// Package recordrolesfixture provides parsed-struct fixtures for the records a role can
// be held on (@holdsRoles): a realm is the tenant record, a project of a realm holds
// roles and names a client, a task is under a project by the project it is in and by the
// one it came from, a comment is on a task, a note is interleaved in a project and a
// reply in a note, an invoice is a client's and stands apart, a setting is the realm's
// alone, and a currency is no realm's. The schema behind them is synthetic
// (recordRolesTables in the tests): the realm names its featured project, so every
// realm's row is under a project through the tenant record, which no way passes.
package recordrolesfixture

import "github.com/impulseframework/impulse"

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in a grouped type block like real project sources do.
type (
	// Realm is the tenant record.
	//
	// @resource
	// @tenant
	Realm struct {
		ID                impulse.UUID `spanner:"Id"`
		Name              string       `spanner:"Name"`
		FeaturedProjectID impulse.UUID `spanner:"FeaturedProjectId"`
		// @tenantDisabled
		Disabled bool `spanner:"Disabled"`
	}

	// Currency is no realm's.
	//
	// @resource
	Currency struct {
		ID   string `spanner:"Id"`
		Name string `spanner:"Name"`
	}

	// Client is a realm's.
	//
	// @resource
	// @permissionScope(domain)
	Client struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		RealmID impulse.UUID `spanner:"RealmId"`
		Name    string       `spanner:"Name"`
	}

	// Project holds roles: it names a client, and its prices are in a currency.
	//
	// @resource
	// @permissionScope(domain)
	// @holdsRoles
	Project struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		RealmID    impulse.UUID `spanner:"RealmId"`
		ClientID   impulse.UUID `spanner:"ClientId"`
		CurrencyID string       `spanner:"CurrencyId"`
	}

	// Task is in a project and came from another.
	//
	// @resource
	// @permissionScope(domain)
	Task struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		RealmID         impulse.UUID `spanner:"RealmId"`
		ProjectID       impulse.UUID `spanner:"ProjectId"`
		FormerProjectID impulse.UUID `spanner:"FormerProjectId"`
	}

	// Comment is on a task.
	//
	// @resource
	// @permissionScope(domain)
	Comment struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		RealmID impulse.UUID `spanner:"RealmId"`
		TaskID  impulse.UUID `spanner:"TaskId"`
	}

	// ProjectNote is interleaved in Projects: its key opens with the project's.
	//
	// @resource
	// @permissionScope(domain)
	ProjectNote struct {
		ProjectID impulse.UUID `spanner:"Id"`
		Sequence  int64        `spanner:"Sequence"`
		// @domain
		RealmID impulse.UUID `spanner:"RealmId"`
	}

	// NoteReply is interleaved in ProjectNotes, two levels down from a project.
	//
	// @resource
	// @permissionScope(domain)
	NoteReply struct {
		ProjectID impulse.UUID `spanner:"Id"`
		Sequence  int64        `spanner:"Sequence"`
		Reply     int64        `spanner:"Reply"`
		// @domain
		RealmID impulse.UUID `spanner:"RealmId"`
	}

	// Invoice is a client's, under no project.
	//
	// @resource
	// @permissionScope(domain)
	Invoice struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		RealmID  impulse.UUID `spanner:"RealmId"`
		ClientID impulse.UUID `spanner:"ClientId"`
	}

	// Setting is the realm's own.
	//
	// @resource
	// @permissionScope(domain)
	Setting struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		RealmID impulse.UUID `spanner:"RealmId"`
	}

	// ProjectSummary is a view of projects, which has no table to hold a role on.
	//
	// @virtual
	// @holdsRoles
	ProjectSummary struct {
		ID impulse.UUID `spanner:"Id"`
	}
)
