package resources

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/shopspring/decimal"
)

type (
	// Refit is the second workflow root: a ship's passage through the hangar bays. It
	// carries no tenant column of its own: tenancy runs two hops through Ship and Hangar
	// (@domain(via: HangarID.SectorID) on ShipId), so a stateful root demonstrates
	// join-path tenancy on every one of its six transitions.
	//
	// InspectedAt is domain data written explicitly by InspectShip (output_only keeps
	// clients out); the Engineer's Update grant carries inspectedAt IS NOT NULL, so an
	// estimate is only ever set after inspection. OpenedByUserID is server-stamped with the id
	// of the opener's account, and OpenedByName with their username as it was then, which
	// is what a page shows. The update
	// path runs both a defaults type and a validator type. Change tracking is on, feeding
	// the refit's history.
	//
	// A ship is in one bay at a time: the create path's validator refuses to open a refit
	// on a ship that has one still open.
	//
	// A refit's delete is declared (@deleteCascade): its tasks go with it, each through its
	// own delete, so a task's photo is released with it.
	//
	// Demonstrates: @state, @domain.join-path, @transition.join-path-root, transition-owned-timestamp, @defaultsUpdateType, @validateUpdateType, change-tracking, condition.day-of-week, condition.time-of-day, condition.local-zone.
	//
	// @resource
	// @permissionScope(domain)
	// @deleteCascade(RefitTask)
	// @order(StatusID asc)
	// @page(default: 10, max: 100)
	// @validateCreateType(RefitCreateValidator)
	// @defaultsUpdateType(RefitUpdateDefaults)
	// @validateUpdateType(RefitUpdateValidator)
	Refit struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: HangarID.SectorID)
		ShipID impulse.UUID `spanner:"ShipId"`
		// @state(default: docked)
		StatusID string `spanner:"StatusId"`
		// @attribute(estimate)
		Estimate decimal.NullDecimal `spanner:"Estimate"`
		// @attribute(inspectedAt)
		InspectedAt *time.Time `spanner:"InspectedAt" conditions:"output_only"`
		// @attribute(openedBy)
		OpenedByUserID string  `spanner:"OpenedByUserId" conditions:"output_only" default_create_fn:"currentUser"`
		OpenedByName   string  `spanner:"OpenedByName"   conditions:"output_only" default_create_fn:"currentUserName"`
		Notes          *string `spanner:"Notes"`
	}
)

// Config enables change tracking: Refit mutations write DataChangeEvents rows in the
// same transaction.
func (Refit) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}

// RefitCreateValidator is wired in by the @validateCreateType annotation; the generated
// create patch calls Validate inside the mutation transaction.
//
// Demonstrates: @validateCreateType.
type RefitCreateValidator struct{}

// Validate refuses to dock a ship that is already in a bay. A ship is in a bay while a
// refit of it is still open, which is every state but the two the workflow ends in:
// cleared and scrapped. The read runs inside the create's own transaction, so two
// people docking the same ship at once cannot both succeed.
func (v *RefitCreateValidator) Validate(ctx context.Context, txn resource.ReadWriteTransaction, p *RefitCreatePatch) error {
	open := NewRefitQuery().
		AddColumns(NewRefitColumns().StatusID()).
		Where(NewRefitQueryClause().ShipID().Equal(p.ShipID()).
			And().StatusID().NotEqual(string(ClearedRefitStatus), string(ScrappedRefitStatus))).
		Limit(1)
	inBay := false
	for _, err := range open.List(ctx, txn) {
		if err != nil {
			return errors.Wrap(err, "RefitQuery.List()")
		}
		inBay = true
	}
	if inBay {
		return httpio.NewConflictMessage("This ship is already in a bay: its refit is still open. Finish that refit before you dock the ship again.")
	}

	return nil
}

// RefitUpdateDefaults is wired in by the @defaultsUpdateType annotation; the generated
// update patch calls Defaults inside the mutation transaction.
//
// Demonstrates: @defaultsUpdateType.
type RefitUpdateDefaults struct{}

// Defaults rounds an estimate to whole credits: the hangar does not quote fractions.
func (d *RefitUpdateDefaults) Defaults(_ context.Context, _ resource.ReadWriteTransaction, p *RefitUpdatePatch) error {
	if p.EstimateIsSet() && p.Estimate().Valid {
		p.SetEstimate(decimal.NewNullDecimal(p.Estimate().Decimal.Round(0)))
	}

	return nil
}

// RefitUpdateValidator is wired in by the @validateUpdateType annotation; the generated
// update patch calls Validate inside the mutation transaction.
//
// Demonstrates: @validateUpdateType.
type RefitUpdateValidator struct{}

// Validate rejects a negative estimate.
func (v *RefitUpdateValidator) Validate(_ context.Context, _ resource.ReadWriteTransaction, p *RefitUpdatePatch) error {
	if p.EstimateIsSet() && p.Estimate().Valid && p.Estimate().Decimal.IsNegative() {
		return httpio.NewBadRequestMessage("estimate must not be negative")
	}

	return nil
}
