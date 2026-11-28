package resources

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
	"github.com/shopspring/decimal"
)

type (
	// Pilot is the personnel registry, the crew roster the console lists, and the FIRST
	// subject-value anchor, carrying TWO bindings on one unique-indexed user column:
	// subject.clearance is the pilot's hazard clearance (hazard <= subject.clearance) and
	// subject.feeLimit the fee they may book without sign-off (new.fee <=
	// subject.feeLimit). The database enforces one row per user; a user with no Pilot row
	// fails such conditions closed. UserId holds the id of the pilot's crew account, which
	// is what the permission store names a person by and what a condition's subject binds
	// to; the record's own DisplayName is what a page shows. Whoever adds a pilot picks the
	// account from the crew's accounts by its username (@enumerate), since nobody types an
	// id.
	//
	// A pilot keeps a change history (Config): who raised a clearance or a fee limit, and
	// who moved a pilot onto another account, is what an audit of the registry reads. A
	// pilot's record keeps the username of the account beside its id, as it was when the
	// change was made, read from the crew auth's accounts, so the history still names an
	// account that has since been renamed or deleted
	// (test/integration/change_history_test.go).
	//
	// The roster declares a default page but no maximum, so limit=all is legal on it and
	// the client's all() has a home.
	//
	// DisplayName is indexed (PilotsByDisplayName), so a list that shows a pilot by name
	// is filtered by that name alone.
	//
	// Demonstrates: @subjectValue, @subjectValue.two-per-anchor, paging.limit-all.
	//
	// @resource
	// @order(DisplayName asc)
	// @page(default: 10)
	Pilot struct {
		ID impulse.UUID `spanner:"Id"`
		// @subjectValue(clearance, value: Clearance)
		// @subjectValue(feeLimit, value: FeeLimit)
		// @enumerate(CrewSessionUsers)
		UserID string `spanner:"UserId"`
		// @name
		DisplayName string          `spanner:"DisplayName"`
		Clearance   int64           `spanner:"Clearance"`
		FeeLimit    decimal.Decimal `spanner:"FeeLimit"`
	}
)

// Config enables change tracking: Pilot mutations write DataChangeEvents rows in the
// same transaction, and a change of a pilot's account keeps the username of each
// account.
func (Pilot) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
