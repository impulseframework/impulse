package resource

import (
	"context"
	"maps"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// Two patches decide what they do from whether their row is there: an update that creates
// the row when it is not (OrCreate), and a create that leaves the row alone when it is
// (OrSkip). Neither is a third kind of write. Each reads the row's key inside the
// transaction and then runs the create path or the update path exactly as a plain create
// or a plain update runs it: the defaults, the validation, the caller's grant with its
// condition, the tenancy check, the change record and the mutation are that path's own.
//
// The read is the key alone, held to the request's tenant where the patch is armed in
// one, so a row of another tenant under the same key reads as not there and the create
// that follows is refused at commit as any create of a taken key is.
//
// A patch armed against a caller is asked for its grant before the read: Create, with the
// fields the patch carries, for an OrSkip, and Update on the resource for an OrCreate. A
// caller who holds neither gets the same refusal whether or not the row is there.
//
// A read inside a read-write transaction does not see what the transaction has buffered,
// so the transaction's own record of the rows its patches wrote is asked first: a row a
// patch of this transaction wrote is there, and one it deleted is not.
//
// One limit is the update path's own. On a resource that keeps change records, and under
// a grant with a row condition or inside a tenant, an update reads the stored row, so an
// OrCreate for a row this same transaction created is refused as not found, as a plain
// update of that row is. An OrSkip for such a row skips.

// Outcome is what buffering an OrCreate or an OrSkip patch did with its row.
type Outcome string

const (
	// OutcomeCreated says the row was not there and the patch created it.
	OutcomeCreated Outcome = "created"
	// OutcomeUpdated says the row was there and the update ran. An update with no
	// fields set is the silent no-op it is everywhere (see Apply and Buffer).
	OutcomeUpdated Outcome = "updated"
	// OutcomeSkipped says the row was there and an OrSkip patch left it as it is:
	// nothing was written and no default or validation ran.
	OutcomeSkipped Outcome = "skipped"
)

// patchFork is what an OrCreate or an OrSkip patch carries beside the patch itself.
type patchFork[Resource Resourcer] struct {
	// orCreate says the patch is an OrCreate; an OrSkip otherwise.
	orCreate bool
	// createSet is the Set a create is checked against, for the create an armed
	// update patch turns into (the patch itself is armed with the update's).
	createSet *Set[Resource]
	outcome   Outcome
	// undo is what the last attempt's path changed on the patch, taken back before
	// the next attempt decides again.
	undo *forkUndo[Resource]
}

// forkGiven is the patch as it stands when an attempt starts: what the caller built.
type forkGiven[Resource Resourcer] struct {
	data               map[accesstypes.Field]any
	patchType          PatchType
	resourceSet        *Set[Resource]
	requiredPermission accesstypes.Permission
}

// forkUndo is one attempt's changes to the patch: the fields the path's defaults and
// hooks set that the caller had not, the caller's values of the fields they changed, and
// the patch's type and arm as the caller gave them.
type forkUndo[Resource Resourcer] struct {
	added   []accesstypes.Field
	changed map[accesstypes.Field]any
	given   *forkGiven[Resource]
}

// touchedFinder is how a forking patch asks its transaction whether one of the
// transaction's own patches already wrote the row. The Spanner and Mock wrappers answer
// from their record of touched rows; a transaction that keeps none (Postgres) is not
// asked, and the stored row decides alone.
type touchedFinder interface {
	touchedRow(patch PatchSetMetadata) (written, deleted bool)
}

// OrCreate turns an update patch into update-or-create: when the row is there the update
// runs as it is, and when it is not the patch creates the row with the fields it carries,
// through the create path whole. createSet is the Set a create of the resource is checked
// against, used when the patch is armed against a caller (Enforce); a patch that runs
// trusted passes nil. The generated update patch's OrCreate passes the resource's own and
// registers the create's defaults and validation beside the update's.
//
// The fields the patch sets are written in both cases. A caller that needs the two cases
// to write different things reads the row and builds the create patch or the update
// patch.
func (p *PatchSet[Resource]) OrCreate(createSet *Set[Resource]) *PatchSet[Resource] {
	p.fork = &patchFork[Resource]{orCreate: true, createSet: createSet}

	return p
}

// OrSkip turns a create patch into create-unless-there: when the row is not there the
// create runs as it is, and when it is the patch does nothing at all. The caller's Create
// grant is checked before the row is looked for, so a caller who may not create is
// refused either way. A grant's row condition is judged only when the row is created.
func (p *PatchSet[Resource]) OrSkip() *PatchSet[Resource] {
	p.fork = &patchFork[Resource]{}

	return p
}

// Outcome reports what the last Apply or Buffer of an OrCreate or OrSkip patch did with
// its row. It is empty on a patch that is neither, and before the patch has run.
func (p *PatchSet[Resource]) Outcome() Outcome {
	if p.fork == nil {
		return ""
	}

	return p.fork.outcome
}

// applyFork runs an OrCreate or OrSkip patch in a new read-write transaction.
func (p *PatchSet[Resource]) applyFork(ctx context.Context, c Client, eventSource ...string) error {
	if err := c.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		return p.bufferFork(ctx, txn, eventSource...)
	}); err != nil {
		return errors.Wrap(err, "Client.ExecuteFunc()")
	}

	return nil
}

// bufferFork decides the patch's case for this attempt and runs that path. A transaction
// that aborts runs its function again, and the row may be there on the second attempt
// where it was not on the first, so what the last attempt's path put on the patch is
// taken back first: a default the create filled must not ride an update.
func (p *PatchSet[Resource]) bufferFork(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error {
	p.undoFork()
	if err := p.validateTouch(); err != nil {
		return err
	}
	if err := p.validateFork(); err != nil {
		return err
	}
	orCreate := p.fork.orCreate
	given := p.forkGiven()
	defer p.recordFork(given)

	// The caller is asked for the grant of what the patch is before the row is looked
	// for, so the answer to a caller with no such grant does not say whether the row is
	// there. A create-unless-there is a create with the fields it carries. An
	// update-or-create is an update, but the fields it carries are held to the Update
	// grant or the Create grant only once its case is known, so it is asked for Update
	// on the resource alone.
	if orCreate {
		if err := p.querySet.checkResourcePermission(ctx); err != nil {
			return err
		}
	} else if err := p.checkPermissions(ctx, txn.DBType()); err != nil {
		return err
	}

	exists, err := p.rowExists(ctx, txn)
	if err != nil {
		return err
	}

	switch {
	case exists && !orCreate:
		p.fork.outcome = OutcomeSkipped

		return nil
	case exists:
		p.fork.outcome = OutcomeUpdated
		if p.Len() == 0 && !p.touch {
			return nil
		}

		return p.bufferUpdate(ctx, txn, eventSource...)
	case orCreate:
		if err := p.becomeCreate(); err != nil {
			return err
		}
	}
	p.fork.outcome = OutcomeCreated

	return p.bufferInsert(ctx, txn, eventSource...)
}

// validateFork refuses a patch of the wrong type: update-or-create is an update, and
// create-unless-there a create. The generated patches offer each method on its own type
// alone, so only a patch set built by hand can get here.
func (p *PatchSet[Resource]) validateFork() error {
	switch {
	case p.fork.orCreate && p.patchType != UpdatePatchType:
		return errors.Newf("update-or-create is an update: PatchType %s cannot be marked OrCreate", p.patchType)
	case !p.fork.orCreate && p.patchType != CreatePatchType:
		return errors.Newf("create-unless-there is a create: PatchType %s cannot be marked OrSkip", p.patchType)
	default:
		return nil
	}
}

// becomeCreate turns the update patch into the create it runs as when the row is not
// there: the mutation is an insert, and an armed patch is checked against the create's
// Set under the Create permission.
func (p *PatchSet[Resource]) becomeCreate() error {
	p.patchType = CreatePatchType
	q := p.querySet
	if q.requiredPermission != accesstypes.Update {
		return nil
	}
	if p.fork.createSet == nil {
		return errors.Newf("%s: an update-or-create armed against a caller needs the Set a create is checked against; pass it to OrCreate", p.Resource())
	}
	q.resourceSet = p.fork.createSet
	q.requiredPermission = accesstypes.Create

	return nil
}

// rowExists reports whether the patch's row is there: written by an earlier patch of this
// transaction, or stored. The read is the key alone, inside the transaction, so the row
// cannot come or go under it before the commit.
func (p *PatchSet[Resource]) rowExists(ctx context.Context, txn ReadWriteTransaction) (bool, error) {
	if finder, ok := txn.(touchedFinder); ok {
		if written, deleted := finder.touchedRow(p); written {
			return !deleted, nil
		}
	}

	qSet := NewQuerySet(p.querySet.rMeta)
	qSet.scope = p.querySet.scope
	qSet.collection = p.querySet.collection
	for _, part := range p.PrimaryKey().Parts() {
		qSet.SetKey(part.Key, part.Value)
		qSet.AddField(part.Key)
	}
	stmt, err := qSet.stmt(txn.DBType())
	if err != nil {
		return false, errors.Wrap(err, "QuerySet.stmt()")
	}
	if _, err := newReader[Resource](txn).Read(ctx, stmt); err != nil {
		if httpio.HasNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "Reader.Read()")
	}

	return true, nil
}

// forkGiven notes the patch as the caller built it, before the attempt's path runs.
func (p *PatchSet[Resource]) forkGiven() *forkGiven[Resource] {
	return &forkGiven[Resource]{
		data:               maps.Clone(p.data.data),
		patchType:          p.patchType,
		resourceSet:        p.querySet.resourceSet,
		requiredPermission: p.querySet.requiredPermission,
	}
}

// recordFork keeps what the attempt's path changed on the patch, measured against the
// patch as it was given: the fields it added, and the given values of those it changed.
func (p *PatchSet[Resource]) recordFork(given *forkGiven[Resource]) {
	undo := &forkUndo[Resource]{
		changed: make(map[accesstypes.Field]any),
		given:   given,
	}
	for _, field := range p.data.fields {
		before, had := given.data[field]
		switch {
		case !had:
			undo.added = append(undo.added, field)
		case !sameValue(before, p.data.data[field]):
			undo.changed[field] = before
		}
	}
	p.fork.undo = undo
}

// undoFork takes back what the last attempt's path changed, leaving the patch as the
// caller built it, with whatever the caller has set since.
func (p *PatchSet[Resource]) undoFork() {
	undo := p.fork.undo
	if undo == nil {
		return
	}
	p.fork.undo = nil
	for _, field := range undo.added {
		p.data.unset(field)
		p.querySet.fields = slices.DeleteFunc(p.querySet.fields, func(f accesstypes.Field) bool {
			return f == field
		})
	}
	for field, value := range undo.changed {
		p.data.Set(field, value)
	}
	p.patchType = undo.given.patchType
	p.querySet.resourceSet = undo.given.resourceSet
	p.querySet.requiredPermission = undo.given.requiredPermission
}

// sameValue reports whether a field holds the value it held before a path ran. A value
// the comparison cannot judge counts as changed, so the caller's value is put back.
func sameValue(before, after any) bool {
	matched, err := match(before, after)

	return err == nil && matched
}
