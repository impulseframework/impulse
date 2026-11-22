package integration

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// TestNameField_passesByLevel pins a list sorted by, and showing, the name of the row an
// id points to. The marshal reads Anvil's hangar passes with each pass's level by name:
// every pass carries the name beside its level's key, one name a key, and the list
// sorted by the name walks two rows a page in the order of the names, the key breaking
// ties, the same rows forward and back. Passes of one level are equal names the cursor
// has to cross.
func TestNameField_passesByLevel(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	// The passes as they stand, with the name each carries. Other tests add passes to
	// the shared world, so every walk below is held to these by their ids.
	type pass struct {
		id   string
		name string
	}
	var passes []pass
	rr := doRequestRecordedAs(t, h, "marshal", http.MethodGet, sectorPath(anvil, "hangar-passes?columns=id,levelId,levelName&sort=id&limit=200"), "")
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	levels := make(map[string]string)
	alike := make(map[string]int)
	tie := 0
	for _, row := range decodeRows(t, rr.Body.Bytes()) {
		p := pass{id: cell[string](t, row, "id"), name: cell[string](t, row, "levelName")}
		level := cell[string](t, row, "levelId")
		if p.name == "" {
			t.Errorf("pass %s of level %s carries an empty name", p.id, level)
		}
		if known, ok := levels[level]; ok && known != p.name {
			t.Errorf("level %s is named %q and %q", level, known, p.name)
		}
		levels[level] = p.name
		alike[p.name]++
		tie = max(tie, alike[p.name])
		passes = append(passes, p)
	}
	if len(alike) < 2 || tie < 3 {
		t.Fatalf("the seeded passes carry %d level names over %d passes, %d at most alike; the walk needs two names and three passes alike", len(alike), len(passes), tie)
	}

	ids := make([]string, 0, len(passes))
	for _, p := range passes {
		ids = append(ids, p.id)
	}
	held := url.QueryEscape("id:in:(" + strings.Join(ids, ",") + ")")

	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()

			// The key always ascends, whichever way the names run.
			sorted := slices.Clone(passes)
			slices.SortFunc(sorted, func(a, b pass) int {
				byName := cmp.Compare(a.name, b.name)
				if direction == "desc" {
					byName = -byName
				}

				return cmp.Or(byName, cmp.Compare(a.id, b.id))
			})
			want := make([]string, 0, len(sorted))
			for _, p := range sorted {
				want = append(want, p.id)
			}

			target := sectorPath(anvil, fmt.Sprintf("hangar-passes?columns=id,levelName&sort=levelName:%s&limit=2&filter=%s", direction, held))
			forward, backward, pages := walkBothWays(t, h, "marshal", target, projectedKeys("id", []string{"id", "levelName"}))
			if !slices.Equal(forward, want) {
				t.Errorf("forward walk = %v, want %v", forward, want)
			}
			if !slices.Equal(backward, want) {
				t.Errorf("backward walk = %v, want %v", backward, want)
			}
			if wantPages := (len(want) + 1) / 2; pages != wantPages {
				t.Errorf("pages = %d, want %d of two rows", pages, wantPages)
			}
		})
	}
}

// TestNameField_fixedValueNames pins that a column of fixed values sorts by what it
// shows: Anvil's missions sorted by their status's name come in the order of the
// names, not of the keys the rows store, each row carrying the name beside its key.
func TestNameField_fixedValueNames(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()

			rr := doRequestRecordedAs(t, h, "marshal", http.MethodGet, sectorPath(anvil, "missions?columns=id,statusId,statusName&sort=statusName:"+direction+"&limit=100"), "")
			assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
			rows := decodeRows(t, rr.Body.Bytes())
			var names []string
			keys := make(map[string]string)
			for _, row := range rows {
				name := cell[string](t, row, "statusName")
				if key := cell[string](t, row, "statusId"); keys[key] == "" {
					keys[key] = name
				} else if keys[key] != name {
					t.Errorf("status %s is named %q and %q", key, keys[key], name)
				}
				names = append(names, name)
			}
			if len(keys) < 2 {
				t.Fatalf("the missions show %d statuses over %d rows; the order needs two", len(keys), len(rows))
			}
			inOrder := slices.IsSorted(names)
			if direction == "desc" {
				inOrder = slices.IsSortedFunc(names, func(a, b string) int { return cmp.Compare(b, a) })
			}
			if !inOrder {
				t.Errorf("missions sorted by statusName %s came with the names %v", direction, names)
			}
		})
	}
}
