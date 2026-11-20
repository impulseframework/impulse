package resource

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
)

// groupDatabase makes a database with the fixture's tables on the shared emulator, with
// a group kept in each yard and one kept for the whole application.
func groupDatabase(t *testing.T, name string) *spanner.Client {
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
	if err := db.MigrateUp("file://testdata/groupcolumns/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	if _, err := db.Client.Apply(context.Background(), []*spanner.Mutation{
		spanner.InsertMap("CastGroups", map[string]any{"Id": groupOfNorth, "DomainId": groupYardNorth, "Name": "North crew"}),
		spanner.InsertMap("CastGroups", map[string]any{"Id": groupOfSouth, "DomainId": groupYardSouth, "Name": "South crew"}),
		spanner.InsertMap("CastGroups", map[string]any{"Id": groupOfEveryone, "DomainId": "", "Name": "Everyone"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	return db.Client
}

// storedGroupRow is what a fixture row holds of its yard (or its share) and its group.
type storedGroupRow struct {
	Anchor string
	Group  string
}

// readGroupRow reads one row of a fixture table past the library; nil when it is not
// there. A NULL group reads as the empty string.
func readGroupRow(t *testing.T, client *spanner.Client, table, anchorColumn, id string) *storedGroupRow {
	t.Helper()

	row, err := client.Single().ReadRow(context.Background(), table, spanner.Key{id}, []string{anchorColumn, "GroupId"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil
		}
		t.Fatalf("spanner.ReadOnlyTransaction.ReadRow(%s %s) error = %v", table, id, err)
	}
	var anchor string
	var group spanner.NullString
	if err := row.Columns(&anchor, &group); err != nil {
		t.Fatalf("spanner.Row.Columns(%s %s) error = %v", table, id, err)
	}

	return &storedGroupRow{Anchor: anchor, Group: group.StringVal}
}

// groupSharePatch builds a patch of one share. An empty yard or a nil group leaves the
// field out of the patch.
func groupSharePatch(patchType PatchType, id, yard string, group *string) *PatchSet[groupShare] {
	p := NewPatchSet(NewMetadata[groupShare]()).SetPatchType(patchType)
	p.SetKey("ID", id)
	if yard != "" {
		p.Set("Yard", yard)
	}
	if group != nil {
		p.Set("GroupID", group)
	}
	if patchType == CreatePatchType {
		p.Set("Note", "a note")
	}

	return p
}

// TestPatchSet_groupColumns_create pins the rule for a create, through the library's
// own write on the emulator: a row is written with a group its yard keeps, and refused
// with nothing written when it names another yard's group, a group kept for the whole
// application or an id that is no group's.
func TestPatchSet_groupColumns_create(t *testing.T) {
	t.Parallel()

	db := groupDatabase(t, "group-create")
	client := NewSpannerClient(db)
	ctx := context.Background()

	for i, tt := range groupShareCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id := fmt.Sprintf("share-%d", i)
			group := tt.group
			err := groupSharePatch(CreatePatchType, id, tt.anchor, &group).Apply(ctx, client)
			tt.judge(t, err)
			stored := readGroupRow(t, db, "GroupShares", "Yard", id)
			if tt.refused && stored != nil {
				t.Errorf("the refused row is stored: %+v", stored)
			}
			if !tt.refused && (stored == nil || stored.Group != tt.group) {
				t.Errorf("the stored row = %+v, want it to name %s", stored, tt.group)
			}
		})
	}

	t.Run("a row that names no group is written", func(t *testing.T) {
		t.Parallel()

		if err := groupSharePatch(CreatePatchType, "share-none", groupYardNorth, nil).Apply(ctx, client); err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		var none *string
		if err := groupSharePatch(CreatePatchType, "share-null", groupYardNorth, nil).Set("GroupID", none).Apply(ctx, client); err != nil {
			t.Fatalf("Apply() with a NULL group error = %v", err)
		}
	})
}

// TestPatchSet_groupColumns_update pins the rule for an update: the row's yard is read
// from the stored row when the patch does not carry it, an update that moves a row
// which names a group is held as one that names it, and update-or-create is held on
// both of its ways.
func TestPatchSet_groupColumns_update(t *testing.T) {
	t.Parallel()

	db := groupDatabase(t, "group-update")
	client := NewSpannerClient(db)
	ctx := context.Background()
	share := func(id, yard string, group any) *spanner.Mutation {
		return spanner.InsertMap("GroupShares", map[string]any{"Id": id, "Yard": yard, "GroupId": group, "Note": "stored"})
	}
	var none *string
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		share("bare-1", groupYardNorth, nil), share("bare-2", groupYardNorth, nil), share("bare-3", groupYardNorth, nil),
		share("held-1", groupYardNorth, groupOfNorth), share("held-2", groupYardNorth, groupOfNorth),
		share("held-3", groupYardNorth, groupOfNorth), share("held-4", groupYardNorth, groupOfNorth),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	north, south, everyone := groupOfNorth, groupOfSouth, groupOfEveryone

	tests := []struct {
		name    string
		patch   *PatchSet[groupShare]
		id      string
		refused bool
		// want is the row afterwards; nil for no row.
		want *storedGroupRow
	}{
		{
			name: "naming a group the stored row's yard keeps is written", id: "bare-1",
			patch: groupSharePatch(UpdatePatchType, "bare-1", "", &north),
			want:  &storedGroupRow{Anchor: groupYardNorth, Group: groupOfNorth},
		},
		{
			name: "naming another yard's group is refused by the stored row's yard", id: "bare-2",
			patch: groupSharePatch(UpdatePatchType, "bare-2", "", &south), refused: true,
			want: &storedGroupRow{Anchor: groupYardNorth},
		},
		{
			name: "naming a group kept for the whole application is refused", id: "bare-3",
			patch: groupSharePatch(UpdatePatchType, "bare-3", "", &everyone), refused: true,
			want: &storedGroupRow{Anchor: groupYardNorth},
		},
		{
			name: "moving a row that names a group to another yard is refused", id: "held-1",
			patch: groupSharePatch(UpdatePatchType, "held-1", groupYardSouth, nil), refused: true,
			want: &storedGroupRow{Anchor: groupYardNorth, Group: groupOfNorth},
		},
		{
			name: "moving a row with the group of the yard it moves to is written", id: "held-2",
			patch: groupSharePatch(UpdatePatchType, "held-2", groupYardSouth, &south),
			want:  &storedGroupRow{Anchor: groupYardSouth, Group: groupOfSouth},
		},
		{
			name: "moving a row and clearing its group is written", id: "held-3",
			patch: groupSharePatch(UpdatePatchType, "held-3", groupYardSouth, nil).Set("GroupID", none),
			want:  &storedGroupRow{Anchor: groupYardSouth},
		},
		{
			name: "an update that touches neither the group nor the yard is written", id: "held-4",
			patch: groupSharePatch(UpdatePatchType, "held-4", "", nil).Set("Note", "changed"),
			want:  &storedGroupRow{Anchor: groupYardNorth, Group: groupOfNorth},
		},
		{
			name: "update-or-create of a row that is not there, naming another yard's group, is refused", id: "new-1",
			patch: groupSharePatch(UpdatePatchType, "new-1", groupYardNorth, &south).Set("Note", "a note").OrCreate(nil), refused: true,
		},
		{
			name: "update-or-create of a row that is not there, naming its yard's group, creates it", id: "new-2",
			patch: groupSharePatch(UpdatePatchType, "new-2", groupYardNorth, &north).Set("Note", "a note").OrCreate(nil),
			want:  &storedGroupRow{Anchor: groupYardNorth, Group: groupOfNorth},
		},
		{
			name: "a create-or-update that names another yard's group is refused", id: "new-3",
			patch: groupSharePatch(CreateOrUpdatePatchType, "new-3", groupYardNorth, &south).Set("Note", "a note"), refused: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.patch.Apply(ctx, client)
			c := groupCase{refused: tt.refused}
			c.judge(t, err)
			got := readGroupRow(t, db, "GroupShares", "Yard", tt.id)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("the row is stored: %+v, want none", got)
			case tt.want != nil && (got == nil || *got != *tt.want):
				t.Errorf("the stored row = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("an update of a row that is not there is answered as any other, not by this rule", func(t *testing.T) {
		t.Parallel()

		err := groupSharePatch(UpdatePatchType, "nowhere", "", &south).Apply(ctx, client)
		if err == nil {
			t.Fatal("Apply() error = nil, want the update of a missing row refused")
		}
		if httpio.Message(err) == groupRefusal {
			t.Errorf("the answer is this rule's: %v", err)
		}
	})
}

// TestPatchSet_groupColumns_parent pins the rule for a row that reaches its yard
// through a parent: the yard is the parent's, read in the same transaction.
func TestPatchSet_groupColumns_parent(t *testing.T) {
	t.Parallel()

	db := groupDatabase(t, "group-parent")
	client := NewSpannerClient(db)
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("GroupShares", map[string]any{"Id": "north-share", "Yard": groupYardNorth, "Note": "stored"}),
		spanner.InsertMap("GroupShares", map[string]any{"Id": "south-share", "Yard": groupYardSouth, "Note": "stored"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	tests := []groupCase{
		{name: "a group kept in the parent's yard is written", anchor: "north-share", group: groupOfNorth},
		{name: "a group kept in another yard is refused", anchor: "north-share", group: groupOfSouth, refused: true},
		{name: "a group kept for the whole application is refused", anchor: "north-share", group: groupOfEveryone, refused: true},
		{name: "the other yard's own group is written under its share", anchor: "south-share", group: groupOfSouth},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id := fmt.Sprintf("seal-%d", i)
			group := tt.group
			p := NewPatchSet(NewMetadata[groupSeal]()).SetPatchType(CreatePatchType)
			p.SetKey("ID", id)
			p.Set("ShareID", tt.anchor)
			p.Set("GroupID", &group)
			tt.judge(t, p.Apply(ctx, client))
			stored := readGroupRow(t, db, "GroupSeals", "ShareId", id)
			if tt.refused != (stored == nil) {
				t.Errorf("the stored row = %+v, refused = %t", stored, tt.refused)
			}
		})
	}

	t.Run("a row under a parent that is not there fails on its foreign key, not by this rule", func(t *testing.T) {
		t.Parallel()

		group := groupOfNorth
		p := NewPatchSet(NewMetadata[groupSeal]()).SetPatchType(CreatePatchType)
		p.SetKey("ID", "seal-orphan")
		p.Set("ShareID", "no-share")
		p.Set("GroupID", &group)
		err := p.Apply(ctx, client)
		if err == nil {
			t.Fatal("Apply() error = nil, want the foreign key's refusal")
		}
		if httpio.Message(err) == groupRefusal {
			t.Errorf("the answer is this rule's: %v", err)
		}
	})
}
