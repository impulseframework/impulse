package resource

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/errors/v5"
)

// DeleteRun is how a request that may hold the delete of a record with rows under it is run
// (Executor.ExecuteDeleteFunc).
type DeleteRun int

const (
	// DeleteRunPlain runs the request in one read-write transaction, as ExecuteFunc does. A
	// delete in it whose token does not hold ends that transaction, and the request is then
	// asked.
	DeleteRunPlain DeleteRun = iota
	// DeleteRunAsks runs the request on a question transaction first: it holds a delete that
	// does not bring the token its question gave.
	DeleteRunAsks
	// DeleteRunSave runs the request as a save of a delete: it holds nothing but deletes that
	// bring their tokens, and its transaction is opened at the repeatable read isolation
	// level, so what the save reads is not locked against other writers. Everything the
	// request's function does runs at that level.
	DeleteRunSave
)

// DeleteRunOf reads how a patch request is run from its body. A request with a remove that
// does not bring the token its question gave ("everythingUnder": true and a "token" in its
// value) asks first. A request in which every operation is a remove that brings its token
// is a save. Any other request, and a body that is not a list of operations, which the
// handler's own loop refuses, is run plainly. It reads the body and leaves it to be read
// again.
func DeleteRunOf(r *http.Request) (DeleteRun, error) {
	clone, err := CloneRequest(r)
	if err != nil {
		return DeleteRunPlain, errors.Wrap(err, "CloneRequest()")
	}
	operations, ok := operationsIn(clone.Body)
	if !ok {
		return DeleteRunPlain, nil
	}
	saves := len(operations) > 0
	for _, op := range operations {
		if !strings.EqualFold(op.Op, string(OperationDelete)) {
			saves = false

			continue
		}
		if !bringsToken(op.Value) {
			return DeleteRunAsks, nil
		}
	}
	if saves {
		return DeleteRunSave, nil
	}

	return DeleteRunPlain, nil
}

// DeleteRunOfToken is how an RPC that declares the field its delete's token comes in
// (@deleteToken) is run: asking first while the field is empty, and plainly once it brings
// a token.
func DeleteRunOfToken(token string) DeleteRun {
	if token == "" {
		return DeleteRunAsks
	}

	return DeleteRunPlain
}

// operationsIn reads a patch request's body as its list of operations; ok is false where
// it is not one.
func operationsIn(body io.Reader) (operations []patchOperation, ok bool) {
	if json.NewDecoder(body).Decode(&operations) != nil {
		return nil, false
	}

	return operations, true
}

// bringsToken reports whether a remove operation's value says "everythingUnder": true and
// carries a token that is not empty. A remove with no value, or with one that is not an
// object, does not.
func bringsToken(value json.RawMessage) bool {
	var said removeValue
	if json.Unmarshal(value, &said) != nil {
		return false
	}

	return said.EverythingUnder && said.Token != ""
}
