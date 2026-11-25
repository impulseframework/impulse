// Package groupdeletefixture provides parsed-struct fixtures for the delete of an auth's
// group: the group's declaration takes its members and its roles, as the auth's
// generated management declares them, and beside them an application's record takes a
// kind of its own. groupDeleteTables in the tests carries the foreign keys.
package groupdeletefixture

import "github.com/impulseframework/impulse"

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in grouped type blocks like real project sources do.
type (
	// CrewGroup is a group of the Crew auth. Its delete takes its members and roles.
	//
	// @resource
	// @deleteCascade(CrewGroupMember, CrewGroupRole)
	CrewGroup struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// CrewGroupMember is a person's place in a group: a foreign key to CrewGroups.
	//
	// @resource
	CrewGroupMember struct {
		GroupID impulse.UUID `spanner:"GroupId"`
		UserID  string       `spanner:"UserId"`
	}

	// CrewGroupRole is a role a group holds: a foreign key to CrewGroups.
	//
	// @resource
	CrewGroupRole struct {
		GroupID impulse.UUID `spanner:"GroupId"`
		Role    string       `spanner:"Role"`
	}

	// Roster is an application's record. Its delete takes its shifts.
	//
	// @resource
	// @deleteCascade(Shift)
	Roster struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Shift belongs to a roster: a foreign key to Rosters.
	//
	// @resource
	Shift struct {
		ID       impulse.UUID `spanner:"Id"`
		RosterID impulse.UUID `spanner:"RosterId"`
	}
)
