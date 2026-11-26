package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser"
)

// keyOrderFixtureTables is the table of the keyorderfixture's InvoiceLine: InvoiceLines,
// keyed by its invoice and then its number, the other way round from the struct's order.
func keyOrderFixtureTables(invoiceOrdinal, lineOrdinal int64) map[string]*tableMetadata {
	return map[string]*tableMetadata{
		"InvoiceLines": {PkCount: 2, Columns: map[string]columnMeta{
			"InvoiceId":   {IsPrimaryKey: true, KeyOrdinalPosition: invoiceOrdinal},
			"LineNumber":  {IsPrimaryKey: true, KeyOrdinalPosition: lineOrdinal},
			"Description": {},
			"UpdatedAt":   {},
		}},
	}
}

// keyOrderFixtureResource makes the fixture's InvoiceLine a resource over the client's
// table, as the generator makes a table's struct one, and answers the struct beside it.
func keyOrderFixtureResource(t *testing.T, c *client) (*resourceInfo, *parser.Struct) {
	t.Helper()

	s := fixtureStructs(loadFixture(t, "keyorderfixture"))["InvoiceLine"]
	if s == nil {
		t.Fatal("struct InvoiceLine not found in fixture package")
	}
	table, err := c.tableMetadataFor("InvoiceLine")
	if err != nil {
		t.Fatalf("tableMetadataFor(InvoiceLine) error = %v", err)
	}
	res := &resourceInfo{TypeInfo: s.TypeInfo, PkCount: table.PkCount}
	res.Fields, err = newResourceFields(res, s, table)
	if err != nil {
		t.Fatalf("newResourceFields(InvoiceLine) error = %v", err)
	}

	return res, s
}

// primaryKeyNames lists the names PrimaryKeys yields, each at the index it yields.
func primaryKeyNames(t *testing.T, res *resourceInfo) []string {
	t.Helper()

	var names []string
	for i, field := range res.PrimaryKeys() {
		if i != len(names) {
			t.Errorf("PrimaryKeys() yields %s at %d, want %d", field.Name(), i, len(names))
		}
		names = append(names, field.Name())
	}

	return names
}

// collapseSpace folds every run of white space in s into one space, so a test can name
// generated code without its indentation.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// inOrder reports an error when rendered does not hold each of want, or holds them in
// another order.
func inOrder(t *testing.T, what, rendered string, want ...string) {
	t.Helper()

	at := -1
	for _, w := range want {
		i := strings.Index(rendered, w)
		if i < 0 {
			t.Errorf("%s lacks %q:\n%s", what, w, rendered)

			return
		}
		if i < at {
			t.Errorf("%s holds %q out of the order %q:\n%s", what, w, want, rendered)

			return
		}
		at = i
	}
}

// Test_PrimaryKeys_keyOrder pins the order of a table's key: the table's, whatever order
// its struct lists the key fields in, and the struct's where the ordinals do not tell
// the fields apart.
func Test_PrimaryKeys_keyOrder(t *testing.T) {
	t.Parallel()

	t.Run("the table's order", func(t *testing.T) {
		t.Parallel()

		res, _ := keyOrderFixtureResource(t, &client{tableMap: keyOrderFixtureTables(0, 1)})
		if got, want := primaryKeyNames(t, res), []string{"InvoiceID", "LineNumber"}; !slices.Equal(got, want) {
			t.Errorf("PrimaryKeys() = %v, want %v", got, want)
		}
		if got := res.PrimaryKey(); got == nil || got.Name() != "InvoiceID" {
			t.Errorf("PrimaryKey() = %v, want InvoiceID", got)
		}
		if got, want := res.KeyParamList(), "invoiceID, lineNumber"; got != want {
			t.Errorf("KeyParamList() = %q, want %q", got, want)
		}
	})

	t.Run("equal ordinals keep the struct's order", func(t *testing.T) {
		t.Parallel()

		res, _ := keyOrderFixtureResource(t, &client{tableMap: keyOrderFixtureTables(0, 0)})
		if got, want := primaryKeyNames(t, res), []string{"LineNumber", "InvoiceID"}; !slices.Equal(got, want) {
			t.Errorf("PrimaryKeys() = %v, want %v", got, want)
		}
	})
}

// Test_resourceFileTemplate_keyOrder pins the key each patch constructor of a table's
// resource file takes and sets: in the table's order, both, for every constructor.
func Test_resourceFileTemplate_keyOrder(t *testing.T) {
	t.Parallel()

	res, _ := keyOrderFixtureResource(t, &client{tableMap: keyOrderFixtureTables(0, 1)})
	r := &resourceGenerator{client: &client{}}
	data := &resourceFileData{Source: "fixture", Package: "resources", Resource: res}
	out, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	formatted, err := r.formatGoBytes("pkg/resources/zz_gen_fixture.go", "resourceFileTemplate", out, data)
	if err != nil {
		t.Fatalf("formatGoBytes() error = %v\n%s", err, out)
	}
	rendered := string(formatted)

	for _, constructor := range []string{
		"NewInvoiceLineCreatePatchFromPatchSet",
		"NewInvoiceLineCreatePatch",
		"NewInvoiceLineUpdatePatchFromPatchSet",
		"NewInvoiceLineUpdatePatch",
		"NewInvoiceLineTouch",
		"NewInvoiceLineDeletePatchFromPatchSet",
		"NewInvoiceLineDeletePatch",
	} {
		t.Run(constructor, func(t *testing.T) {
			t.Parallel()

			_, after, found := strings.Cut(rendered, "\nfunc "+constructor+"(")
			if !found {
				t.Fatalf("the resource file declares no %s:\n%s", constructor, rendered)
			}
			params, body, _ := strings.Cut(after, "{\n")
			body, _, _ = strings.Cut(body, "\n}\n")
			inOrder(t, constructor+"'s parameters", params, "invoiceID impulse.UUID", "lineNumber int64")
			inOrder(t, constructor+"'s key", collapseSpace(body), `SetKey("InvoiceID", invoiceID).`, `SetKey("LineNumber", lineNumber).`)
		})
	}
}

// Test_handlerTemplates_keyOrder pins the key a table's handlers read from a request
// and address the row by: in the table's order in the read, the history, and both patch
// handlers.
func Test_handlerTemplates_keyOrder(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: keyOrderFixtureTables(0, 1)}
	res, _ := keyOrderFixtureResource(t, c)
	res.IsConsolidated = true
	handlerData := handlerContentData{ResourcePackage: "resources", Resource: res, ApplicationName: "App", ReceiverName: "a"}
	render := func(t *testing.T, name, template string, data any) string {
		t.Helper()

		out, err := c.generateTemplateOutput(name, template, data)
		if err != nil {
			t.Fatalf("generateTemplateOutput(%s) error = %v", name, err)
		}

		return collapseSpace(string(out))
	}

	t.Run("the read handler", func(t *testing.T) {
		t.Parallel()

		rendered := render(t, "readTemplate", readTemplate, handlerData)
		inOrder(t, "the read handler", rendered,
			"invoiceID := httpio.Param[impulse.UUID](r, router.InvoiceLineInvoiceID)",
			"lineNumber := httpio.Param[int64](r, router.InvoiceLineLineNumber)",
		)
		for _, want := range []string{
			"resource.RowKey(invoiceID, lineNumber)",
			".SetInvoiceID(invoiceID).SetLineNumber(lineNumber)",
		} {
			if !strings.Contains(rendered, want) {
				t.Errorf("the read handler lacks %q:\n%s", want, rendered)
			}
		}
	})

	t.Run("the history handler", func(t *testing.T) {
		t.Parallel()

		r := &resourceGenerator{client: c, applicationName: "App", receiverName: "a"}
		out, err := r.changeHistoryHandlerContent(res)
		if err != nil {
			t.Fatalf("changeHistoryHandlerContent() error = %v", err)
		}
		if rendered, want := collapseSpace(string(out)), ".SetInvoiceID(invoiceID).SetLineNumber(lineNumber)"; !strings.Contains(rendered, want) {
			t.Errorf("the history handler lacks %q:\n%s", want, rendered)
		}
	})

	for name, rendered := range map[string]func(t *testing.T) string{
		"the resource's own patch handler": func(t *testing.T) string {
			t.Helper()

			return render(t, "patchTemplate", patchTemplate, handlerData)
		},
		"the consolidated patch handler": func(t *testing.T) string {
			t.Helper()

			return render(t, "consolidatedPatchTemplate", consolidatedPatchTemplate, consolidatedPatchData{
				Resources:       []*resourceInfo{res},
				GlobalCases:     []consolidatedCaseData{{resourceInfo: res, ResourcePackage: "resources", ReceiverName: "a"}},
				Package:         "app",
				ResourcePackage: "resources",
				ApplicationName: "App",
				ReceiverName:    "a",
				HandlerName:     "PatchResources",
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rendered := rendered(t)
			for _, want := range []string{
				`id1 := httpio.Param[impulse.UUID](`,
				`id2 := httpio.Param[int64](`,
			} {
				if !strings.Contains(rendered, want) {
					t.Errorf("%s lacks %q:\n%s", name, want, rendered)
				}
			}
			if strings.Contains(rendered, `id1 := httpio.Param[int64](`) {
				t.Errorf("%s reads id1 as the line number:\n%s", name, rendered)
			}
			inOrder(t, name+"'s patch-or-add key", rendered,
				`key := resource.KeySet{} key = key.Add("InvoiceID", httpio.Param[impulse.UUID](`,
				`"id1")) key = key.Add("LineNumber", httpio.Param[int64](`,
				`"id2"))`,
			)
		})
	}
}

// Test_resourceRoute_keyOrder pins a table's read route: its parameters in the table's
// key order, the route's path and the router tests' parameters alike.
func Test_resourceRoute_keyOrder(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: keyOrderFixtureTables(0, 1)}
	res, _ := keyOrderFixtureResource(t, c)
	r := &resourceGenerator{client: c, genRoutes: true, routePrefix: "api", applicationName: "App", receiverName: "a"}
	route, err := r.resourceRoute(res, ReadHandler, "api")
	if err != nil {
		t.Fatalf("resourceRoute() error = %v", err)
	}

	var keys []string
	for _, param := range route.TestParams {
		keys = append(keys, param.Key)
	}
	if want := []string{"invoiceLineInvoiceID", "invoiceLineLineNumber"}; !slices.Equal(keys, want) {
		t.Errorf("TestParams = %v, want %v", keys, want)
	}
	if want := "/{invoiceLineInvoiceID}/{invoiceLineLineNumber}"; !strings.HasSuffix(route.Path, want) {
		t.Errorf("Path = %q, want it to end with %q", route.Path, want)
	}
}

// Test_apiResource_keyOrder pins the TypeScript client's key of a table: in the table's
// key order.
func Test_apiResource_keyOrder(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: keyOrderFixtureTables(0, 1)}
	res, _ := keyOrderFixtureResource(t, c)
	gen := &typescriptGenerator{client: c}

	var keys []string
	for _, key := range gen.apiResource(res).Keys {
		keys = append(keys, key.Name)
	}
	if want := []string{"invoiceId", "lineNumber"}; !slices.Equal(keys, want) {
		t.Errorf("Keys = %v, want %v", keys, want)
	}
}

// Test_targetRootKeys_keyOrder pins the key a method names its target row of a table by:
// in the table's key order, which its generated code hands the row's patch constructor.
func Test_targetRootKeys_keyOrder(t *testing.T) {
	t.Parallel()

	res, s := keyOrderFixtureResource(t, &client{tableMap: keyOrderFixtureTables(0, 1)})
	keys, err := targetRootKeys(s, "InvoiceLine", res, targetKeyword)
	if err != nil {
		t.Fatalf("targetRootKeys() error = %v", err)
	}

	var names []string
	for _, key := range keys {
		names = append(names, key.Name())
	}
	if want := []string{"InvoiceID", "LineNumber"}; !slices.Equal(names, want) {
		t.Errorf("targetRootKeys() = %v, want %v", names, want)
	}
}
