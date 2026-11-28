package resource

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// writeRefusalCollection is the record fixture's collection with two more attributes of a
// task, the project it is in and the project it came from, for a grant's condition on a
// write of a task to read by name.
func writeRefusalCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	data := onRecordConditionData()
	for i := range data.Resources {
		if data.Resources[i].Name == "Tasks" {
			data.Resources[i].Attributes = append(data.Resources[i].Attributes,
				AttributeData{Name: "project", Column: "ProjectId", Type: AttributeTypeString},
				AttributeData{Name: "formerProject", Column: "FormerProjectId", Type: AttributeTypeString},
			)
		}
	}
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// heldUnder is a decision on tasks held under one condition.
func heldUnder(source string) accesstypes.Decision {
	return accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{"Tasks"}, Condition: mustCondition(source)})
}

// TestPatchSet_writeRefusal_onSpanner pins which sentence a write its grant's condition
// refuses is told. Where a value the write sends is what the condition read and the row
// would take another, it is the value's ("but not to this value."), naming the fields
// whose values the condition read; where the stored row refuses the write whatever it
// sends, it is the row's ("but not of this one."); and a write that fails both ways is
// told the row's.
func TestPatchSet_writeRefusal_onSpanner(t *testing.T) {
	t.Parallel()

	client := namedRowsDatabase(t, "write-refusal")
	db := NewSpannerClient(client)
	collection := writeRefusalCollection(t)
	ctx := context.Background()
	project := func(id string) *string { return &id }

	const (
		theValue = "You may change the project id of this named task, but not to this value."
		theRow   = "You may change the project id of some tasks here, but not of this one."
	)
	// t2 is in p2 and came from p1; t3 is in p2 and came from no project. Each write
	// sends the project a task is in.
	tests := []struct {
		name      string
		condition string
		task      string
		sent      string
		said      string
	}{
		{name: "the value alone is read, and fails", condition: "new.project = 'p1'", task: "t3", sent: "p3", said: theValue},
		{name: "the stored row alone is read, and fails", condition: "formerProject = 'p1'", task: "t3", sent: "p1", said: theRow},
		{name: "the row and the value, the value failing", condition: "formerProject = 'p1' AND new.project = 'p1'", task: "t2", sent: "p3", said: theValue},
		{name: "the row and the value, the row failing", condition: "formerProject = 'p1' AND new.project = 'p1'", task: "t3", sent: "p1", said: theRow},
		{name: "the row and the value, both failing", condition: "formerProject = 'p1' AND new.project = 'p1'", task: "t3", sent: "p3", said: theRow},
		{name: "the row or the value, both failing", condition: "formerProject = 'p1' OR new.project = 'p1'", task: "t3", sent: "p3", said: theValue},
		{name: "a value the write does not send, read off the stored row", condition: "new.formerProject = 'p1'", task: "t3", sent: "p1", said: theRow},
		{name: "a value that may not be sent", condition: "NOT (new.project = 'p1')", task: "t3", sent: "p1", said: theValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caller := listsEveryProject()
			caller.update = map[accesstypes.Resource]accesstypes.Decision{"Tasks": heldUnder(tt.condition)}
			w := &namedWrite{patchType: UpdatePatchType, caller: caller, id: tt.task, columns: map[accesstypes.Field]any{"ProjectID": project(tt.sent)}}
			err := taskPatch(t, collection, w).Apply(ctx, db)
			if !httpio.HasForbidden(err) {
				t.Fatalf("error = %v, want a forbidden", err)
			}
			if got := httpio.Message(err); got != tt.said {
				t.Errorf("the refusal reads %q, want %q", got, tt.said)
			}
		})
	}

	t.Run("a new row whose value fails", func(t *testing.T) {
		t.Parallel()

		caller := listsEveryProject()
		caller.create = map[accesstypes.Resource]accesstypes.Decision{"Tasks": heldUnder("project = 'p1'")}
		columns := map[accesstypes.Field]any{"ProjectID": project("p2"), "FormerProjectID": project("p1")}
		err := taskPatch(t, collection, &namedWrite{patchType: CreatePatchType, caller: caller, id: "wr-new", columns: columns}).Apply(ctx, db)
		if !httpio.HasForbidden(err) {
			t.Fatalf("error = %v, want a forbidden", err)
		}
		if got, want := httpio.Message(err), "You may add tasks here, but not with this value for the project id."; got != want {
			t.Errorf("the refusal reads %q, want %q: the field the condition reads, and none of the others", got, want)
		}
		if _, found := storedColumn(t, client, "wr-new", "ProjectId"); found {
			t.Error("the refused task is stored")
		}
	})
}
