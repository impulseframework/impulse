package virtualresources

import (
	"time"

	_ "embed"

	"github.com/impulseframework/impulse"
)

type (
	// MissionBoard is the Missions console page's list: every mission in the sector with
	// the client's name, the assigned squadron's name, and the days left to its deadline,
	// computed in the SQL. The view declares its backing table, a create goes into the
	// table, and the new row shows up in the view on the next list because the view's
	// SQL reads that table. Its key is the mission's own id under the table's column
	// name, so a row opens the mission's page, an edit patches Missions, and a delete
	// removes the mission; the view's other columns are its own and never written. The
	// client's and the squadron's names are the generated ClientName and SquadronName,
	// read from ClientRosters and Squadrons by the two ids (@name on ClientRoster.Name and
	// Squadron.Name); the view joins them no longer.
	//
	// DaysLeft is computed in the SQL and indexed by nothing, so it is allow_filter:
	// its metadata says filterable: withIndexed, and the browser offers a days-left
	// filter only beside one on an indexed column, the rule the database parse enforces.
	//
	// BriefingTemplateId is the key of the briefing template the mission was booked with,
	// a row of the BriefingTemplates catalog. The catalog is computed and declares no name,
	// so the board shows the key itself, which reads on its own (standard, hazard-first),
	// and sorts and filters by it. No index answers it, so it is allow_filter, as DaysLeft
	// is.
	//
	// The board's box searches the mission's title: Title names Missions.Title and the SQL
	// projects its token column, so the box and Title's matches ride MissionsSearch. It is
	// a box over one index, so it stands anywhere in a filter, inside an OR too. A client
	// is found by the Client column's own filter, which tests the name the client rosters
	// give (ClientName).
	//
	// The board is live without a declaration: the generator reads the tables its query
	// names off mission_boards.sql with memefish's lexer (Missions), records them in this
	// package's generated live derivations, which the application assembles for every
	// publish, and marks the board published in its metadata. A mission's write reaches
	// the board's row and list subscribers under the mission's own key, as Missions' own
	// do; a page of the board that shows a client's or a squadron's name registers the
	// list the name is read from, so a write to a client or a squadron reaches it there.
	//
	// Demonstrates: @rowsOf, @rowsOf.same-row, @virtual, virtual.domain, metadata.filterable, search.view, live.derived.
	//
	// @virtual
	// @rowsOf(Missions)
	// @permissionScope(domain)
	// @order(Deadline asc)
	// @page(default: 25, max: 200)
	MissionBoard struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id" index:"true"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId" index:"true"`
		Title    string       `spanner:"Title"    index:"true" search:"Missions.Title"`
		// @enumerate(ClientRosters)
		ClientID impulse.UUID `spanner:"ClientId" index:"true"`
		// @enumerate(Squadrons)
		SquadronID impulse.NullUUID `spanner:"SquadronId" index:"true"`
		// @enumerate(MissionKinds)
		KindID string `spanner:"KindId" index:"true"`
		// @enumerate(MissionStatuses)
		StatusID string `spanner:"StatusId" index:"true"`
		// @enumerate(BriefingTemplates)
		BriefingTemplateID *string   `spanner:"BriefingTemplateId" allow_filter:"true"`
		Deadline           time.Time `spanner:"Deadline"           index:"true"`
		DaysLeft           int64     `spanner:"DaysLeft"           allow_filter:"true"`
	}
)

//go:embed mission_boards.sql
var missionBoardsSubquery string

// Subquery provides the embedded SQL projection backing this virtual resource.
func (MissionBoard) Subquery() (query string, params map[string]any) {
	return missionBoardsSubquery, nil
}
