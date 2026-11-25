// Demonstrates: touch, output_only_update_fn, change-tracking, event-source, @answers.no-content.
package integration

// This suite pins the first-class Touch: HailShip fires the generated NewShipTouch,
// so the full update pipeline runs with no caller-set fields — UpdatedAt takes the
// commit timestamp and the ship's history records who hailed with every field unchanged.

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"google.golang.org/api/iterator"
)

// changeRecordWho is what a change record says of a change beside what changed: what kind
// of write it was, and who made it, in the nine columns the table keeps it in, each as the
// table holds it.
type changeRecordWho struct {
	Operation         string             `spanner:"Operation"`
	Auth              spanner.NullString `spanner:"Auth"`
	ActorUserID       spanner.NullString `spanner:"ActorUserId"`
	ActorUsername     spanner.NullString `spanner:"ActorUsername"`
	PrincipalUserID   spanner.NullString `spanner:"PrincipalUserId"`
	PrincipalUsername spanner.NullString `spanner:"PrincipalUsername"`
	PrincipalRole     spanner.NullString `spanner:"PrincipalRole"`
	SessionID         spanner.NullString `spanner:"SessionId"`
	Process           spanner.NullString `spanner:"Process"`
	RPC               spanner.NullString `spanner:"Rpc"`
}

// changeRecordWhoColumns are changeRecordWho's columns as a statement selects them.
const changeRecordWhoColumns = "Operation, Auth, ActorUserId, ActorUsername, PrincipalUserId, PrincipalUsername, PrincipalRole, SessionId, Process, Rpc"

// columnSet is what a want of changeRecordWho.differs gives a column that holds something,
// whatever it holds.
const columnSet = "(set)"

// differs answers how the record's columns of who made the change differ from want, one
// clause for each column: a column want names holds that value (any value, for
// columnSet), and every other column but Auth holds nothing. It answers the empty text
// where none differs.
func (w *changeRecordWho) differs(want map[string]string) string {
	got := map[string]spanner.NullString{
		"ActorUserId": w.ActorUserID, "ActorUsername": w.ActorUsername,
		"PrincipalUserId": w.PrincipalUserID, "PrincipalUsername": w.PrincipalUsername, "PrincipalRole": w.PrincipalRole,
		"SessionId": w.SessionID, "Process": w.Process, "Rpc": w.RPC,
	}
	var differs []string
	for _, column := range slices.Sorted(maps.Keys(got)) {
		value := got[column]
		wanted, named := want[column]
		switch {
		case named && !value.Valid:
			differs = append(differs, column+" holds nothing, want "+wanted)
		case named && wanted != columnSet && value.StringVal != wanted:
			differs = append(differs, fmt.Sprintf("%s = %q, want %q", column, value.StringVal, wanted))
		case !named && value.Valid:
			differs = append(differs, fmt.Sprintf("%s = %q, want nothing", column, value.StringVal))
		}
	}

	return strings.Join(differs, "; ")
}

// scanChangeRecordWho reads a record's changeRecordWho from the first ten columns of a row
// that selects changeRecordWhoColumns first, and the rest of the row into rest.
func scanChangeRecordWho(row *spanner.Row, rest ...any) (changeRecordWho, error) {
	var w changeRecordWho
	columns := append([]any{
		&w.Operation, &w.Auth, &w.ActorUserID, &w.ActorUsername, &w.PrincipalUserID,
		&w.PrincipalUsername, &w.PrincipalRole, &w.SessionID, &w.Process, &w.RPC,
	}, rest...)
	if err := row.Columns(columns...); err != nil {
		return changeRecordWho{}, fmt.Errorf("spanner.Row.Columns(): %w", err)
	}

	return w, nil
}

// latestChangeEvent reads the newest DataChangeEvents row for one tracked row: who made
// the change, and the fields it changed.
func latestChangeEvent(t *testing.T, db *initiator.SpannerDB, tableName, rowID string) (who changeRecordWho, changeSetKeys []string) {
	t.Helper()

	iter := db.Single().Query(t.Context(), spanner.Statement{
		SQL: `SELECT ` + changeRecordWhoColumns + `, TO_JSON_STRING(ChangeSet)
		        FROM DataChangeEvents
		       WHERE TableName = @tableName AND RowId = @rowID
		    ORDER BY EventTime DESC, Sequence DESC
		       LIMIT 1`,
		Params: map[string]any{"tableName": tableName, "rowID": rowID},
	})
	defer iter.Stop()

	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		t.Fatalf("no DataChangeEvents row for %s %s", tableName, rowID)
	}
	if err != nil {
		t.Fatalf("DataChangeEvents query: %v", err)
	}

	var changeSetJSON string
	who, err = scanChangeRecordWho(row, &changeSetJSON)
	if err != nil {
		t.Fatalf("reading the change record of %s %s: %v", tableName, rowID, err)
	}

	changeSet := map[string]any{}
	if err := json.Unmarshal([]byte(changeSetJSON), &changeSet); err != nil {
		t.Fatalf("ChangeSet unmarshal: %v: %s", err, changeSetJSON)
	}
	for key := range changeSet {
		changeSetKeys = append(changeSetKeys, key)
	}

	return who, changeSetKeys
}

func TestHailShipTouch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	h := newTestApp(db, grants{accesstypes.Execute: {accesstypes.Resource("HailShip")}})

	// Seed rows never carry the stamp: UpdatedAt is written only by the update pipeline.
	if got := readColumn[spanner.NullTime](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "UpdatedAt"); got.Valid {
		t.Fatalf("seeded UpdatedAt = %v, want unset", got)
	}

	// The dock master hails: a persona of the cast, whose session belongs to an account,
	// so the change is written by her username with the id of the account.
	status, body := doRequestAs(t, h, "dock", http.MethodPost, sectorPath(anvil, "hail-ship"), fmt.Sprintf(`{"shipId":%q}`, shipKingfisherID))
	assertStatus(t, status, http.StatusNoContent, body)

	first := readColumn[spanner.NullTime](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "UpdatedAt")
	if !first.Valid {
		t.Fatal("UpdatedAt not stamped by the touch")
	}

	who, changeSetKeys := latestChangeEvent(t, db, "Ships", shipKingfisherID)
	if differs := who.differs(map[string]string{"ActorUsername": "dock", "ActorUserId": string(account(t, "dock")), "SessionId": columnSet, "Rpc": "HailShip"}); differs != "" {
		t.Errorf("who hailed, as the change record says: %s; want the dock master by username and account id, her session, and the method", differs)
	}
	if who.Operation != "update" {
		t.Errorf("the hail's change record is a %s, want an update", who.Operation)
	}
	if len(changeSetKeys) != 1 || changeSetKeys[0] != "UpdatedAt" {
		t.Errorf("changeSet keys = %v, want exactly [UpdatedAt]", changeSetKeys)
	}

	status, body = doRequestAs(t, h, "dock", http.MethodPost, sectorPath(anvil, "hail-ship"), fmt.Sprintf(`{"shipId":%q}`, shipKingfisherID))
	assertStatus(t, status, http.StatusNoContent, body)
	second := readColumn[spanner.NullTime](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "UpdatedAt")
	if !second.Valid || !second.Time.After(first.Time) {
		t.Errorf("second touch UpdatedAt = %v, want after the first %v", second, first)
	}

	// The generated frame locates the @target row before the body runs.
	status, body = doRequestAs(t, h, "dock", http.MethodPost, sectorPath(anvil, "hail-ship"), `{"shipId":"70000000-0000-4000-8000-00000000dead"}`)
	assertStatus(t, status, http.StatusNotFound, body)
}
