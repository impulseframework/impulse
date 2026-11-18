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
// refuses any other value (400) without running the call. A route whose frame does not
// read the header refuses a request that carries it (NoDryRunOrWarning): no call runs
// for real under a header that asked it be held back.
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

// NoDryRunOrWarning is the middleware of a route whose frame reads neither the X-Dry-Run
// header nor the X-Warning header: a list, a read, a resource's create, patch and
// delete, a tenant's creation, a scheduled call. A request that asks for a dry run or
// for a warning asks that the call be held back, and a frame that never reads the
// header would run it for real. So the request is refused here (400), naming the
// header and saying the call has none, and nothing runs. A header that says anything
// but true is refused as a frame that reads it refuses it (asksByHeader). A request
// that carries neither header passes through.
//
// The generated router mounts every route under it but the ones whose frames answer the
// two headers themselves: a method's, an upload's and SetFeature's. A route knows what
// its frame reads, so the rule is written once, where the routes are registered, and
// no frame carries a copy of it.
func NoDryRunOrWarning() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
			for _, asked := range []struct {
				header string
				reads  func(*http.Request) (bool, error)
				none   string
			}{
				{header: DryRunHeader, reads: IsDryRun, none: "a dry run, and this call has none"},
				{header: WarningHeader, reads: AsksWarning, none: "the call's warning, and this call has none"},
			} {
				asks, err := asked.reads(r)
				if err != nil {
					return httpio.NewEncoder(w).ClientMessage(r.Context(), err)
				}
				if asks {
					return httpio.NewEncoder(w).ClientMessage(r.Context(), httpio.NewBadRequestMessagef("the %s header asks for %s, so the call was not run: send it without the header", asked.header, asked.none))
				}
			}
			next.ServeHTTP(w, r)

			return nil
		})
	}
}

// ErrDryRun is the sentinel a generated frame returns from its transaction function
// on a dry run: the transaction rolls back and nothing retries. It never reaches
// the wire.
var ErrDryRun = errors.New("dry run: the transaction rolls back by design")

// DryRunRolledBack reports whether a transaction ended by the dry-run sentinel.
func DryRunRolledBack(err error) bool {
	return errors.Is(err, ErrDryRun)
}
