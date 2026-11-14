package genlang_test

import (
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
