package resource

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/errors/v5"
)

// RemovesAsk reports whether a patch request holds a remove that does not bring the token
// its question gave ("everythingUnder": true and a "token" in its value): such a request
// is run with ExecuteDeleteFunc asking first. It reads the body and leaves it to be read
// again. A body that is not a list of operations answers false: the handler's own loop
// refuses it.
func RemovesAsk(r *http.Request) (bool, error) {
	clone, err := CloneRequest(r)
	if err != nil {
		return false, errors.Wrap(err, "CloneRequest()")
	}
	operations, ok := operationsIn(clone.Body)
	if !ok {
		return false, nil
	}
	for _, op := range operations {
		if strings.EqualFold(op.Op, string(OperationDelete)) && !bringsToken(op.Value) {
			return true, nil
		}
	}

	return false, nil
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
