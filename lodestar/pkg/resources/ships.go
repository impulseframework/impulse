package resources

import (
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Ship is a hull in the service's fleet. Its HangarId foreign key carries both a
	// one-hop @domain binding (Ship to Hangar.SectorId, no denormalized tenant column,
	// which is legal on a plain @target root) and a one-hop @attribute join path
	// (hangarZone = the hangar's zone). Its ClassId carries a SECOND join-path attribute
	// into a GLOBAL table (shipRole = the class's role).
	//
	// Registry is immutable. LastRefitAt is domain data owned by the PassFlightTest
	// transition, which stamps it with the commit timestamp as an explicit update;
	// output_only keeps clients from writing it. UpdatedAt is the mechanical enforcement
	// stamp, an output_only_update_fn, which also gives the resource the generated
	// NewShipTouch that HailShip fires. Change tracking is on so a Hail lands in the
	// ship's log with every field unchanged, and so a refused delete of a ship that has
	// flown proves the change event buffered beside it is not named in the 409.
	//
	// No column on a ship names its sector, so a list of ships scans the whole table,
	// every sector, and no index on Ships changes that; the generator says so at every
	// generate, for this and every other join-path resource. The fleet is small and
	// parent-scoped, which is what a join path suits.
	//
	// Registry is also unique across every ship (ShipsByRegistry): a create that reuses a
	// seeded registry reaches the commit and is refused there as a duplicate unique value.
	//
	// Registry is STRING(16), the fleet's only sized text column: the generated create
	// request carries sqltype:"STRING(16)", so a seventeen-character registry answers 400
	// naming the field at decode, before anything is buffered, and the console's create
	// form refuses it first from the metadata's maxLength. Registry is immutable, so the
	// create is the only path.
	//
	// CargoBays is the tonnage each cargo bay takes, in order: an ARRAY<INT64> column typed
	// []int64, number[] in the console's interface and in its metadata, one member of the
	// display-type vocabulary the client's union lists, so the console builds against what
	// the generator emits. NOT NULL with an empty array as the column's default, so a ship
	// with no bays carries [] and a create that says nothing about bays gets none. It
	// carries no allow_filter: a filter compares single values and Spanner cannot index an
	// array, so the generator would refuse the tag; a sort naming it answers 400.
	//
	// TransponderKey is a secret (conditions:"secret", typed resource.Secret): the marshal
	// sets it, on the create or later, and nothing serves it back. Its column holds the
	// value encrypted, never as it was typed: a request and a patch give the key as text
	// (patch.SetTransponderKey), and the write path seals it with the application's secret
	// keys (pkg/config) and the ship's primary key where it turns the patch into columns,
	// so the stored bytes open on this ship and on no other row. The field holds those
	// bytes and nothing else: it prints as [secret], and code that needs the key opens it
	// with the generated ship.OpenTransponderKey(keys), or ship.OpenTransponderKeyIn(ctx,
	// txn) inside a transaction, which also writes a value sealed under an older version
	// of the application secret again under the current one. It is write-only as an
	// input_only field is (absent from every list and read, refused as a column, a filter
	// or a sort), the console types it into a masked box whose toggle shows what is being
	// typed, and the ship's log, which serves every change of a ship, names a change of
	// the key without the old value or the new one (test/integration/secret_test.go).
	//
	// A ship's delete is declared (@deleteCascade): its refits and their tasks go with it,
	// each through its own delete, and a sortie flown or a droid report filed on the ship
	// refuses it.
	//
	// Demonstrates: @domain.join-path, @attribute.join-path, @attribute.join-path-global, immutable, output_only_update_fn, transition-owned-timestamp, change-tracking, commit.referential-refusal, commit.constraint-refusal, warning.join-path-list, decode.value-limit, typescript.array-column, secret, delete.rows-under-refusal.
	//
	// @resource
	// @permissionScope(domain)
	// @deleteCascade(Refit, RefitTask, refusedBy: Sortie, DroidReport)
	// @order(Name asc)
	// @page(default: 10, max: 100)
	Ship struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: SectorID)
		// @attribute(hangarZone, via: Zone)
		HangarID impulse.UUID `spanner:"HangarId"`
		// @attribute(shipRole, via: RoleID)
		ClassID     impulse.UUID `spanner:"ClassId"`
		Registry    string       `spanner:"Registry"    conditions:"immutable"`
		Name        string       `spanner:"Name"`
		LastRefitAt *time.Time   `spanner:"LastRefitAt" conditions:"output_only"`
		UpdatedAt   *time.Time   `spanner:"UpdatedAt"   output_only_update_fn:"resource.CommitTimestampPtr"`
		CargoBays   []int64      `spanner:"CargoBays"`
		// TransponderKey is the key the ship's transponder signs its answers with.
		TransponderKey *resource.Secret `spanner:"TransponderKey" conditions:"secret"`
	}
)

// Config enables change tracking: Ship mutations (including the Hail touch) write
// DataChangeEvents rows in the same transaction.
func (Ship) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
