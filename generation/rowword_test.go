package generation

import (
	"strings"
	"testing"
)

// Test_declareRowWords pins the words a resource's rows go by: its name split into
// words where it states none, or the label the tenant record declares; the word it
// states, with the plural taken from it or stated beside it; and each refusal naming
// the struct, what is wrong and what to write.
func Test_declareRowWords(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rowwordfixture"))

	tests := []struct {
		name     string
		fixture  string
		label    string
		wantOne  string
		wantMany string
		wantErr  string
	}{
		{name: "no word stated: the name split into words", fixture: "HangarPass", wantOne: "hangar pass", wantMany: "hangar passes"},
		{name: "no word stated on the tenant record: the label it declares for one tenant", fixture: "HangarPass", label: "Bay", wantOne: "bay", wantMany: "bays"},
		{name: "the word stated on the tenant record is the word", fixture: "DockingPermit", label: "Bay", wantOne: "permit", wantMany: "permits"},
		{name: "the word stated, the plural taken from it", fixture: "DockingPermit", wantOne: "permit", wantMany: "permits"},
		{name: "both words stated", fixture: "CrewMember", wantOne: "person", wantMany: "people"},
		{name: "a word of several", fixture: "ShipLog", wantOne: "ship's log", wantMany: "ship's logs"},
		{
			name: "the word with its article is refused", fixture: "WithArticle",
			wantErr: `struct WithArticle: @rowWord(a permit): the word opens with "a"; write the word alone, @rowWord(permit): a screen puts "a", "an" or "this" before it itself`,
		},
		{
			name: "the plural with an article is refused", fixture: "PluralWithArticle",
			wantErr: `struct PluralWithArticle: @rowWord(permit, plural: the permits): the plural opens with "the"; write the word alone, plural: permits`,
		},
		{name: "two words where one is taken", fixture: "TwoWords", wantErr: "struct TwoWords: @rowWord(permit, pass)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pStruct, annotations := scanFixtureStruct(t, structs, tt.fixture)
			var decl rowWordsDecl
			err := (&client{}).declareRowWords(pStruct, annotations, tt.label, &decl)
			if (err != nil) != (tt.wantErr != "") {
				t.Fatalf("declareRowWords() error = %v, wantErr %v", err, tt.wantErr != "")
			}
			if err != nil {
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("declareRowWords() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if decl.RowWord != tt.wantOne || decl.RowWords != tt.wantMany {
				t.Errorf("words = %q and %q, want %q and %q", decl.RowWord, decl.RowWords, tt.wantOne, tt.wantMany)
			}
		})
	}
}

// Test_resolveRowsOf_rowWords pins the words of a view that declares its table: a view
// that states no word takes the table's, on both kinds, and one that states its own
// keeps it.
func Test_resolveRowsOf_rowWords(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rowsoffixture"))
	berths := func() *resourceInfo {
		table := fixtureTable(t, structs, "Berth", "DockID", "Slot")
		table.RowWord, table.RowWords = "berth", "berths"

		return table
	}

	t.Run("a virtual view that states no word takes the table's", func(t *testing.T) {
		t.Parallel()

		view := fixtureVirtualView(t, structs, "Mooring")
		view.RowWord, view.RowWords = "mooring", "moorings"
		c := &client{resources: []*resourceInfo{berths(), view}}
		if err := c.resolveRowsOf([]*resourceInfo{view}, nil); err != nil {
			t.Fatalf("resolveRowsOf() error = %v", err)
		}
		if view.RowWord != "berth" || view.RowWords != "berths" {
			t.Errorf("words = %q and %q, want the table's, berth and berths", view.RowWord, view.RowWords)
		}
	})

	t.Run("a computed view that states no word takes the table's", func(t *testing.T) {
		t.Parallel()

		view := fixtureComputedView(t, structs, "Occupancy")
		view.RowWord, view.RowWords = "occupancy", "occupancies"
		c := &client{resources: []*resourceInfo{berths()}, computedResources: []*computedResource{view}}
		if err := c.resolveRowsOf(nil, []*computedResource{view}); err != nil {
			t.Fatalf("resolveRowsOf() error = %v", err)
		}
		if view.RowWord != "berth" || view.RowWords != "berths" {
			t.Errorf("words = %q and %q, want the table's, berth and berths", view.RowWord, view.RowWords)
		}
	})

	t.Run("a view that states its word keeps it", func(t *testing.T) {
		t.Parallel()

		view := fixtureVirtualView(t, structs, "Mooring")
		view.RowWord, view.RowWords, view.rowWordStated = "slip", "slips", true
		c := &client{resources: []*resourceInfo{berths(), view}}
		if err := c.resolveRowsOf([]*resourceInfo{view}, nil); err != nil {
			t.Fatalf("resolveRowsOf() error = %v", err)
		}
		if view.RowWord != "slip" || view.RowWords != "slips" {
			t.Errorf("words = %q and %q, want its own, slip and slips", view.RowWord, view.RowWords)
		}
	})
}

// Test_rejectRowWord pins the kind that may not state a word: an RPC method has no
// rows, and is refused naming the struct.
func Test_rejectRowWord(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rowwordfixture"))

	tests := []struct {
		name    string
		fixture string
		wantErr string
	}{
		{name: "an RPC method is refused", fixture: "MethodWithRowWord", wantErr: "struct MethodWithRowWord: @rowWord is only valid on a resource (@resource, @virtual or @computed); this RPC method lists no rows to name"},
		{name: "a struct stating nothing passes", fixture: "HangarPass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pStruct, annotations := scanFixtureStruct(t, structs, tt.fixture)
			err := rejectRowWord(pStruct, annotations, "RPC method")
			if (err != nil) != (tt.wantErr != "") {
				t.Fatalf("rejectRowWord() error = %v, wantErr %v", err, tt.wantErr != "")
			}
			if err != nil && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("rejectRowWord() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// Test_typescriptResourcesTemplate_rowWords pins the emitted metadata: a resource of
// either kind carries the word for one row and for several, a word with an apostrophe
// is a valid literal, and a resource with no words carries nothing.
func Test_typescriptResourcesTemplate_rowWords(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rowsoffixture"))
	view := func(one, many string) *resourceInfo {
		res := fixtureVirtualView(t, structs, "Mooring")
		for _, f := range res.Fields {
			f.typescriptType = "string"
		}
		res.RowWord, res.RowWords = one, many

		return res
	}
	computed := fixtureComputedView(t, structs, "Occupancy")
	for _, f := range computed.Fields {
		f.typescriptType = "string"
	}
	computed.RowWord, computed.RowWords = "occupancy", "occupancies"

	tests := []struct {
		name            string
		data            tsResourcesData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "a resource carries its words",
			data:         tsResourcesData{Resources: []*resourceInfo{view("mooring", "moorings")}, GenPrefix: "zz_gen"},
			wantContains: []string{"    rowWords: { one: 'mooring', many: 'moorings' },\n    fields: ["},
		},
		{
			name:         "an apostrophe is escaped",
			data:         tsResourcesData{Resources: []*resourceInfo{view("ship's log", "ship's logs")}, GenPrefix: "zz_gen"},
			wantContains: []string{`rowWords: { one: 'ship\'s log', many: 'ship\'s logs' },`},
		},
		{
			name:         "a computed resource carries its words",
			data:         tsResourcesData{ComputedResources: []*computedResource{computed}, GenPrefix: "zz_gen"},
			wantContains: []string{"    rowWords: { one: 'occupancy', many: 'occupancies' },\n    fields: ["},
		},
		{
			name:            "a resource with no words carries nothing",
			data:            tsResourcesData{Resources: []*resourceInfo{view("", "")}, GenPrefix: "zz_gen"},
			wantNotContains: []string{"rowWords"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			tt.data.File = &typescriptGenerator{client: c}
			out, err := c.generateTemplateOutput(typescriptResourcesTemplate, typescriptResourcesTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(string(out), want) {
					t.Errorf("output does not contain %q:\n%s", want, out)
				}
			}
			for _, not := range tt.wantNotContains {
				if strings.Contains(string(out), not) {
					t.Errorf("output contains %q:\n%s", not, out)
				}
			}
		})
	}
}
