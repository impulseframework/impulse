package generation

import (
	"slices"
	"strings"
	"testing"
)

// Test_resourceFileTemplate_orCreateAndOrSkip pins which generated patch offers which
// method. Every update patch offers OrCreate, since an update always names its row. A
// create patch offers OrSkip only where the caller names the key: a create patch that
// makes its own id never names a row that is there, so the method is not written and a
// call to it does not compile.
func Test_resourceFileTemplate_orCreateAndOrSkip(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: bindingFixtureTables()}
	structs := fixtureStructs(loadFixture(t, "bindingfixture"))

	tests := []struct {
		name       string
		structName string
		wantOrSkip bool
	}{
		{
			name:       "a key the caller names: the create patch offers OrSkip",
			structName: "ScalarOnCompositeKey",
			wantOrSkip: true,
		},
		{
			name:       "an id the create patch makes: no OrSkip",
			structName: "ScalarOnCompositeUnique",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.structName]
			if s == nil {
				t.Fatalf("struct %q not found in fixture package", tt.structName)
			}
			table, err := c.tableMetadataFor(tt.structName)
			if err != nil {
				t.Fatalf("tableMetadataFor(%s) error = %v", tt.structName, err)
			}
			res := &resourceInfo{TypeInfo: s.TypeInfo, PkCount: table.PkCount}
			res.Fields, err = newResourceFields(res, s, table)
			if err != nil {
				t.Fatalf("newResourceFields(%s) error = %v", tt.structName, err)
			}
			if got := !res.PrimaryKeyIsGeneratedUUID(); got != tt.wantOrSkip {
				t.Fatalf("the fixture's create patch takes its key = %v, want %v: the case no longer tests what it names", got, tt.wantOrSkip)
			}

			r := &resourceGenerator{client: c}
			output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{
				Source:   "fixture",
				Package:  "resources",
				Resource: res,
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			rendered := string(output)
			name := tt.structName

			// The update patch: the method, its answer, and a body that registers the
			// create's defaults on the shared patch set and hands over the create's Set.
			for _, want := range []string{
				"func (p *" + name + "UpdatePatch) OrCreate() *" + name + "UpdatePatch {",
				"(&" + name + "CreatePatch{patchSet: p.patchSet}).registerDefaultFuncs()",
				"WriteSets.For(accesstypes.Create))",
				"func (p *" + name + "UpdatePatch) RowCreated() bool {",
			} {
				if !strings.Contains(rendered, want) {
					t.Errorf("rendered %s is missing %q", name, want)
				}
			}

			// The create patch.
			orSkip := "func (p *" + name + "CreatePatch) OrSkip() *" + name + "CreatePatch {"
			rowSkipped := "func (p *" + name + "CreatePatch) RowSkipped() bool {"
			for _, method := range []string{orSkip, rowSkipped} {
				if got := strings.Contains(rendered, method); got != tt.wantOrSkip {
					t.Errorf("rendered %s carries %q = %v, want %v", name, method, got, tt.wantOrSkip)
				}
			}

			// Neither method crosses to the other patch.
			for _, never := range []string{
				"func (p *" + name + "CreatePatch) OrCreate(",
				"func (p *" + name + "UpdatePatch) OrSkip(",
				"func (p *" + name + "DeletePatch) OrCreate(",
				"func (p *" + name + "DeletePatch) OrSkip(",
			} {
				if strings.Contains(rendered, never) {
					t.Errorf("rendered %s carries %q", name, never)
				}
			}
		})
	}
}

// Test_handlerTemplates_decidedOperations pins what the generator writes for the batch's
// patch-or-add and add-or-skip, in both patch handlers and in the browser client's list
// of operations. A resource whose id the server makes gets neither: no request names a
// row of it that may or may not be there, and the decoder refuses the two operations for
// it. A resource whose key the caller names gets both. One of those whose create must
// carry a field no patch may carry gets add-or-skip alone, and answers a patch-or-add
// with the reason: no one value serves both the add and the patch.
func Test_handlerTemplates_decidedOperations(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadCollectionFixture(t))
	// The fixture's ids are UUIDs a create patch makes. A key that is also a foreign
	// key is one the caller names.
	callerNamesKey := func(res *resourceInfo) {
		res.IsConsolidated = true
		for _, f := range res.Fields {
			if f.IsPrimaryKey {
				f.IsForeignKey = true
			}
		}
	}

	tests := []struct {
		name          string
		res           *resourceInfo
		wantAddOrSkip bool
		wantDecision  bool
		wantRefusal   string
	}{
		{
			name: "an id the server makes: neither operation",
			res:  fixtureResource(t, structs, "Gadget", func(res *resourceInfo) { res.IsConsolidated = true }),
		},
		{
			name:          "a key the caller names: both operations",
			res:           fixtureResource(t, structs, "Gadget", callerNamesKey),
			wantAddOrSkip: true,
			wantDecision:  true,
		},
		{
			name:          "a key the caller names and a field only a create takes: add-or-skip alone",
			res:           fixtureResource(t, structs, "Widget", callerNamesKey),
			wantAddOrSkip: true,
			wantRefusal:   "a Widget takes no patch-or-add: a create must carry code, and a patch may not (the field is immutable)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.res.PatchOrAddRefusal(); got != tt.wantRefusal {
				t.Errorf("PatchOrAddRefusal() = %q, want %q", got, tt.wantRefusal)
			}

			c := &client{}
			consolidated, err := c.generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, consolidatedPatchData{
				Resources:       []*resourceInfo{tt.res},
				GlobalCases:     []consolidatedCaseData{{resourceInfo: tt.res, ResourcePackage: "resources", ReceiverName: "a"}},
				Package:         "app",
				ResourcePackage: "resources",
				ApplicationName: "App",
				ReceiverName:    "a",
				HandlerName:     "PatchResources",
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput(consolidatedPatchTemplate) error = %v", err)
			}
			own, err := c.generateTemplateOutput("handler", patchTemplate, handlerContentData{
				ResourcePackage: "resources",
				Resource:        tt.res,
				ApplicationName: "App",
				ReceiverName:    "a",
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput(patchTemplate) error = %v", err)
			}

			for handler, rendered := range map[string]string{"the consolidated handler": string(consolidated), "the resource's own handler": string(own)} {
				for fragment, want := range map[string]bool{
					// An add-or-skip runs as the add it is, with the create patch told
					// to leave a row that is there alone.
					"case resource.OperationCreateOrSkip:": tt.wantAddOrSkip,
					"op, orSkip = op.AsCreate(), true":     tt.wantAddOrSkip,
					"patch.OrSkip()":                       tt.wantAddOrSkip,
					// A patch-or-add is decided by the decoder, which asks for the
					// caller's Update grant before it looks for the row.
					"case resource.OperationUpdateOrCreate:": tt.wantAddOrSkip,
					"ecoder.DecideOperation(ctx, txn, op, ":  tt.wantDecision,
					"op = decided":                           tt.wantDecision,
				} {
					if got := strings.Contains(rendered, fragment); got != want {
						t.Errorf("%s carries %q = %v, want %v", handler, fragment, got, want)
					}
				}
				refusal := `return httpio.NewBadRequestMessage("` + tt.wantRefusal + `")`
				if tt.wantRefusal != "" && !strings.Contains(rendered, refusal) {
					t.Errorf("%s is missing the refusal %q", handler, refusal)
				}
				if tt.wantRefusal == "" && strings.Contains(rendered, "takes no patch-or-add") {
					t.Errorf("%s refuses a patch-or-add the resource takes", handler)
				}
			}

			// The browser client's builders follow the handlers.
			gen := &typescriptGenerator{client: c}
			ops := gen.apiResource(tt.res).Operations
			if got := slices.Contains(ops, opAddOrSkip); got != tt.wantAddOrSkip {
				t.Errorf("the client's operations %v carry %s = %v, want %v", ops, opAddOrSkip, got, tt.wantAddOrSkip)
			}
			if got := slices.Contains(ops, opPatchOrAdd); got != tt.wantDecision {
				t.Errorf("the client's operations %v carry %s = %v, want %v", ops, opPatchOrAdd, got, tt.wantDecision)
			}
		})
	}
}
