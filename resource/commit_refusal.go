package resource

import (
	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
)

// The sentences for a refusal whose cause the record cannot tell apart, one per gRPC code:
// for a referential refusal, deletes and writes buffered together or nothing buffered at
// all; for the other codes, nothing buffered through the wrapper. Each covers every cause
// its code carries.
const (
	referentialRefusalFallback = "The request could not be applied: a deleted record is still referenced, or a referenced record does not exist."
	alreadyExistsFallback      = "The request could not be applied: a record with this key or a unique value already exists."
	outOfRangeFallback         = "The request could not be applied: a value is outside the range the record allows."
	notFoundFallback           = "The request could not be applied: a record to update does not exist, or a referenced record does not exist."
)

// bufferedPatches is the record a write transaction keeps of what it buffered: every
// resource in first-buffered order, each with the patch types buffered for it. When
// Spanner refuses the commit, the record is all the library knows about what the refusal
// concerns, and the client message is composed from it alone: it says the records the
// transaction buffered in words, one by its struct's name ("This hangar") and several by
// their resources' names ("One of the hangars or ships this request deletes"), and never
// a resource's or a table's code name, the referencing table, the constraint, the index,
// the key, the value, or a count.
//
// The change-event rows a tracked resource writes beside its patches are not recorded:
// they are the library's own bookkeeping, never a resource the caller addressed, and
// counting them would turn every tracked delete into the mixed shape.
//
// So that one record is said as every record is said, the record also keeps each
// resource's record label, from the first patch recorded for it that gives one
// (recordLabeler), and how many deletes and how many creates, updates and
// create-or-updates it recorded in all: one or several is counted over the patches, so
// two deletes of one resource are several.
type bufferedPatches struct {
	order   []accesstypes.Resource
	types   map[accesstypes.Resource]map[PatchType]bool
	labels  map[accesstypes.Resource]string
	written int
	deleted int
}

// recordLabeler is how the record of buffered patches asks a patch for its record's
// struct name, the label a sentence says the record by (labelOf). A PatchSet answers;
// a patch that does not answer has no label, and its record is "this record".
type recordLabeler interface {
	recordLabel() string
}

// newBufferedPatches returns an empty record.
func newBufferedPatches() *bufferedPatches {
	return &bufferedPatches{
		types:  make(map[accesstypes.Resource]map[PatchType]bool),
		labels: make(map[accesstypes.Resource]string),
	}
}

// record notes one buffered patch by its resource and patch type, its record's label
// where it is the first of its resource to give one, and whether it deletes or writes.
func (b *bufferedPatches) record(patch PatchSetMetadata) {
	if _, isEvent := patch.(*DataChangeEvent); isEvent {
		return
	}

	res := patch.Resource()
	if _, seen := b.types[res]; !seen {
		b.order = append(b.order, res)
		b.types[res] = make(map[PatchType]bool)
	}
	b.types[res][patch.PatchType()] = true
	if labeler, ok := patch.(recordLabeler); ok && b.labels[res] == "" {
		b.labels[res] = labeler.recordLabel()
	}
	if patch.PatchType() == DeletePatchType {
		b.deleted++
	} else {
		b.written++
	}
}

// deletes returns the resources a delete was buffered for, in first-buffered order.
func (b *bufferedPatches) deletes() []string {
	var deletes []string
	for _, res := range b.order {
		if b.types[res][DeletePatchType] {
			deletes = append(deletes, string(res))
		}
	}

	return deletes
}

// writes returns the resources a create, update, or create-or-update was buffered for (a
// touch is an update), in first-buffered order, and whether any of them buffered a create
// or a create-or-update.
func (b *bufferedPatches) writes() (writes []string, anyCreate bool) {
	for _, res := range b.order {
		types := b.types[res]
		if types[CreatePatchType] || types[CreateOrUpdatePatchType] {
			anyCreate = true
		}
		if types[CreatePatchType] || types[UpdatePatchType] || types[CreateOrUpdatePatchType] {
			writes = append(writes, string(res))
		}
	}

	return writes, anyCreate
}

// deletedRecords is how a sentence about the buffered deletes says their records: "This
// hangar" for one delete, the record by its struct's name in words, and "One of the
// hangars or ships this request deletes" for several, the resources deletes returned in
// words.
func (b *bufferedPatches) deletedRecords(deletes []string) string {
	return b.recordsInWords(deletes, b.deleted, "deletes")
}

// writtenRecords is how a sentence about the buffered writes says their records: "This
// ship" for one write, the record by its struct's name in words, and "One of the ships or
// missions this request adds or changes" for several, the resources writes returned in
// words, with "changes" alone where no create was among them.
func (b *bufferedPatches) writtenRecords(writes []string, anyCreate bool) string {
	verb := "changes"
	if anyCreate {
		verb = "adds or changes"
	}

	return b.recordsInWords(writes, b.written, verb)
}

// recordsInWords says the records of resources, which count patches buffered: one by its
// resource's record label in words ("This record" where no patch gave one), several by
// the resources' names in words, each once in first-buffered order, as what this request
// does to them.
func (b *bufferedPatches) recordsInWords(resources []string, count int, verb string) string {
	if count == 1 {
		return "This " + recordInWords(b.labels[accesstypes.Resource(resources[0])])
	}

	things := make([]string, 0, len(resources))
	for _, res := range resources {
		things = append(things, inWords(res))
	}

	return "One of the " + eitherOf(things) + " this request " + verb
}

// referentialMessage composes the client message for a commit Spanner refused with
// FailedPrecondition: a delete of a row other rows still reference, a write whose foreign
// key names a row that does not exist, a required column left empty, or a string longer
// than its column allows. Deletes and writes each have their own sentence, "This hangar
// cannot be deleted while other records still refer to it." and "This ship refers to a
// record that does not exist, or holds a value that is too long for its field."; deletes
// and writes together, or a record with nothing in it, fall back to a sentence that
// covers both causes, because at commit they cannot be told apart.
func (b *bufferedPatches) referentialMessage() string {
	deletes := b.deletes()
	writes, anyCreate := b.writes()

	switch {
	case len(deletes) > 0 && len(writes) == 0:
		return b.deletedRecords(deletes) + " cannot be deleted while other records still refer to it."
	case len(writes) > 0 && len(deletes) == 0:
		return b.writtenRecords(writes, anyCreate) + " refers to a record that does not exist, or holds a value that is too long for its field."
	default:
		return referentialRefusalFallback
	}
}

// alreadyExistsMessage composes the client message for a commit Spanner refused with
// AlreadyExists: a create whose key is already taken, or a create or update that
// duplicates a unique-index value. A record with a create cannot tell the two apart and
// says both, "This ship already exists, or it has a value that must be unique and
// another record already has it."; a record of updates only can only have duplicated a
// unique value, "This client has a value that must be unique, and another record already
// has it."
func (b *bufferedPatches) alreadyExistsMessage() string {
	writes, anyCreate := b.writes()

	switch {
	case len(writes) == 0:
		return alreadyExistsFallback
	case anyCreate:
		return b.writtenRecords(writes, anyCreate) + " already exists, or it has a value that must be unique and another record already has it."
	default:
		return b.writtenRecords(writes, anyCreate) + " has a value that must be unique, and another record already has it."
	}
}

// outOfRangeMessage composes the client message for a commit Spanner refused with
// OutOfRange: a write that violates a CHECK constraint, "This mission has a value that is
// outside the range allowed."
func (b *bufferedPatches) outOfRangeMessage() string {
	writes, anyCreate := b.writes()
	if len(writes) == 0 {
		return outOfRangeFallback
	}

	return b.writtenRecords(writes, anyCreate) + " has a value that is outside the range allowed."
}

// notFoundError composes the error for a commit Spanner refused with NotFound. A record
// of updates only means the row being updated does not exist: 404, said as every record
// that is not there is said, "This client does not exist." for one update, the record by
// its struct's name in words, and "One of the clients or pilots this request changes
// does not exist." for several. A record with a create means an interleaved parent does
// not exist, which is a record the write refers to: 409, "This refit task refers to a
// record that does not exist." A record with nothing in it gets the sentence covering
// both causes at 409.
func (b *bufferedPatches) notFoundError(err error) error {
	writes, anyCreate := b.writes()

	switch {
	case len(writes) == 0:
		return httpio.NewConflictMessageWithError(err, notFoundFallback)
	case anyCreate:
		return httpio.NewConflictMessageWithError(err, b.writtenRecords(writes, anyCreate)+" refers to a record that does not exist.")
	default:
		return httpio.NewNotFoundMessageWithError(err, b.writtenRecords(writes, anyCreate)+" does not exist.")
	}
}

// translateCommitError turns the error a refused commit returns into the 4xx the caller
// can act on, decided on the gRPC code alone:
//
//   - FailedPrecondition, the code for every referential refusal (a delete of a row other
//     rows still reference through a foreign key without ON DELETE CASCADE or as an
//     interleaved parent under ON DELETE NO ACTION, a create or update whose foreign key
//     names a referenced row that does not exist, a required column left empty) and for a
//     string longer than its column's declared length, answers 409.
//   - AlreadyExists, a duplicate primary key or a duplicate unique-index value, answers 409.
//   - OutOfRange, a violated CHECK constraint, answers 400.
//   - NotFound answers 404 when the record holds updates only (the row being updated does
//     not exist) and 409 when it holds a create (an interleaved parent does not exist). It
//     is also Spanner's code for an unknown column or table, so a deployment whose schema
//     disagrees with the generated code answers 404 or 409 with Spanner's text in the log
//     instead of 500; a schema mismatch fails every write that touches the column, so it
//     does not hide behind one request.
//
// Neither the emulator nor the service attaches structured details, and their message
// texts differ, so the code alone decides and the text is never read. Deletes cannot
// cause any code but FailedPrecondition, so deletes buffered beside writes do not change
// the other codes' sentences. The Spanner error stays the cause in the chain, so the
// server log keeps its full text. Any other code (InvalidArgument, Internal, Aborted, …)
// passes through unchanged.
func translateCommitError(err error, buffered *bufferedPatches) error {
	switch spanner.ErrCode(err) {
	case codes.FailedPrecondition:
		return httpio.NewConflictMessageWithError(err, buffered.referentialMessage())
	case codes.AlreadyExists:
		return httpio.NewConflictMessageWithError(err, buffered.alreadyExistsMessage())
	case codes.OutOfRange:
		return httpio.NewBadRequestMessageWithError(err, buffered.outOfRangeMessage())
	case codes.NotFound:
		return buffered.notFoundError(err)
	default:
		return err
	}
}
