package rpc

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
)

// SetSectorPilotClearance rates a pilot who flies for the sector: it sets the hazard
// clearance on the pilot's record in the registry. Its target is a row of a view
// (@target(SectorPilot)), not of a table. A pilot who flies for two sectors is a row in
// each, so the generated frame reads no row by the pilot's id: the gate locates the row
// by the id within the request's sector and judges the caller's Execute grant against it
// in one statement over the view's query. A pilot who does not fly for the sector is
// not found there, whatever other sector they fly for. The Wing Commander's grant
// carries `fliesElsewhere = false`, a fact the view's query computes, so a pilot shared
// with another sector is refused; the Sector Marshal's grant carries no condition.
//
// The body writes the registry, a global table the sector's routes do not reach: what
// let the caller through is the row of the sector's own view.
//
// Demonstrates: @target, @target.view, @attribute.view, execute-condition, execute-condition.request-field, @answers.no-content.
//
// @rpc
// @permissionScope(domain)
// @answers(204)
type SetSectorPilotClearance struct {
	// @target(SectorPilot)
	PilotID impulse.UUID
	// Clearance is the clearance proposed for the pilot. A grant's condition reads it as
	// new.clearance, so a role may raise a pilot up to a level and no further.
	//
	// @attribute(clearance)
	Clearance int64
}

// maxClearance is the highest hazard clearance the registry records.
const maxClearance = 5

// Execute runs inside the handler's transaction, after the frame located the pilot's
// row in the sector, and answers nothing.
func (m *SetSectorPilotClearance) Execute(ctx context.Context, txn resource.ReadWriteTransaction, _ *Client) error {
	if m.Clearance < 0 || m.Clearance > maxClearance {
		return httpio.NewBadRequestMessagef("clearance %d is not a clearance: it is 0 to %d", m.Clearance, maxClearance)
	}
	if err := resources.NewPilotUpdatePatch(m.PilotID).SetClearance(m.Clearance).Buffer(ctx, txn, resource.UserEvent(ctx)); err != nil {
		return errors.Wrap(err, "resources.PilotUpdatePatch.Buffer()")
	}

	return nil
}
