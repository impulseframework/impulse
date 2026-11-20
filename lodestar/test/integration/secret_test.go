// Demonstrates: secret, secret.rotation.
package integration

// A ship's transponder key is a secret (conditions:"secret" on Ship.TransponderKey,
// typed resource.Secret): a client sets it, the column holds it encrypted, and nothing
// serves it back. This suite holds the declaration to each place the value could come
// out of: the column itself, a read and a list, a columns, a filter and a sort that name
// it, and the ship's log, which serves every change of a ship to the browser and so is
// where a secret that the change record kept would be handed out. And it uses the key
// the way application code does, through the ship's generated Open methods, on its own
// row and across a rotation of the application secret.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
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
	// stored is what the column holds, read past the resource layer.
	stored := func(shipID string) []byte {
		t.Helper()

		return readColumn[[]byte](ctx, t, db, "Ships", spanner.Key{shipID}, "TransponderKey")
	}
	// storedVersion is the version of the application secret the stored key names: its
	// first two bytes, the high byte first.
	storedVersion := func(shipID string) int {
		t.Helper()

		sealed := stored(shipID)
		if len(sealed) < 2 {
			t.Fatalf("the column holds %d byte(s), too few to name a version", len(sealed))
		}

		return int(sealed[0])<<8 | int(sealed[1])
	}
	// opened reads a ship through the resource layer and opens its key the way application
	// code does, with the keys the data level holds: the ship's generated method takes the
	// row's own primary key, which the query reads with the secret.
	opened := func(shipID string, keys *resource.SecretKeys) (string, error) {
		t.Helper()

		ship, err := resources.NewShipQuery().AddColumns(resources.NewShipColumns().TransponderKey()).
			SetID(impulse.Must(impulse.UUIDFromString(shipID))).Read(ctx, resource.NewSpannerClient(db.Client))
		if err != nil {
			t.Fatalf("ShipQuery.Read() error = %v", err)
		}
		key, err := ship.Data.OpenTransponderKey(keys)
		if err != nil {
			return "", fmt.Errorf("Ship.OpenTransponderKey(): %w", err)
		}
		if key == nil {
			t.Fatalf("ship %s holds no key", shipID)
		}

		return *key, nil
	}
	holds := func(shipID, want string, keys *resource.SecretKeys) {
		t.Helper()

		sealed := stored(shipID)
		if bytes.Contains(sealed, []byte(want)) {
			t.Fatalf("the column holds the key as it was given")
		}
		if len(sealed) != resource.SecretOverhead+len(want) {
			t.Fatalf("the column holds %d bytes, want the key's %d and the %d sealing adds", len(sealed), len(want), resource.SecretOverhead)
		}
		if got, err := opened(shipID, keys); err != nil || got != want {
			t.Fatalf("the ship's key opens to %q, error %v; want %q", got, err, want)
		}
	}

	// The key is written sealed: the column holds none of it as it was given, and the
	// ship's generated method opens it.
	status, body := set(fmt.Sprintf(`{"transponderKey":%q}`, first))
	assertStatus(t, status, http.StatusOK, body)
	holds(shipKingfisherID, first, testSecretKeys)
	// A second write with a name beside it, so one change record carries a field that
	// is no secret next to the one that is.
	status, body = set(fmt.Sprintf(`{"name":"Kingfisher II","transponderKey":%q}`, second))
	assertStatus(t, status, http.StatusOK, body)
	holds(shipKingfisherID, second, testSecretKeys)
	// The same key again changes nothing: no error.
	status, body = set(fmt.Sprintf(`{"transponderKey":%q}`, second))
	assertStatus(t, status, http.StatusOK, body)
	holds(shipKingfisherID, second, testSecretKeys)
	// A key longer than the column takes (64 bytes, the column's 106 less the 42 sealing
	// adds) is refused before anything is sealed.
	status, body = set(fmt.Sprintf(`{"transponderKey":%q}`, strings.Repeat("k", 65)))
	assertStatus(t, status, http.StatusBadRequest, body)

	// The stored bytes are bound to their ship: moved onto another ship's row, past the
	// resource layer, they do not open there.
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.Update("Ships", []string{"Id", "TransponderKey"}, []any{shipStubbornMuleID, stored(shipKingfisherID)}),
	}); err != nil {
		t.Fatalf("moving the stored bytes onto another ship: %v", err)
	}
	if _, err := opened(shipStubbornMuleID, testSecretKeys); !errors.Is(err, resource.ErrSecretNotOpened) {
		t.Errorf("the key's bytes on another ship: error = %v, want resource.ErrSecretNotOpened", err)
	}

	// A rotation of the application secret hands the service version 2 beside version 1.
	// The key, sealed under version 1, still opens, and opening it outside a transaction
	// writes nothing.
	rotated := newTestSecretKeys(1, 2)
	holds(shipKingfisherID, second, rotated)
	if version := storedVersion(shipKingfisherID); version != 1 {
		t.Fatalf("the stored key names version %d before it is used in a transaction, want 1", version)
	}
	// Opened inside a transaction of a client that holds both versions, it is written
	// again under the current one: the value is the same, so the ship's update stamp does
	// not move and the ship's log gains no change (asserted on the log below).
	stamp := readColumn[spanner.NullTime](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "UpdatedAt")
	err = resource.NewSpannerClient(db.Client, resource.WithSecretKeys(rotated)).ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		ship, err := resources.NewShipQuery().AddColumns(resources.NewShipColumns().TransponderKey()).
			SetID(impulse.Must(impulse.UUIDFromString(shipKingfisherID))).Read(ctx, txn)
		if err != nil {
			return fmt.Errorf("ShipQuery.Read(): %w", err)
		}
		key, err := ship.Data.OpenTransponderKeyIn(ctx, txn)
		if err != nil {
			return fmt.Errorf("Ship.OpenTransponderKeyIn(): %w", err)
		}
		if key == nil || *key != second {
			t.Errorf("the key opened in the transaction is not the one that was set")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("opening the key in a transaction: %v", err)
	}
	if version := storedVersion(shipKingfisherID); version != 2 {
		t.Errorf("the stored key names version %d after it was used in a transaction, want the current one, 2", version)
	}
	holds(shipKingfisherID, second, rotated)
	if after := readColumn[spanner.NullTime](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "UpdatedAt"); after != stamp {
		t.Errorf("the rewrite moved the ship's update stamp from %v to %v; the ship did not change", stamp, after)
	}
	// Version 1 retired: a service handed version 2 alone opens the key, and one that
	// was never handed version 2 says which version it lacks.
	holds(shipKingfisherID, second, newTestSecretKeys(2))
	var unknown *resource.UnknownSecretVersionError
	if _, err := opened(shipKingfisherID, testSecretKeys); !errors.As(err, &unknown) || unknown.Version != 2 {
		t.Errorf("a service without version 2: error = %v, want a resource.UnknownSecretVersionError naming version 2", err)
	}

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
}
