package virtualresources

import (
	_ "embed"

	"github.com/impulseframework/impulse"
)

type (
	// SectorPilot is a sector's pilots: one row per pilot who flies with a squadron of
	// the sector, however many of its squadrons that is. A pilot is one person in the
	// registry and may fly for two sectors, so the pilot's id is the key of a row in
	// each of them: the bare @domain column holds a row to its sector, and the key names
	// the pilot within it. The tenant column is therefore not part of the key.
	//
	// FliesElsewhere is a fact about the row that no table holds: whether the pilot also
	// flies for another sector. The view's query works it out, and @attribute gives it
	// the name a grant's condition reads (fliesElsewhere). The Wing Commander's grants
	// turn on it. A clearance is one value for the whole registry, so a pilot shared with
	// another sector is not one sector's to rate: the Wing Commander's list carries the
	// clearance, and SetSectorPilotClearance runs, only on the rows of pilots who fly for
	// this sector alone. The Sector Marshal holds both with no condition.
	//
	// The row is the method's target, so the row's Execute answer says whether the
	// method runs on it, and a page draws the button from that answer.
	//
	// Demonstrates: @virtual, virtual.domain, virtual.with-clause, virtual.keyed-read, @attribute, @attribute.view, @target.view.
	//
	// @virtual
	// @permissionScope(domain)
	// @order(DisplayName asc)
	// @page(default: 25, max: 200)
	SectorPilot struct {
		// @primarykey
		PilotID impulse.UUID `spanner:"PilotId" index:"true"`
		// @domain
		SectorID    string `spanner:"SectorId"    index:"true"`
		DisplayName string `spanner:"DisplayName" index:"true"`
		Squadrons   int64  `spanner:"Squadrons"`
		Clearance   int64  `spanner:"Clearance"`
		// @attribute(fliesElsewhere)
		FliesElsewhere bool `spanner:"FliesElsewhere"`
	}
)

//go:embed sector_pilots.sql
var sectorPilotsSubquery string

// Subquery provides the embedded SQL projection backing this virtual resource.
func (SectorPilot) Subquery() (query string, params map[string]any) {
	return sectorPilotsSubquery, nil
}
