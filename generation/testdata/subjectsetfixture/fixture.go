// Package subjectsetfixture provides parsed-struct fixtures for the parts a
// @subjectSet's definition may write, who:, where: and domain:: the shapes that
// resolve, one struct per refusal, the tables the index warning reads, and the tables
// a domain: ends on.
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

	// Pool is a group of people kept in one tenant or in none: the table a pool's
	// members reach their tenant through. Size is a column no tenant's key is held in.
	//
	// @resource
	Pool struct {
		ID       impulse.UUID `spanner:"Id"`
		TenantID string       `spanner:"TenantId"`
		Size     int64        `spanner:"Size"`
	}

	// PoolMember names the people of a pool on a table that is no tenant's. Its set
	// says where a row's tenant is found (domain:), so in a request with a tenant the
	// set holds that tenant's pools alone.
	//
	// @resource
	PoolMember struct {
		PoolID impulse.UUID `spanner:"PoolId"`
		// @subjectSet(pools, value: PoolID, domain: PoolID.TenantID)
		UserID string `spanner:"UserId"`
	}

	// DomainOnTenantTable writes a domain: on a table that carries its tenant itself.
	//
	// @resource
	// @permissionScope(domain)
	DomainOnTenantTable struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID impulse.UUID `spanner:"TenantId"`
		PoolID   impulse.UUID `spanner:"PoolId"`
		// @subjectSet(tenantPools, value: PoolID, domain: PoolID.TenantID)
		UserID string `spanner:"UserId"`
	}

	// Keyholder names the people who hold a key, on a table that is no tenant's whose
	// rows each carry the tenant the key is kept in. Its set's domain: names that
	// column of the row itself and goes nowhere from it.
	//
	// @resource
	Keyholder struct {
		ID       impulse.UUID `spanner:"Id"`
		TenantID string       `spanner:"TenantId"`
		KeyID    impulse.UUID `spanner:"KeyId"`
		// @subjectSet(keys, value: KeyID, domain: TenantID)
		UserID string `spanner:"UserId"`
	}

	// DomainOwnNumber names a column of its own row that is a number, which no
	// tenant's key is.
	//
	// @resource
	DomainOwnNumber struct {
		ID    impulse.UUID `spanner:"Id"`
		KeyID impulse.UUID `spanner:"KeyId"`
		Floor int64        `spanner:"Floor"`
		// @subjectSet(byFloor, value: KeyID, domain: Floor)
		UserID string `spanner:"UserId"`
	}

	// DomainUnknownField starts its path at a field the row does not have.
	//
	// @resource
	DomainUnknownField struct {
		ID     impulse.UUID `spanner:"Id"`
		PoolID impulse.UUID `spanner:"PoolId"`
		// @subjectSet(strayPath, value: PoolID, domain: CrewID.TenantID)
		UserID string `spanner:"UserId"`
	}

	// DomainNotForeignKey starts its path at a column that is no foreign key.
	//
	// @resource
	DomainNotForeignKey struct {
		ID     impulse.UUID `spanner:"Id"`
		PoolID impulse.UUID `spanner:"PoolId"`
		Note   string       `spanner:"Note"`
		// @subjectSet(looseEnd, value: PoolID, domain: Note.TenantID)
		UserID string `spanner:"UserId"`
	}

	// DomainNumber ends its path at a number, which no tenant's key is.
	//
	// @resource
	DomainNumber struct {
		ID     impulse.UUID `spanner:"Id"`
		PoolID impulse.UUID `spanner:"PoolId"`
		// @subjectSet(sized, value: PoolID, domain: PoolID.Size)
		UserID string `spanner:"UserId"`
	}

	// DomainOnValue writes a domain: on a subject value.
	//
	// @resource
	DomainOnValue struct {
		// @subjectValue(homePool, value: PoolID, domain: PoolID.TenantID)
		UserID string       `spanner:"UserId"`
		PoolID impulse.UUID `spanner:"PoolId"`
	}
)

// The tables a domain: ends on that say where their tenant is, and the sets that end
// on them: one that ends at the tenant's column and one that ends at another text
// column of the same table, for each way a table says it.
type (
	// Depot is a tenant's own table: its rows carry their tenant in TenantID, the
	// column its @domain sits on. Region is text, and no tenant's key.
	//
	// @resource
	// @permissionScope(domain)
	Depot struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string `spanner:"TenantId"`
		Region   string `spanner:"Region"`
	}

	// DepotKeeper names the people of a depot on a table that is no tenant's, and
	// ends its domain: at the depot's own tenant column.
	//
	// @resource
	DepotKeeper struct {
		DepotID impulse.UUID `spanner:"DepotId"`
		// @subjectSet(depots, value: DepotID, domain: DepotID.TenantID)
		UserID string `spanner:"UserId"`
	}

	// DomainOtherColumn ends its domain: on the depot at a text column that is not
	// the one the depot's tenant is kept in.
	//
	// @resource
	DomainOtherColumn struct {
		ID      impulse.UUID `spanner:"Id"`
		DepotID impulse.UUID `spanner:"DepotId"`
		// @subjectSet(byRegion, value: DepotID, domain: DepotID.Region)
		UserID string `spanner:"UserId"`
	}

	// Rack is a tenant's table that holds no tenant's key itself: its rows find their
	// tenant through their depot.
	//
	// @resource
	// @permissionScope(domain)
	Rack struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: TenantID)
		DepotID impulse.UUID `spanner:"DepotId"`
		Label   string       `spanner:"Label"`
	}

	// RackKeeper names the people of a rack, and continues its domain: through the
	// rack to the depot's tenant column.
	//
	// @resource
	RackKeeper struct {
		RackID impulse.UUID `spanner:"RackId"`
		// @subjectSet(racks, value: RackID, domain: RackID.DepotID.TenantID)
		UserID string `spanner:"UserId"`
	}

	// DomainShortOfTenant ends its domain: on the rack, which holds no tenant's key.
	//
	// @resource
	DomainShortOfTenant struct {
		ID     impulse.UUID `spanner:"Id"`
		RackID impulse.UUID `spanner:"RackId"`
		// @subjectSet(byLabel, value: RackID, domain: RackID.Label)
		UserID string `spanner:"UserId"`
	}

	// Realm is the tenant record: its rows are the tenants, and a tenant's key is its
	// ID.
	//
	// @resource
	// @tenant
	Realm struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// RealmKeeper ends its domain: at the tenant record's key.
	//
	// @resource
	RealmKeeper struct {
		ID      impulse.UUID `spanner:"Id"`
		RealmID impulse.UUID `spanner:"RealmId"`
		// @subjectSet(realms, value: RealmID, domain: RealmID.ID)
		UserID string `spanner:"UserId"`
	}

	// DomainTenantName ends its domain: at the tenant record's name.
	//
	// @resource
	DomainTenantName struct {
		ID      impulse.UUID `spanner:"Id"`
		RealmID impulse.UUID `spanner:"RealmId"`
		// @subjectSet(byRealmName, value: RealmID, domain: RealmID.Name)
		UserID string `spanner:"UserId"`
	}

	// StaffGroup stands for the permission store's table of groups under an auth
	// named Staff: a group is kept in the tenant DomainID names, or in none.
	//
	// @resource
	StaffGroup struct {
		ID       impulse.UUID `spanner:"Id"`
		DomainID string       `spanner:"DomainId"`
		Name     string       `spanner:"Name"`
	}

	// StaffGroupMember ends its domain: at the column the store keeps a group's
	// tenant in, as the generated declaration of a group's members does.
	//
	// @resource
	StaffGroupMember struct {
		GroupID impulse.UUID `spanner:"GroupId"`
		// @subjectSet(staffGroups, value: GroupID, domain: GroupID.DomainID)
		UserID string `spanner:"UserId"`
	}

	// DomainGroupName ends its domain: at a group's name.
	//
	// @resource
	DomainGroupName struct {
		ID      impulse.UUID `spanner:"Id"`
		GroupID impulse.UUID `spanner:"GroupId"`
		// @subjectSet(byGroupName, value: GroupID, domain: GroupID.Name)
		UserID string `spanner:"UserId"`
	}

	// DepotPass opens a depot to a person or to one of the staff's groups, on a
	// tenant's table that carries its tenant: two sets follow the group to its
	// people, and the column that names it is declared once.
	//
	// @resource
	// @permissionScope(domain)
	DepotPass struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string       `spanner:"TenantId"`
		DepotID  impulse.UUID `spanner:"DepotId"`
		// @subjectSet(depotsOpen, value: DepotID, who: UserID = subject OR GroupID IN subject.staffGroups)
		// @subjectSet(depotsRun, value: DepotID, who: UserID = subject OR GroupID IN subject.staffGroups)
		UserID  *string          `spanner:"UserId"`
		GroupID impulse.NullUUID `spanner:"GroupId"`
	}

	// DepotVisit opens a depot to one of the staff's groups on a row that finds its
	// tenant through its depot.
	//
	// @resource
	// @permissionScope(domain)
	DepotVisit struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: TenantID)
		DepotID impulse.UUID `spanner:"DepotId"`
		// @subjectSet(depotsVisited, value: DepotID, who: UserID = subject OR GroupID IN subject.staffGroups)
		UserID  *string          `spanner:"UserId"`
		GroupID impulse.NullUUID `spanner:"GroupId"`
	}

	// OpenPass names one of the staff's groups on a table that is no tenant's.
	//
	// @resource
	OpenPass struct {
		ID      impulse.UUID `spanner:"Id"`
		DepotID impulse.UUID `spanner:"DepotId"`
		// @subjectSet(depotsShown, value: DepotID, who: UserID = subject OR GroupID IN subject.staffGroups)
		UserID  *string          `spanner:"UserId"`
		GroupID impulse.NullUUID `spanner:"GroupId"`
	}

	// DomainOwnTenantName is a tenant record that declares the set of the tenants a
	// person owns, and names a tenant's name where its key belongs: a domain: of one
	// name ends on the set's own table.
	//
	// @resource
	// @tenant
	DomainOwnTenantName struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
		// @subjectSet(ownedRealms, value: ID, domain: Name)
		OwnerID string `spanner:"OwnerId"`
	}
)
