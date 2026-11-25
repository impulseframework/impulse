package generation

import (
	"maps"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/resource"
)

// A record's change history: every table-backed resource with a read route serves its
// records' history a page at a time at GET <the read route>/changes
// (resource.QueryDecoder.DecodeChangeHistory), under the record's own Read grant and with
// the fields the reader may see on the record. The resource's Config says whether it
// tracks its changes, and that is Go the generator cannot read, so the route is written
// for every such resource and answers 404 for one that does not track them.

// changeHistorySuffix ends the name of a record's change history handler: the read
// handler's, followed by ChangeHistory.
const changeHistorySuffix = "ChangeHistory"

// servesChangeHistory reports whether a resource serves its records' change history: a
// table-backed resource whose read route is written.
func servesChangeHistory(res *resourceInfo) bool {
	return !res.IsVirtual && !res.RoutingDisabled() && slices.Contains(resourceEndpoints(res), ReadHandler)
}

// resourceChangeHistoryRoutes builds the route of a record's change history under the
// outlet route prefix, from the read route as a stored file's route is built: the read
// route's path with the history's segment appended, its parameters, its tenant's guard
// and its feature flag. Empty for a resource that serves no history (servesChangeHistory).
func (r *resourceGenerator) resourceChangeHistoryRoutes(res *resourceInfo, routePrefix string) ([]*generatedRoute, error) {
	if !servesChangeHistory(res) {
		return nil, nil
	}
	read, err := r.resourceRoute(res, ReadHandler, routePrefix)
	if err != nil {
		return nil, err
	}

	return []*generatedRoute{{
		Method:       changeHistoryHandler.method(),
		Path:         read.Path + "/" + resource.ChangeHistoryRoute,
		HandlerFunc:  res.Name() + changeHistorySuffix,
		HandlerType:  changeHistoryHandler,
		DomainScoped: read.DomainScoped,
		TestURL:      read.TestURL + "/" + resource.ChangeHistoryRoute,
		TestParams:   slices.Clone(read.TestParams),
		Feature:      read.Feature,
		Source:       res.Name(),
	}}, nil
}

// validateChangeHistoryRoutes refuses a record's change history whose route another
// generated route of the same outlet already answers (a @file segment named changes), or
// whose handler's name another generated handler of the outlet already has (a resource
// named as another's history handler).
func validateChangeHistoryRoutes(outletRoutes []*outletRouteData) error {
	for _, outlet := range outletRoutes {
		takenPaths := make(map[string]string)
		takenNames := make(map[string]string)
		var histories []*generatedRoute
		for _, name := range slices.Sorted(maps.Keys(outlet.RoutesMap)) {
			for _, route := range outlet.RoutesMap[name] {
				if route.HandlerType == changeHistoryHandler {
					histories = append(histories, route)

					continue
				}
				for _, method := range routeMethods(route) {
					takenPaths[method+" "+route.Path] = route.HandlerFunc
				}
				takenNames[route.HandlerFunc] = "a handler of " + name
			}
		}
		for _, route := range histories {
			if holder, ok := takenPaths[route.Method+" "+route.Path]; ok {
				return errors.Newf("struct %s: the record's change history would be served at GET %s, where %s already answers on the %s outlet; GET <read route>/changes is the history of every table-backed resource, so name that route's segment something else", route.Source, route.Path, holder, outlet.Name)
			}
			if holder, ok := takenNames[route.HandlerFunc]; ok {
				return errors.Newf("struct %s: the handler of the record's change history would be named %s, which %s already is on the %s outlet; rename one of the two structs", route.Source, route.HandlerFunc, holder, outlet.Name)
			}
		}
	}

	return nil
}

// changeHistoryHandlerContent renders the handler of a record's change history.
func (r *resourceGenerator) changeHistoryHandlerContent(res *resourceInfo) ([]byte, error) {
	home := r.homeOf(res.identity)
	output, err := r.generateTemplateOutput("changeHistoryHandler", changeHistoryTemplate, handlerContentData{
		ResourcePackage:         home.resources.Package(),
		Resource:                res,
		VirtualResourcesPackage: home.views.Package(),
		ApplicationName:         home.applicationName,
		ReceiverName:            home.receiverName,
		Home:                    home,
	})
	if err != nil {
		return nil, errors.Wrap(err, "generateTemplateOutput()")
	}

	return output, nil
}
