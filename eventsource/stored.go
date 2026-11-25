package eventsource

// Stored is who made a change as a row keeps it: the text of each of the nine columns
// (ColumnNames), and the empty text where the row sets none. A read fills one from a row
// to write the sentence. Nothing makes an EventSource of it, so no text a program holds
// becomes who made a change.
type Stored struct {
	Auth              string
	ActorUserID       string
	ActorUsername     string
	PrincipalUserID   string
	PrincipalUsername string
	PrincipalRole     string
	SessionID         string
	Process           string
	RPC               string
}

// String writes who made the change for a person to read, in the words EventSource.String
// writes for the value the row was written from. It names people by username and holds
// no id.
func (s Stored) String() string {
	w := words{
		actorUsername: s.ActorUsername, principalUsername: s.PrincipalUsername, principalRole: s.PrincipalRole,
		process: s.Process, rpc: s.RPC,
	}

	return w.sentence()
}
