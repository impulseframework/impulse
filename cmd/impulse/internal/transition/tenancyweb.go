package transition

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
	"github.com/impulseframework/impulse/internal/words"
)

// The browser half of add tenancy: the tenant menus in the menu file, the place outside
// every tenant in the frame's configuration, the routes in the tenant shape, and the
// sign-in page's landing at the root. The tenant pages are the library's, drawn from the
// generated description, and no file is copied for them. The
// reference's record is Tenants; an application whose record is another (Clients,
// ServiceAreas) gets every name the generator derives from the record, and every word
// the screen shows, in its own record's words. The reference's auth is the skeletons'
// placeholder (skeleton.PlaceholderAuth); everything taken from it is written for the
// auth whose people the console's pages are, as impulse new renders a skeleton for an
// auth of another name.

// referenceTable is the tenant record table of the candidates the pages are copied from.
const referenceTable = "Tenants"

// writeBrowser edits the default browser project's menu file, configuration, routes and
// sign-in page.
func (tn Tenancy) writeBrowser(a *app.App, site *app.Site, ch *Change) error {
	source, ok, err := tn.browserSource(a, site.Generator, ch)
	if err != nil || !ok {
		return err
	}
	auth := consoleAuth(a, site, source)
	if err := tn.editMenus(a, source, auth, ch); err != nil {
		return err
	}
	if err := tn.editMenuSpec(a, site, source, auth, ch); err != nil {
		return err
	}
	if err := tn.editDialogsSpec(a, source, auth, ch); err != nil {
		return err
	}
	if err := tn.editFrameConfig(a, source, ch); err != nil {
		return err
	}
	if err := tn.editRoutes(a, source, auth, ch); err != nil {
		return err
	}

	return tn.editSignIn(a, source, ch)
}

// browserSource is the source root of the default browser project, the one the
// generated client is written into; false where the application has none.
func (tn Tenancy) browserSource(a *app.App, g *app.Generator, ch *Change) (source string, ok bool, err error) {
	target := defaultTarget(g)
	if target == nil {
		return "", false, nil
	}
	w, found := a.WebAppFor(target.Dir)
	if !found {
		return "", false, nil
	}
	projects, err := a.ReadAngular(w.Dir)
	if err != nil {
		return "", false, err
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(target.Dir, w.Dir), "/")
	project, found := app.ProjectFor(projects, rel)
	if !found || project.SourceRoot == "" {
		ch.skipf("%s/angular.json has no project rooted over %s, so the browser was left as it was: the tenant menus and the tenant segment in the routes", w.Dir, rel)

		return "", false, nil
	}

	return path.Join(w.Dir, project.SourceRoot), true, nil
}

// identityAuthRE reads the auth a menu file or routes file asks the library's pages of.
var identityAuthRE = regexp.MustCompile(`\{ auth: '([A-Za-z0-9]+)', scope: 'global' \}`)

// consoleAuth is the name of the auth whose people the console's pages are: the one its
// routes or its menu file ask the library's pages of, else the one the site signs its
// people in through, else the skeletons' placeholder.
func consoleAuth(a *app.App, site *app.Site, source string) string {
	for _, rel := range []string{appRoutesFile, appMenusFile} {
		data, _, err := readFile(a, path.Join(source, rel))
		if err != nil {
			continue
		}
		if m := identityAuthRE.FindStringSubmatch(string(data)); m != nil {
			return lowerFirst(m[1])
		}
	}
	if site.Default.Auth != nil {
		return authName(a, site.Default.Auth.ImportPath)
	}

	return skeleton.PlaceholderAuth
}

// forAuth is a reference's text written for the auth: its names of the placeholder auth
// (StaffTenantRoles, staffTenantRoles, staff-tenant-roles, auth: 'Staff', the staff's
// accounts) the auth's, as impulse new renders a skeleton for it.
func forAuth(text, auth string) string {
	return names.Rename(text, skeleton.PlaceholderAuth, auth)
}

// withoutDirectory is one stretch of a reference browser file that speaks of a tenant's
// directory, and what stands in its place in an application whose tenants have none.
type withoutDirectory struct {
	rel, has, becomes string
}

// withoutDirectories are the stretches of the reference's browser files that an
// application whose tenants have no directories is written without: the menu that places
// the tenant's Sign-in settings page, and that page's route, each with its name in the
// import of the library's identity pages. The library's tenants page reads the
// description, which names no directory-only field there.
var withoutDirectories = []withoutDirectory{
	{appMenusFile, "import {\n  identityMenuPages,\n  inviteTenantAction,\n  tenantSignInPage,\n  tenantsPage,\n} from '@impulseframework/resource-angular/identity-pages';\n", "import { identityMenuPages, inviteTenantAction, tenantsPage } from '@impulseframework/resource-angular/identity-pages';\n"},
	{appMenusFile, "    {\n      label: 'Tenant',\n      groups: [{ entries: [tenantSignInPage(apiDescriptor, { auth: 'Staff' })] }],\n    },\n", ""},
	{appRoutesFile, "import { identityRoutes, tenantSignInRoute, tenantsRoute } from '@impulseframework/resource-angular/identity-pages';\n", "import { identityRoutes, tenantsRoute } from '@impulseframework/resource-angular/identity-pages';\n"},
	{appRoutesFile, "          // Who may enter the tenant: its declared directories and its directory-only flag.\n          tenantSignInRoute(apiDescriptor, { auth: 'Staff' }),\n", ""},
}

// forTenants is a reference browser file's text as the transition writes it: as it is
// where a tenant can have a directory, and without what speaks of one where no tenant
// has (withoutDirectories). A stretch the reference no longer carries is an error, so a
// change to the reference is met here and not in an application's failing build.
func (tn Tenancy) forTenants(rel, text string) (string, error) {
	if !tn.NoDirectories {
		return text, nil
	}
	for _, cut := range withoutDirectories {
		if cut.rel != rel {
			continue
		}
		if strings.Count(text, cut.has) != 1 {
			return "", errors.Newf("the reference's %s no longer carries %q once, which an application whose tenants have no directories is written without", rel, cut.has)
		}
		text = strings.Replace(text, cut.has, cut.becomes, 1)
	}

	return text, nil
}

// The words for the record: one and many as the screen shows them ("Service area",
// "Service areas"), and in lower case inside a sentence.
func (tn Tenancy) one() string       { return screenWords(tn.Record()) }
func (tn Tenancy) many() string      { return screenWords(tn.Table) }
func (tn Tenancy) oneLower() string  { return words.InWords(tn.Record()) }
func (tn Tenancy) manyLower() string { return words.InWords(tn.Table) }

// screenWords spells a Go name as words with the first one capitalized, as the generator
// spells the tenant word: "ServiceArea" is "Service area". A plural acronym keeps its
// letters: "PilotIDs" is "Pilot IDs".
func screenWords(name string) string {
	spelled := words.InWords(name)
	if spelled == "" {
		return ""
	}

	return strings.ToUpper(spelled[:1]) + spelled[1:]
}

// recordRename is one name or word of the reference's record and the application's.
type recordRename struct {
	re   *regexp.Regexp
	with string
}

// recordRenames are the renames from the reference's record to the application's, in
// order: the quoted words the screen shows, then the names the generator derives from
// the record (its resources, the types of their bodies, the client's properties, the
// routes, the route parameter, the directories record's tenant field, and the generated methods of the tenant's own:
// the two that turn the record's directory-only switch, the two that say how it admits
// its directory's people, and the two that say whether its directory asks for a second
// factor, each with its route), and last the role a tenant's administrator holds, which
// the command names for the record (AdministratorRole), so a spec's role and the
// sentence that says it in words both speak of the application's.
func (tn Tenancy) recordRenames() []recordRename {
	rec, table := tn.Record(), tn.Table
	r, t := lowerFirst(rec), lowerFirst(table)
	kr, kt := strcase.ToKebab(rec), strcase.ToKebab(table)
	quoted := func(old, with string) recordRename {
		return recordRename{regexp.MustCompile(regexp.QuoteMeta("'" + old + "'")), "'" + with + "'"}
	}

	return []recordRename{
		quoted("Tenants", tn.many()),
		quoted("Tenant", tn.one()),
		quoted("tenants", kt),
		{regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*?)TenantRoles\b`), "${1}" + rec + "Roles"},
		{regexp.MustCompile(`-tenant-roles\b`), "-" + kr + "-roles"},
		{regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*?)TenantDirectoryRoles\b`), "${1}" + rec + "DirectoryRoles"},
		{regexp.MustCompile(`-tenant-directory-roles\b`), "-" + kr + "-directory-roles"},
		{regexp.MustCompile(`\bTenantDirectories\b`), rec + "Directories"},
		{regexp.MustCompile(`\btenantDirectories\b`), r + "Directories"},
		{regexp.MustCompile(`\btenant-directories\b`), kr + "-directories"},
		{regexp.MustCompile(`\b(Make|End)TenantDirectoryOnly\b`), "${1}" + rec + "DirectoryOnly"},
		{regexp.MustCompile(`\b(make|end)TenantDirectoryOnly\b`), "${1}" + rec + "DirectoryOnly"},
		{regexp.MustCompile(`\b(make|end)-tenant-directory-only\b`), "${1}-" + kr + "-directory-only"},
		{regexp.MustCompile(`\bOpenTenantToDirectory\b`), "Open" + rec + "ToDirectory"},
		{regexp.MustCompile(`\bopenTenantToDirectory\b`), "open" + rec + "ToDirectory"},
		{regexp.MustCompile(`\bopen-tenant-to-directory\b`), "open-" + kr + "-to-directory"},
		{regexp.MustCompile(`\bMakeTenantInviteOnly\b`), "Make" + rec + "InviteOnly"},
		{regexp.MustCompile(`\bmakeTenantInviteOnly\b`), "make" + rec + "InviteOnly"},
		{regexp.MustCompile(`\bmake-tenant-invite-only\b`), "make-" + kr + "-invite-only"},
		{regexp.MustCompile(`\b(VouchFor|StopVouchingFor)TenantDirectorySecondFactor\b`), "${1}" + rec + "DirectorySecondFactor"},
		{regexp.MustCompile(`\b(vouchFor|stopVouchingFor)TenantDirectorySecondFactor\b`), "${1}" + rec + "DirectorySecondFactor"},
		{regexp.MustCompile(`\b(vouch-for|stop-vouching-for)-tenant-directory-second-factor\b`), "${1}-" + kr + "-directory-second-factor"},
		{regexp.MustCompile(`\bTenantsCreate\b`), table + "Create"},
		{regexp.MustCompile(`\bTenants\b`), table},
		{regexp.MustCompile(`\.tenants\b`), "." + t},
		{regexp.MustCompile(`\{ tenants: `), "{ " + t + ": "},
		{regexp.MustCompile(`\btenants/`), kt + "/"},
		{regexp.MustCompile(`'/tenants'`), "'/" + kt + "'"},
		{regexp.MustCompile(`\btenantID\b`), r + "ID"},
		{regexp.MustCompile(`\btenantId\b`), r + "Id"},
		{regexp.MustCompile(`\b` + referenceTenantAdministrator + `\b`), tn.AdministratorRole()},
	}
}

// inRecordWords renames the record of a file written from the reference (the menus, the
// routes, the test of the dialogs) from the reference's to the application's: every name
// the generator derives from the record, and the record's word in the sentences the
// screen shows. Those are a template's and a spec's whole text, and in any other
// TypeScript file the text of its string literals, where a component holds what it says
// from its code (a dialog's words, a message). The library's own words stay: a
// Microsoft Entra tenant and its tenant id are the directory's.
func (tn Tenancy) inRecordWords(rel, text string) string {
	if tn.Table == referenceTable {
		return text
	}
	for _, r := range tn.recordRenames() {
		text = r.re.ReplaceAllString(text, r.with)
	}
	switch {
	case strings.HasSuffix(rel, ".html"), strings.HasSuffix(rel, ".spec.ts"):
		text = tn.proseInRecordWords(text)
	case strings.HasSuffix(rel, ".ts"):
		text = tn.literalsInRecordWords(text)
	}

	return text
}

// proseWord is the record's word in a sentence: tenant, Tenant, tenants, Tenants.
var proseWord = regexp.MustCompile(`\b[Tt]enants?\b`)

// proseInRecordWords renames the record's word in sentences, leaving the word where it is
// a name in code (tenant.value(), app-tenant-sign-in, @tenant) and where it is the
// directory's (a Microsoft Entra tenant, its tenant id).
func (tn Tenancy) proseInRecordWords(text string) string {
	var b strings.Builder
	last := 0
	for _, m := range proseWord.FindAllStringIndex(text, -1) {
		before, after := text[:m[0]], text[m[1]:]
		if strings.HasSuffix(before, "Entra ") || strings.HasPrefix(after, " id") || isCodeName(before, after) {
			continue
		}
		word := text[m[0]:m[1]]
		with := tn.oneLower()
		if strings.HasSuffix(word, "s") {
			with = tn.manyLower()
		}
		if word[0] == 'T' {
			with = strings.ToUpper(with[:1]) + with[1:]
		}
		b.WriteString(text[last:m[0]])
		b.WriteString(with)
		last = m[1]
	}
	b.WriteString(text[last:])

	return b.String()
}

// literalsInRecordWords renames the record's word in the sentences a TypeScript file
// holds in its string literals, quoted or template. Its code, its comments and the
// expressions inside a template literal are left as they are, and a name in code that a
// literal holds (a selector, a path) is left by the rule that leaves one in a template.
func (tn Tenancy) literalsInRecordWords(text string) string {
	var b strings.Builder
	last := 0
	for _, literal := range literalText(text) {
		b.WriteString(text[last:literal.start])
		b.WriteString(tn.proseInRecordWords(text[literal.start:literal.end]))
		last = literal.end
	}
	b.WriteString(text[last:])

	return b.String()
}

// stretch is a part of a source text, from start up to end.
type stretch struct {
	start, end int
}

// literalText lists the text of every string literal of a TypeScript source, in order:
// what is between a quoted literal's quotes, and each part of a template literal outside
// its ${...} expressions. An expression is code, read for the literals inside it, and a
// comment is passed over, so an apostrophe in one opens nothing. A regular expression
// literal is read as code: one that holds a quote would be misread, and the copied
// files hold none.
func literalText(src string) []stretch {
	s := literalScan{src: src}
	s.code(0, false)

	return s.found
}

// literalScan reads a TypeScript source for the text of its string literals.
type literalScan struct {
	src   string
	found []stretch
}

// code reads code from i and answers where it stops: the end of the source, or, in a
// template literal's expression, past the brace that closes it.
func (s *literalScan) code(i int, inExpression bool) int {
	depth := 0
	for i < len(s.src) {
		switch c := s.src[i]; {
		case strings.HasPrefix(s.src[i:], "//"):
			i = s.past(i, "\n")
		case strings.HasPrefix(s.src[i:], "/*"):
			i = s.past(i+2, "*/")
		case c == '\'' || c == '"':
			i = s.quoted(i)
		case c == '`':
			i = s.template(i + 1)
		case c == '}' && inExpression && depth == 0:
			return i + 1
		case c == '{':
			depth++
			i++
		case c == '}':
			depth--
			i++
		default:
			i++
		}
	}

	return i
}

// past answers the place after the next end at or after i, or the end of the source.
func (s *literalScan) past(i int, end string) int {
	at := strings.Index(s.src[i:], end)
	if at < 0 {
		return len(s.src)
	}

	return i + at + len(end)
}

// quoted reads the quoted literal that opens at i, notes its text and answers the place
// after it. A quote left open at the end of its line is no literal.
func (s *literalScan) quoted(i int) int {
	for j := i + 1; j < len(s.src); j++ {
		switch s.src[j] {
		case '\\':
			j++
		case '\n':
			return j
		case s.src[i]:
			s.note(i+1, j)

			return j + 1
		}
	}

	return len(s.src)
}

// template reads a template literal from the place after its opening backtick, notes
// each part of its text, reads each expression as code and answers the place after the
// closing backtick.
func (s *literalScan) template(i int) int {
	start := i
	for j := i; j < len(s.src); {
		switch {
		case s.src[j] == '\\':
			j += 2
		case s.src[j] == '`':
			s.note(start, j)

			return j + 1
		case strings.HasPrefix(s.src[j:], "${"):
			s.note(start, j)
			j = s.code(j+2, true)
			start = j
		default:
			j++
		}
	}

	return len(s.src)
}

// note keeps a literal's text that is not empty.
func (s *literalScan) note(start, end int) {
	if start < end {
		s.found = append(s.found, stretch{start: start, end: end})
	}
}

// isCodeName reports whether the word between before and after is part of a name in
// code rather than a sentence: joined to a dot, a hyphen, an at sign or a slash, or
// followed by a member, a call or a hyphen.
func isCodeName(before, after string) bool {
	for _, joined := range []string{".", "-", "@", "/"} {
		if strings.HasSuffix(before, joined) {
			return true
		}
	}
	if strings.HasPrefix(after, "(") || strings.HasPrefix(after, "-") {
		return true
	}

	return len(after) > 1 && after[0] == '.' && (isLetter(after[1]) || after[1] == '_')
}

// isLetter reports whether the byte is an ASCII letter.
func isLetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// referenceText is a browser file of a candidate's console, by its path under the
// project's source root.
func referenceText(candidate, rel string) (string, error) {
	reference, err := skeleton.FS(candidate)
	if err != nil {
		return "", err
	}
	src, err := fs.ReadFile(reference, "web/console/src/"+rel)
	if err != nil {
		return "", errors.Wrap(err, "fs.ReadFile()")
	}

	return string(src), nil
}

// The anchors the menu edit works from: the menu function's opening and its array, and
// the System menu the tenant menus go ahead of. The Feature flags page the System menu
// places needs no edit: its address has no tenant in it, which makes it a page of the
// place outside every tenant once there are tenants.
var (
	menuFunctionRE  = regexp.MustCompile(`export function appMenus\(\): Menu\[\] \{`)
	menuReturnRE    = regexp.MustCompile(`\n(\s*)return \[\n`)
	importStatement = regexp.MustCompile(`(?m)^import [^;]*;\n`)
)

// editMenus writes the tenant menus into the menu file, ahead of its other menus: the
// record's menu (All), People (the tenant's side of the auth's people) and, where a
// tenant can have a directory, the record's own menu (Sign-in settings), each showing in
// its place only, as the reference's menu file has them, with the imports they need.
// The page staff bring a tenant in from is among the auth's pages for the whole
// application, which the menu file places already.
func (tn Tenancy) editMenus(a *app.App, source, auth string, ch *Change) error {
	rel := path.Join(source, appMenusFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		ch.skipf("%s does not exist, so the tenant menus were not written: %s", rel, tn.tenantMenusNamed())

		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	if strings.Contains(text, "tenantSignInPage") {
		ch.skipf("%s places the %s's sign-in page already, so the tenant menus were left as they are", rel, tn.oneLower())

		return nil
	}
	if tn.NoDirectories && strings.Contains(text, "tenantsPage(") {
		ch.skipf("%s places the All %s page already, so the tenant menus were left as they are", rel, tn.manyLower())

		return nil
	}
	opening := menuFunctionRE.FindStringIndex(text)
	if opening == nil {
		ch.skipf("%s does not declare the menus as the skeleton's menu file does (export function appMenus(): Menu[]), so the tenant menus were not written: put %s ahead of the others, as the reference's menu file does", rel, tn.tenantMenusNamed())

		return nil
	}
	body := text[opening[1]:]
	ret := menuReturnRE.FindStringSubmatchIndex(body)
	if ret == nil {
		ch.skipf("%s: appMenus returns no array literal, so the tenant menus were not written: put %s ahead of the others, as the reference's menu file does", rel, tn.tenantMenusNamed())

		return nil
	}
	reference, err := referenceText(tenancyAdminCandidate, appMenusFile)
	if err != nil {
		return err
	}
	if reference, err = tn.forTenants(appMenusFile, reference); err != nil {
		return err
	}
	menus, err := tenantMenus(reference)
	if err != nil {
		return err
	}
	menus = forAuth(menus, auth)
	menus = tn.inRecordWords(appMenusFile, menus)

	edited := text[:opening[1]] + body[:ret[1]] + menus + body[ret[1]:]
	edited = addImports(edited, importStatement.FindAllString(tn.inRecordWords(appMenusFile, forAuth(reference, auth)), -1))
	if err := os.WriteFile(a.Abs(rel), []byte(edited), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	if tn.NoDirectories {
		ch.didf("%s: the tenant menus ahead of the others, as the reference's: %s (All %s, in the place outside every %s) and People (the %s's side of the auth's people), each showing in its place only, and no menu for Sign-in settings, since no %s has a directory to set there; Feature flags, whose address has no %s in it, is a page of the place outside every one", rel, tn.many(), tn.manyLower(), tn.oneLower(), tn.oneLower(), tn.oneLower(), tn.oneLower())

		return nil
	}
	ch.didf("%s: the tenant menus ahead of the others, as the reference's: %s (All %s, in the place outside every %s), People (the %s's side of the auth's people) and %s (Sign-in settings), each showing in its place only; Feature flags, whose address has no %s in it, is a page of the place outside every one", rel, tn.many(), tn.manyLower(), tn.oneLower(), tn.oneLower(), tn.one(), tn.oneLower())

	return nil
}

// tenantMenusNamed names the tenant menus the menu file takes, for a line that says
// they were not written: the record's menu, People and, where a tenant can have a
// directory, the record's own menu with its Sign-in settings.
func (tn Tenancy) tenantMenusNamed() string {
	if tn.NoDirectories {
		return fmt.Sprintf("the %s menu (All %s) and People", tn.many(), tn.manyLower())
	}

	return fmt.Sprintf("the %s menu (All %s), People and %s (Sign-in settings)", tn.many(), tn.manyLower(), tn.one())
}

// appMenusSpecFile is the menu file's test, beside it under a project's source root.
const appMenusSpecFile = "app/app.menus.spec.ts"

// appDialogsSpecFile is the test of the dialogs the library draws for the generated
// methods, under a project's source root.
const appDialogsSpecFile = "app/app.dialogs.spec.ts"

// systemPagesRE reads where the base skeleton's menu test lists the pages the System menu
// alone places: the auth's mailed links, then its sessions.
var systemPagesRE = regexp.MustCompile(`\n([ \t]*)'Mailed links',\n[ \t]*'Sessions',\n`)

// editMenuSpec names the page staff bring a tenant in from in the menu file's test,
// where the test lists the pages the System menu alone places as the base skeleton's
// does. The library draws that page for the auth a tenant is made through, among the
// auth's pages for the whole application, after its mailed links, so a test that pins
// those pages fails until it names the page. A console whose auth makes no tenant has no
// such page, and its test is left without a word, as is an application with no such test.
func (tn Tenancy) editMenuSpec(a *app.App, site *app.Site, source, auth string, ch *Change) error {
	maker := tn.makerAuth(a, site)
	if maker == nil || !strings.EqualFold(maker.Name, auth) {
		return nil
	}
	rel := path.Join(source, appMenusSpecFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	label := "Invited " + tn.manyLower()
	text := string(data)
	if strings.Contains(text, "'"+label+"'") {
		return nil
	}
	loc := systemPagesRE.FindStringSubmatchIndex(text)
	if loc == nil {
		ch.skipf("%s does not list the pages the System menu places as the skeleton's test does ('Mailed links', then 'Sessions'), so it was left as it is: the System menu gains %s, the page staff bring a %s in from, after Mailed links, and a test that pins the menu's pages names it there", rel, label, tn.oneLower())

		return nil
	}
	indent := text[loc[2]:loc[3]]
	at := loc[0] + len("\n"+indent+"'Mailed links',\n")
	edited := text[:at] + indent + "'" + label + "',\n" + text[at:]
	if err := os.WriteFile(a.Abs(rel), []byte(edited), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the pages the System menu places gain %s, the page staff bring a %s in from, after Mailed links", rel, label, tn.oneLower())

	return nil
}

// editDialogsSpec writes the test of the dialogs again as the tenanted skeleton's where
// it is as the base skeleton rendered it, for the auth, as editRoutes writes the routes.
// The base's test expects the dialog that invites a person to ask for no place, and once
// there are tenants that dialog asks for the tenant; the tenanted skeleton's test has no
// test of that dialog. A test edited since is left, and the brief says what it must
// expect; an application with no such test, or with the tenanted skeleton's already, is
// left without a word.
func (tn Tenancy) editDialogsSpec(a *app.App, source, auth string, ch *Change) error {
	rel := path.Join(source, appDialogsSpecFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	reference, err := referenceText(TenancyReferenceCandidate, appDialogsSpecFile)
	if err != nil {
		return err
	}
	if reference, err = tn.forTenants(appDialogsSpecFile, reference); err != nil {
		return err
	}
	reference = tn.inRecordWords(appDialogsSpecFile, forAuth(reference, auth))
	if text == reference {
		return nil
	}
	base, err := referenceText(skeleton.Base, appDialogsSpecFile)
	if err != nil {
		return err
	}
	if text != forAuth(base, auth) {
		ch.skipf("%s was edited since the skeleton rendered it, so it was left as it is: the dialog that invites a person now asks for the %s, so a test that expects it to ask for no place, as the base skeleton's does, fails until it expects the %[2]s or is taken out", rel, tn.oneLower())

		return nil
	}
	if err := os.WriteFile(a.Abs(rel), []byte(reference), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: written again as the tenanted skeleton's, which has no test of the dialog that invites a person: the base skeleton's test expects that dialog to ask for no place, and with %s it asks for the %s", rel, tn.manyLower(), tn.oneLower())

	return nil
}

// tenantMenus is the reference's tenant menus: the text of its menu array ahead of the
// System menu.
func tenantMenus(reference string) (string, error) {
	ret := menuReturnRE.FindStringIndex(reference)
	system := strings.Index(reference, "    {\n      label: 'System',")
	if len(ret) < 2 || system < ret[1] {
		return "", errors.New("the reference's menu file has no tenant menus ahead of its System menu")
	}

	return reference[ret[1]:system], nil
}

// addImports adds each import statement the file lacks. One that imports names from a
// module the file imports names from already adds the names it lacks to that statement,
// so the file keeps one import statement a module; any other goes after the file's last
// import statement.
func addImports(text string, imports []string) string {
	var missing strings.Builder
	for _, statement := range imports {
		if strings.Contains(text, statement) {
			continue
		}
		if merged, ok := mergeImport(text, statement); ok {
			text = merged

			continue
		}
		missing.WriteString(statement)
	}
	if missing.Len() == 0 {
		return text
	}
	all := importStatement.FindAllStringIndex(text, -1)
	if all == nil {
		return missing.String() + text
	}
	end := all[len(all)-1][1]

	return text[:end] + missing.String() + text[end:]
}

// namedImportRE reads an import statement of names, on one line or a name a line: the
// names between its braces and the module they come from.
var namedImportRE = regexp.MustCompile(`^import \{([^}]*)\} from '([^']+)';\n$`)

// skeletonPrintWidth is the line width the skeletons' prettier configuration sets.
const skeletonPrintWidth = 120

// mergeImport adds the names statement imports to the file's import statement of the
// same module, after the names it holds; false where the file imports no names from
// that module.
func mergeImport(text, statement string) (string, bool) {
	adding := namedImportRE.FindStringSubmatch(statement)
	if adding == nil {
		return text, false
	}
	for _, at := range importStatement.FindAllStringIndex(text, -1) {
		held := namedImportRE.FindStringSubmatch(text[at[0]:at[1]])
		if len(held) < 3 || held[2] != adding[2] {
			continue
		}
		joined := importNames(held[1])
		for _, name := range importNames(adding[1]) {
			if !slices.Contains(joined, name) {
				joined = append(joined, name)
			}
		}

		return text[:at[0]] + importOf(joined, adding[2]) + text[at[1]:], true
	}

	return text, false
}

// importNames are the names of an import statement's braces, in order.
func importNames(list string) []string {
	var found []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			found = append(found, name)
		}
	}

	return found
}

// importOf is the import statement of the names from the module as prettier writes it
// at the skeletons' width: on one line where it fits, else a name a line.
func importOf(imported []string, module string) string {
	line := fmt.Sprintf("import { %s } from '%s';", strings.Join(imported, ", "), module)
	if len(line) <= skeletonPrintWidth {
		return line + "\n"
	}

	return fmt.Sprintf("import {\n  %s,\n} from '%s';\n", strings.Join(imported, ",\n  "), module)
}

// The frame's configuration as an untenanted skeleton renders it: one place, so no
// outside, and the sentence saying so.
var (
	frameOneLineRE  = regexp.MustCompile(`provideFrame\(\{ name: '([^']*)', home: '([^']*)', menus: (\w+) \}\)`)
	frameMenusKeyRE = regexp.MustCompile(`(?m)^(\s*)menus: \w+,?\n`)
)

// untenantedSentence is the base skeleton's sentence about the frame's one place, and
// tenantedSentence the reference's about the places a tenanted application has.
const (
	untenantedSentence = `the application's name,
    // the menus of the menu file (app.menus.ts), Help, and the person's own menu. The
    // application has no tenants, so there is one place and no switcher.`
	tenantedSentence = `the application's name,
    // the place the page is in with the way to another (the place outside every tenant,
    // Configuration, and each tenant by name), the menus of the menu file (app.menus.ts),
    // Help, and the person's own menu. The tenant the URL names is the tenant the library's
    // pages work in (the frame provides RESOURCE_DOMAIN from it): a tenant's people ask that
    // tenant's digest and bind their requests to it.`
)

// editFrameConfig names the place outside every tenant in the frame's configuration,
// Configuration with a gear as the skeletons call it, where it names none.
func (tn Tenancy) editFrameConfig(a *app.App, source string, ch *Change) error {
	rel := path.Join(source, appConfigFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	if !strings.Contains(text, "provideFrame(") || strings.Contains(text, "outside:") {
		return nil
	}
	edited := frameOneLineRE.ReplaceAllString(text, "provideFrame({\n      name: '${1}',\n      home: '${2}',\n      outside: { label: 'Configuration', icon: 'settings' },\n      menus: ${3},\n    })")
	if edited == text {
		edited = frameMenusKeyRE.ReplaceAllString(text, "${1}outside: { label: 'Configuration', icon: 'settings' },\n$0")
	}
	if edited == text {
		ch.skipf("%s: provideFrame names no menus key to put the place outside every %s beside, so it was not named; add outside: { label: 'Configuration', icon: 'settings' } to it", rel, tn.oneLower())

		return nil
	}
	edited = strings.Replace(edited, untenantedSentence, tenantedSentence, 1)
	if err := os.WriteFile(a.Abs(rel), []byte(edited), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the frame names the place outside every %s, Configuration with a gear, as the skeletons do; the place switcher shows itself once the session has %s", rel, tn.oneLower(), tn.manyLower())

	return nil
}

// selfServiceSentence is the reference's sentence for a person with no place, whose
// record its own people create; outsideOnlySentence the one for a record an
// administrator alone creates, as the record add tenancy writes.
const (
	selfServiceSentence = "dashboard offers them their own tenant."
	outsideOnlySentence = "dashboard says they hold nothing."
)

// editRoutes writes the routes in the tenant shape where they are as the base skeleton
// rendered them: the home guard at the root, the refusal page, the place outside every
// tenant ahead of the tenant segment (so the record's own page is reached), and the
// segment with its guard, as the reference's routes are, for the auth. Routes edited
// since are left, and the brief says what to change.
func (tn Tenancy) editRoutes(a *app.App, source, auth string, ch *Change) error {
	rel := path.Join(source, appRoutesFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	base, err := referenceText(skeleton.Base, appRoutesFile)
	if err != nil {
		return err
	}
	if text != forAuth(base, auth) {
		ch.skipf("%s was edited since the skeleton rendered it, so it was left as it is: put the routes in the tenant shape, as the brief says", rel)

		return nil
	}
	reference, err := referenceText(TenancyReferenceCandidate, appRoutesFile)
	if err != nil {
		return err
	}
	if !strings.Contains(reference, selfServiceSentence) {
		return errors.Newf("the reference's routes no longer say %q", selfServiceSentence)
	}
	if reference, err = tn.forTenants(appRoutesFile, reference); err != nil {
		return err
	}
	reference = forAuth(strings.Replace(reference, selfServiceSentence, outsideOnlySentence, 1), auth)
	if err := os.WriteFile(a.Abs(rel), []byte(tn.inRecordWords(appRoutesFile, reference)), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	signInSettings := ", Sign-in settings"
	if tn.NoDirectories {
		signInSettings = ""
	}
	ch.didf("%s: the routes in the tenant shape, as the reference's: the home guard at the root, the refusal page, the place outside every %s (the dashboard, All %s, the %s auth's people across the application and the library's pages) ahead of the %s segment (%s, the generated DomainRoutePath) with its guard (the dashboard, the %s's people%s and the library's pages)", rel, tn.oneLower(), tn.manyLower(), auth, tn.oneLower(), tn.Segment()+"/:"+lowerFirst(tn.Record())+"ID", tn.oneLower(), signInSettings)

	return nil
}

// loginPageFile is the application's sign-in page, where the skeletons render it.
const loginPageFile = "app/components/login/login.component.html"

// The library's sign-in component on a sign-in page, and the page it is handed as where a
// sign-in lands.
var (
	loginMethodsRE = regexp.MustCompile(`<impulse-login-methods\b[^>]*>`)
	loginHomeRE    = regexp.MustCompile(`\s\[?home\]?="([^"]*)"`)
)

// rootLanding is what a sign-in page is told to do where add tenancy leaves it as it is.
const rootLanding = `have a sign-in arrive at the root (home="/" on impulse-login-methods), where the home guard sends the person to the place they last worked in`

// editSignIn has a sign-in arrive at the root where the application's sign-in page hands
// the sign-in component the landing the base skeleton rendered, the dashboard. With
// tenants the dashboard at the root is a page of the place outside every tenant, so
// everyone who holds something there would land on it, wherever they last worked; at the
// root the home guard the routes put there sends each person to the place they last
// worked in. The component is written as the reference's page has it. A page that
// arrives at the root already is left without a word; one that names another landing, or
// draws no sign-in component, is left and told what to change.
func (tn Tenancy) editSignIn(a *app.App, source string, ch *Change) error {
	rel := path.Join(source, loginPageFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	rendered, err := signInComponent(skeleton.Base)
	if err != nil {
		return err
	}
	reference, err := signInComponent(TenancyReferenceCandidate)
	if err != nil {
		return err
	}
	text := string(data)
	component := loginMethodsRE.FindString(text)
	if component == "" {
		ch.skipf("%s draws no impulse-login-methods, so it was left as it is: %s", rel, rootLanding)

		return nil
	}
	home := loginHomeRE.FindStringSubmatch(component)
	if len(home) < 2 || home[1] == "/" {
		return nil
	}
	if component != rendered {
		ch.skipf("%s hands the sign-in component another landing than the skeleton rendered (%s), so it was left as it is: %s", rel, strings.TrimSpace(home[0]), rootLanding)

		return nil
	}
	if err := os.WriteFile(a.Abs(rel), []byte(strings.Replace(text, component, reference, 1)), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: a sign-in arrives at the root (home=\"/\"), where the home guard sends the person to the place they last worked in; the dashboard it named is now a page of the place outside every %s", rel, tn.oneLower())

	return nil
}

// signInComponent is the sign-in component as a candidate's sign-in page draws it.
func signInComponent(candidate string) (string, error) {
	page, err := referenceText(candidate, loginPageFile)
	if err != nil {
		return "", err
	}
	component := loginMethodsRE.FindString(page)
	if component == "" {
		return "", errors.Newf("the %s skeleton's sign-in page (%s) draws no impulse-login-methods", candidate, loginPageFile)
	}

	return component, nil
}
