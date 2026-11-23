package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Test_prepareIdentity_notOnOwnAccount pins the methods a person's own row does not
// offer: on every tree, for each auth, the method that disables a person and the one
// that deletes a person carry @notOnOwnAccount(ID) on the whole application's side and a
// tenant's, after the scope's line and any @callerEnters, with the sentence that says so
// as a paragraph of its own before @rpc; and, on a tenant's side of an auth that gives
// roles by hand, the method that removes a person from the tenant carries it after the
// scope's line, declares its warning and writes Warning and Execute. No other file of
// the tree names the annotation. The citadel's tree is written for a tenant record that
// declares the directory-only switch, so the methods that disable and delete carry
// @callerEnters too; the ship's is written for one that declares none.
func Test_prepareIdentity_notOnOwnAccount(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	note := "\n//\n// The person's own row does not offer it (@notOnOwnAccount): nobody disables or deletes\n" +
		"// themselves, and the method refuses the call.\n//\n"
	tests := []struct {
		name    string
		source  string
		tenant  string
		mutate  func(r *resourceGenerator)
		entered string
	}{
		{name: "a tree whose tenant record declares the switch", source: citadelTenantSource, tenant: "citadel", entered: "// @callerEnters\n"},
		{
			name:   "a tree whose tenant record declares no switch",
			source: shipTenantSource,
			tenant: "ship",
			mutate: func(r *resourceGenerator) { r.tableMap["Ships"] = shipsTable() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			if err := citadelGenerator(t, tt.source, tt.mutate).prepareIdentity(); err != nil {
				t.Fatalf("prepareIdentity() error = %v", err)
			}
			// read answers a written source with each line's indentation taken off.
			read := func(file string) string {
				lines := strings.Split(identitySource(t, file), "\n")
				for i := range lines {
					lines[i] = strings.TrimLeft(lines[i], " ")
				}

				return strings.Join(lines, "\n")
			}

			held := map[string]string{}
			for _, auth := range []string{"crew", "members"} {
				for _, verb := range []string{"disable", "delete"} {
					held[auth+"identity/"+verb+"_"+auth+"_user.go"] = "// @rpc\n" + tt.entered + "// @notOnOwnAccount(ID)\n// @outlet("
					held[auth+"identity/"+verb+"_"+auth+"_"+tt.tenant+"_user.go"] = "// @rpc\n// @permissionScope(domain)\n" + tt.entered + "// @notOnOwnAccount(ID)\n// @outlet("
				}
			}
			// Both auths give their roles by hand here, and a tenant of each removes a person
			// in one call. A tree whose directory writes an auth's roles holds no such method
			// of that auth (Test_prepareIdentity_tenantMethods).
			removals := map[string][]string{}
			for _, auth := range []string{"Crew", "Members"} {
				people := auth + strings.ToUpper(tt.tenant[:1]) + tt.tenant[1:] + "User"
				file := strings.ToLower(auth) + "identity/remove_" + strings.ToLower(auth) + "_" + tt.tenant + "_user.go"
				held[file] = "// @rpc\n// @permissionScope(domain)\n// @notOnOwnAccount(ID)\n// @outlet("
				removals[file] = []string{
					"// @answers(204)\n// @warning(typed: remove)\n// @class(access)\ntype Remove" + people + " struct {\n// @target(" + people + ")\nUserID impulse.UUID\n}",
					"Nobody removes themselves with it: the person's own row does not offer it\n// (@notOnOwnAccount), and a person leaves a tenant from their own account page.",
					"func (m *Remove" + people + ") Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error) {",
					"warning, err := tenant.RemovalWarning(ctx, txn, m.UserID)",
					"func (m *Remove" + people + ") Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {",
					"if err := tenant.RemovePerson(ctx, txn, m.UserID); err != nil {",
				}
			}

			var marked []string
			for _, file := range identityTreeFiles(t, "pkg/identity") {
				if strings.Contains(read(file), "@notOnOwnAccount") {
					marked = append(marked, file)
				}
			}
			want := make([]string, 0, len(held))
			for file := range held {
				want = append(want, file)
			}
			slices.Sort(want)
			if diff := cmp.Diff(want, marked); diff != "" {
				t.Errorf("the files that name @notOnOwnAccount (-want +got):\n%s", diff)
			}

			for file, annotations := range held {
				source := read(file)
				if !strings.Contains(source, annotations) {
					t.Errorf("%s lacks %q:\n%s", file, annotations, source)
				}
				if lines, ok := removals[file]; ok {
					for _, line := range lines {
						if !strings.Contains(source, line) {
							t.Errorf("%s lacks %q:\n%s", file, line, source)
						}
					}

					continue
				}
				paragraph := strings.Index(source, note)
				if paragraph < 0 {
					t.Errorf("%s lacks the paragraph %q:\n%s", file, note, source)

					continue
				}
				if rpc := strings.Index(source, "// @rpc\n"); rpc < paragraph {
					t.Errorf("%s says the paragraph after @rpc, want it before:\n%s", file, source)
				}
			}
		})
	}
}
