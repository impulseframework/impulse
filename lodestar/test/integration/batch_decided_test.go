// Demonstrates: batch.patch-or-add, batch.add-or-skip.
package integration

// The batch's two operations that decide from whether their row is there, over the
// refit's task sheet: a task is keyed by its refit and its number, so a client names
// the row it means. patch-or-add updates the task and creates it when the sheet holds
// none under that number; add-or-skip creates it and leaves a task that is there exactly
// as it is. Each runs as the patch or the add it turns out to be, so the fields and the
// rows the caller may write are that operation's own, and each is asked for the grant
// of what it is (Update for the first, Create for the second) before its row is looked
// for.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestBatchPatchOrAddAndAddOrSkip(t *testing.T) {
	t.Parallel()

	_, _, h := demoWorld(t)

	const resources = "/console/api/resources"
	task := func(number int) string {
		return fmt.Sprintf("refit-tasks/%s/%d", refitSamaritanID, number)
	}
	op := func(kind string, number int, value string) string {
		return fmt.Sprintf(`{"op":%q,"path":%q,"value":%s}`, kind, opPath(anvil, task(number)), value)
	}
	send := func(user string, ops ...string) (int, []byte) {
		t.Helper()

		return doRequestAs(t, h, accesstypes.User(user), http.MethodPatch, resources, "["+strings.Join(ops, ",")+"]")
	}
	read := func(number int) map[string]any {
		t.Helper()
		status, body := doRequestAs(t, h, "marshal", http.MethodGet, sectorPath(anvil, task(number)), "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRow(t, body)
	}
	holds := func(number int, instructions string, done bool) {
		t.Helper()
		row := read(number)
		if row["instructions"] != instructions || row["done"] != done {
			t.Errorf("task %d = instructions %q, done %v; want %q, %v", number, row["instructions"], row["done"], instructions, done)
		}
	}

	// patch-or-add: the sheet holds no task 21, so the task is created...
	status, body := send("marshal", op("patch-or-add", 21, `{"instructions":"Seal the aft lock","done":false}`))
	assertStatus(t, status, http.StatusOK, body)
	holds(21, "Seal the aft lock", false)
	// ...and now it does, so the same operation updates it, with the fields it carries.
	status, body = send("marshal", op("patch-or-add", 21, `{"done":true}`))
	assertStatus(t, status, http.StatusOK, body)
	holds(21, "Seal the aft lock", true)

	// add-or-skip: task 21 is there and stays exactly as it is...
	status, body = send("marshal", op("add-or-skip", 21, `{"instructions":"Something else entirely","done":false}`))
	assertStatus(t, status, http.StatusOK, body)
	holds(21, "Seal the aft lock", true)
	// ...and task 22 is not, so it is created.
	status, body = send("marshal", op("add-or-skip", 22, `{"instructions":"Test the seal","done":false}`))
	assertStatus(t, status, http.StatusOK, body)
	holds(22, "Test the seal", false)

	// One batch is one transaction, and no read sees what its own transaction wrote:
	// two adds of one task are refused at commit, where two add-or-skips land, the
	// second finding the task the first wrote.
	status, body = send("marshal", op("add", 23, `{"instructions":"first","done":false}`), op("add", 23, `{"instructions":"second","done":false}`))
	assertStatus(t, status, http.StatusConflict, body)
	status, body = send("marshal", op("add-or-skip", 23, `{"instructions":"first","done":false}`), op("add-or-skip", 23, `{"instructions":"second","done":false}`))
	assertStatus(t, status, http.StatusOK, body)
	holds(23, "first", false)

	// The grant asked for is the one of the operation it turns out to be. The Engineer
	// may create a task with its instructions alone, and may update a task's done flag
	// alone, each while the refit is under way.
	status, body = send("engineer", op("patch-or-add", 21, `{"done":false}`)) // there: an update of done
	assertStatus(t, status, http.StatusOK, body)
	holds(21, "Seal the aft lock", false)
	status, body = send("engineer", op("patch-or-add", 21, `{"instructions":"Rewritten"}`)) // there: an update of instructions
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = send("engineer", op("patch-or-add", 24, `{"done":true}`)) // not there: a create carrying done
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = send("engineer", op("patch-or-add", 24, `{"instructions":"Check the couplings"}`)) // not there: a create
	assertStatus(t, status, http.StatusOK, body)
	holds(24, "Check the couplings", false)
	holds(21, "Seal the aft lock", false)

	// Each is asked for the grant of what it is before its row is looked for: an
	// add-or-skip for Create, a patch-or-add for Update on the resource. The Dockmaster,
	// who reads tasks and writes none, gets one answer for a task that is there and for
	// one that is not, and learns nothing from it.
	for kind, value := range map[string]string{
		"add-or-skip":  `{"instructions":"Let me in","done":false}`,
		"patch-or-add": `{"done":true}`,
	} {
		var answers []string
		for _, number := range []int{21, 25} {
			status, body = send("dock", op(kind, number, value))
			assertStatus(t, status, http.StatusForbidden, body)
			answers = append(answers, string(body))
		}
		if answers[0] != answers[1] {
			t.Errorf("%s by a caller with no grant: a task that is there answers %s, one that is not answers %s; want one answer", kind, answers[0], answers[1])
		}
	}
	holds(21, "Seal the aft lock", false)

	// A resource whose id the server makes names no row that may or may not be there:
	// it takes add, patch and remove.
	for _, kind := range []string{"patch-or-add", "add-or-skip"} {
		status, body = send("marshal", fmt.Sprintf(`{"op":%q,"path":%q,"value":{"title":"No such thing"}}`, kind, opPath(anvil, "missions/a0000000-0000-4000-8000-00000000ffff")))
		assertStatus(t, status, http.StatusBadRequest, body)
		if !strings.Contains(string(body), "Missions takes add, patch and remove") {
			t.Errorf("%s on a mission: body %s, want the refusal naming what the resource takes", kind, body)
		}
	}
}
