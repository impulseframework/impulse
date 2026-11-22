// Command nocollection is a program that does not compile, and is kept for that: it builds
// a Client in each way an application could before the release's permission collection
// became an argument of the constructor, with no collection or with the collection handed
// to an option. The access package's tests type-check it and pin the refusals
// (TestNew_withoutTheCollectionDoesNotCompile).
package main

import (
	"time"

	"github.com/impulseframework/impulse/access"
)

func main() {
	var (
		store      access.Store
		collection access.PermissionCollection
		roles      access.RoleFile
	)

	// A Client of a release that says nothing of what it declares.
	_, _ = access.New(store)

	// An option where the collection goes.
	_, _ = access.New(store, access.WithHeartbeatInterval(time.Minute))

	// The collection handed to the option that carried it.
	_, _ = access.New(store, access.WithPermissionCollection(collection))

	// The collection handed over beside the role file and the auth's name.
	_, _ = access.New(store, collection, access.WithDefaultRoles(collection, roles, "staff"))
}
