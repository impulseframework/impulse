package resource

import (
	"net/http"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
)

// DryRunHeader asks a transaction-form RPC method to run its whole frame — decode,
// the entry check, the target's location and state checks, the body with every
// write it arms — and then roll the transaction back instead of committing. Every
// refusal answers exactly as the real call would; a call that would have succeeded
// answers 200 with no body, since a result could name rows that never came to be.
// A client-form method refuses the header: effects outside a transaction cannot be
// previewed. The header takes the value true or is left off the request, and a frame
// refuses any other value (400) without running the call.
const DryRunHeader = "X-Dry-Run"

// IsDryRun reports whether the request asks for a dry run: the header says true. A
// request without the header asks for none. A header that says anything else is
// refused (asksByHeader), and the frame answers the refusal in place of running the
// call, so a client that sends "1" to rehearse a call never writes for real.
func IsDryRun(r *http.Request) (bool, error) {
	return asksByHeader(r, DryRunHeader)
}

// asksByHeader reads a header that asks a frame for something in place of the call
// (X-Dry-Run, X-Warning). The header takes one value, true, in any case, or is left
// off the request. Anything else is a BadRequest that names the header, the value it
// takes and what was sent: another value, an empty one, and a header sent twice. A
// request that meant to ask and misspelled the value would otherwise run the call it
// meant to hold back.
func asksByHeader(r *http.Request, header string) (bool, error) {
	values := r.Header.Values(header)
	switch {
	case len(values) == 0:
		return false, nil
	case len(values) == 1 && strings.EqualFold(values[0], "true"):
		return true, nil
	}

	return false, httpio.NewBadRequestMessagef("the %s header takes the value true, or is left off the request: %q is not a value it takes, so the call was not run", header, strings.Join(values, ", "))
}

// ErrDryRun is the sentinel a generated frame returns from its transaction function
// on a dry run: the transaction rolls back and nothing retries. It never reaches
// the wire.
var ErrDryRun = errors.New("dry run: the transaction rolls back by design")

// DryRunRolledBack reports whether a transaction ended by the dry-run sentinel.
func DryRunRolledBack(err error) bool {
	return errors.Is(err, ErrDryRun)
}
