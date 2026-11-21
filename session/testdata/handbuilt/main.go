// Command handbuilt is a program that does not compile, and is kept for that: it writes
// the request an impersonated session starts from by hand, naming the actor, the
// principal and the mask itself, as an application could before the declaration became
// the only way to make one. The session package's tests type-check it and pin the
// refusals (TestImpersonationRequest_builtByHandDoesNotCompile).
package main

import (
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
)

func main() {
	_ = &session.ImpersonationRequest{
		Actor:     "alice",
		Principal: accesstypes.UserPrincipal("bob"),
		Mask:      accesstypes.AllowAll(),
	}
}
