// Demonstrates: @deleteCascade.
package integration

// This suite pins what a record declares its delete takes (@deleteCascade). A mission
// names its sorties, their expenses and its documents as going with it; a client names
// its contacts, and a mission or a consignment as refusing its delete. The generator
// holds each list to the schema and writes the description the test reads.

import (
	"reflect"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
)

// TestDeleteCascade_description reads the descriptions the generator wrote for the two
// records that declare one: the kinds taken in delete order, deepest first, with each
// kind's way up to the record, and the kinds that refuse the delete.
func TestDeleteCascade_description(t *testing.T) {
	t.Parallel()

	kinds := func(list []resource.DeleteKind) []accesstypes.Resource {
		names := make([]accesstypes.Resource, 0, len(list))
		for i := range list {
			names = append(names, list[i].Resource)
		}

		return names
	}

	mission := resources.Mission{}.DeleteTree()
	// An expense is on a sortie, so it is deleted before the sortie it is on.
	if got, want := kinds(mission.Taken), []accesstypes.Resource{"SortieExpenses", "MissionDocuments", "Sorties"}; !slices.Equal(got, want) {
		t.Errorf("a mission's delete takes %v, want %v", got, want)
	}
	if len(mission.RefusedBy) != 0 {
		t.Errorf("a mission's delete is refused by %v, want nothing", kinds(mission.RefusedBy))
	}
	wantWay := []resource.DeletePath{{
		{Columns: []string{"SortieId"}, Table: "Sorties", References: []string{"Id"}},
		{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
	}}
	if got := mission.Taken[0].Paths; !reflect.DeepEqual(got, wantWay) {
		t.Errorf("an expense's way up to its mission is %v, want %v", got, wantWay)
	}
	// A document stores a file, and its release is in what a row's delete costs.
	if got := mission.Taken[1].Cost.Files; got != 1 {
		t.Errorf("a mission document's cost counts %d stored files, want 1", got)
	}

	client := resources.Client{}.DeleteTree()
	if got, want := kinds(client.Taken), []accesstypes.Resource{"ClientContacts"}; !slices.Equal(got, want) {
		t.Errorf("a client's delete takes %v, want %v", got, want)
	}
	if got, want := kinds(client.RefusedBy), []accesstypes.Resource{"Missions", "Consignments"}; !slices.Equal(got, want) {
		t.Errorf("a client's delete is refused by %v, want %v", got, want)
	}
}
