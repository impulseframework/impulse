package resources

import (
	"time"

	"github.com/impulseframework/impulse"
)

type (
	// HangarPass says who a hangar is open to. A pass names a person or a squadron, never
	// both, at a level (visit, or run), and may expire. It is the subject-set anchor that
	// writes both parts of a set's definition, and one table that yields two sets:
	//
	//   - who: says which passes are the requester's. A pass is theirs when it names them
	//     (UserID = subject) or names a squadron they fly with (SquadronID IN
	//     subject.squadrons, the set SquadronMembership declares). The second test is a set
	//     that follows another set: nothing in the library knows what a squadron is, and
	//     the pass reaches a person through any table that declares a set of its own.
	//   - where: says which of those passes count. subject.hangarsRun keeps the passes at
	//     the run level, subject.hangarsOpen the passes at either level, and both drop a
	//     pass that has expired (ExpiresAt IS NULL OR ExpiresAt > now), so a pass ends at
	//     its hour with nobody deleting it. LevelID is enumerated (PassLevels), so a level
	//     the table does not hold is refused at generation.
	//
	// The annotations sit on UserID, one of the two columns the who: names; generation
	// refuses any other column, naming these two. The role file does not change shape: the
	// Armorer's grants read hangar IN subject.hangarsOpen and hangar IN subject.hangarsRun
	// and name nothing of this table.
	//
	// Each set keeps its own tenant filter. A pass carries its sector on the row, so the
	// passes read are the request's sector's; subject.squadrons is filtered to the same
	// sector through its own two-hop path, so a squadron the requester flies with in
	// another sector opens nothing here.
	//
	// A check of either set finds the requester's passes by UserId and by SquadronId, so
	// the table carries an index for each (the sector, the column, the hangar); without
	// one the generator warns, naming the CREATE INDEX it wants. HangarPassesBySectorIdHangarId
	// serves the list's own order.
	//
	// Demonstrates: @subjectSet.who, @subjectSet.where, @subjectSet.on-set.
	//
	// @resource
	// @permissionScope(domain)
	// @order(HangarID asc)
	// @page(default: 25, max: 200)
	HangarPass struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		HangarID impulse.UUID `spanner:"HangarId"`
		// @subjectSet(hangarsOpen, value: HangarID,
		//     who:   UserID = subject OR SquadronID IN subject.squadrons,
		//     where: LevelID IN ('visit', 'run') AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @subjectSet(hangarsRun, value: HangarID,
		//     who:   UserID = subject OR SquadronID IN subject.squadrons,
		//     where: LevelID = 'run' AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @enumerate(CrewSectorUsers)
		UserID     *string          `spanner:"UserId"`
		SquadronID impulse.NullUUID `spanner:"SquadronId"`
		LevelID    string           `spanner:"LevelId"`
		ExpiresAt  *time.Time       `spanner:"ExpiresAt"`
	}
)
