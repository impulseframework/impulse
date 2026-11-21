// Command opaqueoption is a program that does not compile, and is kept for that: it
// tries each way to open a session option (session.BaseSessionOption) and get hold of
// the session the library builds, from which a program once started an impersonated
// session with a principal, a mask and an actor of its own choosing and no declaration
// asked. The session package's tests type-check it and pin each refusal
// (TestOpaqueOption_noRouteToTheSessionCompiles).
//
// It is kept outside the session library's directory on purpose. The function that
// makes an option lives in an internal package of the session library, which the go
// command lets the library's own directory import and nobody else: a program kept
// inside that directory could import it, and an application cannot.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// starter is what the program wants of the session inside the library: the start of an
// impersonated session from a record of the program's own writing.
type starter interface {
	StartImpersonatedSession(ctx context.Context, w http.ResponseWriter, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error)
}

// reached is the session the program got hold of, if any route worked.
var reached starter

// capture is the generic function that opened the option while the option was a
// function over the session. Instantiated with the option's type, the compiler worked
// out P, the type of the session inside the library that no program can name, and the
// option it answered was called with the session the constructor had built.
func capture[O ~func(P), P any]() O {
	return func(inside P) {
		reached, _ = any(inside).(starter)
	}
}

// captureField is the same route in the shape the option has now: a struct with one
// field, a function over the session. The field is not exported, so a constraint
// written outside the library names a different field and matches nothing.
func captureField[O ~struct{ set func(P) }, P any]() O {
	return O{set: func(inside P) {
		reached, _ = any(inside).(starter)
	}}
}

// keep is a function over whatever it is handed, for the routes that need no type.
func keep(inside any) {
	reached, _ = inside.(starter)
}

func main() {
	made := session.WithSessionTimeout(time.Minute)

	// The generic route, as it opened the option before, and in the option's shape now.
	_ = capture[session.BaseSessionOption]()
	_ = captureField[session.BaseSessionOption]()
	// An option of the program's own making: a function converted to one, and a struct
	// written out with and without the field's name.
	_ = session.BaseSessionOption(keep)
	_ = session.BaseSessionOption{set: keep}
	_ = session.BaseSessionOption{keep}
	// An option the library made, opened: called as the function it was, and its field read.
	made(nil)
	_ = made.set
	// The library's own constructor, which only the library's directory may import.
	_ = sessioncore.NewBaseSessionOption(nil)
}
