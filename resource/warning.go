package resource

import (
	"net/http"
	"strings"

	"github.com/go-playground/errors/v5"
)

// A method whose effect a person should read before they cause it declares a warning
// (@warning(typed: word)) and writes it in its own Warning method, beside Execute:
//
//	func (m *OpenRegistration) Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error)
//
// The dialog a page runs the method through asks for the warning before anything is
// saved, with the request as the person has filled it so far, shows it in the warning
// style, and holds its confirm button until the person has typed the declared word. The
// words are the server's, written for that call: they can name the row the call runs
// against and what the request would do to it, which no page knows.

// WarningHeader asks a method that declares a warning (@warning) what the call would
// do, in place of doing it: the generated frame decodes the request and checks the
// caller exactly as the call itself is checked, locates the target row where the method
// names one, calls the method's Warning in place of its Execute, and rolls the
// transaction back. The answer is the Warning, 200. Every refusal answers as the call's
// own would, so a caller who may not run the method reads no warning of it. A method
// that declares no warning refuses the header (400): a request that asks for a warning
// never runs the call.
const WarningHeader = "X-Warning"

// AsksWarning reports whether the request asks for the method's warning in place of
// the call.
func AsksWarning(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get(WarningHeader), "true")
}

// Warning is what a method that declares one (@warning) says before it is run, in the
// server's words for the call the request describes.
type Warning struct {
	// Consequence is what the call will do, in sentences a person reads before they
	// confirm it: who is affected and how.
	Consequence string `json:"consequence"`
	// Names are the people or things the consequence speaks of, one a line, where the
	// method can name them and the caller may be told: a dialog shows them as a list
	// under the sentences. Nil where the method names none; the consequence stands
	// without them, and says how many there are where that matters.
	Names []string `json:"names,omitempty"`
	// Confirm is the words on the button that runs the call, which say what it does
	// ("Open to anyone with a Google account"), so nobody confirms by a bare "OK".
	Confirm string `json:"confirm"`
}

// ErrWarningAsked is the sentinel a generated frame returns from its transaction
// function once it has read a method's warning: the transaction rolls back, nothing
// retries, and the frame answers the warning. It never reaches the wire.
var ErrWarningAsked = errors.New("warning asked: the transaction rolls back by design")

// WarningAsked reports whether a transaction ended by the warning sentinel.
func WarningAsked(err error) bool {
	return errors.Is(err, ErrWarningAsked)
}

// CheckWarning holds a method's warning to what a dialog needs of it before the frame
// answers it: a consequence to read and the words for the button. A warning that lacks
// either is the method's mistake, and is an error, never a dialog with an empty warning
// over a button that says nothing.
func CheckWarning(method string, warning *Warning) error {
	switch {
	case warning == nil:
		return errors.Newf("%s.Warning answered no warning: a method that declares @warning says what the call will do", method)
	case strings.TrimSpace(warning.Consequence) == "":
		return errors.Newf("%s.Warning answered a warning with no consequence: say what the call will do, in a sentence a person reads before they confirm it", method)
	case strings.TrimSpace(warning.Confirm) == "":
		return errors.Newf("%s.Warning answered a warning with no words for the confirm button: say what the button does", method)
	}

	return nil
}
