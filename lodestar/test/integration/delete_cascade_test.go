// Demonstrates: @deleteCascade.
package integration

// This suite pins what a record declares its delete takes (@deleteCascade). A mission
// names its sorties, their expenses and its documents as going with it; a client names
// its contacts, and a mission or a consignment as refusing its delete. The generator
// holds each list to the schema and writes the description the test reads.

import (
	"testing"

	"github.com/google/go-cmp/cmp"
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
	if diff := cmp.Diff([]accesstypes.Resource{"SortieExpenses", "MissionDocuments", "Sorties"}, kinds(mission.Taken)); diff != "" {
		t.Errorf("the kinds a mission's delete takes mismatch (-want +got):\n%s", diff)
	}
	if len(mission.RefusedBy) != 0 {
		t.Errorf("a mission's delete is refused by %v, want nothing", kinds(mission.RefusedBy))
	}
	wantWay := []resource.DeletePath{{
		{Columns: []string{"SortieId"}, Table: "Sorties", References: []string{"Id"}},
		{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
	}}
	if diff := cmp.Diff(wantWay, mission.Taken[0].Paths); diff != "" {
		t.Errorf("an expense's way up to its mission mismatch (-want +got):\n%s", diff)
	}
	// A document stores a file, and its release is in what a row's delete costs.
	if got := mission.Taken[1].Cost.Files; got != 1 {
		t.Errorf("a mission document's cost counts %d stored files, want 1", got)
	}

	client := resources.Client{}.DeleteTree()
	if diff := cmp.Diff([]accesstypes.Resource{"ClientContacts"}, kinds(client.Taken)); diff != "" {
		t.Errorf("the kinds a client's delete takes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]accesstypes.Resource{"Missions", "Consignments"}, kinds(client.RefusedBy)); diff != "" {
		t.Errorf("the kinds that refuse a client's delete mismatch (-want +got):\n%s", diff)
	}
}
