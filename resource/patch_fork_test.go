package resource

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"go.uber.org/mock/gomock"
)

// forkRow is a resource with a field a create fills and a field an update stamps, as the
// generator registers them from default_create_fn and output_only_update_fn.
type forkRow struct {
	ID        string `spanner:"Id"`
	Title     string `spanner:"Title"`
	CreatedBy string `spanner:"CreatedBy"`
	UpdatedBy string `spanner:"UpdatedBy"`
}

func (forkRow) Resource() accesstypes.Resource {
	return "ForkRows"
}

// trackedForkRow keeps change records.
type trackedForkRow struct {
	ID    string `spanner:"Id"`
	Title string `spanner:"Title"`
}

func (trackedForkRow) Resource() accesstypes.Resource {
	return "TrackedForkRows"
}

func (trackedForkRow) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (trackedForkRow) DefaultConfig() Config {
	return Config{}
}

// forkPatch builds a patch of forkRow's row-1 as the generated typed patches do.
func forkPatch(patchType PatchType) *PatchSet[forkRow] {
	return forkPatchOf(patchType, "row-1", "asked")
}

// forkPatchOf builds a patch of one forkRow with both paths' functions registered on the
// one patch set, the way the generated OrCreate leaves it.
func forkPatchOf(patchType PatchType, id, title string) *PatchSet[forkRow] {
	p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(patchType)
	p.SetKey("ID", id)
	p.Set("Title", title)
	p.RegisterDefaultCreateFunc("CreatedBy", func(context.Context, ReadWriteTransaction) (any, error) {
		return "creator", nil
	})
	p.RegisterOutputOnlyUpdateFunc("UpdatedBy", func(context.Context, ReadWriteTransaction) (any, error) {
		return "updater", nil
	})

	return p
}

// buffered spells what a transaction buffered: each patch's type and the fields it set.
func buffered(t *testing.T, txn *bufferingTxn) []string {
	t.Helper()

	out := make([]string, 0, len(txn.buffered))
	for _, patch := range txn.buffered {
		out = append(out, string(patch.PatchType()))
	}

	return out
}

func notThere() error {
	return httpio.NewNotFoundMessage("not found")
}

func TestPatchSet_fork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// patch builds the forking patch.
		patch func() *PatchSet[forkRow]
		// stored says whether the read finds the row.
		stored       bool
		wantBuffered []string
		wantOutcome  Outcome
		// wantSet and wantUnset name fields the patch holds, and does not, afterwards.
		wantSet   []accesstypes.Field
		wantUnset []accesstypes.Field
	}{
		{
			name: "an update-or-create whose row is there runs the update",
			patch: func() *PatchSet[forkRow] {
				return forkPatch(UpdatePatchType).OrCreate(nil)
			},
			stored:       true,
			wantBuffered: []string{string(UpdatePatchType)},
			wantOutcome:  OutcomeUpdated,
			wantSet:      []accesstypes.Field{"Title", "UpdatedBy"},
			wantUnset:    []accesstypes.Field{"CreatedBy"},
		},
		{
			name: "an update-or-create whose row is not there runs the create",
			patch: func() *PatchSet[forkRow] {
				return forkPatch(UpdatePatchType).OrCreate(nil)
			},
			wantBuffered: []string{string(CreatePatchType)},
			wantOutcome:  OutcomeCreated,
			wantSet:      []accesstypes.Field{"Title", "CreatedBy"},
			wantUnset:    []accesstypes.Field{"UpdatedBy"},
		},
		{
			name: "an update-or-create with no fields set leaves a row that is there alone",
			patch: func() *PatchSet[forkRow] {
				p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(UpdatePatchType)
				p.SetKey("ID", "row-1")

				return p.OrCreate(nil)
			},
			stored:      true,
			wantOutcome: OutcomeUpdated,
		},
		{
			name: "a create-unless-there whose row is there does nothing",
			patch: func() *PatchSet[forkRow] {
				return forkPatch(CreatePatchType).OrSkip()
			},
			stored:      true,
			wantOutcome: OutcomeSkipped,
			wantSet:     []accesstypes.Field{"Title"},
			wantUnset:   []accesstypes.Field{"CreatedBy", "UpdatedBy"},
		},
		{
			name: "a create-unless-there whose row is not there runs the create",
			patch: func() *PatchSet[forkRow] {
				return forkPatch(CreatePatchType).OrSkip()
			},
			wantBuffered: []string{string(CreatePatchType)},
			wantOutcome:  OutcomeCreated,
			wantSet:      []accesstypes.Field{"Title", "CreatedBy"},
			wantUnset:    []accesstypes.Field{"UpdatedBy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reader := NewMockReader[forkRow](ctrl)
			var selected string
			reader.EXPECT().Read(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, stmt *Statement) (*Row[forkRow], error) {
				selected = stmt.SQL
				if !tt.stored {
					return nil, notThere()
				}

				return &Row[forkRow]{Data: forkRow{ID: "row-1"}}, nil
			})
			buffering := &bufferingTxn{}
			txn := NewMockReadWriteTransaction(buffering, reader)

			p := tt.patch()
			if got := p.Outcome(); got != "" {
				t.Errorf("Outcome() before the patch ran = %q, want none", got)
			}
			if err := p.Buffer(t.Context(), txn); err != nil {
				t.Fatalf("Buffer() error = %v", err)
			}

			// The read is the key alone: no other column is asked for.
			for _, column := range []string{"Title", "CreatedBy", "UpdatedBy"} {
				if strings.Contains(selected, column) {
					t.Errorf("the read asks for %s, want the key alone: %s", column, selected)
				}
			}
			if !strings.Contains(selected, "Id") {
				t.Errorf("the read does not name the key: %s", selected)
			}
			if diff := cmp.Diff(tt.wantBuffered, buffered(t, buffering)); diff != "" && len(tt.wantBuffered)+len(buffering.buffered) > 0 {
				t.Errorf("buffered mismatch (-want +got):\n%s", diff)
			}
			if got := p.Outcome(); got != tt.wantOutcome {
				t.Errorf("Outcome() = %q, want %q", got, tt.wantOutcome)
			}
			for _, field := range tt.wantSet {
				if !p.IsSet(field) {
					t.Errorf("%s is not set on the patch, want it set", field)
				}
			}
			for _, field := range tt.wantUnset {
				if p.IsSet(field) {
					t.Errorf("%s is set on the patch (%v), want it left out", field, p.Get(field))
				}
			}
		})
	}
}

// A read does not see what its transaction buffered, so a row a patch of the same
// transaction wrote is found in the transaction's own record, with no read.
func TestPatchSet_fork_rowsOfTheSameTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// first is buffered before the patches under test; nil for none.
		first func() *PatchSet[forkRow]
		// patches are the forking patches buffered in order.
		patches []func() *PatchSet[forkRow]
		// reads is how many times the row is read; every read finds nothing.
		reads        int
		wantBuffered []string
		wantOutcomes []Outcome
	}{
		{
			name: "a second create-unless-there of one row skips",
			patches: []func() *PatchSet[forkRow]{
				func() *PatchSet[forkRow] { return forkPatch(CreatePatchType).OrSkip() },
				func() *PatchSet[forkRow] { return forkPatch(CreatePatchType).OrSkip() },
			},
			reads:        1,
			wantBuffered: []string{string(CreatePatchType)},
			wantOutcomes: []Outcome{OutcomeCreated, OutcomeSkipped},
		},
		{
			name: "a second update-or-create of one row updates",
			patches: []func() *PatchSet[forkRow]{
				func() *PatchSet[forkRow] { return forkPatch(UpdatePatchType).OrCreate(nil) },
				func() *PatchSet[forkRow] { return forkPatch(UpdatePatchType).OrCreate(nil) },
			},
			reads:        1,
			wantBuffered: []string{string(CreatePatchType), string(UpdatePatchType)},
			wantOutcomes: []Outcome{OutcomeCreated, OutcomeUpdated},
		},
		{
			name: "a row a plain create of the transaction wrote is there",
			first: func() *PatchSet[forkRow] {
				return forkPatch(CreatePatchType)
			},
			patches: []func() *PatchSet[forkRow]{
				func() *PatchSet[forkRow] { return forkPatch(CreatePatchType).OrSkip() },
			},
			wantBuffered: []string{string(CreatePatchType)},
			wantOutcomes: []Outcome{OutcomeSkipped},
		},
		{
			name: "a row the transaction deleted is not there",
			first: func() *PatchSet[forkRow] {
				p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "row-1")

				return p
			},
			patches: []func() *PatchSet[forkRow]{
				func() *PatchSet[forkRow] { return forkPatch(UpdatePatchType).OrCreate(nil) },
			},
			wantBuffered: []string{string(DeletePatchType), string(CreatePatchType)},
			wantOutcomes: []Outcome{OutcomeCreated},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reader := NewMockReader[forkRow](ctrl)
			reader.EXPECT().Read(gomock.Any(), gomock.Any()).Return(nil, notThere()).Times(tt.reads)
			buffering := &bufferingTxn{}
			txn := NewMockReadWriteTransaction(buffering, reader)

			if tt.first != nil {
				if err := tt.first().Buffer(t.Context(), txn); err != nil {
					t.Fatalf("the first patch: Buffer() error = %v", err)
				}
			}
			var outcomes []Outcome
			for i, build := range tt.patches {
				p := build()
				if err := p.Buffer(t.Context(), txn); err != nil {
					t.Fatalf("patch %d: Buffer() error = %v", i, err)
				}
				outcomes = append(outcomes, p.Outcome())
			}
			if diff := cmp.Diff(tt.wantBuffered, buffered(t, buffering)); diff != "" {
				t.Errorf("buffered mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOutcomes, outcomes); diff != "" {
				t.Errorf("outcomes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A transaction that aborts runs again, and the row may be there the second time. What
// the first attempt's path put on the patch must not ride the second attempt's path.
func TestPatchSet_fork_decidesAgainOnEveryAttempt(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reader := NewMockReader[forkRow](ctrl)
	gomock.InOrder(
		reader.EXPECT().Read(gomock.Any(), gomock.Any()).Return(nil, notThere()),
		reader.EXPECT().Read(gomock.Any(), gomock.Any()).Return(&Row[forkRow]{Data: forkRow{ID: "row-1"}}, nil),
		reader.EXPECT().Read(gomock.Any(), gomock.Any()).Return(nil, notThere()),
	)

	p := forkPatch(UpdatePatchType)
	// A create's defaults hook rewrites a field the caller set, as a hook may.
	p.RegisterDefaultsCreateFunc(func(context.Context, ReadWriteTransaction) error {
		p.Set("Title", "rewritten by the create")

		return nil
	})
	p.OrCreate(nil)

	// First attempt: the row is not there, the create runs.
	first := &bufferingTxn{}
	if err := p.Buffer(t.Context(), NewMockReadWriteTransaction(first, reader)); err != nil {
		t.Fatalf("first attempt: Buffer() error = %v", err)
	}
	if diff := cmp.Diff([]string{string(CreatePatchType)}, buffered(t, first)); diff != "" {
		t.Errorf("first attempt buffered mismatch (-want +got):\n%s", diff)
	}
	if got := p.Get("Title"); got != "rewritten by the create" {
		t.Errorf("first attempt Title = %v, want the create hook's value", got)
	}

	// Second attempt: the row is there now, the update runs with what the caller set.
	second := &bufferingTxn{}
	if err := p.Buffer(t.Context(), NewMockReadWriteTransaction(second, reader)); err != nil {
		t.Fatalf("second attempt: Buffer() error = %v", err)
	}
	if diff := cmp.Diff([]string{string(UpdatePatchType)}, buffered(t, second)); diff != "" {
		t.Errorf("second attempt buffered mismatch (-want +got):\n%s", diff)
	}
	if got := p.Outcome(); got != OutcomeUpdated {
		t.Errorf("second attempt Outcome() = %q, want %q", got, OutcomeUpdated)
	}
	if got := p.Get("Title"); got != "asked" {
		t.Errorf("second attempt Title = %v, want the caller's value back", got)
	}
	if p.IsSet("CreatedBy") {
		t.Errorf("second attempt: the create's default rides the update: CreatedBy = %v", p.Get("CreatedBy"))
	}
	if !p.IsSet("UpdatedBy") {
		t.Error("second attempt: the update's stamp is not set")
	}
	if diff := cmp.Diff([]accesstypes.Field{"Title", "UpdatedBy"}, p.Fields()); diff != "" {
		t.Errorf("second attempt fields mismatch (-want +got):\n%s", diff)
	}

	// Third attempt: gone again, the create runs with no trace of the update.
	third := &bufferingTxn{}
	if err := p.Buffer(t.Context(), NewMockReadWriteTransaction(third, reader)); err != nil {
		t.Fatalf("third attempt: Buffer() error = %v", err)
	}
	if diff := cmp.Diff([]string{string(CreatePatchType)}, buffered(t, third)); diff != "" {
		t.Errorf("third attempt buffered mismatch (-want +got):\n%s", diff)
	}
	if p.IsSet("UpdatedBy") {
		t.Errorf("third attempt: the update's stamp rides the create: UpdatedBy = %v", p.Get("UpdatedBy"))
	}
}

func TestPatchSet_fork_refusesTheWrongPatchType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		patch   *PatchSet[forkRow]
		wantErr string
	}{
		{
			name:    "OrCreate on a create",
			patch:   forkPatch(CreatePatchType).OrCreate(nil),
			wantErr: "update-or-create is an update",
		},
		{
			name:    "OrCreate on a delete",
			patch:   forkPatch(DeletePatchType).OrCreate(nil),
			wantErr: "update-or-create is an update",
		},
		{
			name:    "OrSkip on an update",
			patch:   forkPatch(UpdatePatchType).OrSkip(),
			wantErr: "create-unless-there is a create",
		},
		{
			name:    "OrSkip on the low-level insert-or-update",
			patch:   forkPatch(CreateOrUpdatePatchType).OrSkip(),
			wantErr: "create-unless-there is a create",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buffering := &bufferingTxn{}
			txn := NewMockReadWriteTransaction(buffering, NewMockReader[forkRow](gomock.NewController(t)))
			err := tt.patch.Buffer(t.Context(), txn)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Buffer() error = %v, want containing %q", err, tt.wantErr)
			}
			if len(buffering.buffered) != 0 {
				t.Errorf("a refused patch buffered %v", buffered(t, buffering))
			}
		})
	}
}

// On a resource that keeps change records the update path reads the stored row for the
// record, as a plain update does: the key read decides, and the path's own read follows.
func TestPatchSet_fork_changeRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		stored       bool
		wantReads    int
		wantBuffered []string
	}{
		{
			name:         "the row is there: the update and its change record",
			stored:       true,
			wantReads:    2,
			wantBuffered: []string{string(UpdatePatchType), string(CreatePatchType)},
		},
		{
			name:         "the row is not there: the create and its change record",
			wantReads:    1,
			wantBuffered: []string{string(CreatePatchType), string(CreatePatchType)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reader := NewMockReader[trackedForkRow](ctrl)
			reads := 0
			reader.EXPECT().Read(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, *Statement) (*Row[trackedForkRow], error) {
				reads++
				if !tt.stored {
					return nil, notThere()
				}

				return &Row[trackedForkRow]{Data: trackedForkRow{ID: "row-1", Title: "before"}}, nil
			}).AnyTimes()
			buffering := &bufferingTxn{}
			txn := NewMockReadWriteTransaction(buffering, reader)

			p := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(UpdatePatchType)
			p.SetKey("ID", "row-1")
			p.Set("Title", "after")
			if err := p.OrCreate(nil).Buffer(t.Context(), txn, eventsource.ProcessEvent("test")); err != nil {
				t.Fatalf("Buffer() error = %v", err)
			}
			if reads != tt.wantReads {
				t.Errorf("the row was read %d time(s), want %d", reads, tt.wantReads)
			}
			if diff := cmp.Diff(tt.wantBuffered, buffered(t, buffering)); diff != "" {
				t.Errorf("buffered mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// recordingPermissions notes the permission each check asked for.
type recordingPermissions struct {
	renderStubPermissions
	asked *[]accesstypes.Permission
}

func (r recordingPermissions) Check(ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	*r.asked = append(*r.asked, perm)

	return r.renderStubPermissions.Check(ctx, env, scope, perm, resources...)
}

// An armed patch is checked for the permission of the case it runs as, after the grant
// of what the patch is: a create-unless-there for Create, and an update-or-create for
// Update on the resource, each before its row is looked for.
func TestPatchSet_fork_armed(t *testing.T) {
	t.Parallel()

	createSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create)
	if err != nil {
		t.Fatalf("NewSet(Create) error = %v", err)
	}
	updateSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet(Update) error = %v", err)
	}
	id := mustUUIDFromString("8a6570c8-1e51-4870-9def-3f68d0447d09")

	tests := []struct {
		name      string
		orCreate  bool
		createSet *Set[tenancyResource]
		stored    bool
		wantAsked []accesstypes.Permission
		wantErr   string
	}{
		{name: "update-or-create, row there: Update", orCreate: true, createSet: createSet, stored: true, wantAsked: []accesstypes.Permission{accesstypes.Update}},
		{name: "update-or-create, row not there: Update on the resource, then Create", orCreate: true, createSet: createSet, wantAsked: []accesstypes.Permission{accesstypes.Update, accesstypes.Create}},
		{name: "update-or-create armed with no create Set is refused when it must create", orCreate: true, wantErr: "needs the Set a create is checked against"},
		{name: "create-unless-there, row there: Create all the same", stored: true, wantAsked: []accesstypes.Permission{accesstypes.Create}},
		{name: "create-unless-there, row not there: Create", wantAsked: []accesstypes.Permission{accesstypes.Create}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reader := NewMockReader[tenancyResource](ctrl)
			var selected string
			reader.EXPECT().Read(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, stmt *Statement) (*Row[tenancyResource], error) {
				selected = stmt.SQL
				if !tt.stored {
					return nil, notThere()
				}

				return &Row[tenancyResource]{Data: tenancyResource{ID: id}}, nil
			})
			txn := NewMockReadWriteTransaction(&bufferingTxn{}, reader)

			var asked []accesstypes.Permission
			permissions := recordingPermissions{asked: &asked}
			p := NewPatchSet(NewMetadata[tenancyResource]()).SetKey("ID", id)
			p.Set("Name", "dock four")
			if tt.orCreate {
				p.SetPatchType(UpdatePatchType).
					EnableUserPermissionEnforcement(updateSet, permissions, accesstypes.GlobalScope(), accesstypes.Update).
					OrCreate(tt.createSet)
			} else {
				p.SetPatchType(CreatePatchType).
					EnableUserPermissionEnforcement(createSet, permissions, accesstypes.GlobalScope(), accesstypes.Create).
					OrSkip()
			}

			err := p.Buffer(t.Context(), txn)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Buffer() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Buffer() error = %v", err)
			}
			// The permissions asked for, in order, a run of one counted once.
			if diff := cmp.Diff(tt.wantAsked, slices.Compact(asked)); diff != "" {
				t.Errorf("permissions checked mismatch (-want +got):\n%s", diff)
			}
			// A global request reads the key with no tenant predicate.
			if strings.Contains(selected, "StationId") {
				t.Errorf("a global request's read names the tenant column: %s", selected)
			}
		})
	}
}

// A caller who does not hold the grant of what the patch is gets the same refusal whether
// or not the row is there: the grant is asked for before the row is looked for, so the
// reader of this test, which expects no read at all, is never reached.
func TestPatchSet_fork_armed_refusedBeforeTheRead(t *testing.T) {
	t.Parallel()

	createSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create)
	if err != nil {
		t.Fatalf("NewSet(Create) error = %v", err)
	}
	updateSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet(Update) error = %v", err)
	}
	id := mustUUIDFromString("8a6570c8-1e51-4870-9def-3f68d0447d09")

	tests := []struct {
		name     string
		orCreate bool
		// denied is the permission the caller holds nowhere on the resource; the other
		// is granted whole.
		denied accesstypes.Permission
	}{
		{name: "update-or-create by a caller who may create and may not update", orCreate: true, denied: accesstypes.Update},
		{name: "create-unless-there by a caller who may update and may not create", denied: accesstypes.Create},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			txn := NewMockReadWriteTransaction(&bufferingTxn{}, NewMockReader[tenancyResource](gomock.NewController(t)))
			permissions := capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{tt.denied: {}}}
			p := NewPatchSet(NewMetadata[tenancyResource]()).SetKey("ID", id)
			p.Set("Name", "dock four")
			if tt.orCreate {
				p.SetPatchType(UpdatePatchType).
					EnableUserPermissionEnforcement(updateSet, permissions, accesstypes.GlobalScope(), accesstypes.Update).
					OrCreate(createSet)
			} else {
				p.SetPatchType(CreatePatchType).
					EnableUserPermissionEnforcement(createSet, permissions, accesstypes.GlobalScope(), accesstypes.Create).
					OrSkip()
			}

			err := p.Buffer(t.Context(), txn)
			if err == nil || !httpio.HasForbidden(err) {
				t.Fatalf("Buffer() error = %v, want a 403", err)
			}
			if msg, want := httpio.Message(err), "You may not "+permissionVerb(tt.denied)+" tenancy resources here."; msg != want {
				t.Errorf("the refusal = %q, want %q", msg, want)
			}
			if !strings.Contains(err.Error(), string(tt.denied)) {
				t.Errorf("the error's text, which the server's log prints, = %q, want it to name %s", err.Error(), tt.denied)
			}
			if p.Outcome() != "" {
				t.Errorf("Outcome() = %q after a refusal, want none", p.Outcome())
			}
		})
	}
}

// TestPatchSet_Permitted proves the gate a write passes before a rule that reads the
// database answers it: a caller who holds no grant for the write is refused there, with
// the same 403 Buffer answers, and nothing is read or buffered; a caller who holds it
// passes, and so does a patch that is not armed, which runs trusted.
func TestPatchSet_Permitted(t *testing.T) {
	t.Parallel()

	createSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create)
	if err != nil {
		t.Fatalf("NewSet(Create) error = %v", err)
	}
	id := mustUUIDFromString("8a6570c8-1e51-4870-9def-3f68d0447d09")

	// A delete is armed with the record's one Set, as a decoder arms it: Delete is asked
	// of the resource alone.
	tests := []struct {
		name      string
		patchType PatchType
		required  accesstypes.Permission
		// denied is the permission the caller holds nowhere on the resource; every
		// other is granted whole.
		denied      accesstypes.Permission
		unarmed     bool
		wantRefused bool
	}{
		{name: "a create by a caller who may not create is refused", patchType: CreatePatchType, required: accesstypes.Create, denied: accesstypes.Create, wantRefused: true},
		{name: "a delete by a caller who may not delete is refused", patchType: DeletePatchType, required: accesstypes.Delete, denied: accesstypes.Delete, wantRefused: true},
		{name: "a create by a caller who may create passes", patchType: CreatePatchType, required: accesstypes.Create, denied: accesstypes.Delete},
		{name: "a delete by a caller who may delete passes", patchType: DeletePatchType, required: accesstypes.Delete, denied: accesstypes.Create},
		{name: "a patch that is not armed passes: it runs trusted", patchType: CreatePatchType, denied: accesstypes.Create, unarmed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The reader expects no read and the transaction no write.
			txn := NewMockReadWriteTransaction(&bufferingTxn{}, NewMockReader[tenancyResource](gomock.NewController(t)))
			permissions := capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{tt.denied: {}}}
			p := NewPatchSet(NewMetadata[tenancyResource]()).SetKey("ID", id).SetPatchType(tt.patchType)
			if tt.patchType == CreatePatchType {
				p.Set("Name", "dock four")
			}
			if !tt.unarmed {
				p.EnableUserPermissionEnforcement(createSet, permissions, accesstypes.GlobalScope(), tt.required)
			}

			err := p.Permitted(t.Context(), txn)
			if !tt.wantRefused {
				if err != nil {
					t.Fatalf("Permitted() error = %v, want none", err)
				}

				return
			}
			if err == nil || !httpio.HasForbidden(err) {
				t.Fatalf("Permitted() error = %v, want a 403", err)
			}
			if msg, want := httpio.Message(err), "You may not "+permissionVerb(tt.denied)+" tenancy resources here."; msg != want {
				t.Errorf("the refusal = %q, want %q", msg, want)
			}
			if !strings.Contains(err.Error(), string(tt.denied)) {
				t.Errorf("the error's text, which the server's log prints, = %q, want it to name %s", err.Error(), tt.denied)
			}
		})
	}
}

// Inside a tenant the read is held to the tenant: a row of another tenant under the same
// key reads as not there, and the create that follows is refused at commit as any create
// of a taken key is.
func TestPatchSet_fork_readIsHeldToTheTenant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reader := NewMockReader[tenancyResource](ctrl)
	var selected *Statement
	reader.EXPECT().Read(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, stmt *Statement) (*Row[tenancyResource], error) {
		selected = stmt

		return &Row[tenancyResource]{Data: tenancyResource{}}, nil
	})
	txn := NewMockReadWriteTransaction(&bufferingTxn{}, reader)

	createSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create)
	if err != nil {
		t.Fatalf("NewSet(Create) error = %v", err)
	}
	p := NewPatchSet(NewMetadata[tenancyResource]()).SetKey("ID", mustUUIDFromString("8a6570c8-1e51-4870-9def-3f68d0447d09")).SetPatchType(CreatePatchType)
	p.querySet.collection = tenancyCollection(t, &DomainBindingData{Column: "StationId"})
	p.EnableUserPermissionEnforcement(createSet, renderStubPermissions{}, accesstypes.DomainScope("station-alpha"), accesstypes.Create)
	p.Set("Name", "dock four")

	if err := p.OrSkip().Buffer(t.Context(), txn); err != nil {
		t.Fatalf("Buffer() error = %v", err)
	}
	if got := p.Outcome(); got != OutcomeSkipped {
		t.Errorf("Outcome() = %q, want %q", got, OutcomeSkipped)
	}
	if selected == nil {
		t.Fatal("the row was not read")
	}
	if !strings.Contains(selected.SQL, "StationId") || !strings.Contains(selected.SQL, "@"+domainParamName) {
		t.Errorf("the read is not held to the tenant: %s", selected.SQL)
	}
}

func TestTouchedRows_find(t *testing.T) {
	t.Parallel()

	patch := func(id string, patchType PatchType) *PatchSet[forkRow] {
		p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)

		return p
	}

	rows := newTouchedRows()
	rows.record(patch("row-1", CreatePatchType))
	rows.record(patch("row-2", CreatePatchType))
	rows.record(patch("row-2", DeletePatchType))

	tests := []struct {
		name        string
		id          string
		wantWritten bool
		wantDeleted bool
	}{
		{name: "a row the transaction wrote", id: "row-1", wantWritten: true},
		{name: "a row the transaction wrote and then deleted", id: "row-2", wantWritten: true, wantDeleted: true},
		{name: "a row the transaction did not write", id: "row-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			written, deleted := rows.find(patch(tt.id, UpdatePatchType))
			if written != tt.wantWritten || deleted != tt.wantDeleted {
				t.Errorf("find() = (%v, %v), want (%v, %v)", written, deleted, tt.wantWritten, tt.wantDeleted)
			}
		})
	}
}
