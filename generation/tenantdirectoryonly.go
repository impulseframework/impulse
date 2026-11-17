package generation

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ettle/strcase"
	"github.com/impulseframework/impulse/resource"
)

// A tenant record's directory-only switch (@directoryOnly) closes the tenant to every
// sign-in but one through its own directory, so turning it on is held to rules, and a
// page shows what it would do before it is saved. The generator writes both for a
// record that declares the switch and the directories record it is only of:
//
//   - the record's generated update asks the rules before it buffers a write of the
//     switch (resource.TenantDirectoryRules.DirectoryOnly; the splice is
//     directoryOnlyHeld): refused until the tenant has its directory, and unless the
//     caller is signed in through it;
//   - a read, GET <prefix>/directory-only?domain=<tenant>, answers what turning the
//     switch on would do (resource.DirectoryOnlyHandler): the sentence and the people
//     it names, read through the generated identity client of every auth whose people
//     the tenant seats (DirectoryOutsiders), or the refusal the write would answer.

// directoryOnlyHandlerFunc is the read's handler, the generated handlers interface's
// and the application's.
const directoryOnlyHandlerFunc = "DirectoryOnly"

// HoldsDirectoryOnly reports whether the resource is a tenant record whose update is
// held to the rules of the directory-only switch: it declares the switch and the
// directories record.
func (r *resourceInfo) HoldsDirectoryOnly() bool {
	return r.IsTenant && r.HasDeclaration() && r.DirectoryOnlyField != nil
}

// DirectoryOnlyRules is the expression of the rules the tenant record's update asks
// before it writes the directory-only switch: a call of New<Record>DirectoryRules, which
// the record's roster file declares in the handler package. Empty on every other
// resource.
func (r *resourceInfo) DirectoryOnlyRules() string {
	if !r.HoldsDirectoryOnly() {
		return ""
	}

	return "New" + r.Name() + "DirectoryRules()"
}

// DirectoryOnlyWord is the word for one tenant in the comments the tenant record's
// update carries beside the rules of its switch: the record's name in lower-case words.
// Empty on every other resource.
func (r *resourceInfo) DirectoryOnlyWord() string {
	if !r.HoldsDirectoryOnly() {
		return ""
	}

	return strings.ToLower(strcase.ToCase(r.Name(), strcase.LowerCase, ' '))
}

// directoryOnlyOutlets are the outlets the read is served on: every outlet that serves
// sessions and the tenant record, none for a record that declares no switch.
func (r *resourceGenerator) directoryOnlyOutlets() []*routerOutlet {
	record := r.tenantRecord()
	if record == nil || !record.HoldsDirectoryOnly() {
		return nil
	}
	var outlets []*routerOutlet
	for _, outlet := range r.allOutlets() {
		if outlet.servesSessions && record.OnOutlet(outlet.name) {
			outlets = append(outlets, outlet)
		}
	}

	return outlets
}

// directoryOnlyRoute is the read's route under an outlet's prefix.
func directoryOnlyRoute(prefix string) *generatedRoute {
	path := fmt.Sprintf("/%s/%s", prefix, resource.DirectoryOnlyRoute)

	return &generatedRoute{Method: http.MethodGet, Path: path, HandlerFunc: directoryOnlyHandlerFunc, TestURL: path}
}

// accumulateDirectoryOnlyRoute adds the read to every session outlet the tenant record
// is served on, for a record that declares the switch. The route joins the dispatch
// test.
func (r *resourceGenerator) accumulateDirectoryOnlyRoute(outlets []*routerOutlet, outletRoutes []*outletRouteData) (routerTestRoutes []*generatedRoute) {
	served := r.directoryOnlyOutlets()
	for i, outlet := range outlets {
		for _, on := range served {
			if on.name != outlet.name {
				continue
			}
			route := directoryOnlyRoute(outlet.prefix)
			outletRoutes[i].RoutesMap[route.HandlerFunc] = []*generatedRoute{route}
			routerTestRoutes = append(routerTestRoutes, route)
		}
	}

	return routerTestRoutes
}

// directoryOnlyOutsiders names the application's accessors of the generated identity
// clients the read asks who the switch keeps out: every auth whose management the
// program generates and whose client lists them (identityInstance.listsDirectoryOutsiders),
// in declaration order.
func (r *resourceGenerator) directoryOnlyOutsiders() []string {
	var accessors []string
	for _, inst := range r.identity {
		if inst.listsDirectoryOutsiders() {
			accessors = append(accessors, inst.accessor())
		}
	}

	return accessors
}
