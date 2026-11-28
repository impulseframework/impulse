package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Test_namedRowSources pins each home's named-row sources: the views and the computed
// lists of the home that a declared column names, in resource order, a view by its type
// and a computed list by its row type, its list function and its key's field, each as the
// home's handlers package names it; and the generated file of each home: the
// application's method merges its own with each auth's management's, in the auths' name
// order; an auth's Handlers declare their own; a home with none answers an empty map.
func Test_namedRowSources(t *testing.T) {
	t.Parallel()

	c, err := nameFixtureResources(t, "Base", "Wing", "Hangar", "Member", "Flight", "Drill", "Shift", "Ration", "Rota")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	crew := &identityInstance{auth: "Crew", importPath: crewAuthPath, resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handlers: "pkg/identity/crewhandlers"}
	pilots := &identityInstance{auth: "Pilots", resources: "pkg/identity/internal/pilotsresources", methods: "pkg/identity/pilotsidentity", handlers: "pkg/identity/pilotshandlers"}
	for _, res := range c.resources {
		if res.Name() == "Hangar" {
			res.identity = crew
		}
	}
	for _, res := range c.computedResources {
		if res.Name() == "Shift" {
			res.identity = crew
		}
	}
	c.virtual, c.computed, c.resource = "pkg/virtualresources", "pkg/computedresources", "pkg/resources"
	c.identity = []*identityInstance{pilots, crew}
	r := &resourceGenerator{client: c, genRoutes: true, handler: "app", applicationName: "App", receiverName: "a"}
	r.resolveNamedRows(c.resources)

	app := r.namedRowSourcesOf(r.homeOf(nil))
	wantApp := []namedRowSourceData{
		{Resource: "Drills", Type: "computedresources.Drill", List: "computedresources.ListDrill", Key: "ID"},
		{Resource: "Flights", View: "virtualresources.Flight"},
		{Resource: "Members", View: "virtualresources.Member"},
	}
	if diff := cmp.Diff(wantApp, app.Entries); diff != "" {
		t.Errorf("the application's entries mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"crewhandlers", "pilotshandlers"}, app.Auths); diff != "" {
		t.Errorf("the auths the application merges mismatch (-want +got):\n%s", diff)
	}
	ownCrew := r.namedRowSourcesOf(r.homeOf(crew))
	wantCrew := []namedRowSourceData{
		{Resource: "Hangars", View: "crewresources.Hangar"},
		{Resource: "Shifts", Type: "crewidentity.Shift", List: "crewidentity.ListShift", Key: "ID"},
	}
	if diff := cmp.Diff(wantCrew, ownCrew.Entries); diff != "" {
		t.Errorf("the crew auth's entries mismatch (-want +got):\n%s", diff)
	}

	contains := func(t *testing.T, of string, data *namedRowSourcesData, want ...string) {
		t.Helper()

		out := renderFormatted(t, "namedRowSourcesTemplate", namedRowSourcesTemplate, data)
		for _, text := range want {
			if !strings.Contains(out, text) {
				t.Errorf("%s lacks %q:\n%s", of, text, out)
			}
		}
	}
	contains(t, "the application's file", app,
		"package app",
		"func (a *App) namedRowSources() resource.NamedRowSources {\n\treturn resource.MergeNamedRowSources(\n\t\tresource.NamedRowSources{\n",
		"\t\t\t\"Drills\": resource.ComputedRows(\"ID\", func(ctx context.Context, q *resource.QuerySet[computedresources.Drill]) iter.Seq2[*computedresources.Drill, error] {\n\t\t\t\treturn computedresources.ListDrill(ctx, q, a.ResourceClient(), a.ComputedClient())\n\t\t\t}),\n",
		"\t\t\t\"Flights\": resource.ViewRows(virtualresources.Flight{}),\n",
		"\t\tcrewhandlers.New(a, liveDerivations).NamedRowSources(),\n\t\tpilotshandlers.New(a, liveDerivations).NamedRowSources(),\n\t)\n}",
	)
	contains(t, "the crew auth's file", ownCrew,
		"package crewhandlers",
		"func (h *Handlers) NamedRowSources() resource.NamedRowSources {\n\treturn resource.NamedRowSources{\n",
		"\t\t\"Hangars\": resource.ViewRows(crewresources.Hangar{}),\n",
		"\t\t\"Shifts\": resource.ComputedRows(\"ID\", func(ctx context.Context, q *resource.QuerySet[crewidentity.Shift]) iter.Seq2[*crewidentity.Shift, error] {\n\t\t\treturn crewidentity.ListShift(ctx, q, h.ResourceClient(), h.ComputedClient())\n\t\t}),\n",
	)
	none := r.namedRowSourcesOf(r.homeOf(pilots))
	if len(none.Entries) != 0 {
		t.Errorf("the pilots auth's entries = %v, want none", none.Entries)
	}
	contains(t, "a home with none", none, "func (h *Handlers) NamedRowSources() resource.NamedRowSources {\n\treturn resource.NamedRowSources{}\n}")
}

// Test_decodersTemplate_namedRowSources pins that each decoder a write can come through
// is handed the home's named-row sources.
func Test_decodersTemplate_namedRowSources(t *testing.T) {
	t.Parallel()

	out, err := (&client{}).generateTemplateOutput("decodersTemplate", decodersTemplate, decodersFileData{
		Package: "app", ApplicationName: "App", ReceiverName: "a", RPCPackage: "rpc", RouterPackage: "router",
		HasComputedQueryDecoder: true, HasPatchDecoder: true, HasRPCDecoder: true, HasTargetedRPCDecoder: true, HasCollection: true,
		NamedRowSources: "a.namedRowSources()",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	for _, want := range []string{
		"resource.MustNewComputedQueryDecoder[Resource, Request](a.ResourceClient().DBType(), permissions...).WithCursorKey(a.CursorKey()).WithFeatures(a.FeatureSet()).WithCollection(router.Collection()).WithSubjectFacts(a.ResourceClient()).WithNamedRowSources(a.namedRowSources())",
		"resource.MustNewDecoder[Resource, Request](a, router.Collection(), permissions...).WithFeatures(a.FeatureSet()).WithNamedRowSources(a.namedRowSources())",
		"resource.MustNewRPCDecoder[Request](a, method.Method(), perm).WithCollection(router.Collection()).WithNamedRowSources(a.namedRowSources())",
		"resource.MustNewTargetedRPCDecoder[Request](a, router.Collection(), method.Method(), perm).WithNamedRowSources(a.namedRowSources())",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("decodersTemplate output lacks %q:\n%s", want, out)
		}
	}
}
