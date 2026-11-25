// Package deletedwithfixture provides parsed-struct fixtures for the application's records
// that name a group or an account of an auth's (@deletedWith): the group's declaration
// takes its members and its roles, as the auth's generated management declares them, the
// account is declared as the management declares it, and beside them are the
// application's records, of which each test reads the ones it is about.
// deletedWithTables in the tests carries the foreign keys.
package deletedwithfixture

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

	// CrewSessionUser is an account of the Crew auth: a row of the session library's users
	// table.
	//
	// @resource
	// @suppress(patchHandler)
	// @rowWord(account)
	CrewSessionUser struct {
		ID       impulse.UUID `spanner:"Id"`
		Username string       `spanner:"Username"`
	}

	// Task is an application's record that a link names.
	//
	// @resource
	Task struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// TaskAssignment links a task to the group it is assigned to, and goes with the group;
	// it also names the person it is assigned to, and says nothing of the account.
	//
	// @resource
	TaskAssignment struct {
		ID     impulse.UUID `spanner:"Id"`
		TaskID impulse.UUID `spanner:"TaskId"`
		// @deletedWith
		GroupID impulse.UUID `spanner:"GroupId"`
		UserID  impulse.UUID `spanner:"UserId"`
	}

	// TaskWatch links a task to a group that watches it, and says nothing of the group's
	// delete.
	//
	// @resource
	TaskWatch struct {
		ID      impulse.UUID `spanner:"Id"`
		TaskID  impulse.UUID `spanner:"TaskId"`
		GroupID impulse.UUID `spanner:"GroupId"`
	}

	// ShiftCover names the group that covers a shift, which it goes with, and the group
	// that backs it up, which it says nothing of.
	//
	// @resource
	ShiftCover struct {
		ID impulse.UUID `spanner:"Id"`
		// @deletedWith
		GroupID       impulse.UUID `spanner:"GroupId"`
		BackupGroupID impulse.UUID `spanner:"BackupGroupId"`
	}

	// NotedAssignment goes with the group it names and takes its notes with it; a hold on
	// it refuses its delete. It also names a person.
	//
	// @resource
	// @deleteCascade(AssignmentNote, refusedBy: AssignmentHold)
	NotedAssignment struct {
		ID impulse.UUID `spanner:"Id"`
		// @deletedWith
		GroupID impulse.UUID `spanner:"GroupId"`
		UserID  impulse.UUID `spanner:"UserId"`
	}

	// AssignmentNote is a note on a noted assignment.
	//
	// @resource
	AssignmentNote struct {
		ID           impulse.UUID `spanner:"Id"`
		AssignmentID impulse.UUID `spanner:"AssignmentId"`
	}

	// AssignmentHold holds a noted assignment.
	//
	// @resource
	AssignmentHold struct {
		ID           impulse.UUID `spanner:"Id"`
		AssignmentID impulse.UUID `spanner:"AssignmentId"`
	}

	// HeldAssignment goes with the group it names, and declares nothing of the slips
	// under it.
	//
	// @resource
	HeldAssignment struct {
		ID impulse.UUID `spanner:"Id"`
		// @deletedWith
		GroupID impulse.UUID `spanner:"GroupId"`
	}

	// HoldSlip names a held assignment.
	//
	// @resource
	HoldSlip struct {
		ID           impulse.UUID `spanner:"Id"`
		AssignmentID impulse.UUID `spanner:"AssignmentId"`
	}

	// NestedAssignment goes with the group it names; one may be under another, and its
	// delete takes those under it.
	//
	// @resource
	// @deleteCascade(NestedAssignment)
	NestedAssignment struct {
		ID       impulse.UUID  `spanner:"Id"`
		ParentID *impulse.UUID `spanner:"ParentId"`
		// @deletedWith
		GroupID impulse.UUID `spanner:"GroupId"`
	}

	// PlaceBadge names a person's place in a group, and not the group.
	//
	// @resource
	PlaceBadge struct {
		ID      impulse.UUID `spanner:"Id"`
		GroupID impulse.UUID `spanner:"GroupId"`
		UserID  string       `spanner:"UserId"`
	}

	// Project is an application's record its tasks name, which a test may make the
	// tenant record.
	//
	// @resource
	Project struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// ProjectTask names its project and says it goes with it, where the project is the
	// one to say so.
	//
	// @resource
	ProjectTask struct {
		ID impulse.UUID `spanner:"Id"`
		// @deletedWith
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// LevelTask names a level by a column no foreign key holds.
	//
	// @resource
	LevelTask struct {
		ID impulse.UUID `spanner:"Id"`
		// @deletedWith
		LevelID string `spanner:"LevelId"`
	}

	// Rank is a read-only table that names a group.
	//
	// @resource
	Rank struct {
		ID impulse.UUID `spanner:"Id"`
		// @deletedWith
		GroupID impulse.UUID `spanner:"GroupId"`
	}
)
