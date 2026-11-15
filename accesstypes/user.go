package accesstypes

// User represents a user in the authorization system: the key a membership is held
// under, a session's principal is, and a row condition's subject binds to. The library
// reads nothing into it. An application whose people are accounts of an Auth names each
// by the id of their account, as text (sessioninfo.SessionData.User, identity.User): a
// username can change, and two auths over one database can each hold the same one,
// where an account's id does neither.
type User string
