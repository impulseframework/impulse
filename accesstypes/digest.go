package accesstypes

// DigestState is one PermissionDigest entry's answer. Granted: at least one
// unconditional grant covers the target. Conditional on time: only
// conditional grants cover it, and every one of their conditions reads the
// time of the request alone (condition.TimeOnly), so a check settles the
// target whole from the request, granted while one of the conditions holds
// and denied while none does. Conditional: only conditional grants cover it,
// and at least one of their conditions reads more than the time (the row, a
// fact about the caller, a request field), so a check may leave the target
// to be judged row by row. There is no denied state: a denied target is
// absent from the digest entirely, so a consumer that misses a key fails
// closed.
type DigestState string

// The digest states. There is deliberately no denied constant: denial is
// expressed by absence.
const (
	DigestGranted           DigestState = "granted"
	DigestConditional       DigestState = "conditional"
	DigestConditionalOnTime DigestState = "conditionalOnTime"
)

// PermissionDigest is one user's structural grant enumeration for one scope:
// every resource and field the user's grants reach, each mapping permission
// to its DigestState. It is the payload of the frontend's per-scope
// permission digest — advisory UI material, never an enforcement surface.
//
// The digest is structural: it reports grant structure and folds nothing —
// no environment instant, no row data — so a payload is stable for the life
// of a policy snapshot and caches cleanly per scope. Conditional on time is
// structure too, read from the conditions' text and never from a clock: the
// digest does not say whether such a condition holds now. Absence means denied,
// and the payload stays proportional to what the user's grants reach, never to
// the application's whole resource collection: a grant on all of a resource's
// fields is listed field by field, over the fields the release declares for
// that resource. Field targets appear under their dotted resource name
// ("employees.name"), exactly as grants store them.
type PermissionDigest map[Resource]map[Permission]DigestState
