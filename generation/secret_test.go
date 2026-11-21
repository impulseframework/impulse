package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// secretField returns the named field of a fixture resource.
func secretField(t *testing.T, res *resourceInfo, name string) *resourceField {
	t.Helper()

	for _, f := range res.Fields {
		if f.Name() == name {
			return f
		}
	}
	t.Fatalf("field %s not found in the fixture's %s", name, res.Name())

	return nil
}

// lockbox is the fixture's resource with its stored secrets on the columns they need:
// a value of up to 64 bytes sealed, and one of any length.
func lockbox(t *testing.T, structs map[string]*parser.Struct, mutate func(*resourceInfo)) *resourceInfo {
	t.Helper()

	return fixtureResource(t, structs, "Lockbox", func(res *resourceInfo) {
		secretField(t, res, "Name").SpannerType = "STRING(64)"
		secretField(t, res, "Combination").SpannerType = "BYTES(106)"
		secretField(t, res, "Hint").SpannerType = "BYTES(MAX)"
		secretField(t, res, "Hint").IsNullable = true
		secretField(t, res, "Manifest").SpannerType = "STRING(MAX)"
		secretField(t, res, "Manifest").IsNullable = true
		if mutate != nil {
			mutate(res)
		}
	})
}

// A field declared conditions:"secret" is write-only without saying so, and the fact
// that it is a secret is its own: a field that is write-only alone is no secret. A
// stored secret is typed resource.Secret, and everything that carries its value as it
// is given carries text.
func Test_resourceField_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	res := lockbox(t, structs, nil)

	tests := []struct {
		field         string
		wantSecret    bool
		wantInputOnly bool
		wantReadTag   string
		// wantType is the field's own type, which the read structs declare, and
		// wantGiven the type a patch and a request carry its value in.
		wantType    string
		wantGiven   string
		wantSqltype string
		wantMax     int
	}{
		{field: "Name", wantReadTag: `json:"name"`, wantType: "string", wantGiven: "string", wantSqltype: `sqltype:"STRING(64)"`, wantMax: 64},
		{field: "Combination", wantSecret: true, wantInputOnly: true, wantReadTag: `json:"-"`, wantType: "resource.Secret", wantGiven: "string", wantSqltype: `sqltype:"BYTES(106)"`, wantMax: 64},
		{field: "Hint", wantSecret: true, wantInputOnly: true, wantReadTag: `json:"-"`, wantType: "*resource.Secret", wantGiven: "*string"},
		{field: "Manifest", wantInputOnly: true, wantReadTag: `json:"-"`, wantType: "*string", wantGiven: "*string"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			field := secretField(t, res, tt.field)
			// The TypeScript type is what the built-in table gives the field's type.
			field.typescriptType = "string"
			if got := field.IsSecret(); got != tt.wantSecret {
				t.Errorf("IsSecret() = %v, want %v", got, tt.wantSecret)
			}
			if got := field.HoldsSecret(); got != tt.wantSecret {
				t.Errorf("HoldsSecret() = %v, want %v", got, tt.wantSecret)
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
			if got := field.Type(); got != tt.wantType {
				t.Errorf("Type() = %s, want %s", got, tt.wantType)
			}
			if got := field.PatchValueType(); got != tt.wantGiven {
				t.Errorf("PatchValueType() = %s, want %s", got, tt.wantGiven)
			}
			if got := field.RequestType(); got != tt.wantGiven {
				t.Errorf("RequestType() = %s, want %s", got, tt.wantGiven)
			}
			// The decoder sizes a secret against its column less what sealing adds, and
			// the form is told the same room.
			if got := field.SqltypeTag(); got != tt.wantSqltype {
				t.Errorf("SqltypeTag() = %s, want %s", got, tt.wantSqltype)
			}
			if got := field.TypescriptMaxLength(); got != tt.wantMax {
				t.Errorf("TypescriptMaxLength() = %d, want %d", got, tt.wantMax)
			}
		})
	}

	// The built-in TypeScript table types a stored secret as the text it is given as.
	if got := defaultTypescriptOverrides()["resource.Secret"]; got != stringTSType {
		t.Errorf("the TypeScript type of resource.Secret = %q, want %q", got, stringTSType)
	}
	// The authorization matrix's update probe sends a secret as text.
	only := fixtureResource(t, structs, "Drawer", func(res *resourceInfo) {
		secretField(t, res, "Slot").IsPrimaryKey = true
		secretField(t, res, "Chest").IsPrimaryKey = true
	})
	if name, value, patchable, err := updateProbeField(only); err != nil || !patchable || name != "code" || value != `"authz-test-value"` {
		t.Errorf("updateProbeField() = %s, %s, %v, error %v, want the secret probed as text", name, value, patchable, err)
	}
}

// The generator refuses a secret the field cannot honor: one a client never sets or
// could not be kept from reading, and one whose type or column would store the value as
// it was given.
func Test_checkSecretDeclarations(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))

	tests := []struct {
		name    string
		mutate  func(*testing.T, *resourceInfo)
		wantErr string
	}{
		{name: "a secret typed resource.Secret on a BYTES column the client sets"},
		{
			name:   "a secret on a BYTES column of any length",
			mutate: func(t *testing.T, res *resourceInfo) { secretField(t, res, "Combination").SpannerType = "BYTES(MAX)" },
		},
		{
			name:   "a secret on the smallest column with room for a value",
			mutate: func(t *testing.T, res *resourceInfo) { secretField(t, res, "Combination").SpannerType = "BYTES(43)" },
		},
		{
			name:    "on a primary key",
			mutate:  func(t *testing.T, res *resourceInfo) { secretField(t, res, "Combination").IsPrimaryKey = true },
			wantErr: `Lockbox.Combination: conditions:"secret" on a primary key`,
		},
		{
			name:    "on a field the server owns",
			mutate:  func(t *testing.T, res *resourceInfo) { secretField(t, res, "Combination").IsState = true },
			wantErr: `Lockbox.Combination: conditions:"secret" on a field the server owns`,
		},
		{
			name:    "on a field of a view",
			mutate:  func(_ *testing.T, res *resourceInfo) { res.IsVirtual = true },
			wantErr: `Lockbox.Combination: conditions:"secret" on a field of a view`,
		},
		{
			name: "on a foreign key",
			mutate: func(t *testing.T, res *resourceInfo) {
				secretField(t, res, "Combination").IsForeignKey = true
				secretField(t, res, "Combination").ReferencedResource = "Vaults"
			},
			wantErr: `Lockbox.Combination: conditions:"secret" on a foreign key: the value is the key of a row of Vaults`,
		},
		{
			name: "on a field with a picker",
			mutate: func(t *testing.T, res *resourceInfo) {
				vaults := genlang.Arg("Vaults")
				secretField(t, res, "Combination").enumerateArg = &vaults
			},
			wantErr: `Lockbox.Combination: conditions:"secret" on a field with a picker (@enumerate)`,
		},
		{
			name:    "on a STRING column",
			mutate:  func(t *testing.T, res *resourceInfo) { secretField(t, res, "Combination").SpannerType = "STRING(64)" },
			wantErr: `Lockbox.Combination: conditions:"secret" on a STRING(64) column: a stored secret is encrypted bytes; declare the column BYTES(n), n the longest value it takes plus 42, or BYTES(MAX)`,
		},
		{
			name: "on a list of bytes",
			mutate: func(t *testing.T, res *resourceInfo) {
				secretField(t, res, "Combination").SpannerType = "ARRAY<BYTES(MAX)>"
			},
			wantErr: `Lockbox.Combination: conditions:"secret" on a ARRAY<BYTES(MAX)> column: a stored secret is encrypted bytes`,
		},
		{
			name:    "on a column with room for no value",
			mutate:  func(t *testing.T, res *resourceInfo) { secretField(t, res, "Combination").SpannerType = "BYTES(42)" },
			wantErr: `Lockbox.Combination: conditions:"secret" on a BYTES(42) column: sealing adds 42 bytes to a value, so the column has room for none; declare it BYTES(n), n the longest value it takes plus 42, or BYTES(MAX)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := lockbox(t, structs, func(res *resourceInfo) {
				if tt.mutate != nil {
					tt.mutate(t, res)
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

// Every stored secret is encrypted, and there is no plain kind: a secret declared on a
// string or on bytes is refused with the type to give it, and the type on a field that
// declares no secret is refused too.
func Test_checkSecretDeclarations_plainTypes(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	res := fixtureResource(t, structs, "PlainLockbox", func(res *resourceInfo) {
		secretField(t, res, "Combination").SpannerType = "STRING(64)"
		secretField(t, res, "Hint").SpannerType = "STRING(MAX)"
		secretField(t, res, "Hint").IsNullable = true
		secretField(t, res, "Seal").SpannerType = "BYTES(MAX)"
		secretField(t, res, "Seal").IsNullable = true
		secretField(t, res, "Label").SpannerType = "BYTES(MAX)"
	})

	err := checkSecretDeclarations(res)
	if err == nil {
		t.Fatal("checkSecretDeclarations() error = nil, want every plain secret refused")
	}
	// A field named as a method the generated set opens a secret with would not compile.
	named := fixtureResource(t, structs, "NamedLockbox", func(res *resourceInfo) {
		secretField(t, res, "Combination").SpannerType = "BYTES(MAX)"
	})
	if err := checkSecretDeclarations(named); err == nil || !strings.Contains(err.Error(), "NamedLockbox.Combination: the generated set declares the method OpenCombinationIn on NamedLockbox, which opens the secret Combination; rename the field OpenCombinationIn") {
		t.Errorf("checkSecretDeclarations() of a field named as the opening method: error = %v", err)
	}

	for _, want := range []string{
		`PlainLockbox.Combination: conditions:"secret" on a field typed string: a stored secret is kept encrypted, and the field's type is what says so; type the field resource.Secret and declare its column BYTES(n), n the longest value it takes plus 42, or BYTES(MAX)`,
		`PlainLockbox.Hint: conditions:"secret" on a field typed *string: a stored secret is kept encrypted, and the field's type is what says so; type the field *resource.Secret`,
		// Bytes on a column that allows NULL: the type to give is the nullable one.
		`PlainLockbox.Seal: conditions:"secret" on a field typed []byte: a stored secret is kept encrypted, and the field's type is what says so; type the field *resource.Secret`,
		`PlainLockbox.Label: typed resource.Secret and not declared conditions:"secret": the declaration is what keeps the field from being served and has its value sealed when it is written; add it`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkSecretDeclarations() error lacks %q:\n%v", want, err)
		}
	}
}

// A method's request field is held to the rule it always was where the method is
// extracted: a secret a dialog masks (text) or draws no control over (bytes) is taken,
// and one it would draw in the open or list in a picker is refused, naming the field. A
// request field is never stored, so it stays text: the stored type is refused on it.
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
		{method: "StoreLockbox", wantField: "Combination", wantErr: `typed resource.Secret: the type is a stored field's, and holds the bytes its column stores; a method's request carries a secret as text, so type the field string and declare it conditions:"secret"`},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()

			c := &client{resources: []*resourceInfo{lockbox(t, structs, nil)}}
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

// The generated set of a resource with a stored secret: the struct says what its
// secrets are bound to and opens each, the typed patches and the write struct take the
// value as text, and the read struct keeps the field's own type off the wire.
func Test_resourceFileTemplate_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	generated := func(t *testing.T, res *resourceInfo) string {
		t.Helper()

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

		return string(formatted)
	}

	rendered := generated(t, lockbox(t, structs, nil))
	for _, want := range []string{
		"func (Lockbox) SecretContext() []accesstypes.Field {\n\treturn []accesstypes.Field{\n\t\t\"ID\",\n\t}\n}",
		"func (row *Lockbox) OpenCombination(keys *resource.SecretKeys) (string, error) {\n\treturn keys.Open(resource.KeySet{}.Add(\"ID\", row.ID), row.Combination)\n}",
		"func (row *Lockbox) OpenHint(keys *resource.SecretKeys) (*string, error) {\n\treturn keys.OpenNullable(resource.KeySet{}.Add(\"ID\", row.ID), row.Hint)\n}",
		"func (row *Lockbox) OpenCombinationIn(ctx context.Context, txn resource.ReadWriteTransaction) (string, error) {\n\treturn resource.OpenSecretIn[Lockbox](ctx, txn, resource.KeySet{}.Add(\"ID\", row.ID), \"Combination\", row.Combination)\n}",
		"func (row *Lockbox) OpenHintIn(ctx context.Context, txn resource.ReadWriteTransaction) (*string, error) {\n\treturn resource.OpenNullableSecretIn[Lockbox](ctx, txn, resource.KeySet{}.Add(\"ID\", row.ID), \"Hint\", row.Hint)\n}",
		// A patch is given the value as text and reads back what it was given.
		"func (p *LockboxCreatePatch) SetCombination(v string) *LockboxCreatePatch {",
		"func (p *LockboxUpdatePatch) SetHint(v *string) *LockboxUpdatePatch {",
		"func (p *LockboxUpdatePatch) Combination() string {\n\tv, _ := p.patchSet.Get(\"Combination\").(string)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the generated set lacks %q:\n%s", want, rendered)
		}
	}
	// The read struct declares the field's own type, hidden; the write struct the text,
	// sized against the column less what sealing adds.
	read, write := fileStructBody(t, rendered, "lockboxRead"), fileStructBody(t, rendered, "lockboxWrite")
	for body, wants := range map[string][]string{
		read:  {"Combination resource.Secret", "Hint *resource.Secret", `json:"-"`},
		write: {"Combination string", "Hint *string", `json:"combination"`, `sqltype:"BYTES(106)"`},
	} {
		for _, want := range wants {
			if !strings.Contains(strings.Join(strings.Fields(body), " "), want) {
				t.Errorf("a generated struct lacks %q:\n%s", want, body)
			}
		}
	}
	if strings.Contains(write, "resource.Secret") {
		t.Errorf("the write struct carries the stored type:\n%s", write)
	}

	// A key of two columns binds in key order, whatever order the struct declares.
	drawer := generated(t, fixtureResource(t, structs, "Drawer", func(res *resourceInfo) {
		res.PkCount = 2
		for name, ordinal := range map[string]int64{"Chest": 1, "Slot": 2} {
			secretField(t, res, name).IsPrimaryKey = true
			secretField(t, res, name).KeyOrdinalPosition = ordinal
		}
		secretField(t, res, "Code").SpannerType = "BYTES(MAX)"
	}))
	for _, want := range []string{
		"return []accesstypes.Field{\n\t\t\"Chest\",\n\t\t\"Slot\",\n\t}",
		"return keys.Open(resource.KeySet{}.Add(\"Chest\", row.Chest).Add(\"Slot\", row.Slot), row.Code)",
	} {
		if !strings.Contains(drawer, want) {
			t.Errorf("the generated set of a two-column key lacks %q:\n%s", want, drawer)
		}
	}

	// A resource with no secret gets none of it.
	shelf := generated(t, fixtureResource(t, structs, "Shelf", nil))
	for _, absent := range []string{"SecretContext", "SecretKeys", "OpenSecretIn", "func (row *"} {
		if strings.Contains(shelf, absent) {
			t.Errorf("the generated set of a resource with no secret carries %q", absent)
		}
	}
}

// The resources package's generated SecretHolders names every resource that stores a
// secret, and no other: what the migrate command retires a version of the application
// secret over. An application that stores none gets the function all the same, so its
// migrate command calls it.
func Test_secretHoldersTemplate(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	generated := func(t *testing.T, resources ...*resourceInfo) string {
		t.Helper()

		r := &resourceGenerator{client: &client{}}
		r.resource = packageDir("pkg/resources")
		r.resources = resources
		out, err := r.generateTemplateOutput("sealedHoldersTemplate", sealedHoldersTemplate, r.secretHoldersData())
		if err != nil {
			t.Fatalf("generateTemplateOutput() error = %v", err)
		}
		formatted, err := format.Source(out)
		if err != nil {
			t.Fatalf("format.Source() error = %v on:\n%s", err, out)
		}

		return string(formatted)
	}

	holding := generated(t, lockbox(t, structs, nil), fixtureResource(t, structs, "Shelf", nil))
	if want := "func SecretHolders() []resource.SecretHolder {\n\treturn []resource.SecretHolder{\n\t\tresource.SecretHolderOf[Lockbox](),\n\t}\n}"; !strings.Contains(holding, want) {
		t.Errorf("the secret holders are missing %q:\n%s", want, holding)
	}
	if strings.Contains(holding, "Shelf") {
		t.Errorf("the secret holders name a resource that stores no secret:\n%s", holding)
	}

	none := generated(t, fixtureResource(t, structs, "Shelf", nil))
	if want := "func SecretHolders() []resource.SecretHolder {\n\treturn nil\n}"; !strings.Contains(none, want) {
		t.Errorf("the secret holders of an application that stores no secret are missing %q:\n%s", want, none)
	}
}

// fileStructBody returns the fields of a struct type a generated file declares at its
// top level, as written between its braces.
func fileStructBody(t *testing.T, rendered, name string) string {
	t.Helper()

	_, after, found := strings.Cut(rendered, "type "+name+" struct {")
	if !found {
		t.Fatalf("the generated set declares no struct %s:\n%s", name, rendered)
	}
	body, _, _ := strings.Cut(after, "\n}")

	return body
}

// The handlers' patch request structs carry a stored secret as the text a request sends.
func Test_patchTemplates_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	res := lockbox(t, structs, nil)

	c := &client{}
	consolidated, err := c.generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, consolidatedPatchData{
		Resources:       []*resourceInfo{res},
		GlobalCases:     []consolidatedCaseData{{resourceInfo: res, ResourcePackage: "resources", ReceiverName: "a"}},
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
		Resource:        res,
		ApplicationName: "App",
		ReceiverName:    "a",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput(patchTemplate) error = %v", err)
	}

	for handler, rendered := range map[string]string{"the consolidated handler": string(consolidated), "the resource's own handler": string(own)} {
		flat := strings.Join(strings.Fields(rendered), " ")
		for _, want := range []string{
			"Combination string `json:\"combination\" sqltype:\"BYTES(106)\"",
			"Hint *string `json:\"hint\"",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s lacks %q:\n%s", handler, want, rendered)
			}
		}
		if strings.Contains(rendered, "resource.Secret") {
			t.Errorf("%s declares a request field of the stored type:\n%s", handler, rendered)
		}
	}
}

// An application that stores a secret does not start on a client built with no keys:
// the generated router asks the client for them, naming every stored secret, and the
// router test's stub carries keys built from fixed bytes.
func Test_servedRouter_secretKeys(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))
	outlets := []*routerOutlet{{name: "default", prefix: "api", servesSessions: true, auth: &outletAuth{importPath: "example.com/acme/beacon/pkg/config", methods: []AuthMethod{AzureMethod("azure")}}}}

	tests := []struct {
		name           string
		resources      []*resourceInfo
		wantRouter     []string
		wantRouterTest []string
		wantAbsent     []string
	}{
		{
			name:      "stored secrets",
			resources: []*resourceInfo{fixtureResource(t, structs, "Shelf", nil), lockbox(t, structs, nil), fixtureResource(t, structs, "Drawer", nil)},
			wantRouter: []string{
				"\tResourceClient() resource.Client\n",
				"(resource.RequireSecretKeys).\n",
				"\tif err := resource.RequireSecretKeys(h.ResourceClient(), \"Lockbox.Combination\", \"Lockbox.Hint\", \"Drawer.Code\"); err != nil {\n\t\tpanic(fmt.Sprintf(\"router.New: %v\", err))\n\t}\n",
			},
			wantRouterTest: []string{
				"func (s *routerHandlersStub) ResourceClient() resource.Client {\n\treturn resource.NewMockClient(nil, nil, nil, resource.WithSecretKeys(stubSecretKeys()))\n}",
				"func stubSecretKeys() *resource.SecretKeys {\n\tkeys, err := resource.NewSecretKeys(map[int][]byte{1: bytes.Repeat([]byte{1}, 32)})",
			},
			wantAbsent: []string{"RequireFileStores", "stubFileStore"},
		},
		{
			name:       "no stored secret asks for no client",
			resources:  []*resourceInfo{fixtureResource(t, structs, "Shelf", nil)},
			wantAbsent: []string{"ResourceClient", "RequireSecretKeys", "stubSecretKeys", "SecretKeys"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{client: &client{}}
			r.resource = packageDir("pkg/resources")
			r.router = packageDir("pkg/router")
			r.resources = tt.resources
			data := r.servedRouterData(outlets, nil, nil, nil)
			router := render(t, r, "servedRouterTemplate", servedRouterTemplate, data)
			routerTest := render(t, r, "servedRouterTestTemplate", servedRouterTestTemplate, data)
			for _, want := range tt.wantRouter {
				if !strings.Contains(router, want) {
					t.Errorf("router missing %q:\n%s", want, router)
				}
			}
			for _, want := range tt.wantRouterTest {
				if !strings.Contains(routerTest, want) {
					t.Errorf("router test missing %q:\n%s", want, routerTest)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(router, absent) || strings.Contains(routerTest, absent) {
					t.Errorf("router or its test carries %q:\n%s\n%s", absent, router, routerTest)
				}
			}
		})
	}
}

// The browser metadata says which field is a secret, on a resource and on a method, so
// the form types it into a masked input; a write-only field that is no secret says
// write-only alone. A stored secret is a string to the browser, with the room its
// column leaves for the value.
func Test_typescriptTemplates_secret(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "secretfixture"))

	g := &typescriptGenerator{client: &client{}}
	resources, err := g.generateTemplateOutput("typescriptResourcesTemplate", typescriptResourcesTemplate, tsResourcesData{
		File: g,
		Resources: []*resourceInfo{lockbox(t, structs, func(res *resourceInfo) {
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
		"{ fieldName: 'combination', displayType: 'string', required: true, isIndex: false, sortable: false, maxLength: 64, writeOnly: true, secret: true },",
		"{ fieldName: 'hint', displayType: 'string', required: false, isIndex: false, sortable: false, writeOnly: true, secret: true },",
		"{ fieldName: 'manifest', displayType: 'string', required: false, isIndex: false, writeOnly: true },",
		"{ fieldName: 'name', displayType: 'string', required: true, isIndex: false, maxLength: 64 },",
	} {
		if !strings.Contains(string(resources), want) {
			t.Errorf("the resources metadata lacks %q\n%s", want, resources)
		}
	}
	// The row interface carries no write-only field.
	for _, absent := range []string{"  combination?:", "  hint?:", "  manifest?:"} {
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
