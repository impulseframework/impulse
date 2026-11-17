package accesstypes

// Group names a group of people in the authorization system: the id the permission
// store gave the group when it was created, as text. A group's name is what people read
// and may be the name of another group kept somewhere else; its id names it everywhere,
// so a membership, a role the group holds and a row of an application's own that names a
// group all carry the id.
type Group string
