package identity

import (
	"context"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The records the tests of the rule on a row's person write. A seat is keyed by an id
// the record generates and names its person in a column; a badge is keyed by its person
// and what they hold; a pass names a person or nobody, by an id that may be NULL.
type (
	guardedSeat struct {
		ID     impulse.UUID `spanner:"Id"`
		UserID string       `spanner:"UserId"`
		Level  int64        `spanner:"Level"`
	}
	guardedSeatRequest struct {
		UserID string `json:"userId"`
		Level  int64  `json:"level"`
	}
	guardedBadge struct {
		UserID  string `spanner:"UserId"`
		BadgeID string `spanner:"BadgeId"`
		Note    string `spanner:"Note"`
	}
	guardedPass struct {
		ID       impulse.UUID     `spanner:"Id"`
		UserID   *string          `spanner:"UserId"`
		HolderID impulse.NullUUID `spanner:"HolderId"`
		BackupID *impulse.UUID    `spanner:"BackupId"`
		OwnerID  accesstypes.User `spanner:"OwnerId"`
		Door     string           `spanner:"Door"`
	}
)

func (guardedSeat) Resource() accesstypes.Resource {
	return "GuardedSeats"
}

func (guardedBadge) Resource() accesstypes.Resource {
	return "GuardedBadges"
}

func (guardedPass) Resource() accesstypes.Resource {
	return "GuardedPasses"
}

// storedRows is the reader of a record under the transaction: it answers the row a
// test stores, or that there is none, and keeps what was asked of it.
type storedRows[Resource resource.Resourcer] struct {
	row   *Resource
	reads []*resource.Statement
}

func (s *storedRows[Resource]) DBType() resource.DBType {
	return resource.SpannerDBType
}

func (s *storedRows[Resource]) Read(_ context.Context, stmt *resource.Statement) (*resource.Row[Resource], error) {
	s.reads = append(s.reads, stmt)
	if s.row == nil {
		return nil, httpio.NewNotFoundMessage("not found")
	}

	return &resource.Row[Resource]{Data: *s.row}, nil
}

func (s *storedRows[Resource]) List(context.Context, *resource.Statement) iter.Seq2[*resource.Row[Resource], error] {
	return func(func(*resource.Row[Resource], error) bool) {}
}

func (s *storedRows[Resource]) Count(context.Context, *resource.Statement) (int64, error) {
	return 0, nil
}

// refusingPermissions is a caller the grants refuse everything.
type refusingPermissions struct {
	resource.UserPermissions
}

func (refusingPermissions) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Denied()
	}

	return decisions, nil
}

func (refusingPermissions) User() accesstypes.User {
	return dana
}

// TestRefuseImpersonatorAsRowSubject pins the rule on the write of a row that decides
// what a person may do, as the generated write asks it: in a session that acts as
// someone else, the real person behind it adds no row that names them, changes and
// removes none that does, hands none on, and puts their account on no other. The person
// a row names is read before the write, off the stored row, and after it, off the
// operation's fields; the same write for another person goes through, and so does every
// write in a session of the person's own, with nothing read.
func TestRefuseImpersonatorAsRowSubject(t *testing.T) {
	t.Parallel()

	seatID := mustUUID("66666666-6666-4666-8666-666666666661")
	own, others := userID.String(), otherUser.String()
	acting := viewedBy(t.Context(), userID)
	seatKey := resource.KeySet{}.Add("ID", seatID)
	seat := func(user string) *guardedSeat {
		return &guardedSeat{ID: seatID, UserID: user, Level: 2}
	}
	set := func(field accesstypes.Field, value any) func(*resource.PatchSet[guardedSeat]) {
		return func(p *resource.PatchSet[guardedSeat]) {
			p.Set(field, value)
		}
	}

	tests := []struct {
		name string
		ctx  context.Context
		op   resource.OperationType
		key  resource.KeySet
		// stored is the row under the key, nil for none, and fields what the
		// operation sets.
		stored *guardedSeat
		fields []func(*resource.PatchSet[guardedSeat])
		// wantAct is the refusal's act, empty where the write goes through, and
		// wantReads how many rows the rule read.
		wantAct   string
		wantReads int
	}{
		{
			name: "a row added for the real person", ctx: acting, op: resource.OperationCreate,
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", own), set("Level", int64(5))}, wantAct: "add your own seat record",
		},
		{
			name: "a row added for another person", ctx: acting, op: resource.OperationCreate,
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", others), set("Level", int64(5))},
		},
		{
			name: "a row added that names nobody", ctx: acting, op: resource.OperationCreate,
			fields: []func(*resource.PatchSet[guardedSeat]){set("Level", int64(5))},
		},
		{
			name: "the real person's own row changed", ctx: acting, op: resource.OperationUpdate, key: seatKey, stored: seat(own),
			fields: []func(*resource.PatchSet[guardedSeat]){set("Level", int64(5))}, wantAct: "change your own seat record", wantReads: 1,
		},
		{
			name: "the real person's own row handed to another person", ctx: acting, op: resource.OperationUpdate, key: seatKey, stored: seat(own),
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", others)}, wantAct: "change your own seat record", wantReads: 1,
		},
		{
			name: "another person's row moved onto the real person", ctx: acting, op: resource.OperationUpdate, key: seatKey, stored: seat(others),
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", own)}, wantAct: "make another seat record your own", wantReads: 1,
		},
		{
			name: "another person's row changed", ctx: acting, op: resource.OperationUpdate, key: seatKey, stored: seat(others),
			fields: []func(*resource.PatchSet[guardedSeat]){set("Level", int64(5))}, wantReads: 1,
		},
		{
			name: "another person's row handed to a third", ctx: acting, op: resource.OperationUpdate, key: seatKey, stored: seat(others),
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", frankID.String())}, wantReads: 1,
		},
		{
			name: "a row that is not there", ctx: acting, op: resource.OperationUpdate, key: seatKey,
			fields: []func(*resource.PatchSet[guardedSeat]){set("Level", int64(5))}, wantReads: 1,
		},
		{
			name: "a row that is not there, written onto the real person", ctx: acting, op: resource.OperationUpdate, key: seatKey,
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", own)}, wantAct: "make another seat record your own", wantReads: 1,
		},
		{
			name: "the real person's own row removed", ctx: acting, op: resource.OperationDelete, key: seatKey, stored: seat(own),
			wantAct: "remove your own seat record", wantReads: 1,
		},
		{
			name: "another person's row removed", ctx: acting, op: resource.OperationDelete, key: seatKey, stored: seat(others), wantReads: 1,
		},
		{
			name: "the real person's own id in other letters", ctx: acting, op: resource.OperationCreate,
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", strings.ToUpper(own))}, wantAct: "add your own seat record",
		},
		{
			name: "a session of the person's own changes their own row, and nothing is read", ctx: asCaller(t.Context(), dana), op: resource.OperationUpdate,
			key: seatKey, stored: seat(own), fields: []func(*resource.PatchSet[guardedSeat]){set("Level", int64(5))},
		},
		{
			name: "a session of the person's own adds their own row", ctx: asCaller(t.Context(), dana), op: resource.OperationCreate,
			fields: []func(*resource.PatchSet[guardedSeat]){set("UserID", own)},
		},
		{
			name: "a session of the person's own removes their own row", ctx: asCaller(t.Context(), dana), op: resource.OperationDelete,
			key: seatKey, stored: seat(own),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &storedRows[guardedSeat]{row: tt.stored}
			txn := resource.NewMockReadWriteTransaction(stubTxn{}, reader)
			patchSet := resource.NewPatchSet(resource.NewMetadata[guardedSeat]())
			for _, field := range tt.fields {
				field(patchSet)
			}

			err := RefuseImpersonatorAsRowSubject(tt.ctx, txn, tt.op, tt.key, patchSet, "seat", "UserID")
			if got := len(reader.reads); got != tt.wantReads {
				t.Errorf("the rule read %d rows, want %d", got, tt.wantReads)
			}
			for _, stmt := range reader.reads {
				if !strings.Contains(stmt.SQL, "UserId") || !strings.Contains(stmt.SQL, "WHERE `Id` = @") || strings.Contains(stmt.SQL, "Level") {
					t.Errorf("the rule read %q, want the person's column alone, by the row's key", stmt.SQL)
				}
			}
			if tt.wantAct == "" {
				if err != nil {
					t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want nil", err)
				}

				return
			}
			want := actingAsSomeoneElse + tt.wantAct + ": while you act as another person, nothing you do is done to your own account. Stop acting as them first"
			if err == nil || !httpio.HasForbidden(err) || httpio.Message(err) != want {
				t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want the refusal %q", err, want)
			}
		})
	}
}

// TestRefuseImpersonatorAsRowSubject_keyedByPerson pins the rule on a record whose key
// holds the person: the path says whom the row names, before the write and after it,
// so the rule reads no row. The real person's own row is not added, changed or removed,
// and another person's is.
func TestRefuseImpersonatorAsRowSubject_keyedByPerson(t *testing.T) {
	t.Parallel()

	acting := viewedBy(t.Context(), userID)
	tests := []struct {
		name    string
		op      resource.OperationType
		user    impulse.UUID
		wantAct string
	}{
		{name: "the real person's own row added", op: resource.OperationCreate, user: userID, wantAct: "add your own badge record"},
		{name: "the real person's own row changed", op: resource.OperationUpdate, user: userID, wantAct: "change your own badge record"},
		{name: "the real person's own row removed", op: resource.OperationDelete, user: userID, wantAct: "remove your own badge record"},
		{name: "another person's row added", op: resource.OperationCreate, user: otherUser},
		{name: "another person's row changed", op: resource.OperationUpdate, user: otherUser},
		{name: "another person's row removed", op: resource.OperationDelete, user: otherUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &storedRows[guardedBadge]{row: &guardedBadge{UserID: tt.user.String(), BadgeID: "welder"}}
			txn := resource.NewMockReadWriteTransaction(stubTxn{}, reader)
			patchSet := resource.NewPatchSet(resource.NewMetadata[guardedBadge]())
			if tt.op != resource.OperationDelete {
				patchSet.Set("Note", "renewed")
			}
			key := resource.KeySet{}.Add("UserID", tt.user.String()).Add("BadgeID", "welder")

			err := RefuseImpersonatorAsRowSubject(acting, txn, tt.op, key, patchSet, "badge", "UserID")
			if len(reader.reads) != 0 {
				t.Errorf("the rule read %d rows, want none: the key names the person", len(reader.reads))
			}
			if tt.wantAct == "" {
				if err != nil {
					t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !httpio.HasForbidden(err) || !strings.HasPrefix(httpio.Message(err), actingAsSomeoneElse+tt.wantAct+":") {
				t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want the refusal to %q", err, tt.wantAct)
			}
		})
	}
}

// TestRefuseImpersonatorAsRowSubject_columns pins how the rule reads a column that
// holds a person: the id as text, a pointer to it, a UUID in each of its forms and the
// permission store's name for a person all name the account, a NULL in any form names
// nobody, and each of a record's person columns is asked, on the stored row and among
// the operation's fields.
func TestRefuseImpersonatorAsRowSubject_columns(t *testing.T) {
	t.Parallel()

	passID := mustUUID("66666666-6666-4666-8666-666666666662")
	own, others := userID.String(), otherUser.String()
	acting := viewedBy(t.Context(), userID)
	key := resource.KeySet{}.Add("ID", passID)
	columns := []accesstypes.Field{"UserID", "HolderID", "BackupID", "OwnerID"}

	tests := []struct {
		name    string
		stored  guardedPass
		field   accesstypes.Field
		value   any
		wantAct string
	}{
		{name: "the stored row names them by a pointer to the id", stored: guardedPass{UserID: &own}, wantAct: "change your own pass record"},
		{name: "the stored row names them by a UUID that may be NULL", stored: guardedPass{HolderID: impulse.NullUUIDFromUUID(userID)}, wantAct: "change your own pass record"},
		{name: "the stored row names them by a pointer to a UUID", stored: guardedPass{BackupID: &userID}, wantAct: "change your own pass record"},
		{name: "the stored row names them as the permission store does", stored: guardedPass{OwnerID: User(userID)}, wantAct: "change your own pass record"},
		{name: "the stored row names nobody", stored: guardedPass{}},
		{name: "the stored row names others in every column", stored: guardedPass{UserID: &others, HolderID: impulse.NullUUIDFromUUID(otherUser), BackupID: &frankID, OwnerID: User(frankID)}},
		{name: "the operation names them by a pointer to the id", field: "UserID", value: &own, wantAct: "make another pass record your own"},
		{name: "the operation names them by a UUID that may be NULL", field: "HolderID", value: impulse.NullUUIDFromUUID(userID), wantAct: "make another pass record your own"},
		{name: "the operation names them by a pointer to a UUID", field: "BackupID", value: &userID, wantAct: "make another pass record your own"},
		{name: "the operation names them as the permission store does", field: "OwnerID", value: User(userID), wantAct: "make another pass record your own"},
		{name: "the operation clears the person", field: "UserID", value: (*string)(nil)},
		{name: "the operation clears a UUID", field: "HolderID", value: impulse.NullUUID{}},
		{name: "the operation names another person", field: "UserID", value: &others},
		{name: "the operation sets a column that holds no person to their id", field: "Door", value: own},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stored := tt.stored
			stored.ID = passID
			reader := &storedRows[guardedPass]{row: &stored}
			txn := resource.NewMockReadWriteTransaction(stubTxn{}, reader)
			patchSet := resource.NewPatchSet(resource.NewMetadata[guardedPass]())
			if tt.field != "" {
				patchSet.Set(tt.field, tt.value)
			} else {
				patchSet.Set("Door", "east")
			}

			err := RefuseImpersonatorAsRowSubject(acting, txn, resource.OperationUpdate, key, patchSet, "pass", columns...)
			if len(reader.reads) != 1 {
				t.Errorf("the rule read %d rows, want 1", len(reader.reads))
			}
			if tt.wantAct == "" {
				if err != nil {
					t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !httpio.HasForbidden(err) || !strings.HasPrefix(httpio.Message(err), actingAsSomeoneElse+tt.wantAct+":") {
				t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want the refusal to %q", err, tt.wantAct)
			}
		})
	}
}

// TestRefuseImpersonatorAsRowSubject_grantsFirst pins the order of the answers: a
// caller the grants refuse the write gets that refusal and not the rule's, whose
// sentence would tell them whom the row names, and no row is read for them. A change
// that carries no key is the handler's fault and fails closed.
func TestRefuseImpersonatorAsRowSubject_grantsFirst(t *testing.T) {
	t.Parallel()

	seatID := mustUUID("66666666-6666-4666-8666-666666666661")
	acting := viewedBy(t.Context(), userID)
	rSet, err := resource.NewSet[guardedSeat, guardedSeatRequest](accesstypes.Create, accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("resource.NewSet() error = %v", err)
	}

	reader := &storedRows[guardedSeat]{row: &guardedSeat{ID: seatID, UserID: userID.String()}}
	txn := resource.NewMockReadWriteTransaction(stubTxn{}, reader)
	patchSet := resource.NewPatchSet(resource.NewMetadata[guardedSeat]()).Set("Level", int64(5))
	patchSet.EnableUserPermissionEnforcement(rSet, refusingPermissions{}, accesstypes.GlobalScope(), accesstypes.Update)

	err = RefuseImpersonatorAsRowSubject(acting, txn, resource.OperationUpdate, resource.KeySet{}.Add("ID", seatID), patchSet, "seat", "UserID")
	if err == nil || !httpio.HasForbidden(err) || strings.Contains(httpio.Message(err), "acting as someone else") {
		t.Fatalf("RefuseImpersonatorAsRowSubject() error = %v, want the grants' own refusal", err)
	}
	if len(reader.reads) != 0 {
		t.Errorf("the rule read %d rows for a caller the grants refuse, want none", len(reader.reads))
	}

	// The same caller in a session of their own is not asked here at all: the write
	// that follows refuses them.
	if err := RefuseImpersonatorAsRowSubject(asCaller(t.Context(), dana), txn, resource.OperationUpdate, resource.KeySet{}.Add("ID", seatID), patchSet, "seat", "UserID"); err != nil {
		t.Fatalf("RefuseImpersonatorAsRowSubject() outside an impersonated session error = %v, want nil", err)
	}

	trusted := resource.NewPatchSet(resource.NewMetadata[guardedSeat]()).Set("Level", int64(5))
	err = RefuseImpersonatorAsRowSubject(acting, txn, resource.OperationUpdate, resource.KeySet{}, trusted, "seat", "UserID")
	if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "no key to read the GuardedSeats row by") {
		t.Fatalf("RefuseImpersonatorAsRowSubject() with no key error = %v, want the fault that says the row cannot be read", err)
	}
}

// TestStoredColumn pins the read of the value a stored row holds in a column, which the
// rule on a row that names a set asks before a write: the value of that column and no
// other, a NULL as the row holds it, nothing for a row that is not there, and a fault,
// never a value, where the row cannot be read for the column.
func TestStoredColumn(t *testing.T) {
	t.Parallel()

	passID := mustUUID("66666666-6666-4666-8666-666666666663")
	holder := userID.String()
	key := resource.KeySet{}.Add("ID", passID)

	tests := []struct {
		name   string
		stored *guardedPass
		key    resource.KeySet
		column accesstypes.Field
		// want is the value answered and wantFound whether a row was there; wantErr
		// is what the fault says, empty where there is none, and wantRead the column
		// the one statement asks for, empty where nothing is read.
		want      any
		wantFound bool
		wantErr   string
		wantRead  string
	}{
		{
			name: "the value the row holds in the column", stored: &guardedPass{ID: passID, UserID: &holder, Door: "east"}, key: key,
			column: "Door", want: "east", wantFound: true, wantRead: "Door",
		},
		{
			name: "the value of a column between the others", stored: &guardedPass{ID: passID, HolderID: impulse.NullUUIDFromUUID(otherUser), Door: "east"}, key: key,
			column: "HolderID", want: impulse.NullUUIDFromUUID(otherUser), wantFound: true, wantRead: "HolderId",
		},
		{
			name: "a pointer as the row holds it", stored: &guardedPass{ID: passID, UserID: &holder}, key: key,
			column: "UserID", want: &holder, wantFound: true, wantRead: "UserId",
		},
		{
			name: "a NULL as the row holds it", stored: &guardedPass{ID: passID, Door: "east"}, key: key,
			column: "UserID", want: (*string)(nil), wantFound: true, wantRead: "UserId",
		},
		{
			name: "a row that is not there holds nothing", key: key,
			column: "Door", wantRead: "Door",
		},
		{
			name: "no key to read the row by is a fault", stored: &guardedPass{ID: passID, Door: "east"},
			column: "Door", wantErr: "no key to read the GuardedPasses row by",
		},
		{
			name: "a column the record does not have is a fault", stored: &guardedPass{ID: passID, Door: "east"}, key: key,
			column: "Nope", wantErr: "Nope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &storedRows[guardedPass]{row: tt.stored}
			txn := resource.NewMockReadWriteTransaction(stubTxn{}, reader)

			got, found, err := storedColumn[guardedPass](t.Context(), txn, tt.key, tt.column)
			if tt.wantErr != "" {
				if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("storedColumn() error = %v, want a fault that says %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("storedColumn() error = %v, want nil", err)
			}
			if found != tt.wantFound || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("storedColumn() = %#v, %t, want %#v, %t", got, found, tt.want, tt.wantFound)
			}

			wantReads := 0
			if tt.wantRead != "" {
				wantReads = 1
			}
			if len(reader.reads) != wantReads {
				t.Fatalf("storedColumn() read %d rows, want %d", len(reader.reads), wantReads)
			}
			for _, stmt := range reader.reads {
				if !strings.Contains(stmt.SQL, tt.wantRead) || !strings.Contains(stmt.SQL, "WHERE `Id` = @") {
					t.Errorf("storedColumn() read %q, want the column %s, by the row's key", stmt.SQL, tt.wantRead)
				}
				for _, other := range []string{"UserId", "HolderId", "BackupId", "OwnerId", "Door"} {
					if other != tt.wantRead && strings.Contains(stmt.SQL, other) {
						t.Errorf("storedColumn() read %q, want the column %s alone, not %s", stmt.SQL, tt.wantRead, other)
					}
				}
			}
		})
	}
}

// TestRefuseImpersonator_columnTheRecordDoesNotHave pins what the rules answer when they
// are given a column the record does not have, which the generated write never does: the
// stored row cannot be read for it, so each rule answers a fault and the write does not
// happen. Neither lets the write through, and neither answers the refusal a person could
// act on.
func TestRefuseImpersonator_columnTheRecordDoesNotHave(t *testing.T) {
	t.Parallel()

	seatID := mustUUID("66666666-6666-4666-8666-666666666661")
	acting := viewedBy(t.Context(), userID)
	key := resource.KeySet{}.Add("ID", seatID)
	reader := &storedRows[guardedSeat]{row: &guardedSeat{ID: seatID, UserID: userID.String(), Level: 2}}
	txn := resource.NewMockReadWriteTransaction(stubTxn{}, reader)
	patchSet := resource.NewPatchSet(resource.NewMetadata[guardedSeat]()).Set("Level", int64(5))

	for _, op := range []resource.OperationType{resource.OperationUpdate, resource.OperationDelete} {
		err := RefuseImpersonatorAsRowSubject(acting, txn, op, key, patchSet, "seat", "Nope")
		if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "Nope") {
			t.Errorf("RefuseImpersonatorAsRowSubject(%s) error = %v, want a fault that names the column", op, err)
		}
		err = RefuseImpersonatorAsSetSubject(acting, txn, op, key, patchSet, "seat", "Nope", "teams")
		if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "Nope") {
			t.Errorf("RefuseImpersonatorAsSetSubject(%s) error = %v, want a fault that names the column", op, err)
		}
	}
	if len(reader.reads) != 0 {
		t.Errorf("the rules read %d rows for a column the record does not have, want none", len(reader.reads))
	}
}

// TestStoredField pins how the field of a stored row is found for a column: by the name
// of a field the row's type declares, a field of an embedded struct among them, and the
// zero Value for a name the type does not have, which the rules answer as a fault.
func TestStoredField(t *testing.T) {
	t.Parallel()

	type Stamp struct {
		Door string
	}
	type stampedPass struct {
		Stamp

		ID     string
		UserID string
	}
	data := reflect.ValueOf(stampedPass{Stamp: Stamp{Door: "east"}, ID: "pass-1", UserID: "someone"})

	tests := []struct {
		name   string
		column accesstypes.Field
		// want is the value the field holds, nil where the type has no such field.
		want any
	}{
		{name: "a field the type declares", column: "UserID", want: "someone"},
		{name: "the field of that name and no other", column: "ID", want: "pass-1"},
		{name: "a field of an embedded struct", column: "Door", want: "east"},
		{name: "a name the type does not have", column: "Nope"},
		{name: "a name in other letters is another name", column: "userid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := storedField(data, tt.column)
			if tt.want == nil {
				if got.IsValid() {
					t.Fatalf("storedField(%s) = %v, want the zero Value", tt.column, got)
				}

				return
			}
			if !got.IsValid() || got.Interface() != tt.want {
				t.Errorf("storedField(%s) = %v, want %v", tt.column, got, tt.want)
			}
		})
	}
}
