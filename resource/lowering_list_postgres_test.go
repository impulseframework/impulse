package resource

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/accesstypes/condition"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
)

// TestLowerRowCondition_listOnPostgres runs the PostgreSQL rendering of
// conditions that read a list attribute on a real PostgreSQL and holds each
// row's answer to the evaluator's. The semantic differential proves the rule
// on the Spanner emulator with random conditions; this is the same rule in the
// other dialect, over the shapes a list can take: one item, several, an item
// the condition refuses, no item, no list, and an item with no value.
func TestLowerRowCondition_listOnPostgres(t *testing.T) {
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
	db, err := container.CreateDatabase(ctx, "list-conditions")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.Exec(ctx, `CREATE TABLE "Invitations" ("Id" text PRIMARY KEY, "Pending" boolean, "Email" text, "RoleNames" text[], "Labels" text[], "SenderId" text, "Tenant" text)`); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	str := func(s string) *string {
		return &s
	}
	yes, no := true, false
	rows := []struct {
		id      string
		pending *bool
		email   string
		roles   []*string
	}{
		{id: "one-role", pending: &yes, email: "staff", roles: []*string{str("Staff")}},
		{id: "two-roles", pending: &yes, email: "viewer", roles: []*string{str("Staff"), str("Viewer")}},
		{id: "a-refused-role", pending: &yes, email: "admin", roles: []*string{str("Staff"), str("Admin")}},
		{id: "no-role", pending: &yes, email: "nobody", roles: []*string{}},
		{id: "no-list", pending: &yes, email: "nothing", roles: nil},
		{id: "an-item-with-no-value", pending: &yes, email: "partial", roles: []*string{str("Staff"), nil}},
		{id: "not-pending", pending: &no, email: "gone", roles: []*string{str("Staff")}},
		{id: "pending-unknown", pending: nil, email: "unknown", roles: []*string{str("Staff")}},
		{id: "the-requester", pending: &yes, email: "self", roles: []*string{str("erin")}},
	}
	for _, row := range rows {
		if _, err := db.Exec(ctx, `INSERT INTO "Invitations" ("Id", "Pending", "Email", "RoleNames") VALUES ($1, $2, $3, $4)`, row.id, row.pending, row.email, row.roles); err != nil {
			t.Fatalf("inserting %s: %v", row.id, err)
		}
	}

	collection, bindings := listFixtureBindings(t)
	const requester = "erin"
	for _, source := range []string{
		"role != 'Admin'",
		"pending = true AND role != 'Admin'",
		"pending = true AND (role IS NULL OR role != 'Admin')",
		"role IN ('Staff', 'Viewer')",
		"role NOT IN ('Admin', 'Owner')",
		"NOT (role = 'Admin')",
		"role = 'Staff' OR role = 'Viewer'",
		"role IS NOT NULL",
		"role = subject",
		"role != subject OR email = 'self'",
		"role > 'Admin'",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()

			expr, err := condition.Parse(source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", source, err)
			}
			registry := newParamRegistry()
			sql, err := lowerToSQL(expr, &loweringContext{outer: "Invitations", bindings: bindings, collection: collection}, newSQLGenerator(PostgreSQL), registry)
			if err != nil {
				t.Fatalf("lowerToSQL(%q) error = %v", source, err)
			}
			args := pgx.NamedArgs{subjectParamName: requester}
			for _, param := range registry.boundParams() {
				args[param.Name] = param.Value
			}
			result, err := db.Query(ctx, `SELECT "Invitations"."Id", (`+sql+`) FROM "Invitations"`, args)
			if err != nil {
				t.Fatalf("running %s: %v", sql, err)
			}
			got := make(map[string]bool, len(rows))
			for result.Next() {
				var (
					id string
					ok *bool
				)
				if err := result.Scan(&id, &ok); err != nil {
					t.Fatalf("scanning: %v", err)
				}
				if ok == nil {
					t.Errorf("%s: the rendered predicate is NULL; a list condition is TRUE or FALSE", id)

					continue
				}
				got[id] = *ok
			}
			if err := result.Err(); err != nil {
				t.Fatalf("reading the answers of %s: %v", sql, err)
			}

			for _, row := range rows {
				items := condition.List(nil)
				for _, role := range row.roles {
					if role == nil {
						items = append(items, nil)
					} else {
						items = append(items, *role)
					}
				}
				pre := map[string]any{"email": row.email, "role": items}
				if row.pending != nil {
					pre["pending"] = *row.pending
				}
				truth, err := condition.Evaluate(expr, &condition.Image{Pre: pre, Subject: requester})
				if err != nil {
					t.Fatalf("Evaluate(%q) over %s error = %v", source, row.id, err)
				}
				if want := truth.Permits(); got[row.id] != want {
					t.Errorf("%s: PostgreSQL answers %v, the evaluator %v\n%s", row.id, got[row.id], want, sql)
				}
			}
		})
	}
}
