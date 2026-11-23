package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// signInSettingsFiles are the files of the sign-in settings part of the fixture's
// management, by path from the identity tree's root.
func signInSettingsFiles(t *testing.T) []string {
	t.Helper()

	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		for _, part := range []string{"sign_in_settings", "remember", "second_factor"} {
			if strings.Contains(file, part) {
				got = append(got, file)

				break
			}
		}
	}

	return got
}

// Test_prepareIdentity_signInSettings pins the sign-in settings part of an auth's
// management: the declaration over the table, which serves no route and keeps a change
// event for every write; the store's three calls, each write of one field over the one
// row; the list of the two lines and the list of the lengths a browser is remembered
// for; and the three methods, the one that stops requiring the second factor declaring
// its warning and the word a person types. The setting is every auth's: the members
// sign in through a directory that writes their roles, with no password and no choice
// of who may sign up, and have all of it, with the list of their sign-in methods.
func Test_prepareIdentity_signInSettings(t *testing.T) {
	// The generator writes relative to where the program runs, and the working directory
	// belongs to the whole process: no t.Parallel here.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables, []string{"MembersSessionSignInMethods", "MembersSessionSignInSettings"})...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"crewidentity/crew_remember_lengths.go",
		"crewidentity/crew_sign_in_settings.go",
		"crewidentity/require_crew_second_factor.go",
		"crewidentity/set_crew_remembered_browsers.go",
		"crewidentity/stop_requiring_crew_second_factor.go",
		"internal/crewresources/crew_session_sign_in_settings.go",
		"internal/membersresources/members_session_sign_in_settings.go",
		"membersidentity/members_remember_lengths.go",
		"membersidentity/members_sign_in_settings.go",
		"membersidentity/require_members_second_factor.go",
		"membersidentity/set_members_remembered_browsers.go",
		"membersidentity/stop_requiring_members_second_factor.go",
	}
	if diff := cmp.Diff(want, signInSettingsFiles(t)); diff != "" {
		t.Errorf("the sources of the sign-in settings part (-want +got):\n%s", diff)
	}
	if !slices.Contains(identityTreeFiles(t, "pkg/identity"), "membersidentity/members_sign_in_methods.go") {
		t.Error("the members' management holds no members_sign_in_methods.go, want the list: the Sign-in page is every auth's")
	}

	for file, lines := range map[string][]string{
		// The row is written by the three methods alone: the declaration serves no route,
		// and every write leaves its change event.
		"internal/crewresources/crew_session_sign_in_settings.go": {
			generationHeader, "// @resource\n\t// @suppress(listHandler)\n\t// @suppress(readHandler)\n\t// @suppress(patchHandler)\n\t// @outlet(default)\n\t// @rowWord(sign-in setting, plural: sign-in settings)\n\tCrewSessionSignInSetting struct {",
			"Name                string    `spanner:\"Name\"`", "RequireSecondFactor bool      `spanner:\"RequireSecondFactor\"`",
			"RememberBrowserDays int64     `spanner:\"RememberBrowserDays\"`", "UpdatedAt           time.Time `spanner:\"UpdatedAt\"`",
			"func (CrewSessionSignInSetting) Config() resource.Config {\n\treturn defaultConfig().SetTrackChanges(true)\n}",
		},
		"crewidentity/crew_sign_in_settings.go": {
			generationHeader, "// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Position asc)\n// @rowWord(sign-in setting, plural: sign-in settings)\ntype CrewSignInSetting struct {",
			"// @primarykey\n\tName ", "Position            int64", "Says                string", "RequireSecondFactor bool", "RememberBrowserDays int64",
			`return "CrewSignInSettings"`,
			"func ListCrewSignInSetting(ctx context.Context, _ *resource.QuerySet[CrewSignInSetting], reader resource.Client, client *Client) iter.Seq2[*CrewSignInSetting, error] {",
			"client.manager.SignInSettings(ctx, reader)",
		},
		"crewidentity/crew_remember_lengths.go": {
			generationHeader, "// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Days asc)\n// @rowWord(length of time, plural: lengths of time)\ntype CrewRememberLength struct {",
			"// @primarykey\n\tName string", "Days int64", `return "CrewRememberLengths"`, "range identity.RememberLengths()",
		},
		// Requiring the second factor is a plain call; stopping declares the warning and
		// the word; the length is picked from the lengths offered.
		"crewidentity/require_crew_second_factor.go": {
			generationHeader, "// @rpc\n// @outlet(default)\n// @answers(204)\n// @class(access)\ntype RequireCrewSecondFactor struct{}",
			"client.manager.RequireSecondFactor(ctx, txn)",
		},
		"crewidentity/stop_requiring_crew_second_factor.go": {
			generationHeader, "// @rpc\n// @outlet(default)\n// @answers(204)\n// @warning(typed: stop)\n// @class(access)\ntype StopRequiringCrewSecondFactor struct{}",
			"func (m *StopRequiringCrewSecondFactor) Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error) {",
			"client.manager.StopRequiringSecondFactorWarning(ctx, txn)", "client.manager.StopRequiringSecondFactor(ctx, txn)",
		},
		"crewidentity/set_crew_remembered_browsers.go": {
			generationHeader, "// @rpc\n// @outlet(default)\n// @answers(204)\n// @class(access)\ntype SetCrewRememberedBrowsers struct {",
			"// @enumerate(CrewRememberLengths)\n\tLength string `validate:\"required\"`\n}", "client.manager.SetRememberedBrowsers(ctx, txn, m.Length)",
		},
		// Each write is of its one field over the one row: an update where it stands, and
		// where it does not a create with the other field at its default, buffered under
		// the caller, which is what leaves the change event.
		"crewidentity/store.go": {
			"func (store) SignInSettingsRow(ctx context.Context, reader resource.ReadOnlyTransaction) (*identity.SignInSettingsRow, error) {",
			"crewresources.NewCrewSessionSignInSettingColumns().Name().RequireSecondFactor().RememberBrowserDays()",
			"SetName(session.SignInSettingsRowName).\n\t\tRead(ctx, reader)", "case httpio.HasNotFound(err):\n\t\treturn nil, nil",
			"func (store) SetRequireSecondFactor(ctx context.Context, txn resource.ReadWriteTransaction, on bool) error {",
			"crewresources.NewCrewSessionSignInSettingUpdatePatch(session.SignInSettingsRowName).SetRequireSecondFactor(on).SetUpdatedAt(spanner.CommitTimestamp)",
			"SetRequireSecondFactor(on).SetRememberBrowserDays(session.DefaultRememberedBrowserDays).SetUpdatedAt(spanner.CommitTimestamp)",
			"func (store) SetRememberBrowserDays(ctx context.Context, txn resource.ReadWriteTransaction, days int64) error {",
			"crewresources.NewCrewSessionSignInSettingUpdatePatch(session.SignInSettingsRowName).SetRememberBrowserDays(days).SetUpdatedAt(spanner.CommitTimestamp)",
			"SetRequireSecondFactor(true).SetRememberBrowserDays(days).SetUpdatedAt(spanner.CommitTimestamp)",
			"patch.Buffer(ctx, txn, resource.UserEvent(ctx))",
		},
		"membersidentity/require_members_second_factor.go": {"type RequireMembersSecondFactor struct{}"},
	} {
		assertFileContains(t, file, lines, nil)
	}
	// Requiring it again, and picking a length, admit nobody: neither declares a warning.
	for _, file := range []string{"crewidentity/require_crew_second_factor.go", "crewidentity/set_crew_remembered_browsers.go"} {
		assertFileContains(t, file, nil, []string{"@warning", "Warning("})
	}
}

// Test_prepareIdentity_signInSettingsWithoutTheTable pins an auth whose schema holds no
// sign-in settings table: there is no row, which is the defaults, and nothing changes
// them, so the management carries no list and no method, and the store, which still
// stands for identity.Store, answers no row and refuses a write saying which table is
// missing.
func Test_prepareIdentity_signInSettingsWithoutTheTable(t *testing.T) {
	t.Chdir(t.TempDir())

	tables := slices.DeleteFunc(slices.Concat(crewEveryTable, membersIdentityTables), func(table string) bool {
		return table == "CrewSessionSignInSettings"
	})
	if err := identityFixtureGenerator(t, tables...).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	if got := signInSettingsFiles(t); len(got) != 0 {
		t.Errorf("the sources of the sign-in settings part = %v, want none without the table", got)
	}
	assertFileContains(t, "crewidentity/store.go",
		[]string{
			"func (store) SignInSettingsRow(context.Context, resource.ReadOnlyTransaction) (*identity.SignInSettingsRow, error) {\n\treturn nil, nil\n}",
			`errors.New("the schema holds no table CrewSessionSignInSettings: there is no sign-in setting to write")`,
		},
		[]string{"NewCrewSessionSignInSetting", "signInSettingsRowFound"},
	)
}
