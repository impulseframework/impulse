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
	//     are in (GroupID IN subject.crewGroups, the set the library's group members
	//     table declares). The second and third tests are a set that follows another set:
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
	// picker of the sector's people. A group that a pass names is not deleted while the
	// pass stands: the foreign key to CrewGroups refuses it. A pass that names two holders,
	// or none, is refused: by the create validator in words the person filling the form can
	// act on, and by the table's holder check for every other writer. An empty userId
	// names nobody and is not a holder, in the validator and in the table's check alike.
	//
	// A pass keeps a change history (Config): who was given a hangar, and when the pass
	// was written and deleted, is what an audit of the hangar reads. A pass's record keeps
	// its holder's name beside the id, as it was that day: the account's username for a
	// person and the group's name for a group, each read from the crew auth's own table
	// when the pass is written or deleted, and never from the two lists the pickers read.
	// The history so still names a holder whose account or group has since been renamed
	// or deleted (test/integration/change_history_test.go).
	//
	// A pass's holder never changes. UserID, SquadronID and GroupID are immutable: an
	// update that names any of the three is refused for every role with the framework's
	// own answer (400, "json field groupId is immutable"; for two of them, one sentence
	// that names both in the order of their names), before the request's grants are read
	// and before any row is, so the refusal is the same for a pass of the sector, a pass
	// of another sector and an id that is no pass. The collection takes Update off
	// the three columns and a role file that grants it is refused when it is read. A pass
	// that should be somebody else's is deleted and written again; its level and its
	// expiry are what an update may change.
	//
	// Each set keeps its own tenant filter. A pass carries its sector on the row, so the
	// passes read are the request's sector's; subject.squadrons is filtered to the same
	// sector through its own two-hop path, so a squadron the requester flies with in
	// another sector opens nothing here; and subject.crewGroups holds the groups kept in
	// the request's sector alone, so a pass that named another sector's group, or a group
	// kept for the whole fleet, would open nothing. No such pass is written: the picker offers
	// neither, and the library refuses a create that names either, whoever writes it (the
	// API, the bootstrap, a job), with 400 and "groupId names a group that is not one of
	// this sector's own. Choose a group kept in this sector: a group kept in another
	// sector, or for the whole application, cannot be named here." The rule is the
	// library's for every column a set follows to an auth's groups (the generated
	// GroupColumns), so nothing in this file states it.
	//
	// A pass names its hangar by its sector and the hangar's id: the foreign key holds
	// both, so the database refuses a pass that names another sector's hangar, when it is
	// created and when an update moves it, in the sentence of a pass that names no hangar.
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
		// @subjectSet(hangarsOpen, value: HangarID, auth: crew,
		//     who:   UserID = subject OR SquadronID IN subject.squadrons OR GroupID IN subject.crewGroups,
		//     where: LevelID IN ('visit', 'run') AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @subjectSet(hangarsRun, value: HangarID, auth: crew,
		//     who:   UserID = subject OR SquadronID IN subject.squadrons OR GroupID IN subject.crewGroups,
		//     where: LevelID = 'run' AND (ExpiresAt IS NULL OR ExpiresAt > now))
		// @enumerate(CrewSectorUsers)
		UserID     *string          `spanner:"UserId"     conditions:"immutable"`
		SquadronID impulse.NullUUID `spanner:"SquadronId" conditions:"immutable"`
		// @enumerate(CrewSectorGroups)
		GroupID   impulse.NullUUID `spanner:"GroupId"   conditions:"immutable"`
		LevelID   string           `spanner:"LevelId"`
		ExpiresAt *time.Time       `spanner:"ExpiresAt"`
	}
)

// Config enables change tracking: HangarPass mutations write DataChangeEvents rows in the
// same transaction, and a pass's create and its delete each keep the name of the holder the
// pass names.
func (HangarPass) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}

// HangarPassCreateValidator is wired in by the @validateCreateType annotation; the
// generated create patch calls Validate inside the mutation transaction.
type HangarPassCreateValidator struct{}

// Validate refuses a pass that names no holder, or more than one, and says which of the
// three to fill. The table's holder check (CK_HangarPasses_Holder) holds the same rule
// for a writer that does not come through the create patch. No update needs the rule: the
// three holder columns are immutable, so a pass keeps the holder it was written with.
func (v *HangarPassCreateValidator) Validate(_ context.Context, _ resource.ReadWriteTransaction, p *HangarPassCreatePatch) error {
	return oneHolder(p.UserID(), p.SquadronID(), p.GroupID())
}

// oneHolder refuses a pass that names no holder, or more than one, in words whoever
// fills the form can act on. An empty person names nobody, so it is not a holder: a
// pass in the name of "" would open nothing to anyone.
func oneHolder(person *string, squadron, group impulse.NullUUID) error {
	holders := 0
	if person != nil && *person != "" {
		holders++
	}
	if squadron.Valid {
		holders++
	}
	if group.Valid {
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
