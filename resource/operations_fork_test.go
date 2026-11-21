package resource

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"go.uber.org/mock/gomock"
)

// The two operations that decide from whether their row is there name the row in their
// path, as a patch does, and keep their own type until a handler runs them as the add or
// the patch they turn out to be.
func TestOperations_patchOrAddAndAddOrSkip(t *testing.T) {
	t.Parallel()

	body := `[
		{"op":"patch-or-add","path":"/row-1","value":{"title":"asked"}},
		{"op":"add-or-skip","path":"/row-2","value":{"title":"asked"}},
		{"op":"Patch-Or-Add","path":"/row-3"}
	]`
	r := &http.Request{Method: http.MethodPatch, Body: io.NopCloser(bytes.NewBufferString(body))}

	type got struct {
		Type   OperationType
		Method string
		ID     string
	}
	var gots []got
	var ops []*Operation
	for oper, err := range Operations(r, "/{id}", RequireCreatePath()) {
		if err != nil {
			t.Fatalf("Operations() error = %v", err)
		}
		ops = append(ops, oper)
		gots = append(gots, got{Type: oper.Type, Method: oper.Req.Method, ID: httpio.Param[string](oper.Req, "id")})
	}
	want := []got{
		{Type: OperationUpdateOrCreate, Method: http.MethodPatch, ID: "row-1"},
		{Type: OperationCreateOrSkip, Method: http.MethodPatch, ID: "row-2"},
		// The name is read without regard to case, as add, patch and remove are.
		{Type: OperationUpdateOrCreate, Method: http.MethodPatch, ID: "row-3"},
	}
	if diff := cmp.Diff(want, gots); diff != "" {
		t.Errorf("Operations() mismatch (-want +got):\n%s", diff)
	}

	// Run as the add or the patch it turned out to be, an operation keeps its request,
	// and the key its path carries with it.
	create, update := ops[0].AsCreate(), ops[0].AsUpdate()
	if create.Type != OperationCreate || update.Type != OperationUpdate {
		t.Errorf("AsCreate().Type = %q, AsUpdate().Type = %q, want %q and %q", create.Type, update.Type, OperationCreate, OperationUpdate)
	}
	if create.Req != ops[0].Req || update.Req != ops[0].Req {
		t.Error("AsCreate() or AsUpdate() does not carry the operation's own request")
	}
	if ops[0].Type != OperationUpdateOrCreate {
		t.Errorf("the operation itself changed type to %q", ops[0].Type)
	}
	// As an add, the operation still binds its key under the pattern a create with a
	// path takes.
	req, err := create.ReqWithPattern("/{id}", RequireCreatePath())
	if err != nil {
		t.Fatalf("AsCreate().ReqWithPattern() error = %v", err)
	}
	if got := httpio.Param[string](req, "id"); got != "row-1" {
		t.Errorf("the add's key = %q, want row-1", got)
	}
}

// An operation's name is read without regard to case, and reaches the handler and the
// decoder under the one spelling they know: a name in another case once chose its HTTP
// method and then met a decoder that knew no permission for it.
func TestOperations_nameIsReadWithoutRegardToCase(t *testing.T) {
	t.Parallel()

	body := `[
		{"op":"Add","path":"","value":{"title":"asked"}},
		{"op":"PATCH","path":"/row-1","value":{"title":"asked"}},
		{"op":"Remove","path":"/row-1"},
		{"op":"ADD-OR-SKIP","path":"/row-2","value":{"title":"asked"}}
	]`
	r := &http.Request{Method: http.MethodPatch, Body: io.NopCloser(bytes.NewBufferString(body))}

	var got []OperationType
	for oper, err := range Operations(r, "/{id}") {
		if err != nil {
			t.Fatalf("Operations() error = %v", err)
		}
		got = append(got, oper.Type)
		if oper.Type == OperationUpdateOrCreate || oper.Type == OperationCreateOrSkip {
			continue
		}
		// A permission is known for it: this panicked for a name in another case.
		if perm := permissionFromType(oper.Type); perm == "" {
			t.Errorf("permissionFromType(%q) names no permission", oper.Type)
		}
	}
	want := []OperationType{OperationCreate, OperationUpdate, OperationDelete, OperationCreateOrSkip}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Operations() types mismatch (-want +got):\n%s", diff)
	}
}

func TestOperations_patchOrAddAndAddOrSkip_needAPath(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"patch-or-add", "add-or-skip"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()

			r := &http.Request{Method: http.MethodPatch, Body: io.NopCloser(bytes.NewBufferString(`[{"op":"` + op + `","path":"","value":{}}]`))}
			for _, err := range Operations(r, "/{id}", RequireCreatePath()) {
				if err == nil || !httpio.HasBadRequest(err) {
					t.Fatalf("Operations() error = %v, want a 400: the operation names no row", err)
				}

				return
			}
			t.Fatal("Operations() yielded nothing")
		})
	}
}

// A decode never sees one of the two as it came: a handler that takes them decodes the
// add or the patch each runs as, and one that does not answers 400 naming the resource.
func TestDecoder_refusesAnUndecidedOperation(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create, accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewDecoder[tenancyResource, tenancyCreateRequest](rSet)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	for _, opType := range []OperationType{OperationUpdateOrCreate, OperationCreateOrSkip} {
		t.Run(string(opType), func(t *testing.T) {
			t.Parallel()

			oper := &Operation{Type: opType, Req: &http.Request{Body: io.NopCloser(strings.NewReader(`{"name":"dock four"}`))}}
			for name, decode := range map[string]func() error{
				"DecodeOperation": func() error {
					_, err := decoder.DecodeOperation(oper, renderStubPermissions{}, accesstypes.GlobalScope())

					return err
				},
				"DecodeOperationWithoutPermissions": func() error {
					_, err := decoder.DecodeOperationWithoutPermissions(oper)

					return err
				},
			} {
				err := decode()
				if err == nil || !httpio.HasBadRequest(err) {
					t.Fatalf("%s() error = %v, want a 400", name, err)
				}
				if msg := httpio.Message(err); !strings.Contains(msg, string(tenancyTestResource)) || !strings.Contains(msg, string(opType)) {
					t.Errorf("%s() message = %q, want it to name the resource and the operation", name, msg)
				}
			}
		})
	}
}

// DecideOperation answers the patch or the add a patch-or-add runs as. Its read is held
// to the tenant in a tenant's scope and answered from the transaction's own writes before
// any read.
func TestDecoder_DecideOperation(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create, accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewDecoder[tenancyResource, tenancyCreateRequest](rSet)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	decoder.collection = tenancyCollection(t, &DomainBindingData{Column: "StationId"})
	id := mustUUIDFromString("8a6570c8-1e51-4870-9def-3f68d0447d09")
	key := KeySet{}.Add("ID", id)
	scope := accesstypes.DomainScope("station-alpha")
	oper := &Operation{Type: OperationUpdateOrCreate, Req: &http.Request{}}

	ctrl := gomock.NewController(t)
	reader := NewMockReader[tenancyResource](ctrl)
	var selected string
	reader.EXPECT().Read(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, stmt *Statement) (*Row[tenancyResource], error) {
		selected = stmt.SQL

		return nil, notThere()
	})
	txn := NewMockReadWriteTransaction(&bufferingTxn{}, reader)

	asked := map[accesstypes.Permission][]accesstypes.Resource{}
	decided, err := decoder.DecideOperation(t.Context(), txn, oper, capStubPermissions{asked: asked}, scope, key)
	if err != nil {
		t.Fatalf("DecideOperation() error = %v", err)
	}
	if decided.Type != OperationCreate || decided.Req != oper.Req {
		t.Errorf("DecideOperation() = %q for a row the read does not find, want the add, with the operation's own request", decided.Type)
	}
	if !strings.Contains(selected, "StationId") {
		t.Errorf("the read in a tenant's scope is not held to the tenant: %s", selected)
	}
	// The one grant asked for is Update on the resource itself.
	if diff := cmp.Diff(map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Update: {tenancyTestResource}}, asked); diff != "" {
		t.Errorf("permissions asked for mismatch (-want +got):\n%s", diff)
	}

	// A row an earlier patch of the transaction wrote is there, with no second read.
	written := NewPatchSet(NewMetadata[tenancyResource]()).SetKey("ID", id).SetPatchType(CreatePatchType)
	written.Set("Name", "dock four")
	if err := written.Buffer(t.Context(), txn); err != nil {
		t.Fatalf("Buffer() error = %v", err)
	}
	decided, err = decoder.DecideOperation(t.Context(), txn, oper, capStubPermissions{}, scope, key)
	if err != nil {
		t.Fatalf("DecideOperation() after the write: error = %v", err)
	}
	if decided.Type != OperationUpdate {
		t.Errorf("DecideOperation() = %q for a row this transaction wrote, want the patch", decided.Type)
	}
	if oper.Type != OperationUpdateOrCreate {
		t.Errorf("the operation itself changed type to %q", oper.Type)
	}
}

// A caller with no Update grant on the resource is refused before the row is looked for:
// the reader of this test expects no read, so the answer cannot depend on the row.
func TestDecoder_DecideOperation_refusedBeforeTheRead(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create, accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewDecoder[tenancyResource, tenancyCreateRequest](rSet)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	key := KeySet{}.Add("ID", mustUUIDFromString("8a6570c8-1e51-4870-9def-3f68d0447d09"))
	txn := NewMockReadWriteTransaction(&bufferingTxn{}, NewMockReader[tenancyResource](gomock.NewController(t)))

	// The caller may create and may not update.
	permissions := capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Update: {}}}
	oper := &Operation{Type: OperationUpdateOrCreate, Req: &http.Request{}}
	decided, err := decoder.DecideOperation(t.Context(), txn, oper, permissions, accesstypes.GlobalScope(), key)
	if err == nil || !httpio.HasForbidden(err) {
		t.Fatalf("DecideOperation() = %v, error = %v, want a 403", decided, err)
	}
	if msg, want := httpio.Message(err), "You may not change tenancy resources here."; msg != want {
		t.Errorf("the refusal = %q, want %q", msg, want)
	}
	if text := err.Error(); !strings.Contains(text, string(accesstypes.Update)) || !strings.Contains(text, string(tenancyTestResource)) {
		t.Errorf("the error's text, which the server's log prints, = %q, want it to name Update and the resource", text)
	}
}
