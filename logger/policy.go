package logger

import (
	"fmt"
	"math"
	"net/http"

	"cloud.google.com/go/logging"
)

// policyMode is the word a Policy was declared with.
type policyMode uint8

const (
	policyAlways policyMode = iota
	policyOnEvent
	policySampled
	policyNever
)

// The policy words, as String names them.
const (
	wordAlways  = "always"
	wordOnEvent = "on event"
	wordSampled = "sampled at %g"
	wordNever   = "never"
)

// Policy declares what a request, or a command-line run, writes to the log when it ends. It is declared with one
// of four words, each a constructor:
//
//   - Always writes the parent entry for every request. It is today's behavior and the zero value.
//   - OnEvent writes the parent entry when a line attached to the request or the request failed: it answered 400
//     or above, or the run returned an error.
//   - Sampled writes as OnEvent does and, for the quiet requests, draws once per request at the given fraction.
//   - Never writes nothing, whatever happened.
//
// MinSeverity adds a floor: lines below it are dropped before they attach, so they neither count as an event nor
// appear as child entries. The nearest declaration wins: the request logger's options set the policy a request
// starts with, WithPolicy replaces it on the way down the handler chain, and a handler may replace it again for
// its own request through Logger.SetPolicy.
type Policy struct {
	mode        policyMode
	fraction    float64
	minSeverity logging.Severity
}

// Always returns the policy that writes the parent entry for every request. It is today's behavior and the
// default of every exporter.
func Always() Policy {
	return Policy{mode: policyAlways}
}

// OnEvent returns the policy that writes the parent entry when a line attached to the request or the request
// failed: it answered 400 or above, or the run returned an error. It is what an exporter built with
// LogAll(false) or NewAWSExporter(false) defaults to.
func OnEvent() Policy {
	return Policy{mode: policyOnEvent}
}

// Sampled returns the policy that writes the parent entry as OnEvent does and, for the quiet requests, draws
// once per request at the given fraction, so that share of them is written too. The fraction must be above 0 and
// at most 1; any other value is refused with a panic, because the policy is a declaration and a bad fraction is
// a mistake in the code that declares it.
func Sampled(fraction float64) Policy {
	if math.IsNaN(fraction) || fraction <= 0 || fraction > 1 {
		panic(fmt.Sprintf("logger.Sampled(%v): the fraction must be above 0 and at most 1", fraction))
	}

	return Policy{mode: policySampled, fraction: fraction}
}

// Never returns the policy that writes nothing, whatever happened. A health check wants this.
func Never() Policy {
	return Policy{mode: policyNever}
}

// MinSeverity returns a copy of the policy with the floor set: lines below the severity are dropped before they
// attach to the request, so they are neither counted as an event nor written as child entries. The receiver is
// unchanged.
func (p Policy) MinSeverity(severity logging.Severity) Policy {
	p.minSeverity = severity

	return p
}

// String names the policy the way a chain comment reads it: "always", "on event", "sampled at 0.1" or "never",
// followed by the floor when one is set, as in "on event, Warning and above".
func (p Policy) String() string {
	var word string
	switch p.mode {
	case policyOnEvent:
		word = wordOnEvent
	case policySampled:
		word = fmt.Sprintf(wordSampled, p.fraction)
	case policyNever:
		word = wordNever
	default:
		word = wordAlways
	}
	if p.minSeverity == logging.Default {
		return word
	}

	return word + ", " + p.minSeverity.String() + " and above"
}

// admits reports whether a line of the given severity attaches under the policy's floor.
func (p Policy) admits(severity logging.Severity) bool {
	return severity >= p.minSeverity
}

// decide applies the policy when the request or run ends and reports whether the parent entry is written.
// logCount is the number of lines that attached, failed is whether the request answered 400 or above or the run
// returned an error, and draw supplies the per-request draw a sampled policy makes, in [0, 1).
func (p Policy) decide(logCount int, failed bool, draw func() float64) bool {
	switch p.mode {
	case policyOnEvent:
		return logCount > 0 || failed
	case policySampled:
		return logCount > 0 || failed || draw() < p.fraction
	case policyNever:
		return false
	default:
		return true
	}
}

// logAllPolicy is the policy an exporter's LogAll switch names: Always when on, OnEvent when off.
func logAllPolicy(logAll bool) Policy {
	if logAll {
		return Always()
	}

	return OnEvent()
}

// WithPolicy returns a middleware that sets the policy on the request's record. Deeper setters override earlier
// ones, so the nearest declaration wins: a route's over its outlet's, an outlet's over the application default.
// Without a record, because the request logger did not run, it passes the request through unchanged and does
// nothing else.
func WithPolicy(p Policy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			FromReq(r).SetPolicy(p)
			next.ServeHTTP(w, r)
		})
	}
}
