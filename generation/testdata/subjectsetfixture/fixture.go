// Package subjectsetfixture provides parsed-struct fixtures for the two parts a
// @subjectSet's definition may write, who: and where:: the shapes that resolve, one
// struct per refusal, and the tables the index warning reads.
package subjectsetfixture

import (
	"time"

	"cloud.google.com/go/civil"
	"github.com/impulseframework/impulse"
)

// Level is the enumeration the Level columns below hold a value of.
//
// @enumerate(Levels)
type Level string

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in grouped type blocks like real project sources do.
type (
	// TeamMember names the people of a team, outside every tenant: the set other
	// tables build on. It writes neither part, so it is the set it has always been.
	//
	// @resource
	TeamMember struct {
		TeamID impulse.UUID `spanner:"TeamId"`
		// @subjectSet(teams, value: TeamID)
		UserID string `spanner:"UserId"`
	}

	// Seat is one row per person, the anchor of a subject value.
	//
	// @resource
	Seat struct {
		// @subjectValue(rank, value: Rank)
		UserID string `spanner:"UserId"`
		Rank   int64  `spanner:"Rank"`
	}

	// Assignment is the table that yields two sets by a column: a row names a person
	// or a team and puts them on a project at a level, until it expires. The
	// annotations sit on UserID, one of the columns the who: names.
	//
	// @resource
	// @permissionScope(domain)
	Assignment struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID  string       `spanner:"TenantId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		// @subjectSet(leads, value: ProjectID,
		//     who:   UserID = subject OR TeamID IN subject.teams,
		//     where: Level = 'lead' AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @subjectSet(follows, value: ProjectID,
		//     who:   UserID = subject OR TeamID IN subject.teams,
		//     where: Level IN ('lead', 'member') AND Active = true AND Rank >= 2 AND StartsOn <= '2026-10-01')
		UserID    *string          `spanner:"UserId"`
		TeamID    impulse.NullUUID `spanner:"TeamId"`
		Level     Level            `spanner:"Level"`
		Rank      int64            `spanner:"Rank"`
		Active    bool             `spanner:"Active"`
		StartsOn  civil.Date       `spanner:"StartDate"`
		ExpiresAt *time.Time       `spanner:"ExpiresAt"`
		Payload   []byte           `spanner:"Payload"`
	}

	// Delegation follows a set that itself follows a set, two levels deep, and names
	// the requester nowhere itself: its annotation sits on the one column its who:
	// names. It sits outside every tenant and builds on the tenant's leads.
	//
	// @resource
	Delegation struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(delegated, value: TaskID, who: ProjectID IN subject.leads)
		ProjectID impulse.UUID `spanner:"ProjectId"`
		TaskID    impulse.UUID `spanner:"TaskId"`
	}

	// Project is a parsed resource a dotted set value lands on.
	//
	// @resource
	Project struct {
		ID          impulse.UUID `spanner:"Id"`
		PortfolioID impulse.UUID `spanner:"PortfolioId"`
	}

	// Stake yields a value one hop away and writes both parts: the parts read its own
	// row, the hop leaves from it, and its box lists the parts though the value is
	// drawn as an edge.
	//
	// @resource
	Stake struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(portfolios, value: ProjectID.PortfolioID,
		//     who:   UserID = subject OR TeamID IN subject.teams,
		//     where: Active = true)
		UserID    *string          `spanner:"UserId"`
		TeamID    impulse.NullUUID `spanner:"TeamId"`
		ProjectID impulse.UUID     `spanner:"ProjectId"`
		Active    bool             `spanner:"Active"`
	}

	// DeclaredLevel filters on a plain column that declares its enumeration on the
	// field: the values it names are the enumeration's.
	//
	// @resource
	DeclaredLevel struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(graded, value: ProjectID, where: Grade IN ('lead', 'guest'))
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		// @enumerate(Levels)
		Grade string `spanner:"Grade"`
	}

	// OwnWhere writes a where: and no who:, on the requester's column as a set
	// always sat.
	//
	// @resource
	OwnWhere struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(current, value: ProjectID, where: ExpiresAt IS NULL OR ExpiresAt > now)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		ExpiresAt *time.Time   `spanner:"ExpiresAt"`
	}

	// WhereClosingParenthesis, WhereOpeningParenthesis and WhereStrings each filter
	// on a string that holds a parenthesis with no partner: it is the string's, and
	// the annotation ends where it would without it. WhereStrings is continued over
	// the comment's next lines, and one of its strings writes a quote twice and
	// holds a double quote and a comma.
	//
	// @resource
	WhereClosingParenthesis struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(smiling, value: ProjectID, where: Code = ':)')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Code      string       `spanner:"Code"`
	}

	// @resource
	WhereOpeningParenthesis struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(opened, value: ProjectID, where: Code = '(')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Code      string       `spanner:"Code"`
	}

	// @resource
	WhereStrings struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(marked, value: ProjectID,
		//     who:   UserID = subject OR TeamID IN subject.teams,
		//     where: Code IN (':)', ';(') AND (Note IS NULL OR Note != 'it''s ("so", then'))
		UserID    *string          `spanner:"UserId"`
		TeamID    impulse.NullUUID `spanner:"TeamId"`
		ProjectID impulse.UUID     `spanner:"ProjectId"`
		Code      string           `spanner:"Code"`
		Note      *string          `spanner:"Note"`
	}
)

// The refusals, one struct each. Every one carries the columns a part may name.

type (
	// WhoLiteral's who: is a filter, not a test that ties the row to the requester.
	//
	// @resource
	WhoLiteral struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(everyones, value: ProjectID, who: Level = 'lead')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Level     Level        `spanner:"Level"`
	}

	// WhoWithFilter joins a test over the row's own column to the requester's test.
	//
	// @resource
	WhoWithFilter struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(mixed, value: ProjectID, who: UserID = subject AND Level = 'lead')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Level     Level        `spanner:"Level"`
	}

	// WhoEveryoneElse ties the row to everyone but the requester.
	//
	// @resource
	WhoEveryoneElse struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(others, value: ProjectID, who: UserID != subject)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoUnknownField names a column the row does not have.
	//
	// @resource
	WhoUnknownField struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(owned, value: ProjectID, who: UserID = subject OR OwnerID = subject)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoNumberRequester compares a number column to the requester.
	//
	// @resource
	WhoNumberRequester struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(byRank, value: ProjectID, who: Rank = subject)
		Rank      int64        `spanner:"Rank"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoUncomparable tests a column no condition can compare.
	//
	// @resource
	WhoUncomparable struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(sealed, value: ProjectID, who: Payload IN subject.teams)
		Payload   []byte       `spanner:"Payload"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoTwice writes one test twice.
	//
	// @resource
	WhoTwice struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(doubled, value: ProjectID, who: UserID = subject OR UserID = subject)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoMisplaced sits on a column its who: does not name.
	//
	// @resource
	WhoMisplaced struct {
		ID     impulse.UUID     `spanner:"Id"`
		UserID *string          `spanner:"UserId"`
		TeamID impulse.NullUUID `spanner:"TeamId"`
		// @subjectSet(misplaced, value: ProjectID, who: UserID = subject OR TeamID IN subject.teams)
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoUndeclaredSet tests a column against a set nothing declares.
	//
	// @resource
	WhoUndeclaredSet struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(viaNowhere, value: ProjectID, who: TeamID IN subject.squads)
		TeamID    impulse.UUID `spanner:"TeamId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoSubjectValue tests a column against a subject value, which is one value
	// and no set.
	//
	// @resource
	WhoSubjectValue struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(viaValue, value: ProjectID, who: Rank IN subject.rank)
		Rank      int64        `spanner:"Rank"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhoOtherType tests a number column against a set of strings.
	//
	// @resource
	WhoOtherType struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(viaNumber, value: ProjectID, who: Rank IN subject.teams)
		Rank      int64        `spanner:"Rank"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// LoopFirst and LoopSecond build on each other, and LoopSelf on itself.
	//
	// @resource
	LoopFirst struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(loopFirst, value: ProjectID, who: TeamID IN subject.loopSecond)
		TeamID    impulse.UUID `spanner:"TeamId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// @resource
	LoopSecond struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(loopSecond, value: ProjectID, who: TeamID IN subject.loopFirst)
		TeamID    impulse.UUID `spanner:"TeamId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// @resource
	LoopSelf struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(loopSelf, value: ProjectID, who: TeamID IN subject.loopSelf)
		TeamID    impulse.UUID `spanner:"TeamId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhereUnknownField names something that is not a column of the row.
	//
	// @resource
	WhereUnknownField struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(byStatus, value: ProjectID, where: Status = 'open')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// WhereUncomparable reads a column no condition can compare.
	//
	// @resource
	WhereUncomparable struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(byPayload, value: ProjectID, where: Payload IS NOT NULL)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Payload   []byte       `spanner:"Payload"`
	}

	// WhereStringToNumber, WhereNumberToString, WhereBoolToNumber,
	// WhereBadTimestamp, WhereBadDate, WhereNumberToBool and WhereDateToNow each
	// compare a column to a literal, or to now, that does not fit its type: at the
	// top of the part, under AND, under OR, under NOT and in a list.
	//
	// @resource
	WhereStringToNumber struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(highRank, value: ProjectID, where: Active = true AND Rank = 'high')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Rank      int64        `spanner:"Rank"`
		Active    bool         `spanner:"Active"`
	}

	// @resource
	WhereBoolToNumber struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(ranked, value: ProjectID, where: Rank = true)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Rank      int64        `spanner:"Rank"`
	}

	// @resource
	WhereNumberToString struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(third, value: ProjectID, where: Note IN ('a', 3))
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Note      string       `spanner:"Note"`
	}

	// @resource
	WhereBadTimestamp struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(tomorrow, value: ProjectID, where: ExpiresAt IS NULL OR ExpiresAt > 'tomorrow')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		ExpiresAt *time.Time   `spanner:"ExpiresAt"`
	}

	// @resource
	WhereBadDate struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(october, value: ProjectID, where: StartsOn >= '10/01/2026')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		StartsOn  civil.Date   `spanner:"StartsOn"`
	}

	// @resource
	WhereNumberToBool struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(switchedOn, value: ProjectID, where: NOT (Active = 1))
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Active    bool         `spanner:"Active"`
	}

	// @resource
	WhereDateToNow struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(started, value: ProjectID, where: StartsOn <= now)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		StartsOn  civil.Date   `spanner:"StartsOn"`
	}

	// WhereOffTheList compares an enumerated column to a value its enumeration does
	// not have.
	//
	// @resource
	WhereOffTheList struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(misspelled, value: ProjectID, where: Level IN ('lead', 'membre'))
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Level     Level        `spanner:"Level"`
	}

	// WhereOffTheDeclaredList is DeclaredLevel with a value the enumeration does not
	// have.
	//
	// @resource
	WhereOffTheDeclaredList struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(misgraded, value: ProjectID, where: Grade = 'chief')
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		// @enumerate(Levels)
		Grade string `spanner:"Grade"`
	}

	// WhereRequester, WhereOtherSet, WhereSubjectValue and WhereProposed each name
	// the requester, or a proposed value, in the part that reads the row alone.
	//
	// @resource
	WhereRequester struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(mine, value: ProjectID, where: OwnerID = subject)
		UserID    string       `spanner:"UserId"`
		OwnerID   string       `spanner:"OwnerId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// @resource
	WhereOtherSet struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(teamed, value: ProjectID, where: TeamID IN subject.teams)
		UserID    string       `spanner:"UserId"`
		TeamID    impulse.UUID `spanner:"TeamId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// @resource
	WhereSubjectValue struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(outranked, value: ProjectID, where: Rank <= subject.rank)
		UserID    string       `spanner:"UserId"`
		Rank      int64        `spanner:"Rank"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// @resource
	WhereProposed struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(proposed, value: ProjectID, where: new.Rank > 2)
		UserID    string       `spanner:"UserId"`
		Rank      int64        `spanner:"Rank"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// ValueWithParts writes a part on a subject value, which takes neither.
	//
	// @resource
	ValueWithParts struct {
		// @subjectValue(seatRank, value: Rank, where: Rank > 0)
		UserID string `spanner:"UserId"`
		Rank   int64  `spanner:"Rank"`
	}

	// ValueWithStringPart is ValueWithParts with a string that holds a parenthesis:
	// the part is read whole and refused as a part.
	//
	// @resource
	ValueWithStringPart struct {
		// @subjectValue(seatCode, value: Rank, where: Code = ':)')
		UserID string `spanner:"UserId"`
		Rank   int64  `spanner:"Rank"`
		Code   string `spanner:"Code"`
	}
)

// The tables the index warning reads, one struct per outcome. Each declares one set
// whose who: names two columns, so each column's lookup is judged on its own.
type (
	// IndexedBoth has an index for each column, the tenant first, then the column,
	// then the value: silent.
	//
	// @resource
	// @permissionScope(domain)
	IndexedBoth struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string `spanner:"TenantId"`
		// @subjectSet(indexedBoth, value: ProjectID, who: UserID = subject OR TeamID IN subject.teams)
		UserID    *string          `spanner:"UserId"`
		TeamID    impulse.NullUUID `spanner:"TeamId"`
		ProjectID impulse.UUID     `spanner:"ProjectId"`
	}

	// IndexedOne has the person's index and none for the team: one warning.
	//
	// @resource
	// @permissionScope(domain)
	IndexedOne struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string `spanner:"TenantId"`
		// @subjectSet(indexedOne, value: ProjectID, who: UserID = subject OR TeamID IN subject.teams)
		UserID    *string          `spanner:"UserId"`
		TeamID    impulse.NullUUID `spanner:"TeamId"`
		ProjectID impulse.UUID     `spanner:"ProjectId"`
	}

	// IndexedNeither has neither, and its indexes that come close do not serve: one
	// without the tenant, one null-filtered. Two warnings.
	//
	// @resource
	// @permissionScope(domain)
	IndexedNeither struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string `spanner:"TenantId"`
		// @subjectSet(indexedNeither, value: ProjectID, who: UserID = subject OR TeamID IN subject.teams)
		UserID    *string          `spanner:"UserId"`
		TeamID    impulse.NullUUID `spanner:"TeamId"`
		ProjectID impulse.UUID     `spanner:"ProjectId"`
	}

	// PlainUnindexed writes no who:, sits outside every tenant, and has no index on
	// its requester column: the one lookup of a set that writes neither part warns
	// too, with no tenant column in the index it wants.
	//
	// @resource
	PlainUnindexed struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectSet(plainUnindexed, value: ProjectID)
		UserID    string       `spanner:"UserId"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
	}

	// KeyedByBoth is keyed by the value and the requester, in that order, as a
	// membership table is: the primary key serves the lookup, whatever its order.
	//
	// @resource
	KeyedByBoth struct {
		ProjectID impulse.UUID `spanner:"ProjectId"`
		// @subjectSet(keyedByBoth, value: ProjectID)
		UserID string `spanner:"UserId"`
	}

	// SecondaryThenKey has an index on the requester column alone, and the value is
	// its primary key, which Spanner appends to every secondary index: silent.
	//
	// @resource
	SecondaryThenKey struct {
		ProjectID impulse.UUID `spanner:"ProjectId"`
		// @subjectSet(secondaryThenKey, value: ProjectID)
		UserID string `spanner:"UserId"`
		Note   string `spanner:"Note"`
	}
)
