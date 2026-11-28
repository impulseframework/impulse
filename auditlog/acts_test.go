package auditlog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

// TestActs pins every act: its stored name, its place in Acts and its words as a filter's
// choice, word for word, in the order a filter of the log offers them.
func TestActs(t *testing.T) {
	t.Parallel()

	want := []struct {
		act   Act
		name  string
		words string
	}{
		{act: ActInvited, name: "invited", words: "Invited"},
		{act: ActInvitationWithdrawn, name: "invitationWithdrawn", words: "Invitation withdrawn"},
		{act: ActInvitationSentAgain, name: "invitationSentAgain", words: "Invitation sent again"},
		{act: ActInvitationAccepted, name: "invitationAccepted", words: "Invitation accepted"},
		{act: ActPasswordResetLinkSent, name: "passwordResetLinkSent", words: "Password reset link sent"},
		{act: ActPasswordChosen, name: "passwordChosen", words: "Password chosen"},
		{act: ActPasswordChanged, name: "passwordChanged", words: "Password changed"},
		{act: ActPasswordRemoved, name: "passwordRemoved", words: "Password removed"},
		{act: ActPasswordChangeRequired, name: "passwordChangeRequired", words: "New password required"},
		{act: ActIdentityUnlinked, name: "identityUnlinked", words: "Sign-in identity unlinked"},
		{act: ActAddressChangeAsked, name: "addressChangeAsked", words: "Address change asked"},
		{act: ActAddressChanged, name: "addressChanged", words: "Address changed"},
		{act: ActUsernameChanged, name: "usernameChanged", words: "Username changed"},
		{act: ActAccountDisabled, name: "accountDisabled", words: "Account disabled"},
		{act: ActAccountEnabled, name: "accountEnabled", words: "Account enabled"},
		{act: ActAccountDeleted, name: "accountDeleted", words: "Account deleted"},
		{act: ActTokenMade, name: "tokenMade", words: "Token made"},
		{act: ActTokenRevoked, name: "tokenRevoked", words: "Token revoked"},
		{act: ActBrowsersForgotten, name: "browsersForgotten", words: "Remembered browsers forgotten"},
		{act: ActRoleGiven, name: "roleGiven", words: "Role given"},
		{act: ActRoleTaken, name: "roleTaken", words: "Role taken"},
		{act: ActRoleEnded, name: "roleEnded", words: "Role ended"},
		{act: ActGroupMade, name: "groupMade", words: "Group made"},
		{act: ActGroupChanged, name: "groupChanged", words: "Group changed"},
		{act: ActGroupRemoved, name: "groupRemoved", words: "Group removed"},
		{act: ActGroupMemberAdded, name: "groupMemberAdded", words: "Put in a group"},
		{act: ActGroupMemberRemoved, name: "groupMemberRemoved", words: "Taken out of a group"},
		{act: ActGroupPlaceEnded, name: "groupPlaceEnded", words: "Place in a group ended"},
		{act: ActGroupRoleGiven, name: "groupRoleGiven", words: "Group given a role"},
		{act: ActGroupRoleTaken, name: "groupRoleTaken", words: "Role taken from a group"},
		{act: ActDirectoryTaken, name: "directoryTaken", words: "Directory taken"},
		{act: ActDirectoryRemoved, name: "directoryRemoved", words: "Directory removed"},
		{act: ActDirectoryOnlyOn, name: "directoryOnlyOn", words: "Directory only on"},
		{act: ActDirectoryOnlyOff, name: "directoryOnlyOff", words: "Directory only off"},
		{act: ActDirectoryOpened, name: "directoryOpened", words: "Opened to its directory"},
		{act: ActDirectoryInviteOnly, name: "directoryInviteOnly", words: "Made invite only for its directory"},
		{act: ActDirectorySecondFactorVouched, name: "directorySecondFactorVouched", words: "Directory's second factor vouched for"},
		{act: ActDirectorySecondFactorUnvouched, name: "directorySecondFactorUnvouched", words: "Directory's second factor no longer vouched for"},
		{act: ActSignUpOpened, name: "signUpOpened", words: "Sign-up opened"},
		{act: ActSignUpClosed, name: "signUpClosed", words: "Made invite only"},
		{act: ActSecondFactorRequired, name: "secondFactorRequired", words: "Second factor required"},
		{act: ActSecondFactorNotRequired, name: "secondFactorNotRequired", words: "Second factor no longer required"},
		{act: ActRememberedBrowsersSet, name: "rememberedBrowsersSet", words: "Remembered browsers set"},
		{act: ActSelfServiceResetOn, name: "selfServiceResetOn", words: "Own password reset turned on"},
		{act: ActSelfServiceResetOff, name: "selfServiceResetOff", words: "Own password reset turned off"},
	}

	got := Acts()
	if len(got) != len(want) {
		t.Fatalf("Acts() has %d acts, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w.act {
			t.Errorf("Acts()[%d] = %q, want %q", i, got[i], w.act)
		}
		if string(w.act) != w.name {
			t.Errorf("the act %q is stored as %q, want %q", w.words, w.act, w.name)
		}
		if words := w.act.Words(); words != w.words {
			t.Errorf("%q.Words() = %q, want %q", w.act, words, w.words)
		}
	}
	for i, act := range got {
		if slices.Index(got, act) != i {
			t.Errorf("Acts() names %q twice", act)
		}
	}
}

// TestActsHoldsEveryConstant holds every constant of type Act the package declares to
// Acts, so an act added without its place in the list fails here.
func TestActsHoldsEveryConstant(t *testing.T) {
	t.Parallel()

	declared := actConstants(t)
	if len(declared) == 0 {
		t.Fatal("found no constant of type Act in the package's files")
	}
	for name, value := range declared {
		if !slices.Contains(Acts(), Act(value)) {
			t.Errorf("%s (%q) is not in Acts()", name, value)
		}
		if Act(value).Words() == "" || Act(value).Words() == value {
			t.Errorf("%s (%q) has no words", name, value)
		}
	}
}

// TestActsAreThePagesChoices holds the Audit log page's filter of what was done to Acts:
// the browser library's list (auditActWords in
// web/projects/resource-angular/identity-pages/identity-pages.ts) offers one choice for
// each act, by its stored name and in its words, in Acts' order, so an act or a word
// changed here is changed there in the same change.
func TestActsAreThePagesChoices(t *testing.T) {
	t.Parallel()

	file := filepath.Join("..", "web", "projects", "resource-angular", "identity-pages", "identity-pages.ts")
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}
	_, list, found := strings.Cut(string(source), "const auditActWords")
	if !found {
		t.Fatalf("%s declares no auditActWords", file)
	}
	list, _, found = strings.Cut(list, "\n];")
	if !found {
		t.Fatalf("%s: auditActWords has no end", file)
	}
	choices := regexp.MustCompile(`\{ value: '([^']*)', label: (?:'([^']*)'|"([^"]*)") \}`).FindAllStringSubmatch(list, -1)
	got := make([]string, 0, len(choices))
	for _, choice := range choices {
		got = append(got, choice[1]+": "+choice[2]+choice[3])
	}
	want := make([]string, 0, len(Acts()))
	for _, act := range Acts() {
		want = append(want, string(act)+": "+act.Words())
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s offers the acts\n%q\nand Acts with Words gives\n%q", file, got, want)
	}
}

// TestWordsOfAnUnknownAct pins the words of an act Acts does not name: its stored name.
func TestWordsOfAnUnknownAct(t *testing.T) {
	t.Parallel()

	if got := Act("somethingElse").Words(); got != "somethingElse" {
		t.Errorf(`Act("somethingElse").Words() = %q, want "somethingElse"`, got)
	}
}

// TestScopeOf pins where a role held in each kind of policy scope is, as a row stores it.
func TestScopeOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scope      accesstypes.PolicyScope
		wantScope  RoleScope
		wantTenant string
	}{
		{name: "one tenant", scope: accesstypes.DomainPolicyScope("acme"), wantScope: RoleScopeTenant, wantTenant: "acme"},
		{name: "the whole application", scope: accesstypes.GlobalPolicyScope(), wantScope: RoleScopeGlobal, wantTenant: ""},
		{name: "every tenant", scope: accesstypes.EveryDomainPolicyScope(), wantScope: RoleScopeEveryTenant, wantTenant: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotScope, gotTenant := ScopeOf(tt.scope)
			if gotScope != tt.wantScope || gotTenant != tt.wantTenant {
				t.Errorf("ScopeOf() = (%q, %q), want (%q, %q)", gotScope, gotTenant, tt.wantScope, tt.wantTenant)
			}
		})
	}
}

// actConstants answers each constant of type Act the package's own files declare, by its
// name, with its value.
func actConstants(t *testing.T) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	declared := map[string]string{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parser.ParseFile(%s) error = %v", entry.Name(), err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if ident, ok := value.Type.(*ast.Ident); !ok || ident.Name != "Act" {
					continue
				}
				for i, name := range value.Names {
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok {
						t.Fatalf("%s is not a literal", name.Name)
					}
					text, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("strconv.Unquote(%s) error = %v", lit.Value, err)
					}
					declared[name.Name] = text
				}
			}
		}
	}

	return declared
}
