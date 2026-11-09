package logger

import (
	"maps"
	// math/rand/v2 draws the per-request sample of a sampled policy: a statistical
	// choice, not a secret, so the scan's crypto/rand advice does not apply here.
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"math/rand/v2"
	"sync"

	"cloud.google.com/go/logging"
)

// record is the state the request logger keeps for one request or run: the lines that attached, the highest
// severity among them, the attributes of the parent entry, and the policy that decides the entry at the end.
// Each exporter's root logger embeds one; child loggers reach it through their root.
type record struct {
	mu            sync.Mutex
	policy        Policy
	logCount      int
	maxSeverity   logging.Severity
	reqAttributes map[string]any // attributes for the parent request log
	draw          func() float64 // the per-request draw of a sampled policy; nil draws from math/rand/v2
}

// setPolicy replaces the record's policy. The nearest declaration wins, so the latest call is the one that counts.
func (r *record) setPolicy(p Policy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = p
}

// attach counts a line against the record unless the policy's floor drops it, and reports whether the line
// attached. A dropped line is not written.
func (r *record) attach(severity logging.Severity) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.policy.admits(severity) {
		return false
	}
	if r.maxSeverity < severity {
		r.maxSeverity = severity
	}
	r.logCount++

	return true
}

// addRequestAttribute sets an attribute of the parent entry, overwriting an existing value for the key.
func (r *record) addRequestAttribute(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqAttributes[key] = value
}

// decide is the step at the end of the request or run: it applies the policy to what attached and to whether the
// request failed (it answered 400 or above, or the run returned an error) and reports whether the parent entry is
// written, with a copy of the state the entry is built from.
func (r *record) decide(failed bool) decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	draw := r.draw
	if draw == nil {
		draw = rand.Float64
	}
	attributes := make(map[string]any, len(r.reqAttributes))
	maps.Copy(attributes, r.reqAttributes)

	return decision{
		write:       r.policy.decide(r.logCount, failed, draw),
		logCount:    r.logCount,
		maxSeverity: r.maxSeverity,
		attributes:  attributes,
	}
}

// decision is what the record reports when the request or run ends.
type decision struct {
	write       bool
	logCount    int
	maxSeverity logging.Severity
	attributes  map[string]any
}
