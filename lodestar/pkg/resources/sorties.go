package resources

import (
	"time"

	"github.com/impulseframework/impulse"
)

type (
	// Sortie is one flight on a mission and the FIRST hop of the mission workflow's
	// membership chain: @stateRoot on the anchoring foreign key makes it a member, so the
	// uniform state binding is synthesized as a join path through MissionId and state =
	// 'underway' reads identically here and on the root. Its tenancy is its mission's
	// sector, declared as a join-path @domain through the same key.
	//
	// Sortie has its own UUID key and an ordinary foreign key rather than being
	// interleaved, so the second hop (SortieExpense) is a single-column foreign key.
	// PilotUserId names the pilot who flies it by the id of their account, the value
	// Pilots.UserId holds, so the pilot's record gives the name a page shows.
	//
	// LaunchedAt takes no NULL and nothing fills it, so a sortie's create must be given
	// it: the generated collection names it, with the mission, the ship and the pilot,
	// among the fields a create must carry (RequiredTags), and the role file's validation
	// warns for a role that may create a sortie and may not set one of them. The flight
	// lead's grant names all four (pkg/deploy pins that no role raises the warning, and
	// test/integration/required_on_create_test.go runs the lead's create).
	//
	// Demonstrates: @stateRoot, @domain.join-path, create-under-parent, warning.required-field.
	//
	// @resource
	// @permissionScope(domain)
	// @order(LaunchedAt desc)
	// @page(default: 10, max: 100)
	Sortie struct {
		ID impulse.UUID `spanner:"Id"`
		// @stateRoot(Mission)
		// @domain(via: SectorID)
		MissionID   impulse.UUID `spanner:"MissionId"`
		ShipID      impulse.UUID `spanner:"ShipId"`
		PilotUserID string       `spanner:"PilotUserId"`
		LaunchedAt  time.Time    `spanner:"LaunchedAt"`
		ReturnedAt  *time.Time   `spanner:"ReturnedAt"`
		Debrief     *string      `spanner:"Debrief"`
	}
)
