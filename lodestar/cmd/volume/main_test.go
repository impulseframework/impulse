package main

import (
	"reflect"
	"regexp"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
)

// uuidShape is the identifier form the application's UUID type accepts: version 4,
// variant 8, lowercase hex.
var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$`)

// TestWorldSizing pins how many rows a scale derives per table, so a preset's cost is
// known before it runs, and that every identifier scheme yields a well-formed UUID the
// seed cannot collide with.
//
// Demonstrates: bootstrap.volume.
func TestWorldSizing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		scale         scale
		wantSectors   int // the synthetic ones plus Anvil
		wantSquadrons int // per sector
		wantShips     int // per sector
	}{
		{name: "medium", scale: scales["medium"], wantSectors: 21, wantSquadrons: 6, wantShips: 200},
		{name: "large", scale: scales["large"], wantSectors: 51, wantSquadrons: 6, wantShips: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &world{s: tt.scale}
			if got := len(w.sectorIDs()); got != tt.wantSectors {
				t.Errorf("sectorIDs() = %d sectors, want %d", got, tt.wantSectors)
			}
			if got := len(w.sectorNames()); got != tt.wantSectors {
				t.Errorf("sectorNames() = %d names, want %d: one for each sector", got, tt.wantSectors)
			}
			if got := len(w.squadronIDs(1)); got != tt.wantSquadrons {
				t.Errorf("squadronIDs(1) = %d, want %d", got, tt.wantSquadrons)
			}
			if got := len(w.shipIDs(1)); got != tt.wantShips {
				t.Errorf("shipIDs(1) = %d, want %d", got, tt.wantShips)
			}
			if wings := w.wingIDs(0); wings[0] != forgeWingID || len(wings) != tt.scale.wingsPerSector {
				t.Errorf("wingIDs(0) = %v, want Forge Wing first among %d", wings, tt.scale.wingsPerSector)
			}
		})
	}
}

// TestHistorySizing pins how many change records a scale derives: ten for each mission
// and each ship, so the history step's cost is known before it runs.
func TestHistorySizing(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]int{"medium": 567_000, "large": 5_304_000} {
		w := &world{s: scales[name]}
		if got := w.historyRecords(); got != want {
			t.Errorf("%s: historyRecords() = %d, want %d", name, got, want)
		}
	}
}

// TestHistory pins the change history of a mission and a ship of Anvil and of a
// synthetic sector: ten records of the row, each the first of its transaction, carrying
// the row's sector, 35 days apart over the past year; a create first, then one change a
// record, each from the value the record before gave; and every field a record names is
// a field of the row's resource, so a renamed field fails here and not on the page.
func TestHistory(t *testing.T) {
	t.Parallel()

	w := &world{s: scales["medium"]}
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		events   []resource.DataChangeEvent
		table    accesstypes.Resource
		rowID    string
		sectorID string
		fields   reflect.Type
	}{
		{"a mission of Anvil", w.missionHistory(now, 0, 7), "Missions", missionID(0, 7), anvilID, reflect.TypeFor[resources.Mission]()},
		{"a mission of a synthetic sector", w.missionHistory(now, 3, 2499), "Missions", missionID(3, 2499), syntheticSectorID(2), reflect.TypeFor[resources.Mission]()},
		{"a ship of Anvil", w.shipHistory(now, 0, 1, 7, 58), "Ships", shipID(0, 1, 7), anvilID, reflect.TypeFor[resources.Ship]()},
		{"a ship of a synthetic sector", w.shipHistory(now, 3, 2, 49, 750), "Ships", shipID(3, 2, 49), syntheticSectorID(2), reflect.TypeFor[resources.Ship]()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if len(tt.events) != historyPerRow {
				t.Fatalf("%d records, want %d", len(tt.events), historyPerRow)
			}
			fields := fieldNames(tt.fields)
			var before map[accesstypes.Field]resource.DiffElem
			for k, event := range tt.events {
				if event.TableName != tt.table || event.RowID != tt.rowID || event.Sequence != 1 || event.TenantID != tt.sectorID || event.EventSource != historySource {
					t.Errorf("record %d is of %s %s, sequence %d, tenant %q, source %q; want %s %s, 1, %q, %q",
						k, event.TableName, event.RowID, event.Sequence, event.TenantID, event.EventSource, tt.table, tt.rowID, tt.sectorID, historySource)
				}
				if k > 0 {
					if gap := event.EventTime.Sub(tt.events[k-1].EventTime); gap != historyStep {
						t.Errorf("record %d is %v after record %d, want %v", k, gap, k-1, historyStep)
					}
				}
				if _, err := spanner.InsertStruct(string(event.Resource()), event); err != nil {
					t.Errorf("record %d: spanner.InsertStruct() error = %v", k, err)
				}

				changeSet, ok := event.ChangeSet.Value.(map[accesstypes.Field]resource.DiffElem)
				if !event.ChangeSet.Valid || !ok || len(changeSet) == 0 {
					t.Fatalf("record %d holds no change set: %#v", k, event.ChangeSet)
				}
				for field, change := range changeSet {
					if !fields[field] {
						t.Errorf("record %d names %s, which is no field of %s", k, field, tt.fields.Name())
					}
					switch {
					case k == 0 && change.Old != nil:
						t.Errorf("the create's %s has the old value %v, want none", field, change.Old)
					case k > 0 && len(changeSet) != 1:
						t.Errorf("record %d changes %d fields, want one", k, len(changeSet))
					case k > 0 && change.Old == change.New:
						t.Errorf("record %d changes %s from %v to itself", k, field, change.Old)
					case k > 0 && change.Old != before[field].New:
						t.Errorf("record %d changes %s from %v, want %v, the value record %d gave it", k, field, change.Old, before[field].New, k-1)
					}
				}
				before = changeSet
			}
			if newest := tt.events[historyPerRow-1].EventTime; now.Sub(newest) < time.Hour {
				t.Errorf("the newest record is %v before now, want at least an hour", now.Sub(newest))
			}
			if oldest := tt.events[0].EventTime; now.Sub(oldest) >= 351*24*time.Hour {
				t.Errorf("the oldest record is %v before now, want less than 351 days", now.Sub(oldest))
			}
		})
	}
}

// fieldNames answers the names of a struct type's visible fields, each mapped to true.
func fieldNames(structType reflect.Type) map[accesstypes.Field]bool {
	names := make(map[accesstypes.Field]bool)
	for _, field := range reflect.VisibleFields(structType) {
		names[accesstypes.Field(field.Name)] = true
	}

	return names
}

func TestIdentifierSchemes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
	}{
		{name: "sector", id: syntheticSectorID(3)},
		{name: "wing", id: wingID(3, 1)},
		{name: "squadron", id: squadronID(3, 1, 2)},
		{name: "client", id: clientID(7)},
		{name: "hangar", id: hangarID(3, 2)},
		{name: "ship", id: shipID(3, 2, 99)},
		{name: "mission", id: missionID(3, 9999)},
		{name: "sortie", id: sortieID(3, 9995)},
		{name: "report", id: reportID(3, 199)},
		{name: "user", id: userID(3, 42)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !uuidShape.MatchString(tt.id) {
				t.Errorf("%s id %q is not a version-4 UUID", tt.name, tt.id)
			}
			if tt.id[0] != 'e' {
				t.Errorf("%s id %q does not carry the synthetic prefix the seed never uses", tt.name, tt.id)
			}
		})
	}
}
