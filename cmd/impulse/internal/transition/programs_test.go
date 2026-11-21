package transition

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// candidatePrograms are the generate programs of the embedded skeletons, each with the
// subject its first line names.
var candidatePrograms = []struct {
	candidate, program, subject string
}{
	{candidate: "solo", program: "resourcegenerator", subject: "the application"},
	{candidate: "tenanted", program: "resourcegenerator", subject: "the application"},
	{candidate: "outlets", program: "resourcegenerator", subject: "the application"},
	{candidate: "sites", program: "consolegenerator", subject: "the console site"},
	{candidate: "sites", program: "portalgenerator", subject: "the portal site"},
	{candidate: "sites", program: "sharedgenerator", subject: "the shared vocabulary"},
}

// prose is a file's text with the comment markers and the line breaks taken out, so a
// sentence that wraps is found whole.
func prose(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(text, "//", " ")), " ")
}

// TestGenerateProgram_saysWhatAWarningIsAbout pins what the generate program and its
// warnings test, as a second site's shared program gets them (runnerProgram,
// warningsTest), say a schema warning is: a finding about the schema, most of them about
// performance and one about access, never "a performance finding" one and all. The
// warnings test says what each kind is about beside the typed value that accepts it, so
// it names every kind the generator raises, read off the generation package's source: a
// kind added there fails here until the words say what it is about.
func TestGenerateProgram_saysWhatAWarningIsAbout(t *testing.T) {
	t.Parallel()

	sources, err := filepath.Glob("../../../../generation/*.go")
	if err != nil {
		t.Fatal(err)
	}
	kind := regexp.MustCompile(`(?m)^func \((?:\w+ )?\*?(\w+)\) warning\(\) \{\}$`)
	var kinds []string
	for _, file := range sources {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range kind.FindAllStringSubmatch(string(source), -1) {
			kinds = append(kinds, match[1])
		}
	}
	if len(kinds) == 0 {
		t.Fatal("no kind of warning was found in the generation package's source: the test reads the types that declare warning()")
	}

	program := prose(fmt.Sprintf(runnerProgram, "the application"))
	pin := prose(fmt.Sprintf(warningsTest, "example.com/acme/beacon"))
	for _, name := range kinds {
		if !strings.Contains(pin, "(generation."+name+")") {
			t.Errorf("the warnings test does not say what a generation.%s is about: it names every kind of warning the generator raises", name)
		}
	}
	for name, text := range map[string]string{"the generate program": program, "the warnings test": pin} {
		if strings.Contains(text, "performance finding") {
			t.Errorf("%s calls a warning a performance finding: not every warning is about performance", name)
		}
		for _, want := range []string{"a finding about the schema", "about performance", "about access: a table a fact about people is read through that no resource declares"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q", name, want)
			}
		}
	}
}

// TestGenerateProgram_theSkeletonsCarryTheTemplates holds the generate program and the
// warnings test of every embedded skeleton to the two templates a second site's shared
// program is written from (runnerProgram, warningsTest). The templates are in the
// skeletons' shape, so what they say is said once: a change to the words of one is a
// change to the other. Run with IMPULSE_UPDATE_PROGRAMS=1 to rewrite the skeletons'
// files from the templates.
func TestGenerateProgram_theSkeletonsCarryTheTemplates(t *testing.T) {
	t.Parallel()

	update := os.Getenv("IMPULSE_UPDATE_PROGRAMS") != ""
	for _, p := range candidatePrograms {
		dir := filepath.Join("..", "skeleton", "_candidates", p.candidate, "cmd", "generate", p.program)
		module := "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/" + p.candidate
		for name, want := range map[string]string{
			"main.go":          fmt.Sprintf(runnerProgram, p.subject),
			"warnings_test.go": fmt.Sprintf(warningsTest, module),
		} {
			file := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(file, []byte(want), 0o600); err != nil {
					t.Fatal(err)
				}

				continue
			}
			got, err := os.ReadFile(file)
			if err != nil {
				t.Errorf("%v (run with IMPULSE_UPDATE_PROGRAMS=1 to write the skeletons' programs)", err)

				continue
			}
			if string(got) != want {
				t.Errorf("%s differs from the template's rendering (run with IMPULSE_UPDATE_PROGRAMS=1 to rewrite it)", file)
			}
		}
	}
}
