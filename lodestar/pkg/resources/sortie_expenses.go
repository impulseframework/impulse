package resources

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/shopspring/decimal"
)

type (
	// SortieExpense is the SECOND hop: fuel, supplies, and tow gear booked against a
	// sortie. @stateRoot(Mission) on SortieId declares its immediate hop, and the chain
	// resolver composes Sortie's hop to reach the root, so the Quartermaster's state =
	// 'underway' grant evaluates two hops deep, through Sortie to Mission. Tenancy runs
	// the same two hops (Sortie, Mission.SectorId).
	//
	// Memo is the purser's line note, renamed from Note: the Go field and the wire name
	// moved, the column stayed, and @formerly keeps the old name answered beside the new
	// one for consoles built before the rename (a body, columns, sort or filter naming
	// note is read as memo, a body naming both is refused, and every row carries both
	// keys), while the role file and the console's code name memo alone and the permission
	// digest mirrors memo's entry under note. The annotation leaves, and the old name with
	// it, once the console outlet's oldest answered release (cmd/generate) passes the
	// release that renamed it.
	//
	// A penalty stays on the record: @validateDeleteType names the rule
	// (SortieExpenseDeleteRule) every delete of an expense asks first, whatever role the
	// person holds.
	//
	// Demonstrates: @stateRoot.two-hop, @domain.join-path, create-under-parent, api.formerly, @validateDeleteType.
	//
	// @resource
	// @permissionScope(domain)
	// @order(Amount desc)
	// @page(default: 10, max: 100)
	// @validateDeleteType(SortieExpenseDeleteRule)
	SortieExpense struct {
		ID impulse.UUID `spanner:"Id"`
		// @stateRoot(Mission)
		// @domain(via: MissionID.SectorID)
		SortieID impulse.UUID `spanner:"SortieId"`
		Category string       `spanner:"Category"`
		// @attribute(amount)
		Amount decimal.Decimal `spanner:"Amount"`
		// @formerly(Note)
		Memo *string `spanner:"Note"`
	}
)

// PenaltyCategory is the category of an expense the fleet was fined: the one kind of
// expense that is never deleted (SortieExpenseDeleteRule).
const PenaltyCategory = "penalty"

// SortieExpenseDeleteRule is wired in by the @validateDeleteType annotation; the
// generated delete patch calls Validate inside the delete's transaction, once the
// person's grants have passed and before the row is buffered.
//
// Demonstrates: @validateDeleteType.
type SortieExpenseDeleteRule struct{}

// Validate refuses the delete of a penalty: a fine stays on the sortie's record whoever
// asks, which is why it is a rule in code and not a condition on a grant. Conditions add
// up across a person's grants, so one unconditional Delete would get around it. Any
// other expense passes, and so does a row that is not there, which the delete itself
// answers.
func (r *SortieExpenseDeleteRule) Validate(ctx context.Context, txn resource.ReadWriteTransaction, p *SortieExpenseDeletePatch) error {
	expense, err := NewSortieExpenseQuery().
		AddColumns(NewSortieExpenseColumns().Category()).
		SetID(p.ID()).
		Read(ctx, txn)
	if err != nil {
		if httpio.HasNotFound(err) {
			return nil
		}

		return errors.Wrap(err, "SortieExpenseQuery.Read()")
	}
	if expense.Data.Category == PenaltyCategory {
		return httpio.NewBadRequestMessage("A penalty stays on the sortie's record and can't be deleted.")
	}

	return nil
}
