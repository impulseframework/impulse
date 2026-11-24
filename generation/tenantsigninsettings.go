package generation

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/resource"
)

// A tenant's sign-in settings are what its Sign-in settings page shows a person who may
// change any of them: the tenant's directory, the role its people get, whether it is
// directory only and whom that keeps out, and whether it vouches for its directory's
// second factor, each with what the change takes of the caller
// (resource.TenantDirectoryRules.SignInSettings). The read is the tenant's own, so the
// generator writes it in the tenant's scope for a tenant record that declares a
// directories record, whether or not it declares the switch: GET
// <prefix>/<tenants>/{tenant}/sign-in-settings (resource.TenantSignInSettingsHandler),
// on every outlet that serves sessions and at least one of the tenant's own sign-in
// methods (signInSettingsMethods), by one handler asked under the grant of any of those
// methods the application serves, on whichever outlet (signInSettingsGrants).

// tenantSignInSettingsHandlerFunc is the read's handler, the generated handlers
// interface's and the application's.
const tenantSignInSettingsHandlerFunc = "TenantSignInSettings"

// signInSettingsMethods are the tenant's own sign-in methods the outlet serves, in the
// order the read names them: the two of the switch (directoryOnlyOutlets), the two of
// the default role (directoryRole) and the two of the word for the directory's second
// factor (directoryFactor). None where the outlet serves none of them, and the outlet
// then serves no read.
func (r *resourceGenerator) signInSettingsMethods(outlet string) []tenantOwnMethod {
	var methods []tenantOwnMethod
	switched := slices.ContainsFunc(r.directoryOnlyOutlets(), func(on *routerOutlet) bool {
		return on.name == outlet
	})
	if switched {
		methods = append(methods, directoryOnlyMethodsOf(r.tenantRecord().Name())...)
	}
	if role := r.directoryRole(); role.servedOn(outlet) {
		methods = append(methods, role.methods()...)
	}
	if factor := r.directoryFactor(); factor.servedOn(outlet) {
		methods = append(methods, factor.methods()...)
	}

	return methods
}

// signInSettingsGrants are the methods whose grants the read is asked under: every one
// of the tenant's own sign-in methods an outlet of the application serves, in the order
// signInSettingsMethods names them. The application has one handler of the read for all
// its outlets, and a grant is the application's, not an outlet's.
func (r *resourceGenerator) signInSettingsGrants() []tenantOwnMethod {
	return slices.Concat(r.directoryOnlyMethods(), r.directoryRole().methods(), r.directoryFactor().methods())
}

// signInSettingsRoute is the read's route under an outlet's prefix, under the tenant's
// own route and so behind the domain guard.
func (r *resourceGenerator) signInSettingsRoute(prefix string) *generatedRoute {
	return r.tenantOwnRoute(prefix, http.MethodGet, tenantSignInSettingsHandlerFunc, resource.TenantSignInSettingsRoute)
}

// tenantOwnOutletRoutes are the routes of the tenant's own sign-in settings an outlet
// serves: the read, and then the methods the outlet serves, none where it serves none.
func (r *resourceGenerator) tenantOwnOutletRoutes(outlet *routerOutlet) []*generatedRoute {
	methods := r.signInSettingsMethods(outlet.name)
	if len(methods) == 0 {
		return nil
	}

	return append([]*generatedRoute{r.signInSettingsRoute(outlet.prefix)}, r.tenantOwnRoutes(outlet.prefix, methods)...)
}

// tenantSignInSettingsData feeds the handler package's file of the read of a tenant's
// sign-in settings: the read's route, the methods whose grants it is asked under and the
// rules it says the settings by.
type tenantSignInSettingsData struct {
	Source  string
	Package string
	// Name is the tenant record's struct name and Word what a sentence calls one
	// tenant.
	Name string
	Word string
	// Func is the read's handler, Route its segment under the tenant's own route and
	// DomainRoute that route as a doc names it.
	Func        string
	Route       string
	DomainRoute string
	// Methods are the methods whose grants the read is asked under
	// (signInSettingsGrants).
	Methods []tenantOwnMethod
	// Rules is the expression that builds the rules of the record's directory as the
	// tenant's own methods are given them (switchDirectoryRules), so the read says what
	// each change takes of the caller in the words the method refuses in.
	Rules string

	LocalPackageImports string
	RouterPackage       string
	ApplicationName     string
	ReceiverName        string
}

// generateTenantSignInSettings emits the handler package's file of the read of a
// tenant's sign-in settings, where an outlet serves one of the tenant's own sign-in
// methods.
func (r *resourceGenerator) generateTenantSignInSettings() error {
	methods := r.signInSettingsGrants()
	if len(methods) == 0 {
		return nil
	}
	record := r.tenantRecord()

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantSignInSettingsOutputName))
	data := &tenantSignInSettingsData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		Name:                record.Name(),
		Word:                r.directoryTenantWord(record),
		Func:                tenantSignInSettingsHandlerFunc,
		Route:               resource.TenantSignInSettingsRoute,
		DomainRoute:         fmt.Sprintf("%s/{%s}", r.domainRouteSegment, r.domainRouteParam),
		Methods:             methods,
		Rules:               r.switchDirectoryRules(record),
		LocalPackageImports: r.localPackageImports(),
		RouterPackage:       r.router.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
	}
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantSignInSettingsTemplate", tenantSignInSettingsTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated the tenant's sign-in settings file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}
