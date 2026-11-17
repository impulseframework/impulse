package generation

import "fmt"

// The period of an assignment in the generated management. A person's role, a person's
// place in a group and a group's role may each carry a start and an end
// (accesstypes.Period), kept in the two nullable columns StartsAt and EndsAt of the
// permission store's memberships, group members and group roles tables. The generated
// management reads and writes them in four places:
//
//   - the declarations over the three tables hold the two columns, and the members'
//     subject set of a person's groups counts a place inside its period alone, with the
//     where: part every subject set may write;
//   - the methods that give a role, a place in a group and a group's role take the two
//     times, each optional, and giving again gives the period of the new request;
//   - the lists of people hold who holds something now, and the lists of roles and of
//     places carry the period and leave out a row that has ended
//     (identitySourceData.Counting, identitySourceData.Standing);
//   - the store reads the periods and removes the ended rows of one person, which the
//     client's SignedIn hook does at a sign-in (identityPeriodStore).
//
// A list reads the present from the database's clock, as the list of a tenant's
// invitations reads whether one has expired; the permission engine reads it from the
// clock a check is made with.

// Counting is the SQL test that a row counts now: its period has begun and has not
// ended. alias is the name the row of a memberships, group members or group roles table
// is read under. The lists of people apply it: a person is one of a tenant's people by
// what they hold at the moment.
func (d *identitySourceData) Counting(alias string) string {
	return "(" + alias + ".StartsAt IS NULL OR " + alias + ".StartsAt <= CURRENT_TIMESTAMP()) AND " + d.Standing(alias)
}

// Standing is the SQL test that a row has not ended, whether or not it has begun. alias
// is the name the row is read under. The lists of roles and of places apply it: a row
// that has ended is nothing anybody holds and waits only for its person's next sign-in,
// and one that has not begun is listed with its period, where it is changed or taken
// away.
func (d *identitySourceData) Standing(alias string) string {
	return "(" + alias + ".EndsAt IS NULL OR " + alias + ".EndsAt > CURRENT_TIMESTAMP())"
}

// ThroughGroupPeriod is the two columns of the period a person holds a role for through
// a group, as the last columns of a select: where the period the group holds the role
// for (the row read under role) and the period of the person's place in the group (the
// row read under place) meet. The later of two starts and the earlier of two ends, and
// the one there is where the other is empty.
func (d *identitySourceData) ThroughGroupPeriod(role, place string) string {
	return fmt.Sprintf("  COALESCE(GREATEST(%[1]s.StartsAt, %[2]s.StartsAt), %[1]s.StartsAt, %[2]s.StartsAt) AS StartsAt,\n"+
		"  COALESCE(LEAST(%[1]s.EndsAt, %[2]s.EndsAt), %[1]s.EndsAt, %[2]s.EndsAt) AS EndsAt", role, place)
}

// ThroughGroupMeets is the SQL tests, each led by AND, that a person's place in a group
// (the row read under place) has not ended and that its period meets the one the group
// holds the role for (the row read under role): a role whose two periods never meet is
// nothing the person holds or will hold.
func (d *identitySourceData) ThroughGroupMeets(role, place string) string {
	return fmt.Sprintf("  AND %[3]s\n"+
		"  AND (%[1]s.EndsAt IS NULL OR %[2]s.StartsAt IS NULL OR %[2]s.StartsAt < %[1]s.EndsAt)\n"+
		"  AND (%[2]s.EndsAt IS NULL OR %[1]s.StartsAt IS NULL OR %[1]s.StartsAt < %[2]s.EndsAt)", role, place, d.Standing(place))
}

// GroupSetWhere is the where: part of the subject set of a person's groups: a place
// counts inside its period alone, in the language every set's where: is written in.
func (d *identitySourceData) GroupSetWhere() string {
	return "(StartsAt IS NULL OR StartsAt <= now) AND (EndsAt IS NULL OR EndsAt > now)"
}
