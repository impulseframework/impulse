package resource

import (
	"context"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
)

// The rows the questions about a named person are asked over, on both databases:
// erin is in the west hub, omar in the east one; one loan names erin and lends the
// north hub, another names no one, belongs to the west hub and lends the south one;
// erin's home is the north hub and omar's the east one. The west and north hubs are
// in the dry zone, the east and south ones in the wet zone.
const (
	reachHubWest  = "10000000-0000-4000-8000-000000000001"
	reachHubEast  = "10000000-0000-4000-8000-000000000002"
	reachHubNorth = "10000000-0000-4000-8000-000000000003"
	reachHubSouth = "10000000-0000-4000-8000-000000000004"
	reachZoneDry  = "z-dry"
	reachZoneWet  = "z-wet"
)

// reachHub is the fixture's hub as a record a change is made to: its key is an id
// of the library's own type, as an application's is.
type reachHub struct {
	ID     impulse.UUID `postgres:"Id"     spanner:"Id"`
	Region *string      `postgres:"Region" spanner:"Region"`
	ZoneID *string      `postgres:"ZoneId" spanner:"ZoneId"`
}

func (reachHub) Resource() accesstypes.Resource {
	return "Hubs"
}

// reachZone is the fixture's zone as a record a change is made to.
type reachZone struct {
	ID      string  `postgres:"Id"      spanner:"Id"`
	Climate *string `postgres:"Climate" spanner:"Climate"`
}

func (reachZone) Resource() accesstypes.Resource {
	return "Zones"
}

// reachQuestion is one question about a named person, asked inside a transaction
// of the database under test.
type reachQuestion struct {
	name string
	ask  func(ctx context.Context, txn ReadWriteTransaction, collection *GeneratedCollection) (bool, error)
	want bool
}

// reachHolds asks whether value is in the person's set.
func reachHolds(set string, subject accesstypes.User, value any) func(context.Context, ReadWriteTransaction, *GeneratedCollection) (bool, error) {
	return func(ctx context.Context, txn ReadWriteTransaction, collection *GeneratedCollection) (bool, error) {
		patch := NewPatchSet(NewMetadata[reachHub]())
		patch.querySet.collection = collection

		return patch.SubjectSetHolds(ctx, txn, set, subject, value)
	}
}

// reachThrough asks whether the person's fact is read through the field of the
// row of Resource that key names.
func reachThrough[Resource Resourcer](fact string, field accesstypes.Field, subject accesstypes.User, key any) func(context.Context, ReadWriteTransaction, *GeneratedCollection) (bool, error) {
	return func(ctx context.Context, txn ReadWriteTransaction, collection *GeneratedCollection) (bool, error) {
		patch := NewPatchSet(NewMetadata[Resource]())
		patch.querySet.collection = collection

		return patch.SubjectReadThrough(ctx, txn, fact, field, subject, KeySet{}.Add("ID", key))
	}
}

// reachQuestions are the cases both databases answer: every shape a set takes (its
// own column, a path of one hop and of two, a who: that follows another set), a
// subject value, a person with no row, and a value handed over as text and as the
// library's own id types.
func reachQuestions(t *testing.T) []reachQuestion {
	t.Helper()

	id := func(text string) impulse.UUID {
		parsed, err := impulse.UUIDFromString(text)
		if err != nil {
			t.Fatalf("impulse.UUIDFromString(%s) error = %v", text, err)
		}

		return parsed
	}

	return []reachQuestion{
		{name: "a hub the person is in, as text", ask: reachHolds("hubs", "erin", reachHubWest), want: true},
		{name: "a hub the person is in, as an id", ask: reachHolds("hubs", "erin", id(reachHubWest)), want: true},
		{name: "a hub the person is in, as an id that may be absent", ask: reachHolds("hubs", "erin", impulse.NullUUID{UUID: id(reachHubWest), Valid: true}), want: true},
		{name: "a hub another person is in", ask: reachHolds("hubs", "erin", id(reachHubEast)), want: false},
		{name: "the other person's hub, asked of them", ask: reachHolds("hubs", "omar", id(reachHubEast)), want: true},
		{name: "a person with no row", ask: reachHolds("hubs", "nobody", id(reachHubWest)), want: false},
		{name: "a region one hop from the person's hub", ask: reachHolds("regions", "erin", "west"), want: true},
		{name: "a region of another person's hub", ask: reachHolds("regions", "erin", "east"), want: false},
		{name: "a climate two hops from the person's hub", ask: reachHolds("climates", "erin", "dry"), want: true},
		{name: "a climate of another person's hub", ask: reachHolds("climates", "erin", "wet"), want: false},
		{name: "a region lent by a row that names the person", ask: reachHolds("lentRegions", "erin", "north"), want: true},
		{name: "a region lent by a row of a hub the person is in", ask: reachHolds("lentRegions", "erin", "south"), want: true},
		{name: "that region, asked of a person in another hub", ask: reachHolds("lentRegions", "omar", "south"), want: false},
		{name: "a region no row lends the person", ask: reachHolds("lentRegions", "erin", "west"), want: false},

		{name: "the hub the person's regions are read through", ask: reachThrough[reachHub]("regions", "Region", "erin", id(reachHubWest)), want: true},
		{name: "a hub another person's regions are read through", ask: reachThrough[reachHub]("regions", "Region", "erin", id(reachHubEast)), want: false},
		{name: "the other person's hub, asked of them through its region", ask: reachThrough[reachHub]("regions", "Region", "omar", id(reachHubEast)), want: true},
		{name: "the hub the person's climates are read through, at the first hop", ask: reachThrough[reachHub]("climates", "ZoneID", "erin", id(reachHubWest)), want: true},
		{name: "the zone the person's climates are read through, at the second hop", ask: reachThrough[reachZone]("climates", "Climate", "erin", reachZoneDry), want: true},
		{name: "a zone only another person's climates are read through", ask: reachThrough[reachZone]("climates", "Climate", "erin", reachZoneWet), want: false},
		{name: "that zone, asked of the other person", ask: reachThrough[reachZone]("climates", "Climate", "omar", reachZoneWet), want: true},
		{name: "the hub the person's home region is read through", ask: reachThrough[reachHub]("homeRegion", "Region", "erin", id(reachHubNorth)), want: true},
		{name: "a hub that is not the person's home", ask: reachThrough[reachHub]("homeRegion", "Region", "erin", id(reachHubWest)), want: false},
		{name: "the hub a row that names the person lends", ask: reachThrough[reachHub]("lentRegions", "Region", "erin", id(reachHubNorth)), want: true},
		{name: "the hub a row of the person's hub lends", ask: reachThrough[reachHub]("lentRegions", "Region", "erin", id(reachHubSouth)), want: true},
		{name: "that hub, asked of a person in another hub", ask: reachThrough[reachHub]("lentRegions", "Region", "omar", id(reachHubSouth)), want: false},
	}
}

// TestPatchSet_subjectReach_onPostgres asks the two questions about a named person
// on a real PostgreSQL, through the PostgreSQL transaction, over real rows: the
// statements are the set's own declaration in PostgreSQL's dialect, and the answers
// are the ones the Spanner emulator gives over the same rows
// (TestPatchSet_subjectReach_onSpanner).
func TestPatchSet_subjectReach_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
	ctx := context.Background()
	container, err := initiator.NewPostgresContainer(ctx, "16")
	if err != nil {
		t.Fatalf("initiator.NewPostgresContainer() error = %v", err)
	}
	t.Cleanup(func() {
		container.Close()
		if err := container.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	db, err := container.CreateDatabase(ctx, "subject-reach")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, statement := range []string{
		`CREATE TABLE "Zones" ("Id" text PRIMARY KEY, "Climate" text)`,
		`CREATE TABLE "Hubs" ("Id" uuid PRIMARY KEY, "Region" text, "ZoneId" text)`,
		`CREATE TABLE "Memberships" ("Id" text PRIMARY KEY, "UserId" text, "HubId" uuid)`,
		`CREATE TABLE "Loans" ("Id" text PRIMARY KEY, "UserId" text, "HubId" uuid, "LentHubId" uuid)`,
		`CREATE TABLE "Profiles" ("UserId" text PRIMARY KEY, "HomeHubId" uuid)`,
		`INSERT INTO "Zones" ("Id", "Climate") VALUES ('` + reachZoneDry + `', 'dry'), ('` + reachZoneWet + `', 'wet')`,
		`INSERT INTO "Hubs" ("Id", "Region", "ZoneId") VALUES
			('` + reachHubWest + `', 'west', '` + reachZoneDry + `'),
			('` + reachHubEast + `', 'east', '` + reachZoneWet + `'),
			('` + reachHubNorth + `', 'north', '` + reachZoneDry + `'),
			('` + reachHubSouth + `', 'south', '` + reachZoneWet + `')`,
		`INSERT INTO "Memberships" ("Id", "UserId", "HubId") VALUES ('m1', 'erin', '` + reachHubWest + `'), ('m2', 'omar', '` + reachHubEast + `')`,
		`INSERT INTO "Loans" ("Id", "UserId", "HubId", "LentHubId") VALUES
			('l1', 'erin', NULL, '` + reachHubNorth + `'),
			('l2', NULL, '` + reachHubWest + `', '` + reachHubSouth + `')`,
		`INSERT INTO "Profiles" ("UserId", "HomeHubId") VALUES ('erin', '` + reachHubNorth + `'), ('omar', '` + reachHubEast + `')`,
	} {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	collection := reachCollection(t)
	txn := newPostgresPostgresReadWriteTransaction(db.Pool)
	for _, question := range reachQuestions(t) {
		t.Run(question.name, func(t *testing.T) {
			t.Parallel()

			got, err := question.ask(ctx, txn, collection)
			if err != nil {
				t.Fatalf("on PostgreSQL the question answered an error: %v", err)
			}
			if got != question.want {
				t.Errorf("on PostgreSQL the question answered %t, want %t", got, question.want)
			}
		})
	}
}

// TestPatchSet_subjectReach_onSpanner asks the same questions over the same rows on
// the Spanner emulator, inside a read-write transaction of the library's client.
func TestPatchSet_subjectReach_onSpanner(t *testing.T) {
	t.Parallel()

	container := spannerEmulator(t)
	ctx := context.Background()
	db, err := container.CreateDatabase(ctx, "subject-reach")
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
	if err := db.MigrateUp("file://testdata/subjectreach/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("Zones", map[string]any{"Id": reachZoneDry, "Climate": "dry"}),
		spanner.InsertMap("Zones", map[string]any{"Id": reachZoneWet, "Climate": "wet"}),
		spanner.InsertMap("Hubs", map[string]any{"Id": reachHubWest, "Region": "west", "ZoneId": reachZoneDry}),
		spanner.InsertMap("Hubs", map[string]any{"Id": reachHubEast, "Region": "east", "ZoneId": reachZoneWet}),
		spanner.InsertMap("Hubs", map[string]any{"Id": reachHubNorth, "Region": "north", "ZoneId": reachZoneDry}),
		spanner.InsertMap("Hubs", map[string]any{"Id": reachHubSouth, "Region": "south", "ZoneId": reachZoneWet}),
		spanner.InsertMap("Memberships", map[string]any{"Id": "m1", "UserId": "erin", "HubId": reachHubWest}),
		spanner.InsertMap("Memberships", map[string]any{"Id": "m2", "UserId": "omar", "HubId": reachHubEast}),
		spanner.InsertMap("Loans", map[string]any{"Id": "l1", "UserId": "erin", "LentHubId": reachHubNorth}),
		spanner.InsertMap("Loans", map[string]any{"Id": "l2", "HubId": reachHubWest, "LentHubId": reachHubSouth}),
		spanner.InsertMap("Profiles", map[string]any{"UserId": "erin", "HomeHubId": reachHubNorth}),
		spanner.InsertMap("Profiles", map[string]any{"UserId": "omar", "HomeHubId": reachHubEast}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	collection := reachCollection(t)
	client := NewSpannerClient(db.Client)
	for _, question := range reachQuestions(t) {
		t.Run(question.name, func(t *testing.T) {
			var got bool
			err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = question.ask(ctx, txn, collection)

				return err
			})
			if err != nil {
				t.Fatalf("on Spanner the question answered an error: %v", err)
			}
			if got != question.want {
				t.Errorf("on Spanner the question answered %t, want %t", got, question.want)
			}
		})
	}
}
