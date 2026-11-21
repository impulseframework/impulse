package transition

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/app"
)

// saidBy is what an error says at its cause, under what its callers wrapped it in.
func saidBy(err error) string {
	if err == nil {
		return ""
	}

	return errors.Cause(err).Error()
}

// rewriteOnce replaces one text of an application's file, which must hold it once.
func rewriteOnce(t *testing.T, a *app.App, rel, old, replacement string) {
	t.Helper()

	text := read(t, a, rel)
	if strings.Count(text, old) != 1 {
		t.Fatalf("%s holds %q %d times, want once", rel, old, strings.Count(text, old))
	}
	if err := os.WriteFile(a.Abs(rel), []byte(strings.Replace(text, old, replacement, 1)), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}

// once holds a change to saying a line once, among its did lines or its skipped lines.
func once(t *testing.T, lines []string, want string) {
	t.Helper()

	count := 0
	for _, line := range lines {
		if line == want {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the change says %q %d times, want once; it says:\n%s", want, count, strings.Join(lines, "\n"))
	}
}

// leftStart applies a Directory roles start to a new base somebody edited first, and
// returns the application, the change, and what the files that hold the first
// administrator held before the start ran.
func leftStart(t *testing.T, edit func(t *testing.T, a *app.App)) (a *app.App, ch *Change, before map[string]string) {
	t.Helper()

	before = map[string]string{}
	start := directoryRolesStart(app.MethodGoogle, "fleet.example")
	a, ch = startedOver(t, &start, func(a *app.App) {
		edit(t, a)
		for _, rel := range []string{deployFile, migrateCommand, baseDataLevel, deployAdministratorsTest, baseEnvTemplate} {
			if exists(a, rel) {
				before[rel] = read(t, a, rel)
			}
		}
	}, "send_staff_user_password_reset_link", "assign_staff_role", "remove_staff_role", "invite_staff_user", "withdraw_staff_invitation")

	return a, ch, before
}

// TestDirectoryStart_Apply_administratorLeft holds the all or nothing of what a start
// whose directory gives every role takes out: where the deployment package, the migrate
// command or the data level is not as the base writes it, none of what invites a first
// administrator is taken out of any of them, and the change says which file stopped it
// and what to take out by hand.
func TestDirectoryStart_Apply_administratorLeft(t *testing.T) {
	t.Parallel()

	const handStep = ", so what invites a first administrator was left as it is. The staff auth invites nobody, since its directory gives every role, the Super Administrator's included, and the application does not compile while its deployment invites one: take what invites one out of the deployment package (AdministratorAuths, firstAdministrator, administratorOf, InviteAdministrators, ReinviteAdministrator, invitationRefused, reportAdministrator), of the migrate command (the call of InviteAdministrators and the -reinvite-administrator flag) and of the data level (the APP_STAFF_ADMINISTRATOR setting and its accessor)"
	tests := []struct {
		name    string
		edit    func(t *testing.T, a *app.App)
		wantWhy string
	}{
		{
			name: "the deployment package declares one of the names no more",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, deployFile, "func invitationRefused(err error, auth, method string) error {", "func refused(err error, auth, method string) error {")
			},
			wantWhy: "pkg/deploy/deploy.go is not as the base writes it (it declares no invitationRefused)",
		},
		{
			name: "the deployment package's comment says something else of the invitation",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, deployFile, "against what the permission engine's store holds, inviting", "against what the store of the permission engine holds, inviting")
			},
			wantWhy: `pkg/deploy/deploy.go is not as the base writes it ("applying the schema migrations, checking the release's role policy\n// against what the permission engine's store holds," is not there once)`,
		},
		{
			name: "the migrate command says something else of what it runs",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, migrateCommand, "// run does what the flags ask: the report, the force, the retirement,", "// run does what the flags ask of it: the report, the force, the retirement,")
			},
			wantWhy: `cmd/deployment/migrate/main.go is not as the base writes it ("// run does what the flags ask: the report, the force, the retirement," is not there once)`,
		},
		{
			name: "the migrate command is not there",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.Remove(a.Abs(migrateCommand)); err != nil {
					t.Fatalf("os.Remove() error = %v", err)
				}
			},
			wantWhy: "cmd/deployment/migrate/main.go is not there",
		},
		{
			name: "the data level has no accessor of the name",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, baseDataLevel, "func (c *DataConfiguration) StaffAdministrator() string {", "func (c *DataConfiguration) FirstAdministrator() string {")
			},
			wantWhy: "pkg/config/data.go is not as the base writes it (it declares no StaffAdministrator)",
		},
		{
			name: "the data level keeps the address in a field of another name",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, baseDataLevel, "\tStaffAdministrator string `env:", "\tFirstAdministrator string `env:")
			},
			wantWhy: "pkg/config/data.go is not as the base writes it (it declares no field StaffAdministrator)",
		},
		{
			name: "the data level says something else of its mail sender",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, baseDataLevel, "// refused until it is. The log is no mailbox", "// refused until then. The log is no mailbox")
			},
			wantWhy: `pkg/config/data.go is not as the base writes it ("// relay takes over once the version is pinned; a release to any other environment is\n// refused until it is." is not there once)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, ch, before := leftStart(t, tt.edit)

			once(t, ch.Skipped, tt.wantWhy+handStep)
			for _, rel := range []string{deployFile, migrateCommand, deployAdministratorsTest} {
				held, had := before[rel]
				if !had {
					continue
				}
				if got := read(t, a, rel); got != held {
					t.Errorf("%s changed, and what invites a first administrator was to be left as it is (-before +after):\n%s", rel, cmp.Diff(held, got))
				}
			}
			level := read(t, a, baseDataLevel)
			for _, kept := range []string{"Administrator string `env:\"APP_STAFF_ADMINISTRATOR\"`\n", "\treturn c.env.StaffAdministrator\n", "invite its first\n// administrator through it"} {
				if !strings.Contains(level, kept) {
					t.Errorf("%s lost %q, and what invites a first administrator was to be left as it is", baseDataLevel, kept)
				}
			}
			if template := read(t, a, baseEnvTemplate); !strings.Contains(template, "# export APP_STAFF_ADMINISTRATOR=\n") {
				t.Errorf("%s lost the variable, and what invites a first administrator was to be left as it is", baseEnvTemplate)
			}
			if got := linesWith(ch.Did, "", []string{"what invites a first administrator was taken out"}); len(got) != 0 {
				t.Errorf("the change says it took out what invites a first administrator, and it left it: %q", got)
			}
		})
	}
}

// TestDirectoryStart_Apply_administratorNotTheBases holds what the start does about the
// two files it takes the first administrator out of only where they are the base's: each
// is left as it is where somebody changed it, the change says what to take out of it,
// and the rest is taken out all the same.
func TestDirectoryStart_Apply_administratorNotTheBases(t *testing.T) {
	t.Parallel()

	const taken = "pkg/deploy/deploy.go, cmd/deployment/migrate/main.go, pkg/config/data.go"

	t.Run("an environment template that names the variable another way", func(t *testing.T) {
		t.Parallel()

		a, ch, _ := leftStart(t, func(t *testing.T, a *app.App) {
			t.Helper()
			rewriteOnce(t, a, baseEnvTemplate, "# export APP_STAFF_ADMINISTRATOR=\n", "export APP_STAFF_ADMINISTRATOR=first@fleet.example\n")
		})

		if template := read(t, a, baseEnvTemplate); !strings.Contains(template, "export APP_STAFF_ADMINISTRATOR=first@fleet.example\n") {
			t.Errorf("%s lost a variable somebody had set:\n%s", baseEnvTemplate, template)
		}
		once(t, ch.Skipped, ".envrc.template: it names APP_STAFF_ADMINISTRATOR, which nothing reads now, and not as the base writes it, so it was left as it is; take the variable out")
		if got := linesWith(ch.Did, taken+", "+deployAdministratorsTest+": what invites a first administrator was taken out", nil); len(got) != 1 {
			t.Errorf("the change does not say once that it took the rest out, the template not among the files: %q", ch.Did)
		}
		if got := linesWith(ch.Did, "", []string{"the environment template's variable"}); len(got) != 0 {
			t.Errorf("the change says it took the variable out of the template, which it left: %q", got)
		}
		if strings.Contains(read(t, a, baseDataLevel), "StaffAdministrator") {
			t.Errorf("%s reads the address still", baseDataLevel)
		}
	})

	t.Run("a test of the deployment's report that somebody changed", func(t *testing.T) {
		t.Parallel()

		const own = "package deploy\n\nimport \"testing\"\n\nfunc TestOwn(t *testing.T) {\n\tt.Parallel()\n}\n"
		a, ch, _ := leftStart(t, func(t *testing.T, a *app.App) {
			t.Helper()
			if err := os.WriteFile(a.Abs(deployAdministratorsTest), []byte(own), 0o644); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
		})

		if got := read(t, a, deployAdministratorsTest); got != own {
			t.Errorf("%s was changed, and it is somebody's own:\n%s", deployAdministratorsTest, got)
		}
		once(t, ch.Skipped, "pkg/deploy/administrators_test.go: it tests what the deployment writes about a first administrator's invitation, which was taken out, and it is not as the base writes it, so it was left as it is; take those tests out of it")
		if got := linesWith(ch.Did, taken+", "+baseEnvTemplate+": what invites a first administrator was taken out", nil); len(got) != 1 {
			t.Errorf("the change does not say once that it took the rest out, the test not among the files: %q", ch.Did)
		}
		if got := linesWith(ch.Did, "", []string{"the test of what the deployment writes about an invitation"}); len(got) != 0 {
			t.Errorf("the change says it took the test out, which it left: %q", got)
		}
	})

	t.Run("no test of the deployment's report", func(t *testing.T) {
		t.Parallel()

		a, ch, _ := leftStart(t, func(t *testing.T, a *app.App) {
			t.Helper()
			if err := os.Remove(a.Abs(deployAdministratorsTest)); err != nil {
				t.Fatalf("os.Remove() error = %v", err)
			}
		})

		if exists(a, deployAdministratorsTest) {
			t.Errorf("%s was written, and the application had none", deployAdministratorsTest)
		}
		if got := linesWith(ch.Did, taken+", "+baseEnvTemplate+": what invites a first administrator was taken out", nil); len(got) != 1 {
			t.Errorf("the change does not say once what it took out, the test not among the files: %q", ch.Did)
		}
		if got := linesWith(ch.Skipped, deployAdministratorsTest, nil); len(got) != 0 {
			t.Errorf("the change speaks of a test the application does not have: %q", got)
		}
	})
}

// TestPieces_roundTrip holds the two directions to one account of where the first
// administrator stands: what a start takes out of the base's migrate command, deployment
// package and data level is what impulse add auth puts back, byte for byte, whatever the
// application's module path sorts before or after among the imports and whatever its
// auth is called.
func TestPieces_roundTrip(t *testing.T) {
	t.Parallel()

	files := map[string][]piece{
		migrateCommand: migratePieces,
		deployFile:     {deployDocPiece},
		baseDataLevel:  {dataLevelDocPiece},
	}
	for _, module := range []string{"example.com/acme/depot", "github.com/acme/depot", "github.com/zeta/depot", "gitlab.example/acme/depot"} {
		for _, auth := range []string{"staff", "partners"} {
			for rel, pieces := range files {
				t.Run(module+" "+auth+" "+rel, func(t *testing.T) {
					t.Parallel()

					rendering, err := baseRendering(rel, module, auth)
					if err != nil {
						t.Fatalf("baseRendering() error = %v", err)
					}
					without, err := withoutPieces(rendering, pieces)
					if err != nil {
						t.Fatalf("withoutPieces() error = %v", err)
					}
					if bytes.Equal(without, rendering) {
						t.Fatalf("withoutPieces() took nothing out of %s", rel)
					}
					for _, own := range []string{module + "/pkg/config\"", module + "/pkg/auth/" + auth + "\""} {
						if strings.Contains(string(rendering), own) && !strings.Contains(string(without), own) {
							t.Errorf("withoutPieces() took the import %s out of %s, which is the application's own", own, rel)
						}
					}
					with, err := withPieces(without, rendering, pieces)
					if err != nil {
						t.Fatalf("withPieces() error = %v", err)
					}
					if diff := cmp.Diff(string(rendering), string(with)); diff != "" {
						t.Errorf("withPieces() over withoutPieces() is not the base's %s (-want +got):\n%s", rel, diff)
					}
					// The texts a piece stands between name no auth, so what one start left
					// takes the pieces of an auth of any name.
					other, err := baseRendering(rel, module, "members")
					if err != nil {
						t.Fatalf("baseRendering() error = %v", err)
					}
					if _, err := withPieces(without, other, pieces); err != nil {
						t.Errorf("withPieces() for an auth of another name: error = %v", err)
					}
				})
			}
		}
	}
}

// Test_piece_stretch holds a piece to being found once, by the text before it and the
// text after, and to saying which is missing.
func Test_piece_stretch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		piece   piece
		text    string
		want    string
		wantErr string
	}{
		{name: "between the two", piece: piece{before: "a, ", after: " and c"}, text: "a, b and c", want: "b"},
		{name: "nothing between the two", piece: piece{before: "a,", after: " and c"}, text: "a, and c", want: ""},
		{name: "the first text that follows", piece: piece{before: "a ", after: "c"}, text: "a b c c", want: "b "},
		{name: "what stands before is not there", piece: piece{before: "x ", after: "c"}, text: "a b c", wantErr: `"x " is not there once`},
		{name: "what stands before is there twice", piece: piece{before: "a ", after: "c"}, text: "a a b c", wantErr: `"a " is not there once`},
		{name: "what stands after does not follow", piece: piece{before: "b ", after: "a"}, text: "a b c", wantErr: `"a" does not follow "b "`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			from, to, err := tt.piece.stretch(tt.text)
			if tt.wantErr != "" {
				if saidBy(err) != tt.wantErr {
					t.Fatalf("stretch() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("stretch() error = %v", err)
			}
			if got := tt.text[from:to]; got != tt.want {
				t.Errorf("stretch() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Test_withPieces holds the putting back to a file that stands as the taking out left
// it: a file that says something else at a piece's place is refused, and so is a
// rendering that does not hold the piece.
func Test_withPieces(t *testing.T) {
	t.Parallel()

	pieces := []piece{{before: "It applies", after: ".\n", without: " the migrations"}}
	const (
		rendering = "It applies the migrations and invites the first administrator.\n"
		taken     = "It applies the migrations.\n"
	)
	tests := []struct {
		name      string
		src       string
		rendering string
		want      string
		wantErr   string
	}{
		{name: "the file as the taking out left it", src: taken, rendering: rendering, want: rendering},
		{name: "a file that says something else there", src: "It applies every migration.\n", rendering: rendering, wantErr: `"It applies the migrations.\n" is not there once`},
		{name: "a file that says it twice", src: taken + taken, rendering: rendering, wantErr: `"It applies the migrations.\n" is not there once`},
		{name: "a rendering without the piece", src: taken, rendering: "It runs.\n", wantErr: `"It applies" is not there once`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := withPieces([]byte(tt.src), []byte(tt.rendering), pieces)
			if tt.wantErr != "" {
				if saidBy(err) != tt.wantErr {
					t.Fatalf("withPieces() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("withPieces() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("withPieces() = %q, want %q", got, tt.want)
			}
			back, err := withoutPieces(got, pieces)
			if err != nil || string(back) != tt.src {
				t.Errorf("withoutPieces() = %q, %v, want the file it was put back into", back, err)
			}
		})
	}
	if _, err := withoutPieces([]byte("It runs.\n"), pieces); saidBy(err) != `"It applies" is not there once` {
		t.Errorf("withoutPieces() error = %v, want it to refuse a file that does not hold the piece", err)
	}
}

// Test_cutField holds the cut of one struct field to taking its comment and the blank
// line above it, and to refusing a file whose structs declare none of the name.
func Test_cutField(t *testing.T) {
	t.Parallel()

	const src = `package config

type settings struct {
	// Mailbox is who the application mails as.
	Mailbox string ` + "`env:\"APP_MAILBOX\"`" + `

	// Administrator is the address a release invites,
	// where it invites one.
	Administrator string ` + "`env:\"APP_ADMINISTRATOR\"`" + `
}

type other struct {
	Only string
}
`
	tests := []struct {
		name    string
		field   string
		want    string
		wantErr string
	}{
		{
			name:  "a field set apart with its comment",
			field: "Administrator",
			want:  "package config\n\ntype settings struct {\n\t// Mailbox is who the application mails as.\n\tMailbox string `env:\"APP_MAILBOX\"`\n}\n\ntype other struct {\n\tOnly string\n}\n",
		},
		{
			name:  "the first field of its struct",
			field: "Mailbox",
			want:  "package config\n\ntype settings struct {\n\n\t// Administrator is the address a release invites,\n\t// where it invites one.\n\tAdministrator string `env:\"APP_ADMINISTRATOR\"`\n}\n\ntype other struct {\n\tOnly string\n}\n",
		},
		{
			name:  "the one field of its struct",
			field: "Only",
			want:  strings.Replace(src, "\tOnly string\n", "", 1),
		},
		{name: "no field of the name", field: "Secret", wantErr: "it declares no field Secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cutField("pkg/config/data.go", []byte(src), tt.field)
			if tt.wantErr != "" {
				if saidBy(err) != tt.wantErr {
					t.Fatalf("cutField() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("cutField() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("cutField() mismatch (-want +got):\n%s", diff)
			}
		})
	}
	if _, err := cutField("pkg/config/data.go", []byte("package config\n\ntype settings struct {\n"), "Mailbox"); err == nil {
		t.Error("cutField() error = nil for a file that does not parse")
	}
}

// Test_environmentStruct holds the tool to reading where a data level keeps its
// environment: the struct its DataConfiguration points at under the field env.
func Test_environmentStruct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "a pointer to a struct of the file", src: "package config\n\ntype DataConfiguration struct {\n\tenv *dataConfig\n\tclosers []func()\n}\n", want: "dataConfig"},
		{name: "kept by value", src: "package config\n\ntype DataConfiguration struct {\n\tenv dataConfig\n}\n"},
		{name: "a pointer to another package's struct", src: "package config\n\ntype DataConfiguration struct {\n\tenv *settings.Data\n}\n"},
		{name: "under another field", src: "package config\n\ntype DataConfiguration struct {\n\tsettings *dataConfig\n}\n"},
		{name: "another struct's field", src: "package config\n\ntype CoreConfiguration struct {\n\tenv *coreConfig\n}\n"},
		{name: "a data level that is no struct", src: "package config\n\ntype DataConfiguration interface {\n\tClose()\n}\n"},
		{name: "a file that does not parse", src: "package config\n\ntype DataConfiguration struct {\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := environmentStruct("pkg/config/data.go", []byte(tt.src)); got != tt.want {
				t.Errorf("environmentStruct() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Test_declarationText holds the reading of one declaration to its whole text, comment
// included, and Test of the import block to the block as the file writes it.
func Test_declarationText(t *testing.T) {
	t.Parallel()

	const src = `package deploy

import (
	"fmt"

	"example.com/acme/depot/pkg/config"
)

// Auths names the auths.
func Auths() []string {
	return []string{"staff"}
}

// first is one auth's.
type first struct {
	email string
}

var limit = 3
`
	for name, want := range map[string]string{
		"Auths": "// Auths names the auths.\nfunc Auths() []string {\n\treturn []string{\"staff\"}\n}\n",
		"first": "// first is one auth's.\ntype first struct {\n\temail string\n}\n",
		"limit": "var limit = 3\n",
	} {
		got, err := declarationText("pkg/deploy/deploy.go", []byte(src), name)
		if err != nil || got != want {
			t.Errorf("declarationText(%s) = %q, %v, want %q", name, got, err, want)
		}
	}
	if _, err := declarationText("pkg/deploy/deploy.go", []byte(src), "gone"); saidBy(err) != "pkg/deploy/deploy.go declares no gone" {
		t.Errorf("declarationText() error = %v, want it to say the file declares none of the name", err)
	}
	if _, err := declarationText("pkg/deploy/deploy.go", []byte("package deploy\n\nfunc ("), "Auths"); err == nil {
		t.Error("declarationText() error = nil for a file that does not parse")
	}

	block, err := importBlock("pkg/deploy/deploy.go", []byte(src))
	if want := "import (\n\t\"fmt\"\n\n\t\"example.com/acme/depot/pkg/config\"\n)\n"; err != nil || block != want {
		t.Errorf("importBlock() = %q, %v, want %q", block, err, want)
	}
	if block, err := importBlock("pkg/deploy/doc.go", []byte("package deploy\n\nconst one = 1\n")); err != nil || block != "" {
		t.Errorf("importBlock() = %q, %v, want nothing for a file that imports nothing", block, err)
	}
	if _, err := importBlock("pkg/deploy/doc.go", []byte("pakage deploy\n")); err == nil {
		t.Error("importBlock() error = nil for a file that does not parse")
	}
}

// Test_administratorEnvRE holds the block of an environment template that names a first
// administrator to the one the base writes: its comment, down to the variable unset.
func Test_administratorEnvRE(t *testing.T) {
	t.Parallel()

	const (
		block    = "# APP_STAFF_ADMINISTRATOR is the email address of the staff auth's first administrator\n# (pkg/auth/staff, InviteAdministrator): unset here.\n# export APP_STAFF_ADMINISTRATOR=\n"
		template = "# APP_MAIL_MAILBOX is who the application mails as.\n# export APP_MAIL_MAILBOX=\n\n" + block + "\nexport APP_USERNAME=staff-dev\n"
	)
	re := administratorEnvRE("APP_STAFF_ADMINISTRATOR")
	if got := re.FindAllString(template, -1); len(got) != 1 || got[0] != block {
		t.Errorf("the block found = %q, want %q", got, block)
	}
	for name, other := range map[string]string{
		"another auth's variable": strings.ReplaceAll(template, "APP_STAFF_ADMINISTRATOR", "APP_STAFF_ADMINISTRATORS"),
		"the variable set":        strings.Replace(template, "# export APP_STAFF_ADMINISTRATOR=\n", "export APP_STAFF_ADMINISTRATOR=first@fleet.example\n", 1),
		"no comment of the base":  "# export APP_STAFF_ADMINISTRATOR=\n",
	} {
		if got := re.FindAllString(other, -1); len(got) != 0 {
			t.Errorf("%s: the block found = %q, want none", name, got)
		}
	}
}
