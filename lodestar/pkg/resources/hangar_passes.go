package resources

import (
	"context"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

type (
	// HangarPass says who a hangar is open to. A pass names one holder, a person, a
	// squadron or a group, at a level (visit, or run), and may expire. It is the
	// subject-set anchor that writes both parts of a set's definition, and one table that
	// yields two sets:
	//
	//   - who: says which passes are the requester's. A pass is theirs when it names them
	//     (UserID = subject), names a squadron they fly with (SquadronID IN
	//     subject.squadrons, the set SquadronMembership declares), or names a group they
	//     are in (GroupID IN subject.groups, the set the library's group members table
	//     declares). The second and third tests are a set that follows another set:
	//     nothing in the library knows what a squadron is, nothing here knows how a group
	//     keeps its people, and the pass reaches a person through any table that declares
	//     a set of its own.
	//   - where: says which of those passes count. subject.hangarsRun keeps the passes at
	//     the run level, subject.hangarsOpen the passes at either level, and both drop a
	//     pass that has expired (ExpiresAt IS NULL OR ExpiresAt > now), so a pass ends at
	//     its hour with nobody deleting it. LevelID is enumerated (PassLevels), so a level
	//     the table does not hold is refused at generation.
	//
	// The annotations sit on UserID, one of the three columns the who: names; generation
	// refuses any other column, naming these three. The role file does not change shape:
	// the Armorer's grants read hangar IN subject.hangarsOpen and hangar IN
	// subject.hangarsRun and name nothing of this table.
	//
	// The group is one of the crew's groups, kept in the permission store and managed on
	// the sector's Groups page. Everyone in the group holds the pass while their place in
	// it counts: a place outside its period (StartsAt, EndsAt) gives nothing, and a person
	// taken out of the group loses the hangar at their next request. Whoever writes a pass
	// picks the group from the sector's own (@enumerate of CrewSectorGroups), beside the
	// picker of the sector's people. A group that is deleted takes its passes with it (the
	// foreign key to CrewGroups, ON DELETE CASCADE). A pass that names two holders, or
	// none, is refused: by the create validator in words the person filling the form can
	// act on, and by the table's holder check for every other writer.
	//
	// Each set keeps its own tenant filter. A pass carries its sector on the row, so the
	// passes read are the request's sector's; subject.squadrons is filtered to the same
	// sector through its own two-hop path, so a squadron the requester flies with in
	// another sector opens nothing here; and subject.groups holds the groups kept in the
	// request's sector alone, so a pass that names another sector's group, or a group kept
	// for the whole fleet, is written and opens nothing: the picker offers neither, and
	// the set counts neither in a sector's request.
	//
	// A check of either set finds the requester's passes by UserId, by SquadronId and by
	// GroupId, so the table carries an index for each (the sector, the column, the
	// hangar); without one the generator warns, naming the CREATE INDEX it wants.
	// HangarPassesBySectorIdHangarId serves the list's own order.
	//
	// Demonstrates: @subjectSet.who, @subjectSet.where, @subjectSet.on-set, @subjectSet.on-groups.
	//
	// @resource
	// @permissionScope(domain)
	// @order(HangarID asc)
	// @page(default: 25, max: 200)
	// @validateCreateType(HangarPassCreateValidator)
	HangarPass struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		HangarID impulse.UUID `spanner:"HangarId"`
		// @subjectSet(hangarsOpen, value: HangarID,
		//     who:   UserID = subject OR SquadronID IN subject.squadrons OR GroupID IN subject.groups,
		//     where: LevelID IN ('visit', 'run') AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @subjectSet(hangarsRun, value: HangarID,
		//     who:   UserID = subject OR SquadronID IN subject.squadrons OR GroupID IN subject.groups,
		//     where: LevelID = 'run' AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @enumerate(CrewSectorUsers)
		UserID     *string          `spanner:"UserId"`
		SquadronID impulse.NullUUID `spanner:"SquadronId"`
		// @enumerate(CrewSectorGroups)
		GroupID   impulse.NullUUID `spanner:"GroupId"`
		LevelID   string           `spanner:"LevelId"`
		ExpiresAt *time.Time       `spanner:"ExpiresAt"`
	}
)

// HangarPassCreateValidator is wired in by the @validateCreateType annotation; the
// generated create patch calls Validate inside the mutation transaction.
type HangarPassCreateValidator struct{}

// Validate refuses a pass that names no holder, or more than one, and says which of the
// three to fill. The table's holder check (CK_HangarPasses_Holder) holds the same rule
// for a writer that does not come through the create patch.
func (v *HangarPassCreateValidator) Validate(_ context.Context, _ resource.ReadWriteTransaction, p *HangarPassCreatePatch) error {
	holders := 0
	if p.UserID() != nil {
		holders++
	}
	if p.SquadronID().Valid {
		holders++
	}
	if p.GroupID().Valid {
		holders++
	}
	switch {
	case holders == 0:
		return httpio.NewBadRequestMessage("a pass needs a holder: choose a person, a squadron or a group")
	case holders > 1:
		return httpio.NewBadRequestMessage("a pass has one holder: choose a person, a squadron or a group, and leave the other two empty")
	}

	return nil
}
