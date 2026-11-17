package resource

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
)

// The rows TestSubjectSetParts_onPostgres runs over, the requester erin in the
// partition north at partsNow. Each row is one way a row meets a set's two
// parts: named by the requester, by another person, by a circle of the
// requester's, by a circle whose row has lapsed, by a circle of another
// person's, by nobody; at each level; lapsed and not; in another partition;
// and with no value where a part reads one.
var (
	partsNow     = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	partsLapsed  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	partsPending = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
)

const (
	partsRequester = "erin"
	partsDepot     = "north"
)

func partsCircles() []*semanticCircle {
	s := func(v string) *string {
		return &v
	}

	return []*semanticCircle{
		{ID: "c1", Circle: s("red"), UserID: s("erin")},
		{ID: "c2", Circle: s("blue"), UserID: s("erin"), ValidUntil: &partsLapsed},
		{ID: "c3", Circle: s("green"), UserID: s("erin"), ValidUntil: &partsPending},
		{ID: "c4", Circle: s("gold"), UserID: s("omar")},
		{ID: "n1", Circle: s("crimson"), InnerCircle: s("red")},
		{ID: "n2", Circle: s("navy"), InnerCircle: s("blue")},
		{ID: "n3", Circle: s("amber"), InnerCircle: s("gold")},
		{ID: "n4", Circle: s("void")},
	}
}

func partsPasses() []*semanticPass {
	s := func(v string) *string {
		return &v
	}
	n := func(v int64) *int64 {
		return &v
	}
	f := func(v float64) *float64 {
		return &v
	}
	yes, no := true, false

	return []*semanticPass{
		{ID: "p1", Depot: "north", UserID: s("erin"), Team: s("alpha"), Level: s("edit"), Tier: n(1), Sealed: &no},
		{ID: "p2", Depot: "north", UserID: s("erin"), Team: s("beta"), Level: s("view"), Tier: n(2), Sealed: &yes, Mass: f(12.5)},
		{ID: "p3", Depot: "north", UserID: s("erin"), Team: s("gamma"), Level: s("edit"), Tier: n(3), Sealed: &yes, Mass: f(1), ExpiresAt: &partsLapsed},
		{ID: "p4", Depot: "north", Circle: s("red"), Team: s("delta"), Level: s("edit"), Tier: n(4), ExpiresAt: &partsPending},
		{ID: "p5", Depot: "north", Circle: s("blue"), Team: s("epsilon"), Level: s("edit"), Tier: n(5)},
		{ID: "p6", Depot: "north", Circle: s("gold"), Team: s("zeta"), Level: s("edit"), Tier: n(6)},
		{ID: "p7", Depot: "north", UserID: s("omar"), Team: s("eta"), Level: s("edit"), Tier: n(7), Sealed: &no},
		{ID: "p8", Depot: "south", UserID: s("erin"), Team: s("theta"), Level: s("edit"), Tier: n(8), Sealed: &no},
		{ID: "p9", Depot: "north", Circle: s("crimson"), Team: s("iota"), Level: s("none"), Tier: n(9)},
		{ID: "p10", Depot: "north", Circle: s("navy"), Team: s("kappa"), Level: s("edit"), Tier: n(10)},
		{ID: "p11", Depot: "north", UserID: s("erin"), Level: s("edit")},
		{ID: "p12", Depot: "north", UserID: s("erin"), Team: s("lambda")},
		{ID: "p13", Depot: "north", Team: s("mu"), Level: s("edit"), Tier: n(13), Sealed: &no},
	}
}

func partsMemberships() []*semanticMembership {
	s := func(v string) *string {
		return &v
	}

	return []*semanticMembership{
		{ID: "m1", UserID: "erin", Depot: "north", Team: s("alpha")},
		{ID: "m2", UserID: "erin", Depot: "south", Team: s("theta")},
		{ID: "m3", UserID: "omar", Depot: "north", Team: s("eta")},
	}
}

func partsBadges() []*semanticBadge {
	s := func(v string) *string {
		return &v
	}

	return []*semanticBadge{
		{ID: "b1", Team: s("alpha"), Badge: s("gold-star")},
		{ID: "b2", Team: s("theta"), Badge: s("south-star")},
		{ID: "b3", Team: s("eta"), Badge: s("omar-star")},
		{ID: "b4", Badge: s("nobody")},
	}
}

// partsParcel is a checked row: a label and a weight a condition tests for
// membership in a set.
type partsParcel struct {
	ID     string
	Label  *string
	Weight *int64
}

func partsParcels() []*partsParcel {
	labels := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa", "lambda", "mu", "red", "blue", "green", "gold", "crimson", "navy", "gold-star", "south-star", "omar-star"}
	parcels := make([]*partsParcel, 0, len(labels)+1)
	for i, label := range labels {
		weight := int64(i + 1)
		parcels = append(parcels, &partsParcel{ID: fmt.Sprintf("parcel-%02d", i), Label: &label, Weight: &weight})
	}

	return append(parcels, &partsParcel{ID: "parcel-none"})
}

// TestSubjectSetParts_onPostgres runs the PostgreSQL rendering of subject sets
// that write a who: or a where: part on a real PostgreSQL and holds every
// answer to the reference (setReference): the membership test a condition
// renders, row by row of a checked table, and the statement that reads a set's
// values for a computed resource. The semantic differential proves the same
// rendering on the Spanner emulator over generated worlds; this is the other
// dialect over rows written to meet each shape, in a partitioned request and in
// a global one, with the sets' values written out beside the reference's so
// the reference itself is held to a reading a person made.
func TestSubjectSetParts_onPostgres(t *testing.T) {
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
	db, err := container.CreateDatabase(ctx, "subject-set-parts")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, ddl := range []string{
		`CREATE TABLE "Parcels" ("Id" text PRIMARY KEY, "Label" text, "Weight" bigint)`,
		`CREATE TABLE "Hubs" ("Id" text PRIMARY KEY, "Region" text)`,
		`CREATE TABLE "Memberships" ("Id" text PRIMARY KEY, "UserId" text, "Depot" text, "Team" text, "Tier" bigint, "HubId" text)`,
		`CREATE TABLE "Circles" ("Id" text PRIMARY KEY, "Circle" text, "UserId" text, "InnerCircle" text, "ValidUntil" timestamptz)`,
		`CREATE TABLE "Passes" ("Id" text PRIMARY KEY, "Depot" text NOT NULL, "UserId" text, "Circle" text, "Team" text, "Level" text, "Tier" bigint, "Mass" double precision, "Stake" numeric, "Sealed" boolean, "IssuedOn" date, "ExpiresAt" timestamptz)`,
		`CREATE TABLE "Badges" ("Id" text PRIMARY KEY, "Team" text, "Badge" text)`,
	} {
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
	circles, passes, memberships, badges, parcels := partsCircles(), partsPasses(), partsMemberships(), partsBadges(), partsParcels()
	for _, row := range circles {
		if _, err := db.Exec(ctx, `INSERT INTO "Circles" ("Id", "Circle", "UserId", "InnerCircle", "ValidUntil") VALUES ($1, $2, $3, $4, $5)`, row.ID, row.Circle, row.UserID, row.InnerCircle, row.ValidUntil); err != nil {
			t.Fatalf("inserting circle %s: %v", row.ID, err)
		}
	}
	for _, row := range passes {
		if _, err := db.Exec(ctx, `INSERT INTO "Passes" ("Id", "Depot", "UserId", "Circle", "Team", "Level", "Tier", "Mass", "Sealed", "ExpiresAt") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			row.ID, row.Depot, row.UserID, row.Circle, row.Team, row.Level, row.Tier, row.Mass, row.Sealed, row.ExpiresAt); err != nil {
			t.Fatalf("inserting pass %s: %v", row.ID, err)
		}
	}
	for _, row := range memberships {
		if _, err := db.Exec(ctx, `INSERT INTO "Memberships" ("Id", "UserId", "Depot", "Team") VALUES ($1, $2, $3, $4)`, row.ID, row.UserID, row.Depot, row.Team); err != nil {
			t.Fatalf("inserting membership %s: %v", row.ID, err)
		}
	}
	for _, row := range badges {
		if _, err := db.Exec(ctx, `INSERT INTO "Badges" ("Id", "Team", "Badge") VALUES ($1, $2, $3)`, row.ID, row.Team, row.Badge); err != nil {
			t.Fatalf("inserting badge %s: %v", row.ID, err)
		}
	}
	for _, row := range parcels {
		if _, err := db.Exec(ctx, `INSERT INTO "Parcels" ("Id", "Label", "Weight") VALUES ($1, $2, $3)`, row.ID, row.Label, row.Weight); err != nil {
			t.Fatalf("inserting parcel %s: %v", row.ID, err)
		}
	}

	collection := semanticCollection(t)
	bindings, ok := collection.Bindings(accesstypes.DomainPermissionScope, semanticResource)
	if !ok {
		t.Fatal("fixture bindings missing")
	}
	rows := setRows{
		"Memberships": setRowsOf(memberships),
		"Circles":     setRowsOf(circles),
		"Passes":      setRowsOf(passes),
		"Badges":      setRowsOf(badges),
	}
	// args supplies a statement's reserved parameters and the literals it bound.
	// A decimal literal binds as an exact NUMERIC, the Spanner client's type for
	// it; PostgreSQL is handed its text, which it reads as the column's type.
	args := func(registry *paramRegistry) pgx.NamedArgs {
		named := pgx.NamedArgs{}
		for _, name := range registry.referencedNames() {
			switch name {
			case subjectParamName:
				named[name] = partsRequester
			case domainParamName:
				named[name] = partsDepot
			case nowParamName:
				named[name] = partsNow
			}
		}
		for _, param := range registry.boundParams() {
			if rat, ok := param.Value.(*big.Rat); ok {
				named[param.Name] = rat.FloatString(9)

				continue
			}
			named[param.Name] = param.Value
		}

		return named
	}

	// The sets as a person reads the rows above, for the partitioned request and
	// the global one: what the reference is held to before anything is held to it.
	wantSets := map[bool]map[string][]string{
		true: {
			"circles":      {"green", "red"},
			"widerCircles": {"crimson"},
			"editTeams":    {"alpha", "delta"},
			"viewTeams":    {"alpha", "beta", "delta", "gamma"},
			"farTeams":     {"iota"},
			"drawnTeams":   {"alpha", "beta", "delta", "gamma"},
			"passTiers":    {"1", "2"},
			"badges":       {"gold-star"},
		},
		false: {
			"circles":      {"green", "red"},
			"widerCircles": {"crimson"},
			"editTeams":    {"alpha", "delta", "theta"},
			"viewTeams":    {"alpha", "beta", "delta", "gamma", "theta"},
			"farTeams":     {"iota"},
			"drawnTeams":   {"alpha", "beta", "delta", "gamma", "theta"},
			"passTiers":    {"1", "2", "8"},
			"badges":       {"gold-star", "south-star"},
		},
	}

	for _, partitioned := range []bool{true, false} {
		scope, request := accesstypes.DomainScope(partsDepot), "a partitioned request"
		if !partitioned {
			scope, request = accesstypes.GlobalScope(), "a global request"
		}
		reference := newSetReference(collection, rows, partsRequester, partsNow, scope)
		sets := make(map[string][]any, len(semanticPartSetNames)+1)
		for _, name := range append([]string{"teams"}, semanticPartSetNames...) {
			sets[name] = reference.values(t, name)
		}

		for _, name := range semanticPartSetNames {
			t.Run(request+"/subject."+name, func(t *testing.T) {
				t.Parallel()

				if got := textsOf(sets[name]); !slices.Equal(got, wantSets[partitioned][name]) {
					t.Fatalf("the reference reads subject.%s as %v, want %v", name, got, wantSets[partitioned][name])
				}

				sql, registry, err := subjectFactStatement(collection, PostgresDBType, partitioned, name, true)
				if err != nil {
					t.Fatalf("subjectFactStatement() error = %v", err)
				}
				result, err := db.Query(ctx, `SELECT "fact"::text FROM (`+sql+`) AS "facts"`, args(registry))
				if err != nil {
					t.Fatalf("running %s: %v", sql, err)
				}
				var got []string
				for result.Next() {
					var value *string
					if err := result.Scan(&value); err != nil {
						t.Fatalf("scanning: %v", err)
					}
					if value != nil {
						got = append(got, *value)
					}
				}
				if err := result.Err(); err != nil {
					t.Fatalf("reading the rows of %s: %v", sql, err)
				}
				slices.Sort(got)
				if !slices.Equal(got, wantSets[partitioned][name]) {
					t.Errorf("PostgreSQL reads subject.%s as %v, want %v\n%s", name, got, wantSets[partitioned][name], sql)
				}
			})
		}

		for _, source := range []string{
			"label IN subject.circles",
			"label IN subject.widerCircles",
			"label IN subject.editTeams",
			"label NOT IN subject.editTeams",
			"label IN subject.viewTeams AND label NOT IN subject.editTeams",
			"label IN subject.farTeams OR label IN subject.badges",
			"label IN subject.drawnTeams",
			"weight IN subject.passTiers",
			"NOT (weight IN subject.passTiers) AND label IN subject.viewTeams",
		} {
			t.Run(request+"/"+source, func(t *testing.T) {
				t.Parallel()

				expr, err := condition.Parse(source)
				if err != nil {
					t.Fatalf("Parse(%q) error = %v", source, err)
				}
				registry := newParamRegistry()
				sql, err := lowerToSQL(expr, &loweringContext{outer: "p", bindings: bindings, collection: collection, partitioned: partitioned}, newSQLGenerator(PostgreSQL), registry)
				if err != nil {
					t.Fatalf("lowerToSQL(%q) error = %v", source, err)
				}
				result, err := db.Query(ctx, `SELECT "p"."Id", (`+sql+`) FROM "Parcels" "p"`, args(registry))
				if err != nil {
					t.Fatalf("running %s: %v", sql, err)
				}
				got := make(map[string]bool, len(parcels))
				for result.Next() {
					var (
						id string
						ok *bool
					)
					if err := result.Scan(&id, &ok); err != nil {
						t.Fatalf("scanning: %v", err)
					}
					got[id] = ok != nil && *ok
				}
				if err := result.Err(); err != nil {
					t.Fatalf("reading the answers of %s: %v", sql, err)
				}

				var permitted int
				for _, parcel := range parcels {
					truth, err := condition.Evaluate(expr, &condition.Image{
						Pre:         map[string]any{"label": semanticValue(parcel.Label), "weight": semanticValue(parcel.Weight)},
						Subject:     partsRequester,
						Now:         partsNow,
						SubjectSets: sets,
					})
					if err != nil {
						t.Fatalf("Evaluate(%q) over %s error = %v", source, parcel.ID, err)
					}
					if want := truth.Permits(); got[parcel.ID] != want {
						t.Errorf("%s: PostgreSQL answers %v, the evaluator %v\n%s", parcel.ID, got[parcel.ID], want, sql)
					}
					if truth.Permits() {
						permitted++
					}
				}
				if permitted == 0 || permitted == len(parcels) {
					t.Errorf("%q permits %d of %d rows; the rows are written for it to decide both ways", source, permitted, len(parcels))
				}
			})
		}
	}
}

// textsOf renders a set's values as sorted text, for a comparison a person can read.
func textsOf(values []any) []string {
	texts := make([]string, 0, len(values))
	for _, value := range values {
		texts = append(texts, strings.TrimSpace(fmt.Sprint(value)))
	}
	slices.Sort(texts)

	return texts
}
