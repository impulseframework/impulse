package generation

import "fmt"

// Finding is an advisory finding the audit pass raises about a shape the framework
// handles under a stated limitation: a normal generation never prints it, since it
// would fire on every run of every application that carries the shape, and a runner
// prints it on demand after a successful run (Generator.Audit, the -audit flag of an
// application's generate program). No shape is reported today.
type Finding interface {
	fmt.Stringer
	finding()
}

// auditFindings is the audit pass over the extracted resources. No shape is reported
// today, so it answers none.
func (c *client) auditFindings(_ []*resourceInfo) []Finding {
	return nil
}
