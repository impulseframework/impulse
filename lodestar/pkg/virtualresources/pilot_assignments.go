package virtualresources

import (
	_ "embed"

	"github.com/impulseframework/impulse"
)

type (
	// PilotAssignment is the pilot page's list: one row per membership of the pilot in
	// the selected sector, carrying the squadron's and the wing's names; the squadron id
	// names Squadrons so a row opens the squadron's page. The squadron's name is the
	// generated SquadronName, read from Squadrons by the id (@name on Squadron.Name); the
	// view joins it no longer. It is the second view over
	// SquadronMemberships, and the two together show the direction of the declaration:
	// each view names the table, and the table names no view. The view declares its
	// backing table, a create goes into the table, and the new row shows up in the view
	// on the next list because the view's SQL reads that table.
	//
	// Demonstrates: @rowsOf, @rowsOf.association, @enumerate, list.row-route, @virtual, virtual.domain, @rowWord.
	//
	// @virtual
	// @rowsOf(SquadronMemberships)
	// @permissionScope(domain)
	// @order(SquadronName asc)
	// @page(default: 25, max: 200)
	// @rowWord(assignment)
	PilotAssignment struct {
		// @primarykey
		// @enumerate(Squadrons)
		SquadronID impulse.UUID `spanner:"SquadronId" index:"true"`
		// @primarykey
		UserID string `spanner:"UserId" index:"true"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId" index:"true"`
		WingName string       `spanner:"WingName" index:"true"`
	}
)

//go:embed pilot_assignments.sql
var pilotAssignmentsSubquery string

// Subquery provides the embedded SQL projection backing this virtual resource.
func (PilotAssignment) Subquery() (query string, params map[string]any) {
	return pilotAssignmentsSubquery, nil
}
