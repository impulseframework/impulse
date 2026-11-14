package generation

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// A field declared conditions:"secret" is write-only without saying so, and the fact
// that it is a secret is its own: a field that is write-only alone is no secret.
func Test_resourceField_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	res := fixtureResource(t, structs, "Lockbox", nil)

	tests := []struct {
		field         string
		wantSecret    bool
		wantInputOnly bool
		wantReadTag   string
	}{
		{field: "Name", wantReadTag: `json:"name"`},
		{field: "Combination", wantSecret: true, wantInputOnly: true, wantReadTag: `json:"-"`},
		{field: "Manifest", wantInputOnly: true, wantReadTag: `json:"-"`},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			var field *resourceField
			for _, f := range res.Fields {
				if f.Name() == tt.field {
					field = f
				}
			}
			if field == nil {
				t.Fatalf("field %s not found in the fixture", tt.field)
			}
			if got := field.IsSecret(); got != tt.wantSecret {
				t.Errorf("IsSecret() = %v, want %v", got, tt.wantSecret)
			}
			if got := field.IsInputOnly(); got != tt.wantInputOnly {
				t.Errorf("IsInputOnly() = %v, want %v", got, tt.wantInputOnly)
			}
			// The read and list structs hide it; the patch struct takes it.
			if got := field.JSONTag(); got != tt.wantReadTag {
				t.Errorf("JSONTag() = %s, want %s", got, tt.wantReadTag)
			}
			if got := field.JSONTagForPatch(); strings.Contains(got, `"-"`) {
				t.Errorf("JSONTagForPatch() = %s, want the field accepted on a write", got)
			}
		})
	}
}

// The generator refuses a secret the field cannot honor: one a client never sets or
// could not be kept from reading, and one a form would not draw as masked text.
func Test_checkSecretDeclarations(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	combination := func(res *resourceInfo) *resourceField {
		for _, f := range res.Fields {
			if f.Name() == "Combination" {
				return f
			}
		}
		t.Fatal("field Combination not found in the fixture")

		return nil
	}

	tests := []struct {
		name    string
		mutate  func(*resourceInfo)
		wantErr string
	}{
		{name: "a secret in a STRING column the client sets"},
		{
			name:   "a secret in a BYTES column",
			mutate: func(res *resourceInfo) { combination(res).SpannerType = "BYTES(MAX)" },
		},
		{
			name:    "on a primary key",
			mutate:  func(res *resourceInfo) { combination(res).IsPrimaryKey = true },
			wantErr: `Lockbox.Combination: conditions:"secret" on a primary key`,
		},
		{
			name:    "on a field the server owns",
			mutate:  func(res *resourceInfo) { combination(res).IsState = true },
			wantErr: `Lockbox.Combination: conditions:"secret" on a field the server owns`,
		},
		{
			name:    "on a field of a view",
			mutate:  func(res *resourceInfo) { res.IsVirtual = true },
			wantErr: `Lockbox.Combination: conditions:"secret" on a field of a view`,
		},
		{
			name: "on a foreign key",
			mutate: func(res *resourceInfo) {
				combination(res).IsForeignKey = true
				combination(res).ReferencedResource = "Vaults"
			},
			wantErr: `Lockbox.Combination: conditions:"secret" on a foreign key: the value is the key of a row of Vaults`,
		},
		{
			name: "on a field with a picker",
			mutate: func(res *resourceInfo) {
				vaults := genlang.Arg("Vaults")
				combination(res).enumerateArg = &vaults
			},
			wantErr: `Lockbox.Combination: conditions:"secret" on a field with a picker (@enumerate)`,
		},
		{
			name:    "on a number",
			mutate:  func(res *resourceInfo) { combination(res).SpannerType = "INT64" },
			wantErr: `Lockbox.Combination: conditions:"secret" on a INT64 column: a secret is text or bytes`,
		},
		{
			name:    "on a list of text",
			mutate:  func(res *resourceInfo) { combination(res).SpannerType = "ARRAY<STRING(MAX)>" },
			wantErr: `Lockbox.Combination: conditions:"secret" on a ARRAY<STRING(MAX)> column: a secret is text or bytes`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := fixtureResource(t, structs, "Lockbox", func(res *resourceInfo) {
				combination(res).SpannerType = "STRING(64)"
				if tt.mutate != nil {
					tt.mutate(res)
				}
			})
			err := checkSecretDeclarations(res)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("checkSecretDeclarations() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("checkSecretDeclarations() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// A method's request field is held to the same rule where the method is extracted: a
// secret a dialog masks (text) or draws no control over (bytes) is taken, and one it
// would draw in the open or list in a picker is refused, naming the field.
func Test_structsToRPCMethods_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))

	tests := []struct {
		method    string
		wantField string
		wantErr   string
	}{
		{method: "OpenLockbox"},
		{method: "SealLockbox"},
		{method: "DialLockbox", wantField: "Number", wantErr: `conditions:"secret" on a number field: a secret is text or bytes`},
		{method: "PickLockbox", wantField: "LockboxID", wantErr: `conditions:"secret" on a field with a picker (@enumerate)`},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()

			c := &client{resources: []*resourceInfo{fixtureResource(t, structs, "Lockbox", nil)}}
			methods, err := c.structsToRPCMethods([]*parser.Struct{structs[tt.method]})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("structsToRPCMethods() error = %v, want none", err)
				}
				var secrets int
				for _, f := range methods[0].Fields {
					if f.IsSecret() {
						secrets++
					}
				}
				if secrets != 1 {
					t.Errorf("the method carries %d secret field(s), want the one", secrets)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("structsToRPCMethods() error = %v, want it to name %s with %q", err, tt.wantField, tt.wantErr)
			}
		})
	}
}

// The browser metadata says which field is a secret, on a resource and on a method, so
// the form types it into a masked input; a write-only field that is no secret says
// write-only alone.
func Test_typescriptTemplates_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))

	g := &typescriptGenerator{client: &client{}}
	resources, err := g.generateTemplateOutput("typescriptResourcesTemplate", typescriptResourcesTemplate, tsResourcesData{
		File: g,
		Resources: []*resourceInfo{fixtureResource(t, structs, "Lockbox", func(res *resourceInfo) {
			for _, f := range res.Fields {
				f.typescriptType = "string"
			}
		})},
		GenPrefix: "zz_gen",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput(typescriptResourcesTemplate) error = %v", err)
	}
	for _, want := range []string{
		"{ fieldName: 'combination', displayType: 'string', required: true, isIndex: false, writeOnly: true, secret: true },",
		"{ fieldName: 'manifest', displayType: 'string', required: true, isIndex: false, writeOnly: true },",
		"{ fieldName: 'name', displayType: 'string', required: true, isIndex: false },",
	} {
		if !strings.Contains(string(resources), want) {
			t.Errorf("the resources metadata lacks %q\n%s", want, resources)
		}
	}
	// The row interface carries neither write-only field.
	for _, absent := range []string{"  combination?:", "  manifest?:"} {
		if strings.Contains(string(resources), absent) {
			t.Errorf("the row interface carries %q", absent)
		}
	}

	open := structs["OpenLockbox"]
	if open == nil {
		t.Fatal("struct OpenLockbox not found in the fixture")
	}
	method := &rpcMethodInfo{Struct: open}
	for _, f := range open.Fields() {
		method.Fields = append(method.Fields, &rpcField{Field: f, typescriptType: "string"})
	}
	c := &client{rpcMethods: []*rpcMethodInfo{method}}
	methods, err := c.generateTemplateOutput("typescriptMethodsTemplate", typescriptMethodsTemplate, tsMethodsData{
		File:       &typescriptGenerator{client: c},
		RPCMethods: c.rpcMethods,
		GenPrefix:  "zz_gen",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput(typescriptMethodsTemplate) error = %v", err)
	}
	for _, want := range []string{
		"{ fieldName: 'combination', displayType: 'string', required: true, secret: true },",
		"{ fieldName: 'note', displayType: 'string', optional: true },",
	} {
		if !strings.Contains(string(methods), want) {
			t.Errorf("the methods metadata lacks %q\n%s", want, methods)
		}
	}
	if got := strings.Count(string(methods), ", secret: true }"); got != 1 {
		t.Errorf("the methods metadata marks %d field(s) secret, want the one", got)
	}
	// The file declares the type its entries are checked against, so the flag is in it:
	// without it the generated file does not compile.
	if !strings.Contains(string(methods), "  secret?: true;") {
		t.Errorf("the generated RPCFieldMeta declares no secret flag\n%s", methods)
	}
}
