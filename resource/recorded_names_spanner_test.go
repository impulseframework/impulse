package resource

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// The fixture of the recorded names tests: two harbors (the tenants), ships of no tenant,
// a pilot in each harbor, and two ranks.
const (
	namedHarborNorth = "harbor-north"
	namedHarborSouth = "harbor-south"
)

// namedShip is a row of no tenant, known by its name; its code is not unique.
type namedShip struct {
	ID   string  `spanner:"Id"`
	Code *string `spanner:"Code"`
	Name *string `spanner:"Name"`
}

func (namedShip) Resource() accesstypes.Resource {
	return "Ships"
}

func (namedShip) Config() Config {
	return Config{}
}

// namedPilot is a harbor's row, known by its name.
type namedPilot struct {
	ID       string `spanner:"Id"`
	HarborID string `spanner:"HarborId"`
	Name     string `spanner:"Name"`
}

func (namedPilot) Resource() accesstypes.Resource {
	return "Pilots"
}

func (namedPilot) Config() Config {
	return Config{}
}

func (namedPilot) TenantBinding() TenantBinding {
	return TenantBinding{Field: "HarborID"}
}

// namedVoyage is a harbor's tracked row that points at a ship by its key and by its code,
// at a pilot of a harbor and at a rank, and keeps the name of each.
type namedVoyage struct {
	ID       string  `spanner:"Id"`
	HarborID string  `spanner:"HarborId"`
	ShipID   *string `spanner:"ShipId"`
	ShipCode *string `spanner:"ShipCode"`
	PilotID  *string `spanner:"PilotId"`
	RankID   *string `spanner:"RankId"`
	Note     *string `spanner:"Note"`
}

func (namedVoyage) Resource() accesstypes.Resource {
	return "Voyages"
}

func (namedVoyage) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (namedVoyage) TenantBinding() TenantBinding {
	return TenantBinding{Field: "HarborID"}
}

func (namedVoyage) RecordedNames() []NameField {
	return []NameField{
		{Key: "ShipID", Resource: "Ships", Tag: "name", Table: "Ships", KeyColumn: "Id", NameColumn: "Name"},
		{Key: "ShipCode", Resource: "Ships", Tag: "name", Table: "Ships", KeyColumn: "Code", NameColumn: "Name"},
		{Key: "PilotID", Resource: "Pilots", Tag: "name", Table: "Pilots", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "HarborId"},
		{Key: "RankID", Table: "Ranks", KeyColumn: "Id", NameColumn: "Description"},
	}
}

// plainVoyage is the same tracked row, which keeps no names: the write as it was before a
// change record kept them.
type plainVoyage struct {
	ID       string  `spanner:"Id"`
	HarborID string  `spanner:"HarborId"`
	ShipID   *string `spanner:"ShipId"`
	ShipCode *string `spanner:"ShipCode"`
	PilotID  *string `spanner:"PilotId"`
	RankID   *string `spanner:"RankId"`
	Note     *string `spanner:"Note"`
}

func (plainVoyage) Resource() accesstypes.Resource {
	return "Voyages"
}

func (plainVoyage) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (plainVoyage) TenantBinding() TenantBinding {
	return TenantBinding{Field: "HarborID"}
}

// namesDatabase makes a database with the recorded names fixture's tables on the shared
// emulator, with its ships, pilots and ranks written past the library.
func namesDatabase(t *testing.T, name string) *spanner.Client {
	t.Helper()

	container := spannerEmulator(t)
	db, err := container.CreateDatabase(context.Background(), name)
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/recordednames/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	ship := func(id, code string, name any) *spanner.Mutation {
		return spanner.InsertMap("Ships", map[string]any{"Id": id, "Code": code, "Name": name})
	}
	seedChangeRows(t, db.Client,
		ship("ship-1", "solo", "Endeavor"),
		ship("ship-2", "twin", "Resolution"),
		ship("ship-3", "twin", "Adventure"),
		ship("ship-blank", "blank", nil),
		ship("ship-r", "renamed", "Beagle"),
		ship("ship-t", "same", "Erebus"),
		spanner.InsertMap("Pilots", map[string]any{"Id": "pilot-north", "HarborId": namedHarborNorth, "Name": "Ada"}),
		spanner.InsertMap("Pilots", map[string]any{"Id": "pilot-south", "HarborId": namedHarborSouth, "Name": "Grace"}),
		spanner.InsertMap("Ranks", map[string]any{"Id": "rank-1", "Description": "Captain"}),
		spanner.InsertMap("Ranks", map[string]any{"Id": "rank-2", "Description": "Mate"}),
	)

	return db.Client
}

// seedVoyage writes a voyage of the north harbor past the library, so it has no change
// records.
func seedVoyage(t *testing.T, db *spanner.Client, id string, columns map[string]any) {
	t.Helper()

	row := map[string]any{"Id": id, "HarborId": namedHarborNorth}
	for column, value := range columns {
		row[column] = value
	}
	seedChangeRows(t, db, spanner.InsertMap("Voyages", row))
}

// voyage builds a patch of one voyage that keeps the names of the rows it points at
// (pointed).
func voyage(patchType PatchType, id string, fields map[accesstypes.Field]any) *PatchSet[namedVoyage] {
	return changePatch[namedVoyage](patchType, id, pointed(fields))
}

// pointed answers a voyage's fields as the voyage holds them: each but the harbor a
// pointer, nil where the value is.
func pointed(fields map[accesstypes.Field]any) map[accesstypes.Field]any {
	held := make(map[accesstypes.Field]any, len(fields))
	for field, value := range fields {
		switch value := value.(type) {
		case nil:
			held[field] = (*string)(nil)
		case string:
			if field == "HarborID" {
				held[field] = value
			} else {
				held[field] = &value
			}
		default:
			held[field] = value
		}
	}

	return held
}

// named is a field's entry in a change record that keeps the names beside its values, as
// the record reads back.
func named(oldV, newV any, oldName, newName string) DiffElem {
	return DiffElem{Old: oldV, New: newV, OldName: oldName, NewName: newName}
}

// recordedChangeSets reads past the library the change sets of one voyage, oldest first.
func recordedChangeSets(t *testing.T, db *spanner.Client, id string) []map[accesstypes.Field]DiffElem {
	t.Helper()

	changeSets := []map[accesstypes.Field]DiffElem{}
	for _, record := range changeRecords(t, db, "Voyages", id) {
		changeSets = append(changeSets, record.ChangeSet)
	}

	return changeSets
}

// TestRecordedNames_spanner pins, through the library's own writes on the emulator, the
// name a change record keeps beside each side of a field that points at a row with a
// name: the row's name as it was at the write, read in the write's transaction, or what
// an earlier patch of the transaction wrote; none where no one row of the record's tenant
// gives one.
func TestRecordedNames_spanner(t *testing.T) {
	t.Parallel()

	db := namesDatabase(t, "recorded-names")
	client := NewSpannerClient(db)

	t.Run("a create keeps the new name of each pointing field, an enumeration's its description", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client, voyage(CreatePatchType, "v-create", map[accesstypes.Field]any{
			"HarborID": namedHarborNorth, "ShipID": "ship-1", "ShipCode": "solo", "PilotID": "pilot-north", "RankID": "rank-1", "Note": "out",
		}))
		want := []map[accesstypes.Field]DiffElem{{
			"HarborID": changed(nil, namedHarborNorth),
			"ShipID":   named(nil, "ship-1", "", "Endeavor"),
			"ShipCode": named(nil, "solo", "", "Endeavor"),
			"PilotID":  named(nil, "pilot-north", "", "Ada"),
			"RankID":   named(nil, "rank-1", "", "Captain"),
			"Note":     changed(nil, "out"),
		}}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-create")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("an update from one row to another keeps both names", func(t *testing.T) {
		t.Parallel()

		seedVoyage(t, db, "v-update", map[string]any{"ShipId": "ship-1", "RankId": "rank-1"})
		applyEach(t, client, voyage(UpdatePatchType, "v-update", map[accesstypes.Field]any{"ShipID": "ship-2", "RankID": "rank-2"}))
		want := []map[accesstypes.Field]DiffElem{{
			"ShipID": named("ship-1", "ship-2", "Endeavor", "Resolution"),
			"RankID": named("rank-1", "rank-2", "Captain", "Mate"),
		}}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-update")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a record keeps the name as it was after the row is renamed and after it is deleted, and a new change keeps the new one", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client, voyage(CreatePatchType, "v-rename", map[accesstypes.Field]any{"HarborID": namedHarborNorth, "ShipID": "ship-r"}))
		seedChangeRows(t, db, spanner.UpdateMap("Ships", map[string]any{"Id": "ship-r", "Name": "Bounty"}))
		applyEach(t, client,
			voyage(UpdatePatchType, "v-rename", map[accesstypes.Field]any{"ShipID": "ship-1"}),
			voyage(UpdatePatchType, "v-rename", map[accesstypes.Field]any{"ShipID": "ship-r"}),
		)
		seedChangeRows(t, db, spanner.Delete("Ships", spanner.Key{"ship-r"}))
		applyEach(t, client, voyage(UpdatePatchType, "v-rename", map[accesstypes.Field]any{"ShipID": "ship-2"}))
		want := []map[accesstypes.Field]DiffElem{
			{"HarborID": changed(nil, namedHarborNorth), "ShipID": named(nil, "ship-r", "", "Beagle")},
			{"ShipID": named("ship-r", "ship-1", "Bounty", "Endeavor")},
			{"ShipID": named("ship-1", "ship-r", "Endeavor", "Bounty")},
			{"ShipID": named("ship-r", "ship-2", "", "Resolution")},
		}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-rename")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a delete keeps the old name of every pointing field", func(t *testing.T) {
		t.Parallel()

		seedVoyage(t, db, "v-delete", map[string]any{"ShipId": "ship-1", "ShipCode": "solo", "PilotId": "pilot-north", "RankId": "rank-2", "Note": "in"})
		applyEach(t, client, voyage(DeletePatchType, "v-delete", nil))
		want := []map[accesstypes.Field]DiffElem{{
			"ID":       changed("v-delete", nil),
			"HarborID": changed(namedHarborNorth, nil),
			"ShipID":   named("ship-1", nil, "Endeavor", ""),
			"ShipCode": named("solo", nil, "Endeavor", ""),
			"PilotID":  named("pilot-north", nil, "Ada", ""),
			"RankID":   named("rank-2", nil, "Mate", ""),
			"Note":     changed("in", nil),
		}}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-delete")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a pointer at a row of another tenant keeps no name", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client, voyage(CreatePatchType, "v-other", map[accesstypes.Field]any{"HarborID": namedHarborNorth, "ShipID": "ship-1", "PilotID": "pilot-south"}))
		want := []map[accesstypes.Field]DiffElem{{
			"HarborID": changed(nil, namedHarborNorth),
			"ShipID":   named(nil, "ship-1", "", "Endeavor"),
			"PilotID":  changed(nil, "pilot-south"),
		}}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-other")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a pointer that no row answers, one two rows answer, and one at a row with no name keep none, and the write succeeds", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client,
			voyage(CreatePatchType, "v-none", map[accesstypes.Field]any{"HarborID": namedHarborNorth, "ShipID": "ship-none", "ShipCode": "twin", "RankID": "rank-none"}),
			voyage(UpdatePatchType, "v-none", map[accesstypes.Field]any{"ShipID": "ship-blank", "ShipCode": "blank"}),
		)
		want := []map[accesstypes.Field]DiffElem{
			{
				"HarborID": changed(nil, namedHarborNorth),
				"ShipID":   changed(nil, "ship-none"),
				"ShipCode": changed(nil, "twin"),
				"RankID":   changed(nil, "rank-none"),
			},
			{
				"ShipID":   changed("ship-none", "ship-blank"),
				"ShipCode": changed("twin", "blank"),
			},
		}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-none")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a row an earlier patch of the transaction created or renamed gives the name that patch set", func(t *testing.T) {
		t.Parallel()

		shipName := "Discovery"
		renamed := "Terror"
		applyTogether(t, client,
			changePatch[namedShip](CreatePatchType, "ship-new", map[accesstypes.Field]any{"Name": &shipName}),
			changePatch[namedShip](UpdatePatchType, "ship-t", map[accesstypes.Field]any{"Name": &renamed}),
			voyage(CreatePatchType, "v-same", map[accesstypes.Field]any{"HarborID": namedHarborNorth, "ShipID": "ship-new"}),
			voyage(UpdatePatchType, "v-same", map[accesstypes.Field]any{"ShipID": "ship-t"}),
		)
		want := []map[accesstypes.Field]DiffElem{
			{"HarborID": changed(nil, namedHarborNorth), "ShipID": named(nil, "ship-new", "", "Discovery")},
			{"ShipID": named("ship-new", "ship-t", "Discovery", "Terror")},
		}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "v-same")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})
}

// emulatorPort is the emulator's gRPC port, the one package db exposes.
const emulatorPort = "9010/tcp"

// statementCountingClient makes another client of the database, on the shared emulator,
// that counts every statement and read it sends; the count is of the client's calls, so a
// test that reads it writes through this client alone, one transaction at a time.
func statementCountingClient(t *testing.T, db *spanner.Client) (*SpannerClient, *atomic.Int64) {
	t.Helper()

	ctx := context.Background()
	container := spannerEmulator(t)
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("testcontainers.Container.Host() error = %v", err)
	}
	port, err := container.MappedPort(ctx, emulatorPort)
	if err != nil {
		t.Fatalf("testcontainers.Container.MappedPort() error = %v", err)
	}
	reads := map[string]bool{
		"/google.spanner.v1.Spanner/ExecuteSql":          true,
		"/google.spanner.v1.Spanner/ExecuteStreamingSql": true,
		"/google.spanner.v1.Spanner/Read":                true,
		"/google.spanner.v1.Spanner/StreamingRead":       true,
	}
	statements := &atomic.Int64{}
	counted, err := spanner.NewClientWithConfig(ctx, db.DatabaseName(), spanner.ClientConfig{DisableNativeMetrics: true},
		option.WithEndpoint(fmt.Sprintf("passthrough:///%s:%s", host, port.Port())),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithoutAuthentication(),
		internaloption.SkipDialSettingsValidation(),
		option.WithGRPCDialOption(grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			if reads[method] {
				statements.Add(1)
			}

			return invoker(ctx, method, req, reply, cc, opts...)
		})),
		option.WithGRPCDialOption(grpc.WithChainStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			if reads[method] {
				statements.Add(1)
			}

			return streamer(ctx, desc, cc, method, opts...)
		})),
	)
	if err != nil {
		t.Fatalf("spanner.NewClientWithConfig() error = %v", err)
	}
	t.Cleanup(counted.Close)

	return NewSpannerClient(counted), statements
}

// countStatements buffers the patches in one transaction and answers the statements and
// reads the attempt that commits sent.
func countStatements(t *testing.T, client *SpannerClient, statements *atomic.Int64, writes ...changeWrite) int64 {
	t.Helper()

	if err := client.ExecuteFunc(context.Background(), func(ctx context.Context, txn ReadWriteTransaction) error {
		statements.Store(0)
		for _, w := range writes {
			if err := w.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return errors.Wrap(err, "changeWrite.Buffer()")
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("ExecuteFunc() error = %v", err)
	}

	return statements.Load()
}

// TestRecordedNames_spannerStatements pins what the names cost a write, counted against
// the same writes of a resource that keeps no names: a write that changes no pointing
// field runs no statement more, and every name a transaction's change records keep is
// read by one statement a record, each name once a transaction. The count is of a
// counting client's calls, so each case counts on a client of its own.
func TestRecordedNames_spannerStatements(t *testing.T) {
	t.Parallel()

	db := namesDatabase(t, "recorded-names-counted")
	for _, id := range []string{"plain-note", "named-note", "plain-two", "named-two"} {
		seedVoyage(t, db, id, map[string]any{"ShipId": "ship-1", "PilotId": "pilot-north", "Note": "in"})
	}
	var plainDeletes, namedDeletes []changeWrite
	for i := range 10 {
		plainID, namedID := fmt.Sprintf("plain-delete-%d", i), fmt.Sprintf("named-delete-%d", i)
		seedVoyage(t, db, plainID, map[string]any{"ShipId": "ship-1"})
		seedVoyage(t, db, namedID, map[string]any{"ShipId": "ship-1"})
		plainDeletes = append(plainDeletes, changePatch[plainVoyage](DeletePatchType, plainID, nil))
		namedDeletes = append(namedDeletes, voyage(DeletePatchType, namedID, nil))
	}

	t.Run("an update that changes no pointing field runs no statement more", func(t *testing.T) {
		t.Parallel()

		client, statements := statementCountingClient(t, db)
		plainCount := countStatements(t, client, statements, changePatch[plainVoyage](UpdatePatchType, "plain-note", pointed(map[accesstypes.Field]any{"Note": "out"})))
		namedCount := countStatements(t, client, statements, voyage(UpdatePatchType, "named-note", map[accesstypes.Field]any{"Note": "out"}))
		if namedCount != plainCount {
			t.Errorf("statements = %d, want %d, the count of the same write that keeps no names", namedCount, plainCount)
		}
	})

	t.Run("an update that changes two pointing fields runs exactly one more", func(t *testing.T) {
		t.Parallel()

		client, statements := statementCountingClient(t, db)
		fields := map[accesstypes.Field]any{"ShipID": "ship-2", "PilotID": nil}
		plainCount := countStatements(t, client, statements, changePatch[plainVoyage](UpdatePatchType, "plain-two", pointed(fields)))
		namedCount := countStatements(t, client, statements, voyage(UpdatePatchType, "named-two", fields))
		if namedCount != plainCount+1 {
			t.Errorf("statements = %d, want %d, one more than the same write that keeps no names", namedCount, plainCount+1)
		}
		want := []map[accesstypes.Field]DiffElem{{
			"ShipID":  named("ship-1", "ship-2", "Endeavor", "Resolution"),
			"PilotID": named("pilot-north", nil, "Ada", ""),
		}}
		if diff := cmp.Diff(want, recordedChangeSets(t, db, "named-two")); diff != "" {
			t.Errorf("change sets mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("ten deletes in one transaction of rows that point at one row run one more in all", func(t *testing.T) {
		t.Parallel()

		client, statements := statementCountingClient(t, db)
		plainCount := countStatements(t, client, statements, plainDeletes...)
		namedCount := countStatements(t, client, statements, namedDeletes...)
		if namedCount != plainCount+1 {
			t.Errorf("statements = %d, want %d, one more than the same deletes that keep no names", namedCount, plainCount+1)
		}
	})
}
