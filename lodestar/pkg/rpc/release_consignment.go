package rpc

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
)

type (
	// ReleaseConsignment stamps a consignment's release to its owner. It is served on both
	// the default and droids outlets: a droid releases cargo by bearer token through the same
	// generated surface humans use. Cargo is released once, whoever asks, and the rule is the
	// method's own: the body refuses a consignment that already carries its release, with a
	// sentence that says when it left bond, and writes nothing. The Supercargo's and the
	// droid's Execute grants carry `releasedAt IS NULL` as well, evaluated by the frame
	// against the located row (@target(Consignment)), so their second release is the
	// frame's uniform Forbidden before the body runs and their rows answer no Execute on
	// released cargo; a grant that carries no condition (the Sector Marshal's) reaches the
	// body, which holds the rule for it.
	//
	// The body reads the manifest ARMED (Enforce(caller)) before it stamps, so the read
	// runs under the caller's own Read grant: the droid sees only the fields its grant
	// names, and a caller with no Read on the manifest is refused before anything is
	// written. It answers a typed receipt.
	//
	// Demonstrates: @target, execute-condition, outlet.shared, rpc.armed-read, rpc.typed-result.
	//
	// @rpc
	// @permissionScope(domain)
	// @outlet(default, droids)
	ReleaseConsignment struct {
		// @target(Consignment)
		ConsignmentID impulse.UUID
	}

	// Released is the receipt: which bond was released and when.
	Released struct {
		ConsignmentID impulse.UUID
		BondCode      string
		ReleasedAt    time.Time
	}
)

// releasedAtLayout writes the moment a consignment left bond in the refusal of a second
// release: the day and the minute, in UTC, as a person reads a date.
const releasedAtLayout = "January 2, 2006 at 15:04 UTC"

// Execute runs inside the handler's transaction.
func (m *ReleaseConsignment) Execute(ctx context.Context, txn resource.ReadWriteTransaction, _ *Client) (*Released, error) {
	manifest, err := resources.NewConsignmentQuery().
		AddColumns(resources.NewConsignmentColumns().ID().BondCode().ReleasedAt()).
		SetID(m.ConsignmentID).
		Enforce(resource.CallerFrom(ctx)).
		Read(ctx, txn)
	if err != nil {
		return nil, errors.Wrap(err, "resources.ConsignmentQuery.Read()")
	}
	if manifest == nil {
		return nil, resource.TargetNotFound("Consignment", m.ConsignmentID)
	}
	if released := manifest.Data.ReleasedAt; released != nil {
		return nil, httpio.NewConflictMessagef(
			"%s was already released, on %s. Cargo is released once.",
			manifest.Data.BondCode, released.UTC().Format(releasedAtLayout),
		)
	}

	now := time.Now().UTC()
	if err := resources.NewConsignmentUpdatePatch(m.ConsignmentID).SetReleasedAt(&now).Buffer(ctx, txn, eventsource.UserEvent(ctx)); err != nil {
		return nil, errors.Wrap(err, "resources.ConsignmentUpdatePatch.Buffer()")
	}

	return &Released{ConsignmentID: m.ConsignmentID, BondCode: manifest.Data.BondCode, ReleasedAt: now}, nil
}
