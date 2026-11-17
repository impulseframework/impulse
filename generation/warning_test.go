package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// TestResolveWarning pins the @warning contract: the declaration and the Warning method
// go together, the typed word is required and one lowercase word, the transaction form
// is the only one, an upload declares none, and each malformed shape is refused naming
// the struct and what to write.
func TestResolveWarning(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rpcform"))

	tests := []struct {
		name       string
		structName string
		wantTyped  string
		wantErr    string
	}{
		{name: "a declared warning with its method", structName: "WarningForm", wantTyped: "open"},
		{name: "a method that declares nothing warns of nothing", structName: "TxnForm"},
		{name: "a Warning method without the declaration is refused", structName: "WarningUndeclared", wantErr: "the method declares Warning but no @warning; declare the word a person types before the call is confirmed, for example @warning(typed: open)"},
		{name: "the declaration without a Warning method is refused", structName: "WarningNoMethod", wantErr: "@warning is declared but the method has no Warning; write the warning for the call beside Execute: Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error)"},
		{name: "the client form cannot warn", structName: "WarningClientForm", wantErr: "@warning requires the transaction form of Execute"},
		{name: "an upload cannot warn", structName: "WarningUpload", wantErr: "@warning on an upload"},
		{name: "the typed word is required, as typed:", structName: "WarningNoTyped", wantErr: "expected 0 positional argument(s), found 1"},
		{name: "the typed word is one lowercase word", structName: "WarningBadWord", wantErr: "the typed word is one lowercase word of 2 to 24 letters"},
		{name: "a Warning that answers no warning is refused", structName: "WarningWrongResult", wantErr: "Warning is declared returning (string, error); a @warning method declares Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error)"},
		{name: "a Warning with other parameters is refused", structName: "WarningWrongParams", wantErr: "Warning is declared taking (context.Context, *rpcform.Client)"},
		{name: "a Warning over another client is refused", structName: "WarningOtherClient", wantErr: "whose client is not the one Execute takes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.structName]
			if s == nil {
				t.Fatalf("struct %q not found in fixture package", tt.structName)
			}
			annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
			if err != nil {
				t.Fatalf("ScanStruct(%s) error = %v", tt.structName, err)
			}
			signature, err := classifyExecute(s)
			if err != nil {
				t.Fatalf("classifyExecute(%s) error = %v", tt.structName, err)
			}
			rpcMethod := &rpcMethodInfo{Struct: s, Form: signature.form, takesFiles: signature.takesFiles, filesStore: signature.filesStore}

			err = resolveWarning(rpcMethod, s, annotations)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveWarning(%s) error = %v, want containing %q", tt.structName, err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.structName) {
					t.Errorf("resolveWarning(%s) error does not name the struct: %v", tt.structName, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveWarning(%s) error = %v", tt.structName, err)
			}
			switch {
			case tt.wantTyped == "" && rpcMethod.Warning != nil:
				t.Errorf("Warning = %+v, want none", rpcMethod.Warning)
			case tt.wantTyped != "" && (rpcMethod.Warning == nil || rpcMethod.Warning.Typed != tt.wantTyped):
				t.Errorf("Warning = %+v, want typed %q", rpcMethod.Warning, tt.wantTyped)
			}
		})
	}
}

// Test_rpcHandlerTemplate_warning pins the warning in the generated frame. A method
// that declares one reads the header once the request is decoded and the caller
// checked, calls Warning in place of Execute inside the transaction, returns the
// sentinel so the transaction rolls back, and answers the warning. A method that
// declares none refuses the header before it runs anything, in either form of Execute,
// so a request that asks for a warning never runs the call.
func Test_rpcHandlerTemplate_warning(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "wirefixture"))
	c := &client{}

	warning := fixtureAnsweringMethod(t, structs, "Inspect")
	warning.Warning = &rpcWarning{Typed: "open"}

	tests := []struct {
		name            string
		method          *rpcMethodInfo
		wantContains    []string
		wantNotContains []string
		wantOrder       []string
	}{
		{
			name:   "a method that declares a warning answers it and rolls back",
			method: warning,
			wantContains: []string{
				"asksWarning := resource.AsksWarning(r)",
				"said, err := p.Warning(ctx, txn, a.RPCClient())",
				`resource.CheckWarning("Inspect", said)`,
				"warning = said\n\n\t\t\t\treturn resource.ErrWarningAsked",
				"if asksWarning && resource.WarningAsked(err) {\n\t\t\t\treturn httpio.NewEncoder(w).Ok(warning)\n\t\t\t}",
			},
			wantNotContains: []string{"declares no warning"},
			// The caller is checked before the warning is read, and the warning is read
			// before the body would run.
			wantOrder: []string{"decoder.DecodeCaller(r,", "if asksWarning {", "p.Execute(ctx, txn, a.RPCClient())"},
		},
		{
			name:   "a transaction-form method that declares none refuses the header",
			method: fixtureAnsweringMethod(t, structs, "Inspect"),
			wantContains: []string{
				"if resource.AsksWarning(r) {",
				`httpio.NewBadRequestMessage("Inspect declares no warning: send the call without the X-Warning header")`,
			},
			wantNotContains: []string{"asksWarning", "p.Warning(", "ErrWarningAsked"},
			wantOrder:       []string{"decoder.DecodeCaller(r,", "if resource.AsksWarning(r) {", "ExecuteFunc("},
		},
		{
			name:   "a client-form method refuses the header",
			method: fixtureClientMethod(t, structs, "Notify"),
			wantContains: []string{
				`httpio.NewBadRequestMessage("Notify declares no warning: send the call without the X-Warning header")`,
			},
			wantNotContains: []string{"asksWarning", "p.Warning("},
			wantOrder:       []string{"decoder.DecodeCaller(r,", "if resource.AsksWarning(r) {", "p.Execute(ctx, a.ResourceClient(), a.RPCClient())"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := c.generateTemplateOutput("rpcHandlerTemplate", rpcHandlerTemplate, &rpcHandlerData{
				Source:           "pkg/rpc",
				RPCMethod:        tt.method,
				Package:          "app",
				ApplicationName:  "App",
				ReceiverName:     "a",
				ResourcesPackage: "resources",
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("format.Source() error = %v on:\n%s", err, out)
			}
			text := string(formatted)
			for _, want := range tt.wantContains {
				if !strings.Contains(text, want) {
					t.Errorf("handler missing %q:\n%s", want, text)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(text, notWant) {
					t.Errorf("handler must not contain %q:\n%s", notWant, text)
				}
			}
			at := -1
			for _, next := range tt.wantOrder {
				i := strings.Index(text, next)
				if i < 0 {
					t.Fatalf("handler missing %q:\n%s", next, text)
				}
				if i <= at {
					t.Errorf("handler has %q before what must come first (%v):\n%s", next, tt.wantOrder, text)
				}
				at = i
			}
		})
	}
}
