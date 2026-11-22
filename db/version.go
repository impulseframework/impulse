package db

import "github.com/impulseframework/impulse/db/internal/runner"

// Version is what a migrations table says about a database: no version yet, clean at a
// version, or dirty at one with the progress the runner recorded. Its String prints one
// phrase, such as "version 41, dirty: 2 statements applied".
type Version = runner.Version

// DirtyError is returned when a Spanner migration file stops before its end: the version
// is dirty with the applied part recorded, and the next run continues from the failed
// statement once the cause is fixed. Test for it with errors.As.
type DirtyError = runner.DirtyError

// ErrAnotherRun is returned by a Spanner migration run that finds another run, or a forced
// version, changed the version row under it. The run stops before applying anything more,
// and running it again once the other has finished is safe. Test for it with errors.Is.
var ErrAnotherRun = runner.ErrAnotherRun
