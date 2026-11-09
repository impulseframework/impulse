package rpc

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
)

type (
	// ClaimMission moves a mission open -> claimed and assigns the squadron that
	// claimed it. Eligibility is NOT in the body: the Cadet's grant carries
	// `hazard IN (1, 2)` and the Pilot's `hazard <= subject.clearance AND
	// (requiredCert IS NULL OR requiredCert IN subject.certifications)`, evaluated by
	// the generated frame against the located row inside this transaction (E5). The
	// body only records who claimed. SquadronID carries the field-scope @enumerate, so
	// the generated TypeScript renders it as the Squadrons picker.
	//
	// Demonstrates: @transition, execute-condition, @enumerate, rpc.dry-run.
	//
	// @rpc
	// @permissionScope(domain)
	// @transition(Mission, from: open, to: claimed)
	ClaimMission struct {
		// @target
		MissionID impulse.UUID
		// @enumerate(Squadrons)
		SquadronID impulse.UUID
	}
)

// Execute runs inside the handler's transaction.
func (m *ClaimMission) Execute(ctx context.Context, txn resource.ReadWriteTransaction, _ *Client) error {
	squadron := impulse.NullUUID{UUID: m.SquadronID, Valid: true}
	if err := resources.NewMissionUpdatePatch(m.MissionID).SetAssignedSquadronID(squadron).Buffer(ctx, txn, resource.UserEvent(ctx)); err != nil {
		return errors.Wrap(err, "resources.MissionUpdatePatch.Buffer()")
	}

	return nil
}
