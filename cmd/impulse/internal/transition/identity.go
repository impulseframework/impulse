package transition

import (
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// optGenerateIdentity is the generator option that generates the management of each
// auth's people into a tree the program owns.
const optGenerateIdentity = "GenerateIdentity"

// noteIdentityRetired records what is left to unwire when the named outlet of a site
// stops declaring its auth (the outlet is removed, or bound to another auth) and no
// other outlet of the site's program declares that auth: where the program generates
// each auth's management (GenerateIdentity), the regeneration removes the auth's three
// packages from the tree, and the hand-written code that names them, the client the
// auth's package builds and the accessor the generated contract asked for, no longer
// compiles. The tool does not find that code, so it is the agent's to remove. The site
// is the profile's reading from before the change.
func noteIdentityRetired(a *app.App, site *app.Site, outlet string, ch *Change) {
	tree := site.Generator.IdentityDir()
	if tree == "" {
		return
	}
	// Every outlet of the program, the one GenerateRoutes declares among them.
	outlets := site.AllOutlets()
	var declared *app.OutletAuth
	for i := range outlets {
		if outlets[i].Name == outlet {
			declared = outlets[i].Auth
		}
	}
	if declared == nil {
		return
	}
	for i := range outlets {
		if o := &outlets[i]; o.Name != outlet && o.Auth != nil && o.Auth.ImportPath == declared.ImportPath {
			return
		}
	}
	name := authName(a, declared.ImportPath)
	ch.skipf("%[1]s: no outlet declares the %[2]s auth any longer, so the regeneration removes its generated management under %[3]s (%[2]sidentity, %[2]shandlers and internal/%[2]sresources); remove what names it, which no longer compiles: the client the auth's package builds in New with the hooks it sets from it and its Identity method (or the package, where nothing signs in through the auth any longer), and %[4]sIdentity() on the Configurer, on the App and on every test configurer", site.Generator.File, name, tree, names.Pascal(name))
}
