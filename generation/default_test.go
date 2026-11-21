package generation

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser"
	"golang.org/x/tools/go/packages"
)

// A yes or no column's declared default reaches the browser metadata, where a create
// form starts the field's box: the schema read carries each column's default expression
// (Test_createTableMapUsingQuery pins how the information schema spells it), and the
// metadata says schemaDefault for a BOOL column whose default is one of the two words.

// defaultFixtureTables is the synthetic schema behind the defaultfixture struct: each
// column's default as the information schema spells it, the expression without the
// parentheses the DDL wraps it in.
func defaultFixtureTables() map[string]*tableMetadata {
	withDefault := func(spannerType, expression string, nullable bool) columnMeta {
		return columnMeta{SpannerType: spannerType, IsNullable: nullable, HasDefault: true, Default: expression}
	}

	return map[string]*tableMetadata{
		"Switches": {PkCount: 1, Columns: map[string]columnMeta{
			"Id":    {IsPrimaryKey: true, SpannerType: "STRING(36)"},
			"Lit":   withDefault("BOOL", "TRUE", false),
			"Dark":  withDefault("BOOL", "FALSE", false),
			"Low":   withDefault("BOOL", "false", false),
			"Open":  {SpannerType: "BOOL"},
			"Odd":   withDefault("BOOL", "1 = 1", false),
			"Maybe": withDefault("BOOL", "TRUE", true),
			"Count": withDefault("INT64", "0", false),
		}, Indexes: []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}}}},
	}
}

// Test_typescriptResourcesTemplate_yesOrNoDefault pins the metadata each column of the
// default fixture renders: schemaDefault true or false where a BOOL column declares that
// word, in either case and on a nullable column too, and no schemaDefault where the
// column declares nothing, declares an expression, or is a number. A column with a
// default is not required, whatever the default is.
func Test_typescriptResourcesTemplate_yesOrNoDefault(t *testing.T) {
	t.Parallel()

	loaded, parsed := loadFixturePackage(t, "defaultfixture")
	c := &client{tableMap: defaultFixtureTables()}
	c.notePackages(map[string]*packages.Package{loaded.Name: loaded})
	pStruct := fixtureStructs(parsed)["Switch"]
	if pStruct == nil {
		t.Fatal("struct Switch not found in the default fixture")
	}
	resources, err := c.structsToResources([]*parser.Struct{pStruct})
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("structsToResources() extracted %d resources, want 1", len(resources))
	}
	generator := &typescriptGenerator{client: c, outletExcludedTables: map[string]struct{}{}}
	if err := generator.resourceFieldsTypescriptType(resources[0]); err != nil {
		t.Fatalf("resourceFieldsTypescriptType() error = %v", err)
	}

	data := tsResourcesData{Resources: resources, GenPrefix: "zz_gen", File: generator}
	out, err := c.generateTemplateOutput("typescriptResourcesTemplate", typescriptResourcesTemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}

	tests := []struct {
		name string
		want string
	}{
		{name: "TRUE is carried as true", want: "{ fieldName: 'lit', displayType: 'boolean', required: false, schemaDefault: true, isIndex: false },"},
		{name: "FALSE is carried as false", want: "{ fieldName: 'dark', displayType: 'boolean', required: false, schemaDefault: false, isIndex: false },"},
		{name: "false in lower case is carried as false", want: "{ fieldName: 'low', displayType: 'boolean', required: false, schemaDefault: false, isIndex: false },"},
		{name: "a column with no default is required and carries none", want: "{ fieldName: 'open', displayType: 'boolean', required: true, isIndex: false },"},
		{name: "an expression that is neither word is not carried", want: "{ fieldName: 'odd', displayType: 'boolean', required: false, isIndex: false },"},
		{name: "a nullable column's TRUE is carried as true", want: "{ fieldName: 'maybe', displayType: 'nullboolean', required: false, schemaDefault: true, isIndex: false },"},
		{name: "a number's default is not carried", want: "{ fieldName: 'count', displayType: 'number', required: false, isIndex: false },"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(string(out), tt.want) {
				t.Errorf("typescriptResourcesTemplate output missing %q:\n%s", tt.want, out)
			}
		})
	}
}
