package resource

// These tests pin how a computed resource's conditions get the facts about the
// caller that are held in tables (subject_facts.go): the statements' text in both
// databases, and, called from the semantic differential over each of its generated
// worlds on the emulator (checkSubjectFacts), the facts read against the fixture's
// own model of them and a computed list judged under conditions that read them.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

func TestSubjectFactStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fact        string
		set         bool
		partitioned bool
		wantSpanner string
		wantPG      string
		wantNames   []string
		wantErr     string
	}{
		{
			name:        "a subject value is one value off the caller's anchor row",
			fact:        "nickname",
			partitioned: true,
			wantSpanner: "SELECT (SELECT `ca1`.`Nickname` FROM `Profiles` `ca1` WHERE `ca1`.`UserId` = @subject) AS `fact`",
			wantPG:      `SELECT (SELECT "ca1"."Nickname" FROM "Profiles" "ca1" WHERE "ca1"."UserId" = @subject) AS "fact"`,
			wantNames:   []string{"subject"},
		},
		{
			name:        "a dotted subject value continues hop by hop",
			fact:        "homeRegion",
			partitioned: true,
			wantSpanner: "SELECT (SELECT `ca2`.`Region` FROM `Hubs` `ca2` WHERE `ca2`.`Id` = (SELECT `ca1`.`HomeHubId` FROM `Profiles` `ca1` WHERE `ca1`.`UserId` = @subject)) AS `fact`",
			wantPG:      `SELECT (SELECT "ca2"."Region" FROM "Hubs" "ca2" WHERE "ca2"."Id" = (SELECT "ca1"."HomeHubId" FROM "Profiles" "ca1" WHERE "ca1"."UserId" = @subject)) AS "fact"`,
			wantNames:   []string{"subject"},
		},
		{
			name:        "a subject set is the caller's anchor rows in the request's partition",
			fact:        "teams",
			set:         true,
			partitioned: true,
			wantSpanner: "SELECT `ca1`.`Team` AS `fact` FROM `Memberships` `ca1` WHERE `ca1`.`UserId` = @subject AND `ca1`.`Depot` = @domain",
			wantPG:      `SELECT "ca1"."Team" AS "fact" FROM "Memberships" "ca1" WHERE "ca1"."UserId" = @subject AND "ca1"."Depot" = @domain`,
			wantNames:   []string{"domain", "subject"},
		},
		{
			name:        "a dotted subject set reads each anchor row's value through its hops",
			fact:        "regions",
			set:         true,
			partitioned: true,
			wantSpanner: "SELECT (SELECT `ca2`.`Region` FROM `Hubs` `ca2` WHERE `ca2`.`Id` = `ca1`.`HubId`) AS `fact` FROM `Memberships` `ca1` WHERE `ca1`.`UserId` = @subject AND `ca1`.`Depot` = @domain",
			wantPG:      `SELECT (SELECT "ca2"."Region" FROM "Hubs" "ca2" WHERE "ca2"."Id" = "ca1"."HubId") AS "fact" FROM "Memberships" "ca1" WHERE "ca1"."UserId" = @subject AND "ca1"."Depot" = @domain`,
			wantNames:   []string{"domain", "subject"},
		},
		{
			name:        "a global request filters a partitioned anchor to no partition",
			fact:        "teams",
			set:         true,
			wantSpanner: "SELECT `ca1`.`Team` AS `fact` FROM `Memberships` `ca1` WHERE `ca1`.`UserId` = @subject",
			wantPG:      `SELECT "ca1"."Team" AS "fact" FROM "Memberships" "ca1" WHERE "ca1"."UserId" = @subject`,
			wantNames:   []string{"subject"},
		},
		{name: "a set the application does not declare", fact: "nope", set: true, wantErr: "subject.nope is not a declared subject set"},
		{name: "a value the application does not declare", fact: "nope", wantErr: "subject.nope is not a declared subject value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collection := semanticCollection(t)
			for dbType, want := range map[DBType]string{SpannerDBType: tt.wantSpanner, PostgresDBType: tt.wantPG} {
				sql, registry, err := subjectFactStatement(collection, dbType, tt.partitioned, tt.fact, tt.set)
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("subjectFactStatement(%s) error = %v, want containing %q", dbType, err, tt.wantErr)
					}

					continue
				}
				if err != nil {
					t.Fatalf("subjectFactStatement(%s) error = %v", dbType, err)
				}
				if sql != want {
					t.Errorf("subjectFactStatement(%s) =\n%s\nwant\n%s", dbType, sql, want)
				}
				if names := slices.Sorted(slices.Values(registry.referencedNames())); !slices.Equal(names, tt.wantNames) {
					t.Errorf("subjectFactStatement(%s) names %v, want %v", dbType, names, tt.wantNames)
				}
			}
		})
	}
}

// TestReadSubjectFacts_otherDatabase pins the refusal on a database whose client
// reads nothing yet: the statement is rendered for it, and nothing runs it.
func TestReadSubjectFacts_otherDatabase(t *testing.T) {
	t.Parallel()

	_, _, err := readSubjectFacts(t.Context(), postgresFactSource{}, semanticCollection(t), testScope, "u1", []string{"teams"}, nil)
	if err == nil || !strings.Contains(err.Error(), "reading one is implemented for spanner alone") {
		t.Fatalf("readSubjectFacts() error = %v, want the refusal naming the database it is implemented for", err)
	}
}

// postgresFactSource is a client of the database nothing reads from yet.
type postgresFactSource struct{}

func (postgresFactSource) DBType() DBType { return PostgresDBType }

func (postgresFactSource) SpannerReadOnlyTransaction() spxapi.Querier {
	panic("postgresFactSource.SpannerReadOnlyTransaction() should never be called")
}

const semanticFactResource = accesstypes.Resource("semanticFactRows")

// semanticFactRow is a computed row whose fields the caller's facts are compared
// against: one of each type the semantic fixture's anchors yield.
type semanticFactRow struct {
	Name   string
	Team   string
	Tier   int64
	Region string
	Ratio  float64
	Active bool
	At     time.Time
	Day    civil.Date
}

func (semanticFactRow) Resource() accesstypes.Resource { return semanticFactResource }

func (semanticFactRow) DefaultConfig() Config { return Config{} }

type semanticFactRequest struct {
	Name string `json:"name" perm:"-"`
}

// semanticFactConditions pairs each of the fixture's subject sets and values with a
// field of its comparison type, as deploy validation admits them.
var semanticFactConditions = []string{
	"team IN subject.teams",
	"team NOT IN subject.teams",
	"tier IN subject.tiers",
	"region IN subject.regions",
	"region NOT IN subject.regions OR team IN subject.teams",
	"team = subject.nickname",
	"tier <= subject.quota",
	"ratio >= subject.rate",
	"tier < subject.allowance",
	"active = subject.active",
	"at < subject.clearedUntil",
	"day >= subject.startDate",
	"region = subject.homeRegion",
	"region != subject.homeRegion AND tier IN subject.tiers",
}

// semanticFactCollection is the semantic fixture's collection with the computed
// resource beside its anchors.
func semanticFactCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	resources := append(semanticResources(), CollectionResource{
		Name:        semanticFactResource,
		Scope:       accesstypes.DomainPermissionScope,
		Computed:    true,
		Permissions: []accesstypes.Permission{accesstypes.List},
		Attributes: []AttributeData{
			{Name: "team", Column: "Team", Type: AttributeTypeString},
			{Name: "tier", Column: "Tier", Type: AttributeTypeNumber},
			{Name: "region", Column: "Region", Type: AttributeTypeString},
			{Name: "ratio", Column: "Ratio", Type: AttributeTypeNumber},
			{Name: "active", Column: "Active", Type: AttributeTypeBool},
			{Name: "at", Column: "At", Type: AttributeTypeTimestamp},
			{Name: "day", Column: "Day", Type: AttributeTypeDate},
		},
	})
	g, err := NewGeneratedCollection(CollectionData{Resources: resources})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// factTally counts what checkSubjectFacts compared, so the suite can refuse a run
// in which the facts never decided a row either way.
type factTally struct {
	sets, values  int
	shown, hidden int
}

// checkSubjectFacts holds the facts read from the database to the fixture's own
// model of the world, and a computed list judged under conditions that read them
// to the evaluator over that model.
func (h *semanticHarness) checkSubjectFacts(t *testing.T, c *semanticCase, collection *GeneratedCollection, tally *factTally) {
	t.Helper()

	w := c.world
	model := w.image(&semanticParcel{}, nil)
	client := NewSpannerClient(h.client)

	setNames := []string{"teams", "tiers", "regions"}
	valueNames := []string{"nickname", "quota", "rate", "allowance", "active", "clearedUntil", "startDate", "homeRegion"}
	sets, values, err := readSubjectFacts(t.Context(), client, collection, w.scope(), accesstypes.User(w.requester), setNames, valueNames)
	if err != nil {
		t.Fatalf("%s\nreadSubjectFacts() error = %v", c.report(), err)
	}
	for _, name := range setNames {
		got, want := slices.Clone(sets[name]), slices.Clone(model.SubjectSets[name])
		slices.SortFunc(got, compareSemantic)
		slices.SortFunc(want, compareSemantic)
		if !slices.EqualFunc(got, want, func(a, b any) bool { return compareSemantic(a, b) == 0 }) {
			t.Errorf("%s\nsubject.%s read %v, want the model's %v", c.report(), name, got, want)
		}
		tally.sets += len(got)
	}
	for _, name := range valueNames {
		got, want := values[name], model.SubjectValues[name]
		if (got == nil) != (want == nil) || (got != nil && compareSemantic(got, want) != 0) {
			t.Errorf("%s\nsubject.%s read %v, want the model's %v", c.report(), name, got, want)
		}
		if got != nil {
			tally.values++
		}
	}

	// A computed list of rows drawn from the same pools the anchors' values come
	// from, so a row meets the caller's facts often enough to be decided both ways.
	rows := h.drawFactRows(w.index)

	decoder := MustNewComputedQueryDecoder[semanticFactRow, semanticFactRequest](SpannerDBType, accesstypes.List).
		WithCollection(collection).
		WithSubjectFacts(client)
	for _, source := range semanticFactConditions {
		expr := mustCondition(source).Expr()
		perms := &semanticPermissions{
			user: accesstypes.User(w.requester),
			decisions: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.List: {
				semanticFactResource: conditionalOn(semanticFactResource, source),
			}},
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?columns=name&limit=all", http.NoBody)
		qSet, err := decoder.Decode(req, perms, w.scope())
		if err != nil {
			t.Fatalf("%s\n%s: Decode() error = %v", c.report(), source, err)
		}
		page, err := qSet.Collect(func(yield func(*semanticFactRow, error) bool) {
			for _, row := range rows {
				if !yield(row, nil) {
					return
				}
			}
		})
		if err != nil {
			t.Fatalf("%s\n%s: Collect() error = %v", c.report(), source, err)
		}
		var got []string
		for _, row := range page.Rows() {
			got = append(got, row.Name)
		}

		var want []string
		for _, row := range rows {
			truth, err := condition.Evaluate(expr, &condition.Image{
				Pre: map[string]any{
					"team": row.Team, "tier": row.Tier, "region": row.Region, "ratio": row.Ratio,
					"active": row.Active, "at": row.At.UTC(), "day": semanticDate(row.Day),
				},
				Subject:       w.requester,
				SubjectSets:   model.SubjectSets,
				SubjectValues: model.SubjectValues,
			})
			if err != nil {
				t.Fatalf("%s\n%s: condition.Evaluate() error = %v", c.report(), source, err)
			}
			if truth.Permits() {
				want = append(want, row.Name)
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s\n%s: the computed list shows %v, want %v", c.report(), source, got, want)
		}
		tally.shown += len(got)
		tally.hidden += len(rows) - len(got)
	}
}
