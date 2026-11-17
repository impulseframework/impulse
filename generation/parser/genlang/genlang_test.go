package genlang_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

var keywords = map[string]genlang.KeywordOpts{
	"primarykey":  {genlang.ScanStruct: genlang.ArgsRequired | genlang.Exclusive, genlang.ScanField: genlang.NoArgs | genlang.Exclusive},
	"foreignkey":  {genlang.ScanStruct: genlang.ArgsRequired, genlang.ScanField: genlang.ArgsRequired},
	"check":       {genlang.ScanField: genlang.ArgsRequired | genlang.Exclusive},
	"default":     {genlang.ScanField: genlang.ArgsRequired | genlang.Exclusive},
	"hidden":      {genlang.ScanField: genlang.NoArgs | genlang.Exclusive},
	"substring":   {genlang.ScanField: genlang.ArgsRequired},
	"fulltext":    {genlang.ScanField: genlang.ArgsRequired},
	"ngram":       {genlang.ScanField: genlang.ArgsRequired},
	"index":       {genlang.ScanStruct: genlang.ArgsRequired, genlang.ScanField: genlang.NoArgs},
	"uniqueindex": {genlang.ScanStruct: genlang.ArgsRequired, genlang.ScanField: genlang.NoArgs},
	"view":        {genlang.ScanStruct: genlang.NoArgs | genlang.Exclusive},
	"using":       {genlang.ScanField: genlang.ArgsRequired | genlang.Exclusive},
	"suppress":    {genlang.ScanField: genlang.NoArgs | genlang.Exclusive},
	"omit":        {genlang.ScanField: genlang.NoArgs | genlang.Exclusive},
	"policy":      {genlang.ScanStruct: genlang.ArgsRequired},
}

func Test_ScanStruct(t *testing.T) {
	t.Parallel()

	type args struct {
		filepath string
	}
	tests := []struct {
		name       string
		args       args
		wantStruct map[string]genlang.Arg
		wantFields []map[string]genlang.Arg
		wantErr    bool
	}{
		{
			name: "multiline comments",
			args: args{
				filepath: "./testdata/multiline.go",
			},
			wantStruct: map[string]genlang.Arg{
				"uniqueindex": genlang.Arg("Id, Description"),
				"foreignkey":  genlang.Arg("Type, StatusTypes(Id)\x00Status, Statuses(Id)"),
			},
			wantFields: []map[string]genlang.Arg{
				{
					"primarykey": genlang.Arg(""),
					"check":      genlang.Arg("@self = 'N'"),
					"hidden":     genlang.Arg(""),
					"substring":  genlang.Arg("@self"),
				},
			},
		},
		{
			name: "single-line comments",
			args: args{filepath: "./testdata/singular.go"},
			wantStruct: map[string]genlang.Arg{
				"primarykey": genlang.Arg("Id, Description"),
			},
			wantFields: []map[string]genlang.Arg{
				{"primarykey": genlang.Arg("")},
			},
		},
		{
			name:    "singular missing args error",
			args:    args{filepath: "./testdata/singular_err.go"},
			wantErr: true,
		},
		{
			name:    "typo returns a helpful error",
			args:    args{filepath: "./testdata/typo.go"},
			wantErr: true,
		},
		{
			name:    "exclusive error",
			args:    args{filepath: "./testdata/exclusive_err.go"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkgMap, err := parser.LoadPackages(tt.args.filepath)
			if err != nil {
				t.Errorf("loadPackages() error = %v", err)
				return
			}

			parsedStructs := parser.ParsePackage(pkgMap["resources"]).Structs

			for _, pStruct := range parsedStructs {
				scanner := genlang.NewScanner(keywords)

				results, err := scanner.ScanStruct(pStruct)
				if err != nil && tt.wantErr {
					return
				}
				if err != nil {
					t.Errorf("%s: genlang.Scan() error (wantErr=%v): %s", t.Name(), tt.wantErr, err.Error())
				}
				if tt.wantErr {
					t.Errorf("%s: genlang.Scan() did not error (wantErr=%v)", t.Name(), tt.wantErr)

					return
				}

				got := make(map[string]genlang.Arg)
				for key := range results.Struct.Keys() {
					got[key] = results.Struct.Get(key)
				}

				if diff := cmp.Diff(tt.wantStruct, got); diff != "" {
					t.Errorf("%s: genlang.Result() mismatch (-want +got):\n%s", t.Name(), diff)
				}

				if tt.wantFields == nil {
					continue
				}

				for i := range pStruct.Fields() {
					gotField := make(map[string]genlang.Arg)
					for key := range results.Fields[i].Keys() {
						gotField[key] = results.Fields[i].Get(key)
					}

					if diff := cmp.Diff(tt.wantFields[i], gotField); diff != "" {
						t.Errorf("%s: genlang.Result() fields mismatch (-want +gotField):\n%s", t.Name(), diff)
					}
				}
			}
		})
	}
}

// Test_ScanStructComment pins the scan of a struct's comment given as text: what
// ScanStruct reads off the struct's own comment, prose lines passed over, two keywords on
// one line both read, and the scanner's refusals kept.
func Test_ScanStructComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		comment string
		want    map[string]genlang.Arg
		wantErr bool
	}{
		{name: "no comment", comment: "", want: map[string]genlang.Arg{}},
		{name: "prose alone", comment: "Ship is one ship.\nIt says view somewhere.\n", want: map[string]genlang.Arg{}},
		{
			name:    "annotations under prose",
			comment: "Ship is one ship.\n\n@view\n@policy(Read, crew)\n",
			want:    map[string]genlang.Arg{"view": genlang.Arg(""), "policy": genlang.Arg("Read, crew")},
		},
		{
			name:    "two keywords on one line",
			comment: "@view @policy(Read, crew)\n",
			want:    map[string]genlang.Arg{"view": genlang.Arg(""), "policy": genlang.Arg("Read, crew")},
		},
		{name: "a keyword that is no keyword", comment: "@veiw\n", wantErr: true},
		{name: "an exclusive keyword twice", comment: "@view\n@view\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := genlang.NewScanner(keywords).ScanStructComment(tt.comment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ScanStructComment() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			for keyword, arg := range tt.want {
				if !got.Has(keyword) || got.Get(keyword) != arg {
					t.Errorf("ScanStructComment() %s = %q (has %t), want %q", keyword, got.Get(keyword), got.Has(keyword), arg)
				}
			}
			count := 0
			for range got.Keys() {
				count++
			}
			if count != len(tt.want) {
				t.Errorf("ScanStructComment() read %d keywords, want %d", count, len(tt.want))
			}
		})
	}
}

// setSpec is the argument shape of the set keyword the quote tests scan: a subject set's,
// whose who: and where: are expressions of the condition language.
var setSpec = &genlang.ArgSpec{Positional: 1, Keys: []string{"value", "who", "where"}, Required: []string{"value"}, Expressions: []string{"who", "where"}}

// quoteKeywords are the keywords the quote tests scan with: set takes its scan flags from
// its spec, as a keyword parsed with one does, and the others take names and quoted values.
var quoteKeywords = map[string]genlang.KeywordOpts{
	"set":        {genlang.ScanStruct: genlang.ArgsRequired | setSpec.ScanFlags()},
	"typescript": {genlang.ScanStruct: genlang.ArgsRequired},
	"label":      {genlang.ScanStruct: genlang.ArgsRequired},
	"view":       {genlang.ScanStruct: genlang.NoArgs | genlang.Exclusive},
}

// Test_ScanStructComment_quotes pins where an annotation ends when its arguments hold
// quoted text: a parenthesis inside a double-quoted value, or inside a string of an
// annotation that takes expressions, is text and closes nothing, and an apostrophe in any
// other annotation is a character like the rest. Every comment carries a second annotation
// after the first, which is read only where the first ended at its own parenthesis.
func Test_ScanStructComment_quotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		comment string
		want    map[string]genlang.Arg
	}{
		{
			name:    "a closing parenthesis inside a string of an expression",
			comment: "@set(smiling, value: ProjectID, where: Code = ':)')\n@view\n",
			want:    map[string]genlang.Arg{"set": "smiling, value: ProjectID, where: Code = ':)'", "view": ""},
		},
		{
			name:    "an opening parenthesis inside a string of an expression",
			comment: "@set(opened, value: ProjectID, where: Code = '(')\n@view\n",
			want:    map[string]genlang.Arg{"set": "opened, value: ProjectID, where: Code = '('", "view": ""},
		},
		{
			name:    "a quote written twice keeps the string open",
			comment: "@set(noted, value: ProjectID, where: Note = 'it''s :)')\n@view\n",
			want:    map[string]genlang.Arg{"set": "noted, value: ProjectID, where: Note = 'it''s :)'", "view": ""},
		},
		{
			name:    "a double quote inside a string is the string's",
			comment: "@set(quoted, value: ProjectID, where: Note = '\")')\n@view\n",
			want:    map[string]genlang.Arg{"set": "quoted, value: ProjectID, where: Note = '\")'", "view": ""},
		},
		{
			name:    "a single quote inside a double-quoted value is the value's",
			comment: "@set(owned, value: \"it's)\", where: Rank > 1)\n@view\n",
			want:    map[string]genlang.Arg{"set": "owned, value: \"it's)\", where: Rank > 1", "view": ""},
		},
		{
			name:    "the parentheses of an expression are counted outside its strings",
			comment: "@set(leads, value: ProjectID, where: Level IN ('lead', 'member') AND (ExpiresAt IS NULL OR ExpiresAt > now))\n@view\n",
			want: map[string]genlang.Arg{
				"set":  "leads, value: ProjectID, where: Level IN ('lead', 'member') AND (ExpiresAt IS NULL OR ExpiresAt > now)",
				"view": "",
			},
		},
		{
			name:    "an annotation continued over the next lines keeps its strings",
			comment: "Assignment is one assignment.\n\n@set(leads, value: ProjectID,\n    who:   UserID = subject OR TeamID IN subject.teams,\n    where: Code IN (':)', ';(')\n        AND Note != ')')\n@view\n",
			want: map[string]genlang.Arg{
				"set":  "leads, value: ProjectID,\n    who:   UserID = subject OR TeamID IN subject.teams,\n    where: Code IN (':)', ';(')\n        AND Note != ')'",
				"view": "",
			},
		},
		{
			name:    "a closing parenthesis inside a double-quoted value",
			comment: "@typescript(Point, from: \"a)b\")\n@view\n",
			want:    map[string]genlang.Arg{"typescript": "Point, from: \"a)b\"", "view": ""},
		},
		{
			name:    "an opening parenthesis inside a double-quoted value",
			comment: "@typescript(Point, from: \"a(b\")\n@view\n",
			want:    map[string]genlang.Arg{"typescript": "Point, from: \"a(b\"", "view": ""},
		},
		{
			name:    "a double-quoted value continued over the next line",
			comment: "@typescript(Point,\n    from: \"a)b\")\n@view\n",
			want:    map[string]genlang.Arg{"typescript": "Point,\n    from: \"a)b\"", "view": ""},
		},
		{
			name:    "an apostrophe outside an expression is a character like the rest",
			comment: "@label(Pilot's roster)\n@view\n",
			want:    map[string]genlang.Arg{"label": "Pilot's roster", "view": ""},
		},
		{
			name:    "a parenthesis after an apostrophe outside an expression is counted",
			comment: "@label(Pilot's (first) roster)\n@label(the crews' (second) roster)\n@view\n",
			want:    map[string]genlang.Arg{"label": "Pilot's (first) roster\x00the crews' (second) roster", "view": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := genlang.NewScanner(quoteKeywords).ScanStructComment(tt.comment)
			if err != nil {
				t.Fatalf("ScanStructComment() error = %v", err)
			}
			read := make(map[string]genlang.Arg)
			for keyword := range got.Keys() {
				read[keyword] = got.Get(keyword)
			}
			if diff := cmp.Diff(tt.want, read); diff != "" {
				t.Errorf("ScanStructComment() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_ScanStructComment_unclosed pins the refusal of an annotation that never ends: a
// quote left open is named, single or double, and says how the quoted text is written;
// each refusal shows the annotation's own line with the caret at its keyword, where the
// unclosed parenthesis has always shown it, however many lines the annotation runs to.
func Test_ScanStructComment_unclosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		comment string
		want    string
	}{
		{
			name:    "a parenthesis never closed",
			comment: "Assignment is one assignment.\n\n@set(leads, value: ProjectID,\n    where: Level = 'lead'\n@view\n",
			want:    "unclosed parenthesis:\n@set(leads, value: ProjectID,\n   ^unclosed parenthesis",
		},
		{
			name:    "a string never closed in an expression",
			comment: "Assignment is one assignment.\n\n@set(leads, value: ProjectID,\n    where: Level = 'lead)\n@view\n",
			want:    "unclosed single quote:\n@set(leads, value: ProjectID,\n   ^a string opens with a single quote and closes with the next one; close this annotation's string, and write a quote inside a string twice ('it''s')",
		},
		{
			name:    "a string closed and opened again by a third quote",
			comment: "@set(leads, value: ProjectID, where: Note = 'it's')\n",
			want:    "unclosed single quote:\n@set(leads, value: ProjectID, where: Note = 'it's')\n   ^a string opens with a single quote and closes with the next one; close this annotation's string, and write a quote inside a string twice ('it''s')",
		},
		{
			name:    "a double-quoted value never closed",
			comment: "Point is one point.\n\n@typescript(Point,\n    from: \"geojson)\n@view\n",
			want:    "unclosed double quote:\n@typescript(Point,\n          ^a quoted value opens with a double quote and closes with the next one; close this annotation's value",
		},
		{
			name:    "a double-quoted value never closed in an expression's annotation",
			comment: "@set(leads, value: \"ProjectID, where: Level = 'lead')\n",
			want:    "unclosed double quote:\n@set(leads, value: \"ProjectID, where: Level = 'lead')\n   ^a quoted value opens with a double quote and closes with the next one; close this annotation's value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := genlang.NewScanner(quoteKeywords).ScanStructComment(tt.comment)
			if err == nil {
				t.Fatalf("ScanStructComment() expected the refusal %q, got none", tt.want)
			}
			// The error opens with the place in the scanner that raised it; the refusal
			// an author reads is what follows.
			_, refusal, _ := strings.Cut(err.Error(), "error=")
			if diff := cmp.Diff(tt.want, refusal); diff != "" {
				t.Errorf("ScanStructComment() refusal mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_ScanStructComment_parsed pins that what the scanner hands over, the keyword's
// spec parses: an annotation ended by its own parenthesis splits on the commas outside
// its strings, so a string that holds a parenthesis and a comma reaches the part it was
// written in, whole.
func Test_ScanStructComment_parsed(t *testing.T) {
	t.Parallel()

	got, err := genlang.NewScanner(quoteKeywords).ScanStructComment("@set(marked, where: Code IN (':)', 'a, b(', 'it''s)'), value: ProjectID,\n    who: UserID = subject)\n@view\n")
	if err != nil {
		t.Fatalf("ScanStructComment() error = %v", err)
	}
	invocations, err := got.Get("set").ParseInvocations(setSpec)
	if err != nil {
		t.Fatalf("ParseInvocations() error = %v", err)
	}
	if len(invocations) != 1 {
		t.Fatalf("ParseInvocations() read %d invocations, want 1", len(invocations))
	}

	want := map[string]string{"where": "Code IN (':)', 'a, b(', 'it''s)')", "value": "ProjectID", "who": "UserID = subject"}
	for key, wantValue := range want {
		if value, _ := invocations[0].Named(key); value != wantValue {
			t.Errorf("%s: = %q, want %q", key, value, wantValue)
		}
	}
	if diff := cmp.Diff([]string{"marked"}, invocations[0].Positional); diff != "" {
		t.Errorf("positional values mismatch (-want +got):\n%s", diff)
	}
}
