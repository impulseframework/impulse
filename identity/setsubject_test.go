package identity

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// reachWrite is the write the rules for a row that reaches a person without naming
// them are asked about: the fields it sets, the values the real person's set holds,
// and the facts read through the row for them. It records what was asked, and of whom.
type reachWrite struct {
	fields     map[accesstypes.Field]any
	inSet      []any
	readFor    bool
	refused    error
	askedOf    []accesstypes.User
	askedSets  []string
	askedAbout []any
	askedFacts []string
}

func (w *reachWrite) Permitted(context.Context, resource.ReadWriteTransaction) error {
	return w.refused
}

func (w *reachWrite) IsSet(field accesstypes.Field) bool {
	_, ok := w.fields[field]

	return ok
}

func (w *reachWrite) Get(field accesstypes.Field) any {
	return w.fields[field]
}

func (w *reachWrite) SubjectSetHolds(_ context.Context, _ resource.ReadWriteTransaction, set string, subject accesstypes.User, value any) (bool, error) {
	w.askedOf = append(w.askedOf, subject)
	w.askedSets = append(w.askedSets, set)
	w.askedAbout = append(w.askedAbout, value)
	for _, held := range w.inSet {
		if held == value {
			return true, nil
		}
	}

	return false, nil
}

func (w *reachWrite) SubjectReadThrough(_ context.Context, _ resource.ReadWriteTransaction, name string, _ accesstypes.Field, subject accesstypes.User, _ resource.KeySet) (bool, error) {
	w.askedOf = append(w.askedOf, subject)
	w.askedFacts = append(w.askedFacts, name)

	return w.readFor, nil
}

// TestRefuseImpersonatorAsSetSubject pins the rule on a row that names a set the real
// person is in: in a session that acts as someone else they add no such row, change
// and remove none, and move none onto their own set, while the same write for a set
// they are not in goes through. The set is asked about the real person, never the
// person acted as, and a session of the person's own asks nothing.
func TestRefuseImpersonatorAsSetSubject(t *testing.T) {
	t.Parallel()

	acting := viewedBy(t.Context(), userID)
	passKey := resource.KeySet{}.Add("ID", "pass-1")
	holds := func(value any) func() (any, bool, error) {
		return func() (any, bool, error) {
			return value, true, nil
		}
	}
	missing := func() (any, bool, error) {
		return nil, false, nil
	}
	var none *string

	tests := []struct {
		name    string
		ctx     context.Context
		op      resource.OperationType
		key     resource.KeySet
		stored  func() (any, bool, error)
		fields  map[accesstypes.Field]any
		wantAct string
		// wantAsked is the values the set was asked about, in order.
		wantAsked []any
	}{
		{
			name: "a row added for a set the real person is in is refused", ctx: acting, op: resource.OperationCreate,
			fields: map[accesstypes.Field]any{"SquadronID": "mine"}, wantAct: "add a hangar pass record for one of your own squadrons", wantAsked: []any{"mine"},
		},
		{
			name: "a row added for another set goes through", ctx: acting, op: resource.OperationCreate,
			fields: map[accesstypes.Field]any{"SquadronID": "theirs"}, wantAsked: []any{"theirs"},
		},
		{
			name: "a row added with no value in the column asks nothing", ctx: acting, op: resource.OperationCreate,
			fields: map[accesstypes.Field]any{"UserID": "someone"},
		},
		{
			name: "a row added with a null in the column asks nothing", ctx: acting, op: resource.OperationCreate,
			fields: map[accesstypes.Field]any{"SquadronID": none},
		},
		{
			name: "a change to a row of the real person's set is refused, whatever it sets", ctx: acting, op: resource.OperationUpdate, key: passKey,
			stored: holds("mine"), fields: map[accesstypes.Field]any{"ExpiresAt": "later"}, wantAct: "change a hangar pass record of one of your own squadrons", wantAsked: []any{"mine"},
		},
		{
			name: "a row moved off the real person's set is a change of theirs", ctx: acting, op: resource.OperationUpdate, key: passKey,
			stored: holds("mine"), fields: map[accesstypes.Field]any{"SquadronID": "theirs"}, wantAct: "change a hangar pass record of one of your own squadrons", wantAsked: []any{"mine", "theirs"},
		},
		{
			name: "another row moved onto the real person's set is refused", ctx: acting, op: resource.OperationUpdate, key: passKey,
			stored: holds("theirs"), fields: map[accesstypes.Field]any{"SquadronID": "mine"}, wantAct: "move a hangar pass record to one of your own squadrons", wantAsked: []any{"theirs", "mine"},
		},
		{
			name: "a change to another set's row goes through", ctx: acting, op: resource.OperationUpdate, key: passKey,
			stored: holds("theirs"), fields: map[accesstypes.Field]any{"ExpiresAt": "later"}, wantAsked: []any{"theirs"},
		},
		{
			name: "a change to a row that is not there asks about what it sets alone", ctx: acting, op: resource.OperationUpdate, key: passKey,
			stored: missing, fields: map[accesstypes.Field]any{"SquadronID": "theirs"}, wantAsked: []any{"theirs"},
		},
		{
			name: "a removal of a row of the real person's set is refused", ctx: acting, op: resource.OperationDelete, key: passKey,
			stored: holds("mine"), wantAct: "remove a hangar pass record of one of your own squadrons", wantAsked: []any{"mine"},
		},
		{
			name: "a removal of another set's row goes through", ctx: acting, op: resource.OperationDelete, key: passKey,
			stored: holds("theirs"), wantAsked: []any{"theirs"},
		},
		{
			name: "a column of the key holds the same value before and after", ctx: acting, op: resource.OperationUpdate, key: resource.KeySet{}.Add("SquadronID", "mine"),
			fields: map[accesstypes.Field]any{"ExpiresAt": "later"}, wantAct: "change a hangar pass record of one of your own squadrons", wantAsked: []any{"mine"},
		},
		{
			name: "a session of the person's own asks nothing", ctx: asCaller(t.Context(), dana), op: resource.OperationCreate,
			fields: map[accesstypes.Field]any{"SquadronID": "mine"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			write := &reachWrite{fields: tt.fields, inSet: []any{"mine"}}
			stored := tt.stored
			if stored == nil {
				stored = func() (any, bool, error) {
					t.Error("the rule read the stored row, want no read")

					return nil, false, nil
				}
			}
			err := refuseImpersonatorAsSetSubject(tt.ctx, stubTxn{}, tt.op, tt.key, write, stored, "hangar pass", "SquadronID", "squadrons")
			if tt.wantAct == "" {
				if err != nil {
					t.Fatalf("refuseImpersonatorAsSetSubject() error = %v, want nil", err)
				}
			} else {
				want := "You are acting as someone else, so you cannot " + tt.wantAct + "."
				if err == nil || !httpio.HasForbidden(err) || httpio.Message(err) != want {
					t.Fatalf("refuseImpersonatorAsSetSubject() error = %v, want Forbidden saying %q", err, want)
				}
			}
			if len(write.askedAbout) != len(tt.wantAsked) {
				t.Fatalf("the set was asked about %v, want %v", write.askedAbout, tt.wantAsked)
			}
			for i, value := range tt.wantAsked {
				if write.askedAbout[i] != value {
					t.Errorf("the set was asked about %v, want %v", write.askedAbout, tt.wantAsked)
				}
				if write.askedSets[i] != "squadrons" || write.askedOf[i] != User(userID) {
					t.Errorf("question %d asked subject.%s of %s, want subject.squadrons of the real person %s", i, write.askedSets[i], write.askedOf[i], User(userID))
				}
			}
		})
	}
}

// TestRefuseImpersonatorAsSetSubject_setInWords pins how the refusal names the set: the
// person is told it in words ("crewGroups" is "crew groups"), and the line the refusal
// is logged with names it as the code writes it, in each of the four acts. A set named
// in one lower-case word reads the same both ways. It is not parallel: the standard
// logger, which a context with no logger of its own writes through, is the process's
// own.
func TestRefuseImpersonatorAsSetSubject_setInWords(t *testing.T) { //nolint:paralleltest // it reads the standard logger's output, which is the whole process's
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	acting := viewedBy(t.Context(), userID)
	passKey := resource.KeySet{}.Add("ID", "pass-1")
	holds := func(value any) func() (any, bool, error) {
		return func() (any, bool, error) {
			return value, true, nil
		}
	}

	tests := []struct {
		set       string
		op        resource.OperationType
		key       resource.KeySet
		stored    func() (any, bool, error)
		fields    map[accesstypes.Field]any
		wantSaid  string
		wantShown string
	}{
		{
			set: "crewGroups", op: resource.OperationCreate, fields: map[accesstypes.Field]any{"GroupID": "mine"},
			wantSaid:  "add a hangar pass record for one of your own crew groups",
			wantShown: "add a hangar pass record for one of their own crewGroups",
		},
		{
			set: "crewGroups", op: resource.OperationDelete, key: passKey, stored: holds("mine"),
			wantSaid:  "remove a hangar pass record of one of your own crew groups",
			wantShown: "remove a hangar pass record of one of their own crewGroups",
		},
		{
			set: "crewGroups", op: resource.OperationUpdate, key: passKey, stored: holds("mine"), fields: map[accesstypes.Field]any{"ExpiresAt": "later"},
			wantSaid:  "change a hangar pass record of one of your own crew groups",
			wantShown: "change a hangar pass record of one of their own crewGroups",
		},
		{
			set: "crewGroups", op: resource.OperationUpdate, key: passKey, stored: holds("theirs"), fields: map[accesstypes.Field]any{"GroupID": "mine"},
			wantSaid:  "move a hangar pass record to one of your own crew groups",
			wantShown: "move a hangar pass record to one of their own crewGroups",
		},
		{
			set: "squadrons", op: resource.OperationCreate, fields: map[accesstypes.Field]any{"GroupID": "mine"},
			wantSaid:  "add a hangar pass record for one of your own squadrons",
			wantShown: "add a hangar pass record for one of their own squadrons",
		},
	}

	for _, tt := range tests {
		logged.Reset()
		write := &reachWrite{fields: tt.fields, inSet: []any{"mine"}}
		stored := tt.stored
		if stored == nil {
			stored = func() (any, bool, error) {
				return nil, false, nil
			}
		}

		err := refuseImpersonatorAsSetSubject(acting, stubTxn{}, tt.op, tt.key, write, stored, "hangar pass", "GroupID", tt.set)
		want := "You are acting as someone else, so you cannot " + tt.wantSaid + "."
		if err == nil || !httpio.HasForbidden(err) || httpio.Message(err) != want {
			t.Errorf("subject.%s: refuseImpersonatorAsSetSubject() error = %v, want Forbidden saying %q", tt.set, err, want)
		}
		wantLine := "impersonation: the person who started this impersonated session (" + userID.String() + ") asked to " + tt.wantShown +
			": refused, since nobody is the subject of what they do while they act as someone else"
		if got := strings.Count(logged.String(), wantLine); got != 1 {
			t.Errorf("subject.%s: the log holds the line %q %d times, want once:\n%s", tt.set, wantLine, got, logged.String())
		}
	}
}

// TestRefuseImpersonatorAsReadThroughSubject pins the rule on the row a person's
// standing is read through: only a change that sets the column asks, the real person
// is the one asked about, and their own row's column is not theirs to set while they
// act as someone else.
func TestRefuseImpersonatorAsReadThroughSubject(t *testing.T) {
	t.Parallel()

	acting := viewedBy(t.Context(), userID)
	squadronKey := resource.KeySet{}.Add("ID", "squadron-1")

	tests := []struct {
		name      string
		ctx       context.Context
		op        resource.OperationType
		fields    map[accesstypes.Field]any
		readFor   bool
		wantAct   string
		wantAsked bool
	}{
		{
			name: "the column set on the row the real person's set is read through is refused", ctx: acting, op: resource.OperationUpdate,
			fields: map[accesstypes.Field]any{"WingID": "wing-2"}, readFor: true, wantAsked: true,
			wantAct: "change the wing id of the squadron record that decides your own wings",
		},
		{
			name: "the column set on another row goes through", ctx: acting, op: resource.OperationUpdate,
			fields: map[accesstypes.Field]any{"WingID": "wing-2"}, wantAsked: true,
		},
		{
			name: "a change that leaves the column alone asks nothing", ctx: acting, op: resource.OperationUpdate,
			fields: map[accesstypes.Field]any{"Name": "Red"}, readFor: true,
		},
		{
			name: "an add asks nothing: no row points at it yet", ctx: acting, op: resource.OperationCreate,
			fields: map[accesstypes.Field]any{"WingID": "wing-2"}, readFor: true,
		},
		{
			name: "a removal asks nothing", ctx: acting, op: resource.OperationDelete, readFor: true,
		},
		{
			name: "a session of the person's own asks nothing", ctx: asCaller(t.Context(), dana), op: resource.OperationUpdate,
			fields: map[accesstypes.Field]any{"WingID": "wing-2"}, readFor: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			write := &reachWrite{fields: tt.fields, readFor: tt.readFor}
			err := refuseImpersonatorAsReadThroughSubject(tt.ctx, stubTxn{}, tt.op, squadronKey, write, "squadron", "WingID", "wings")
			if tt.wantAct == "" {
				if err != nil {
					t.Fatalf("refuseImpersonatorAsReadThroughSubject() error = %v, want nil", err)
				}
			} else {
				want := "You are acting as someone else, so you cannot " + tt.wantAct + "."
				if err == nil || !httpio.HasForbidden(err) || httpio.Message(err) != want {
					t.Fatalf("refuseImpersonatorAsReadThroughSubject() error = %v, want Forbidden saying %q", err, want)
				}
			}
			if asked := len(write.askedFacts) == 1; asked != tt.wantAsked {
				t.Fatalf("the fact was asked about %v, want asked = %t", write.askedFacts, tt.wantAsked)
			}
			if tt.wantAsked && (write.askedFacts[0] != "wings" || write.askedOf[0] != User(userID)) {
				t.Errorf("asked subject.%s of %s, want subject.wings of the real person %s", write.askedFacts[0], write.askedOf[0], User(userID))
			}
		})
	}
}

// TestRefuseImpersonatorThroughASet_grantsFirst pins the order of the answers: a caller
// the grants refuse gets the grants' refusal from both rules, and nothing is asked of
// the sets on their behalf.
func TestRefuseImpersonatorThroughASet_grantsFirst(t *testing.T) {
	t.Parallel()

	acting := viewedBy(t.Context(), userID)
	denied := httpio.NewForbiddenMessage("the grants refuse this write")
	stored := func() (any, bool, error) {
		t.Error("the rule read the stored row for a caller the grants refuse")

		return nil, false, nil
	}

	write := &reachWrite{fields: map[accesstypes.Field]any{"SquadronID": "mine", "WingID": "wing-2"}, inSet: []any{"mine"}, readFor: true, refused: denied}
	err := refuseImpersonatorAsSetSubject(acting, stubTxn{}, resource.OperationUpdate, resource.KeySet{}.Add("ID", "pass-1"), write, stored, "hangar pass", "SquadronID", "squadrons")
	if !errors.Is(err, denied) || strings.Contains(httpio.Message(err), "acting as someone else") {
		t.Errorf("refuseImpersonatorAsSetSubject() error = %v, want the grants' own refusal", err)
	}
	err = refuseImpersonatorAsReadThroughSubject(acting, stubTxn{}, resource.OperationUpdate, resource.KeySet{}.Add("ID", "squadron-1"), write, "squadron", "WingID", "wings")
	if !errors.Is(err, denied) || strings.Contains(httpio.Message(err), "acting as someone else") {
		t.Errorf("refuseImpersonatorAsReadThroughSubject() error = %v, want the grants' own refusal", err)
	}
	if len(write.askedOf) != 0 {
		t.Errorf("the sets were asked %d questions for a caller the grants refuse, want none", len(write.askedOf))
	}
}

// TestRefuseImpersonatorThroughASet_cannotAsk pins that a write whose patch cannot ask
// the sets is an error from the exported rules, and never a pass: a patch decoded with
// no collection.
func TestRefuseImpersonatorThroughASet_cannotAsk(t *testing.T) {
	t.Parallel()

	acting := viewedBy(t.Context(), userID)
	txn := resource.NewMockReadWriteTransaction(stubTxn{}, &storedRows[guardedSeat]{})

	patchSet := resource.NewPatchSet(resource.NewMetadata[guardedSeat]()).Set("UserID", "team-1")
	err := RefuseImpersonatorAsSetSubject(acting, txn, resource.OperationCreate, resource.KeySet{}, patchSet, "seat", "UserID", "teams")
	if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "carries no collection") {
		t.Errorf("RefuseImpersonatorAsSetSubject() error = %v, want the fault that names the missing collection", err)
	}
	err = RefuseImpersonatorAsReadThroughSubject(acting, txn, resource.OperationUpdate, resource.KeySet{}.Add("ID", "seat-1"), patchSet, "seat", "UserID", "teams")
	if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "carries no collection") {
		t.Errorf("RefuseImpersonatorAsReadThroughSubject() error = %v, want the fault that names the missing collection", err)
	}
}
