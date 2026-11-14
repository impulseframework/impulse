// Demonstrates: secret.
package integration

// A ship's transponder key is a secret (conditions:"secret" on Ship.TransponderKey): a
// client sets it and nothing serves it back. This suite holds the declaration to each
// place the value could come out of: a read and a list, a columns, a filter and a sort
// that name it, and the ship's log, which serves every change of a ship to the browser
// and so is where a secret that the change record kept would be handed out.

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
)

func TestSecretField(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	g := grants{
		accesstypes.List:   append(withFields("Ships", "name", "registry"), resources.ShipsLogEntries),
		accesstypes.Read:   withFields("Ships", "name", "registry"),
		accesstypes.Update: withFields("Ships", "name", "transponderKey"),
	}
	h := mountLog(app.New(&testConfigurer{db: db, access: &staticAccess{g: g}}))

	const first, second = "kf-7741-open-sesame", "kf-7741-close-sesame"
	ship := sectorPath(anvil, "ships/"+shipKingfisherID)
	set := func(value string) (int, []byte) {
		t.Helper()

		return doRequestAs(t, h, "keymaster", http.MethodPatch, "/console/api/resources",
			fmt.Sprintf(`[{"op":"patch","path":%q,"value":%s}]`, opPath(anvil, "ships/"+shipKingfisherID), value))
	}
	stored := func() string {
		t.Helper()

		return readColumn[spanner.NullString](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "TransponderKey").StringVal
	}

	// The key is written as it was given.
	status, body := set(fmt.Sprintf(`{"transponderKey":%q}`, first))
	assertStatus(t, status, http.StatusOK, body)
	if got := stored(); got != first {
		t.Fatalf("the stored key = %q, want it as it was given", got)
	}
	// A second write with a name beside it, so one change record carries a field that
	// is no secret next to the one that is.
	status, body = set(fmt.Sprintf(`{"name":"Kingfisher II","transponderKey":%q}`, second))
	assertStatus(t, status, http.StatusOK, body)
	if got := stored(); got != second {
		t.Fatalf("the stored key after the second write = %q, want the new one", got)
	}
	// The same key again changes nothing: no error.
	status, body = set(fmt.Sprintf(`{"transponderKey":%q}`, second))
	assertStatus(t, status, http.StatusOK, body)

	leaks := func(where string, answer []byte) {
		t.Helper()
		for _, key := range []string{first, second} {
			if strings.Contains(string(answer), key) {
				t.Errorf("%s carries the key: %s", where, answer)
			}
		}
	}

	// A read and a list answer the ship without the field.
	status, body = doRequest(t, h, http.MethodGet, ship, "")
	assertStatus(t, status, http.StatusOK, body)
	if row := decodeRow(t, body); row["name"] != "Kingfisher II" {
		t.Errorf("the read answers %v, want the ship as it was renamed", row)
	} else if _, served := row["transponderKey"]; served {
		t.Errorf("the read serves the field: %s", body)
	}
	leaks("the read", body)
	status, body = doRequest(t, h, http.MethodGet, sectorPath(anvil, "ships?limit=100"), "")
	assertStatus(t, status, http.StatusOK, body)
	leaks("the list", body)

	// Nothing asks about it: a columns, a filter and a sort that name it are refused, in
	// the URL and in a posted list request alike.
	for name, request := range map[string]struct{ method, target, body string }{
		"columns naming the key":         {http.MethodGet, ship + "?columns=name,transponderKey", ""},
		"a sort by the key":              {http.MethodGet, sectorPath(anvil, "ships?sort=transponderKey:asc"), ""},
		"a filter on the key":            {http.MethodGet, sectorPath(anvil, "ships?filter=transponderKey:eq:"+second), ""},
		"a filter on the key, beside id": {http.MethodGet, sectorPath(anvil, "ships?filter=id:eq:"+shipKingfisherID+",transponderKey:eq:"+second), ""},
		"a posted filter on the key":     {http.MethodPost, sectorPath(anvil, "ships"), fmt.Sprintf(`{"filter":"transponderKey:eq:%s"}`, second)},
	} {
		status, body := doRequest(t, h, request.method, request.target, request.body)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", name, status, body)
		}
	}
	// And no answer says whether a guess is right: the right key and a wrong one are
	// refused in the same words, the guess itself aside.
	guess := func(key string) string {
		t.Helper()
		status, body := doRequest(t, h, http.MethodGet, sectorPath(anvil, "ships?filter=transponderKey:eq:"+key), "")
		if status != http.StatusBadRequest {
			t.Errorf("a filter guessing %q: status = %d, want 400: %s", key, status, body)
		}

		return strings.ReplaceAll(string(body), key, "(the guess)")
	}
	if right, wrong := guess(second), guess("not-the-key"); right != wrong {
		t.Errorf("a right guess answers %s and a wrong one %s; want one answer", right, wrong)
	}

	// The ship's log serves the changes: the key's, by the field's name, with neither
	// the old value nor the new one, beside the rename with both of its values.
	status, body = doRequest(t, h, http.MethodGet, sectorPath(anvil, "ships-log-entries"), "")
	assertStatus(t, status, http.StatusOK, body)
	leaks("the ship's log", body)
	var named, renamed int
	for _, entry := range decodeRows(t, body) {
		if entry["tableName"] != "Ships" || entry["rowId"] != shipKingfisherID {
			continue
		}
		changes, _ := entry["changeSet"].(map[string]any)
		if change, ok := changes["TransponderKey"].(map[string]any); ok {
			named++
			if change["Secret"] != true || change["Old"] != nil || change["New"] != nil {
				t.Errorf("the log's change of the key = %v, want it marked a secret with neither value", change)
			}
		}
		if change, ok := changes["Name"].(map[string]any); ok {
			renamed++
			if change["Old"] != "Kingfisher" || change["New"] != "Kingfisher II" {
				t.Errorf("the log's change of the name = %v, want both values: a field that is no secret keeps them", change)
			}
		}
	}
	if named != 2 || renamed != 1 {
		t.Errorf("the log names the key in %d change(s) and the name in %d, want 2 and 1 (the write that changed nothing writes no record): %s", named, renamed, body)
	}
}

// The generated browser metadata says the field is write-only and a secret, which is what
// makes the console type it into a masked box; the row interface has no property for it.
func TestSecretFieldGeneratedMetadata(t *testing.T) {
	t.Parallel()

	meta, err := os.ReadFile("../../web/console/src/app/core/service/zz_gen_resources.ts")
	if err != nil {
		t.Fatal(err)
	}
	const entry = "{ fieldName: 'transponderKey', displayType: 'string', required: false, isIndex: false, maxLength: 64, writeOnly: true, secret: true },"
	if !strings.Contains(string(meta), entry) {
		t.Errorf("zz_gen_resources.ts is missing the secret's metadata entry %q", entry)
	}
	methods, err := os.ReadFile("../../web/console/src/app/core/service/zz_gen_methods.ts")
	if err != nil {
		t.Fatal(err)
	}
	// The generated identity methods that take a password declare it a secret too.
	if got := strings.Count(string(methods), "{ fieldName: 'password', displayType: 'string', required: true, secret: true },"); got != 2 {
		t.Errorf("zz_gen_methods.ts marks %d required password field(s) secret, want the two temporary-password methods'", got)
	}
}
