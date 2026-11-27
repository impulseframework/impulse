package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// TestResolveDeleteToken pins the @deleteToken contract: one field named Token, of type
// string, on a transaction-form method that takes no files and is not scheduled, and
// each other shape refused naming the struct and what to write.
func TestResolveDeleteToken(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rpcform"))

	tests := []struct {
		name       string
		structName string
		want       string
		wantErr    string
	}{
		{name: "the field that carries the token", structName: "DeleteTokenForm", want: "Token"},
		{name: "a method that declares none carries none", structName: "TxnForm", want: ""},
		{name: "two fields are refused", structName: "DeleteTokenTwice", wantErr: "struct DeleteTokenTwice: @deleteToken appears on fields Token and Again; a method deletes one record, so one field carries its token"},
		{name: "a field that is not a string is refused", structName: "DeleteTokenNotText", wantErr: "struct DeleteTokenNotText field Token: @deleteToken is on a []byte; the token a delete's question gives is text, so the field is a string"},
		{name: "a field named otherwise is refused", structName: "DeleteTokenOtherName", wantErr: `struct DeleteTokenOtherName field Key: the field that carries a delete's token is named Token; the delete dialog sends it back as "token"`},
		{name: "the client form is refused", structName: "DeleteTokenClientForm", wantErr: "struct DeleteTokenClientForm: @deleteToken requires the transaction form of Execute (second parameter resource.ReadWriteTransaction); the handler asks the delete's question before it opens that transaction"},
		{name: "an upload is refused", structName: "DeleteTokenUpload", wantErr: "struct DeleteTokenUpload: @deleteToken on an upload; a delete's question is asked with the request alone, so declare it on a method that takes no files"},
		{name: "a scheduled method is refused", structName: "DeleteTokenScheduled", wantErr: "struct DeleteTokenScheduled: @deleteToken with @schedule: a token is given to a person's session, and a scheduled call has none; drop one of them"},
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

			err = resolveDeleteToken(rpcMethod, s, annotations)
			if tt.wantErr != "" {
				if err == nil || !strings.HasSuffix(err.Error(), "error="+tt.wantErr) {
					t.Fatalf("resolveDeleteToken(%s) error = %v, want one ending %q", tt.structName, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveDeleteToken(%s) error = %v", tt.structName, err)
			}
			if rpcMethod.DeleteToken != tt.want {
				t.Errorf("DeleteToken = %q, want %q", rpcMethod.DeleteToken, tt.want)
			}
		})
	}
}

// Test_rpcHandlerTemplate_deleteToken pins the token in the generated frame. A method
// that declares one stamps the field into the context once the request is converted, and
// runs its body through ExecuteDeleteFunc, asking first while the field is empty. A
// method that declares none runs through ExecuteFunc as before.
func Test_rpcHandlerTemplate_deleteToken(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "wirefixture"))
	c := &client{}

	inspect := func(token string) *rpcMethodInfo {
		method := fixtureAnsweringMethod(t, structs)
		method.DeleteToken = token

		return method
	}

	tests := []struct {
		name            string
		method          *rpcMethodInfo
		wantContains    []string
		wantNotContains []string
		wantOrder       []string
	}{
		{
			name:   "a method that declares a token asks first while it is empty",
			method: inspect("Token"),
			wantContains: []string{
				"ctx = resource.WithDeleteToken(ctx, p.Token)",
				`a.ResourceClient().ExecuteDeleteFunc(ctx, p.Token == "", func(ctx context.Context, txn resource.ReadWriteTransaction) error {`,
			},
			wantNotContains: []string{"ExecuteFunc(ctx, func("},
			wantOrder:       []string{"p := ", "ctx = resource.WithDeleteToken(ctx, p.Token)", "ExecuteDeleteFunc(", "p.Execute(ctx, txn, a.RPCClient())"},
		},
		{
			name:            "a method that declares none runs as before",
			method:          inspect(""),
			wantContains:    []string{"a.ResourceClient().ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {"},
			wantNotContains: []string{"WithDeleteToken", "ExecuteDeleteFunc"},
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
