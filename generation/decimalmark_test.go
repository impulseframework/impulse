package generation

import (
	"strings"
	"testing"
)

// Test_decimalMark pins the mark the generated metadata carries beside a decimal's
// display type. A decimal is a string in the interface and a number in the metadata, so
// without the mark a form cannot tell it from an integer and sends a JavaScript number,
// which has lost every digit a float does not hold. The mark is on a column, a computed
// row's field and a method's request field alike, on each decimal type of the built-in
// table and on a list of one, and on nothing else: an integer beside them carries none.
func Test_decimalMark(t *testing.T) {
	t.Parallel()

	c, structs := columnFixtureClient(t)
	res := fixtureResource(t, structs, "Priced", nil)
	if err := columnFixtureGenerator(c).resourceFieldsTypescriptType(res); err != nil {
		t.Fatalf("resourceFieldsTypescriptType() error = %v", err)
	}
	columns := make(map[string]*resourceField, len(res.Fields))
	for _, f := range res.Fields {
		columns[f.Name()] = f
	}
	settlement := structs["Settlement"]
	if settlement == nil {
		t.Fatal("struct Settlement not found in the fixture")
	}
	shape, err := newWireWalker(c.leaves(), "columnfixture").walk(settlement)
	if err != nil {
		t.Fatalf("walk(Settlement) error = %v", err)
	}
	wire := make(map[string]*wireField, len(shape.Fields))
	for _, f := range shape.Fields {
		wire[f.Name] = f
	}

	tests := []struct {
		name  string
		field string
		want  bool
	}{
		{name: "a decimal is marked", field: "Fee", want: true},
		{name: "a NullDecimal is marked", field: "Rebate", want: true},
		{name: "a pointer to a decimal is marked", field: "Reserve", want: true},
		{name: "spanner.NullNumeric is marked", field: "Levy", want: true},
		{name: "a list of decimals is marked", field: "Fees", want: true},
		{name: "an integer is not", field: "Count", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			column := columns[tt.field]
			if column == nil {
				t.Fatalf("column %q not on the fixture", tt.field)
			}
			if got := column.IsDecimal(); got != tt.want {
				t.Errorf("column IsDecimal() = %v, want %v", got, tt.want)
			}
			field := wire[tt.field]
			if field == nil {
				t.Fatalf("wire field %q not on the fixture", tt.field)
			}
			if got := field.IsDecimal(); got != tt.want {
				t.Errorf("wire IsDecimal() = %v, want %v", got, tt.want)
			}
			if got := (&rpcField{wire: field}).IsDecimal(); got != tt.want {
				t.Errorf("method field IsDecimal() = %v, want %v", got, tt.want)
			}
			if got := (&computedField{wire: field}).IsDecimal(); got != tt.want {
				t.Errorf("computed field IsDecimal() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("a nested struct is not", func(t *testing.T) {
		t.Parallel()

		lines := wire["Lines"]
		if lines == nil {
			t.Fatal("wire field Lines not on the fixture")
		}
		if lines.IsDecimal() {
			t.Error("wire IsDecimal() = true for a list of structs, want false")
		}
	})

	t.Run("a picker is not, whatever its key's type", func(t *testing.T) {
		t.Parallel()

		if (&rpcField{wire: wire["Fee"], enumeratedResource: "Tariffs"}).IsDecimal() {
			t.Error("method field IsDecimal() = true for an enumerated field, want false")
		}
		if (&computedField{wire: wire["Fee"], IsEnumerated: true}).IsDecimal() {
			t.Error("computed field IsDecimal() = true for an enumerated field, want false")
		}
	})

	t.Run("a field built without a walk reads its table row", func(t *testing.T) {
		t.Parallel()

		if !(&rpcField{typescriptType: decimalTSType}).IsDecimal() {
			t.Error("method field IsDecimal() = false for the decimal row, want true")
		}
		if (&rpcField{typescriptType: numberTSType}).IsDecimal() {
			t.Error("method field IsDecimal() = true for the number row, want false")
		}
		if !(&computedField{typescriptType: decimalTSType + sliceSuffix}).IsDecimal() {
			t.Error("computed field IsDecimal() = false for a list of the decimal row, want true")
		}
	})
}

// Test_typescriptTemplates_decimalMark pins where the mark is written: beside
// displayType, on a resource's field, a computed resource's field and a method's request
// field, and on no field that is not a decimal.
func Test_typescriptTemplates_decimalMark(t *testing.T) {
	t.Parallel()

	c, structs := columnFixtureClient(t)
	res := fixtureResource(t, structs, "Priced", nil)
	if err := columnFixtureGenerator(c).resourceFieldsTypescriptType(res); err != nil {
		t.Fatalf("resourceFieldsTypescriptType() error = %v", err)
	}
	settlement := structs["Settlement"]
	if settlement == nil {
		t.Fatal("struct Settlement not found in the fixture")
	}
	shape, err := newWireWalker(c.leaves(), "columnfixture").walk(settlement)
	if err != nil {
		t.Fatalf("walk(Settlement) error = %v", err)
	}
	wire := make(map[string]*wireField, len(shape.Fields))
	for _, f := range shape.Fields {
		wire[f.Name] = f
	}
	computed := &computedResource{Struct: settlement}
	method := &rpcMethodInfo{Struct: settlement}
	for _, f := range settlement.Fields() {
		field := wire[f.Name()]
		if field == nil {
			t.Fatalf("wire field %q not on the fixture", f.Name())
		}
		computed.Fields = append(computed.Fields, &computedField{Field: f, wire: field, namespace: "Settlements"})
		method.Fields = append(method.Fields, &rpcField{Field: f, wire: field, namespace: "Settlement"})
	}

	g := &typescriptGenerator{client: c}
	resources, err := g.generateTemplateOutput("typescriptResourcesTemplate", typescriptResourcesTemplate, tsResourcesData{
		File:              g,
		Resources:         []*resourceInfo{res},
		ComputedResources: []*computedResource{computed},
		GenPrefix:         "zz_gen",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput(typescriptResourcesTemplate) error = %v", err)
	}
	methods, err := g.generateTemplateOutput("typescriptMethodsTemplate", typescriptMethodsTemplate, tsMethodsData{
		File:       g,
		RPCMethods: []*rpcMethodInfo{method},
		GenPrefix:  "zz_gen",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput(typescriptMethodsTemplate) error = %v", err)
	}

	// The methods file declares the type its entries are checked against, so the mark
	// is in it: without it the generated file does not compile.
	if !strings.Contains(string(methods), "  decimal?: true;") {
		t.Errorf("the generated RPCFieldMeta declares no decimal mark\n%s", methods)
	}

	tests := []struct {
		name    string
		out     string
		want    []string
		wantNot []string
		marks   int
	}{
		{
			name: "a resource's and a computed resource's fields",
			out:  string(resources),
			want: []string{
				"{ fieldName: 'fee', displayType: 'number', decimal: true, required: ",
				"{ fieldName: 'rebate', displayType: 'number', decimal: true, required: ",
				"{ fieldName: 'reserve', displayType: 'number', decimal: true, required: ",
				"{ fieldName: 'levy', displayType: 'number', decimal: true, required: ",
				"{ fieldName: 'fees', displayType: 'number[]', decimal: true, required: ",
				"{ fieldName: 'count', displayType: 'number', required: ",
			},
			wantNot: []string{"displayType: 'object[]', decimal: true"},
			// Five decimals on the resource and the same five on the computed resource.
			marks: 10,
		},
		{
			name: "a method's request fields",
			out:  string(methods),
			want: []string{
				"{ fieldName: 'fee', displayType: 'number', decimal: true",
				"{ fieldName: 'fees', displayType: 'number[]', decimal: true",
				"{ fieldName: 'count', displayType: 'number' }",
			},
			wantNot: []string{"displayType: 'object[]', decimal: true"},
			marks:   5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, want := range tt.want {
				if !strings.Contains(tt.out, want) {
					t.Errorf("the metadata lacks %q\n%s", want, tt.out)
				}
			}
			for _, not := range tt.wantNot {
				if strings.Contains(tt.out, not) {
					t.Errorf("the metadata carries %q", not)
				}
			}
			if got := strings.Count(tt.out, ", decimal: true"); got != tt.marks {
				t.Errorf("the metadata marks %d field(s) decimal, want %d", got, tt.marks)
			}
		})
	}
}
