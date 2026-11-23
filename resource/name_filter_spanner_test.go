package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestNameFilter_onSpanner runs lists of sector-1's passes filtered by a name on
// the Spanner emulator through the calls the generated handler makes. A filter
// tests the name the row shows: an id that points to another tenant's row, no
// id, and a pointed-to row the name's grant does not cover all show no name, so
// they match isnull and nothing else.
func TestNameFilter_onSpanner(t *testing.T) {
	t.Parallel()

	client := lookupSpannerClient(t, "name-filters")
	grantAll := renderStubPermissions{}
	rankOne := renderStubPermissions{decisions: accesstypes.Decisions{"Squadrons.name": conditionalOn("Squadrons.name", "rank = 1")}}

	tests := []struct {
		name   string
		filter string
		perms  UserPermissions
		want   []string
	}{
		{name: "a squadron's name", filter: "squadronName:eq:Red", perms: grantAll, want: []string{"p1"}},
		{name: "another sector's squadron's name", filter: "squadronName:eq:Blue", perms: grantAll},
		{name: "not a squadron's name: only rows that show another", filter: "squadronName:ne:Red", perms: grantAll, want: []string{"p3"}},
		{name: "no squadron's name shown", filter: "squadronName:isnull", perms: grantAll, want: []string{"p2", "p4", "p5"}},
		{name: "a squadron's name that contains the text", filter: "squadronName:contains:ed", perms: grantAll, want: []string{"p1"}},
		{name: "a crew's name, through its wing's sector", filter: "crewName:eq:Bravo", perms: grantAll, want: []string{"p1", "p3"}},
		{name: "another sector's crew's name", filter: "crewName:eq:Zulu", perms: grantAll},
		{name: "a level's name", filter: "levelName:eq:Visit", perms: grantAll, want: []string{"p1", "p2", "p3", "p4", "p5"}},
		{name: "a squadron's name the person may list only where its rank is 1: shown", filter: "squadronName:isnotnull", perms: rankOne, want: []string{"p1"}},
		{name: "a squadron's name the person may list only where its rank is 1: not shown", filter: "squadronName:isnull", perms: rankOne, want: []string{"p2", "p3", "p4", "p5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows, _ := lookupPage(t, client, lookupDecoder(t, Paging{}), "/?columns=id&filter="+tt.filter+"&sort=id&limit=all", tt.perms)
			if diff := cmp.Diff(tt.want, rows); diff != "" {
				t.Errorf("the rows mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("filtered and sorted by a crew's name, walked by its cursor", func(t *testing.T) {
		t.Parallel()

		want := [][]string{{"p1"}, {"p3"}}
		pages, _ := lookupWalk(t, client, lookupDecoder(t, Paging{}), "/?columns=id&filter=crewName:eq:Bravo&sort=crewName:asc&limit=1", grantAll)
		if diff := cmp.Diff(want, pages); diff != "" {
			t.Errorf("the walk forward mismatch (-want +got):\n%s", diff)
		}
	})
}
