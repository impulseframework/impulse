package resource

import (
	"net/http"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// recordedPatch is the least a buffered patch is: a resource and a patch type.
type recordedPatch struct {
	res accesstypes.Resource
	typ PatchType
}

func (p recordedPatch) Resource() accesstypes.Resource {
	return p.res
}

func (p recordedPatch) PatchType() PatchType {
	return p.typ
}

func (p recordedPatch) PrimaryKey() KeySet {
	return KeySet{}
}

// labeledPatch is a buffered patch that also says its record's struct name, as a
// PatchSet does.
type labeledPatch struct {
	recordedPatch

	label string
}

func (p labeledPatch) recordLabel() string {
	return p.label
}

// labeled is a buffered patch of res whose record's struct is label.
func labeled(res accesstypes.Resource, typ PatchType, label string) labeledPatch {
	return labeledPatch{recordedPatch{res, typ}, label}
}

// The stand-ins a case says a record of by its struct's name.
var (
	clientUpdate = labeled("Clients", UpdatePatchType, "Client")
	hangarDelete = labeled("Hangars", DeletePatchType, "Hangar")
	shipCreate   = labeled("Ships", CreatePatchType, "Ship")
)

// The sentences, each named once so the tables below read as shapes.
const (
	hangarDeleteSentence  = "This hangar cannot be deleted while other records still refer to it."
	shipReferencedMissing = "This ship refers to a record that does not exist, or holds a value that is too long for its field."
)

// TestBufferedPatches_referentialMessage pins the sentences of a referential refusal over
// what the transaction buffered: one sentence per shape, one record said by its struct's
// name in words and several by their resources' names in words, each once in
// first-buffered order, one or several counted over the patches, the change-event rows of
// a tracked resource never counted.
func TestBufferedPatches_referentialMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		patches []PatchSetMetadata
		want    string
	}{
		{
			name:    "one delete says the record by its struct's name in words",
			patches: []PatchSetMetadata{hangarDelete},
			want:    hangarDeleteSentence,
		},
		{
			name:    "one delete by a patch that gives no label says this record",
			patches: []PatchSetMetadata{recordedPatch{"Hangars", DeletePatchType}},
			want:    "This record cannot be deleted while other records still refer to it.",
		},
		{
			name:    "two deletes of one resource are several",
			patches: []PatchSetMetadata{hangarDelete, hangarDelete},
			want:    "One of the hangars this request deletes cannot be deleted while other records still refer to it.",
		},
		{
			name:    "deletes on two resources say each once in words in buffering order",
			patches: []PatchSetMetadata{hangarDelete, labeled("Ships", DeletePatchType, "Ship"), hangarDelete},
			want:    "One of the hangars or ships this request deletes cannot be deleted while other records still refer to it.",
		},
		{
			name:    "deletes on three resources say all three in words",
			patches: []PatchSetMetadata{hangarDelete, recordedPatch{"Ships", DeletePatchType}, recordedPatch{"Refits", DeletePatchType}},
			want:    "One of the hangars, ships or refits this request deletes cannot be deleted while other records still refer to it.",
		},
		{
			name:    "one create says the record that refers to a record that does not exist or holds a value too long",
			patches: []PatchSetMetadata{shipCreate},
			want:    shipReferencedMissing,
		},
		{
			name:    "one update is a write",
			patches: []PatchSetMetadata{labeled("Ships", UpdatePatchType, "Ship")},
			want:    shipReferencedMissing,
		},
		{
			name:    "one create-or-update is a write",
			patches: []PatchSetMetadata{labeled("Ships", CreateOrUpdatePatchType, "Ship")},
			want:    shipReferencedMissing,
		},
		{
			name:    "a create and an update on two resources are what this request adds or changes",
			patches: []PatchSetMetadata{shipCreate, labeled("Missions", UpdatePatchType, "Mission")},
			want:    "One of the ships or missions this request adds or changes refers to a record that does not exist, or holds a value that is too long for its field.",
		},
		{
			name:    "two updates on two resources are what this request changes",
			patches: []PatchSetMetadata{labeled("Ships", UpdatePatchType, "Ship"), labeled("Missions", UpdatePatchType, "Mission")},
			want:    "One of the ships or missions this request changes refers to a record that does not exist, or holds a value that is too long for its field.",
		},
		{
			name:    "deletes and writes together fall back to the sentence covering both causes",
			patches: []PatchSetMetadata{recordedPatch{"Hangars", DeletePatchType}, recordedPatch{"Ships", CreatePatchType}},
			want:    referentialRefusalFallback,
		},
		{
			name:    "a delete and an update on one resource are the mixed shape",
			patches: []PatchSetMetadata{recordedPatch{"Ships", DeletePatchType}, recordedPatch{"Ships", UpdatePatchType}},
			want:    referentialRefusalFallback,
		},
		{
			name:    "nothing buffered through the wrapper falls back to the same sentence",
			patches: nil,
			want:    referentialRefusalFallback,
		},
		{
			name:    "a tracked delete beside its change event is one delete",
			patches: []PatchSetMetadata{labeled("Ships", DeletePatchType, "Ship"), &DataChangeEvent{TableName: "Ships"}},
			want:    "This ship cannot be deleted while other records still refer to it.",
		},
		{
			name:    "a tracked create beside its change event is one write",
			patches: []PatchSetMetadata{labeled("Missions", CreatePatchType, "Mission"), &DataChangeEvent{TableName: "Missions"}},
			want:    "This mission refers to a record that does not exist, or holds a value that is too long for its field.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buffered := newBufferedPatches()
			for _, p := range tt.patches {
				buffered.record(p)
			}

			if got := buffered.referentialMessage(); got != tt.want {
				t.Errorf("referentialMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTranslateCommitError pins that the translation keys on the gRPC code alone and
// words each code from the record's shape: FailedPrecondition, AlreadyExists, and a
// NotFound beside a create answer 409; OutOfRange answers 400; a NotFound over updates
// only answers 404; a record with nothing in it gets the code's covering sentence at the
// code's write status; the Spanner error stays the cause through any wrapping; and every
// other code, or an error that is no gRPC status at all, passes through as the same error
// with no client message.
func TestTranslateCommitError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		patches     []PatchSetMetadata
		wantStatus  int
		wantMessage string
	}{
		// FailedPrecondition: the referential shapes, worded by referentialMessage.
		{
			name:        "FailedPrecondition over a delete answers 409 with the delete sentence",
			err:         status.Error(codes.FailedPrecondition, "Foreign key constraint `FK_Ships_HangarId` is violated on table `Ships`."),
			patches:     []PatchSetMetadata{hangarDelete},
			wantStatus:  http.StatusConflict,
			wantMessage: hangarDeleteSentence,
		},
		{
			name:        "FailedPrecondition over a create answers 409 with the broadened write sentence",
			err:         status.Error(codes.FailedPrecondition, "New value exceeds the maximum size limit for this column"),
			patches:     []PatchSetMetadata{shipCreate},
			wantStatus:  http.StatusConflict,
			wantMessage: shipReferencedMissing,
		},
		{
			name:        "the code reads through go-playground wrapping",
			err:         errors.Wrap(errors.Wrap(status.Error(codes.FailedPrecondition, "refused"), "inner"), "outer"),
			patches:     []PatchSetMetadata{hangarDelete},
			wantStatus:  http.StatusConflict,
			wantMessage: hangarDeleteSentence,
		},
		// AlreadyExists: a create cannot tell a taken key from a duplicate unique value.
		{
			name:        "AlreadyExists over a create answers 409 saying the record exists or holds another's unique value",
			err:         status.Error(codes.AlreadyExists, "Row [a0…, 2] in table RefitTasks already exists"),
			patches:     []PatchSetMetadata{labeled("RefitTasks", CreatePatchType, "RefitTask")},
			wantStatus:  http.StatusConflict,
			wantMessage: "This refit task already exists, or it has a value that must be unique and another record already has it.",
		},
		{
			name:        "AlreadyExists over a create-or-update is the create shape",
			err:         status.Error(codes.AlreadyExists, "Unique index violation on index ShipsByRegistry"),
			patches:     []PatchSetMetadata{labeled("Ships", CreateOrUpdatePatchType, "Ship")},
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship already exists, or it has a value that must be unique and another record already has it.",
		},
		{
			name:        "AlreadyExists over updates only answers 409 saying another record has the unique value",
			err:         status.Error(codes.AlreadyExists, "Unique index violation on index ClientsByName"),
			patches:     []PatchSetMetadata{clientUpdate},
			wantStatus:  http.StatusConflict,
			wantMessage: "This client has a value that must be unique, and another record already has it.",
		},
		{
			name:        "AlreadyExists over an update and a create on two resources is the create shape in buffering order",
			err:         status.Error(codes.AlreadyExists, "already exists"),
			patches:     []PatchSetMetadata{clientUpdate, shipCreate},
			wantStatus:  http.StatusConflict,
			wantMessage: "One of the clients or ships this request adds or changes already exists, or it has a value that must be unique and another record already has it.",
		},
		{
			name:        "AlreadyExists over two updates on two resources is what this request changes",
			err:         status.Error(codes.AlreadyExists, "already exists"),
			patches:     []PatchSetMetadata{clientUpdate, labeled("Ships", UpdatePatchType, "Ship")},
			wantStatus:  http.StatusConflict,
			wantMessage: "One of the clients or ships this request changes has a value that must be unique, and another record already has it.",
		},
		{
			name:        "AlreadyExists ignores deletes buffered beside the writes",
			err:         status.Error(codes.AlreadyExists, "already exists"),
			patches:     []PatchSetMetadata{recordedPatch{"Sorties", DeletePatchType}, clientUpdate},
			wantStatus:  http.StatusConflict,
			wantMessage: "This client has a value that must be unique, and another record already has it.",
		},
		{
			name:        "AlreadyExists over nothing buffered answers 409 with the covering sentence",
			err:         status.Error(codes.AlreadyExists, "already exists"),
			wantStatus:  http.StatusConflict,
			wantMessage: alreadyExistsFallback,
		},
		// OutOfRange: a CHECK constraint, 400 for any write.
		{
			name:        "OutOfRange over an update answers 400 saying the range",
			err:         status.Error(codes.OutOfRange, "Check constraint `Missions`.`CK_Missions_Hazard` is violated."),
			patches:     []PatchSetMetadata{labeled("Missions", UpdatePatchType, "Mission")},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "This mission has a value that is outside the range allowed.",
		},
		{
			name:        "OutOfRange over a create answers 400 with the same sentence",
			err:         status.Error(codes.OutOfRange, "Check constraint violated"),
			patches:     []PatchSetMetadata{labeled("Missions", CreatePatchType, "Mission"), &DataChangeEvent{TableName: "Missions"}},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "This mission has a value that is outside the range allowed.",
		},
		{
			name:        "OutOfRange over a create and an update on two resources is what this request adds or changes",
			err:         status.Error(codes.OutOfRange, "Check constraint violated"),
			patches:     []PatchSetMetadata{shipCreate, labeled("Missions", UpdatePatchType, "Mission")},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "One of the ships or missions this request adds or changes has a value that is outside the range allowed.",
		},
		{
			name:        "OutOfRange over nothing buffered answers 400 with the covering sentence",
			err:         status.Error(codes.OutOfRange, "Check constraint violated"),
			wantStatus:  http.StatusBadRequest,
			wantMessage: outOfRangeFallback,
		},
		// NotFound: updates only means the row is missing; a create means its parent is.
		{
			name:        "NotFound over one update answers 404 for the record by its struct's name",
			err:         status.Error(codes.NotFound, "Row [missing] in table Clients is missing. Row cannot be updated."),
			patches:     []PatchSetMetadata{clientUpdate},
			wantStatus:  http.StatusNotFound,
			wantMessage: "This client does not exist.",
		},
		{
			name:        "NotFound over one update by a PatchSet says the record by its struct's name",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{NewPatchSet(NewMetadata[goneRow]()).SetPatchType(UpdatePatchType)},
			wantStatus:  http.StatusNotFound,
			wantMessage: "This gone row does not exist.",
		},
		{
			name:        "NotFound over one update by a patch that gives no label says this record",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{recordedPatch{"Clients", UpdatePatchType}},
			wantStatus:  http.StatusNotFound,
			wantMessage: "This record does not exist.",
		},
		{
			name:        "NotFound over one tracked update does not count its change event",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{clientUpdate, &DataChangeEvent{TableName: "Clients"}},
			wantStatus:  http.StatusNotFound,
			wantMessage: "This client does not exist.",
		},
		{
			name:        "NotFound over two updates of one resource says one of them does not exist",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{clientUpdate, clientUpdate},
			wantStatus:  http.StatusNotFound,
			wantMessage: "One of the clients this request changes does not exist.",
		},
		{
			name:        "NotFound over updates on two resources says both in words in buffering order",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{clientUpdate, labeledPatch{recordedPatch{"Pilots", UpdatePatchType}, "Pilot"}},
			wantStatus:  http.StatusNotFound,
			wantMessage: "One of the clients or pilots this request changes does not exist.",
		},
		{
			name:        "NotFound over updates on three resources says all three in words",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{clientUpdate, recordedPatch{"Pilots", UpdatePatchType}, recordedPatch{"RefitTasks", UpdatePatchType}},
			wantStatus:  http.StatusNotFound,
			wantMessage: "One of the clients, pilots or refit tasks this request changes does not exist.",
		},
		{
			name:        "NotFound over a create answers 409 for the referenced parent",
			err:         status.Error(codes.NotFound, "Parent row for row [p404, n9] in table Nested is missing."),
			patches:     []PatchSetMetadata{labeled("RefitTasks", CreatePatchType, "RefitTask")},
			wantStatus:  http.StatusConflict,
			wantMessage: "This refit task refers to a record that does not exist.",
		},
		{
			name:        "NotFound over an update beside a create is the create shape",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{recordedPatch{"Refits", UpdatePatchType}, recordedPatch{"RefitTasks", CreateOrUpdatePatchType}},
			wantStatus:  http.StatusConflict,
			wantMessage: "One of the refits or refit tasks this request adds or changes refers to a record that does not exist.",
		},
		{
			name:        "NotFound ignores deletes buffered beside the update",
			err:         status.Error(codes.NotFound, "missing"),
			patches:     []PatchSetMetadata{clientUpdate, recordedPatch{"Sorties", DeletePatchType}},
			wantStatus:  http.StatusNotFound,
			wantMessage: "This client does not exist.",
		},
		{
			name:        "NotFound over nothing buffered answers 409 with the covering sentence",
			err:         status.Error(codes.NotFound, "missing"),
			wantStatus:  http.StatusConflict,
			wantMessage: notFoundFallback,
		},
		// Everything else passes through.
		{
			name:    "InvalidArgument passes through",
			err:     status.Error(codes.InvalidArgument, "NUMERIC value exceeds the precision"),
			patches: []PatchSetMetadata{recordedPatch{"Missions", CreatePatchType}},
		},
		{
			name:    "Internal passes through",
			err:     status.Error(codes.Internal, "string field contains invalid UTF-8"),
			patches: []PatchSetMetadata{recordedPatch{"Missions", CreatePatchType}},
		},
		{
			name:    "Aborted passes through",
			err:     status.Error(codes.Aborted, "Transaction was aborted."),
			patches: []PatchSetMetadata{recordedPatch{"Hangars", DeletePatchType}},
		},
		{
			name:    "an unknown code passes through",
			err:     status.Error(codes.Code(1000), "no such code"),
			patches: []PatchSetMetadata{recordedPatch{"Hangars", DeletePatchType}},
		},
		{
			name:    "an error that is no gRPC status passes through",
			err:     errors.New("the function's own failure"),
			patches: []PatchSetMetadata{recordedPatch{"Hangars", DeletePatchType}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buffered := newBufferedPatches()
			for _, p := range tt.patches {
				buffered.record(p)
			}

			got := translateCommitError(tt.err, buffered)
			if tt.wantStatus == 0 {
				if !errors.Is(got, tt.err) || httpio.HasClientMessage(got) {
					t.Fatalf("translateCommitError() = %v, want the error passed through with no client message", got)
				}

				return
			}

			if gotStatus := clientStatus(got); gotStatus != tt.wantStatus {
				t.Fatalf("translateCommitError() = %v, answers %d, want %d", got, gotStatus, tt.wantStatus)
			}
			if msg := httpio.Message(got); msg != tt.wantMessage {
				t.Errorf("httpio.Message() = %q, want %q", msg, tt.wantMessage)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("the Spanner error is not the cause of the client message: %v", got)
			}
		})
	}
}

// clientStatus reads the status an error's client message answers with, 0 when it has
// none of the statuses the translation can produce.
func clientStatus(err error) int {
	switch {
	case httpio.HasConflict(err):
		return http.StatusConflict
	case httpio.HasBadRequest(err):
		return http.StatusBadRequest
	case httpio.HasNotFound(err):
		return http.StatusNotFound
	default:
		return 0
	}
}
