package transition

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// The browser half of add tenancy: the tenant pages copied into the default browser
// project in the record's words, the tenant menus in the menu file, the place outside
// every tenant in the frame's configuration, the routes in the tenant shape, and the
// sign-in page's landing at the root. The
// reference's record is Tenants; an application whose record is another (Clients,
// ServiceAreas) gets every name the generator derives from the record, and every word
// the screen shows, in its own record's words. The reference's auth is the skeletons'
// placeholder (skeleton.PlaceholderAuth); everything taken from it is written for the
// auth whose people the console's pages are, as impulse new renders a skeleton for an
// auth of another name.

// referenceTable is the tenant record table of the candidates the pages are copied from.
const referenceTable = "Tenants"

// writeBrowser lays the tenant pages into the default browser project and edits its menu
// file, configuration, routes and sign-in page.
func (tn Tenancy) writeBrowser(a *app.App, site *app.Site, ch *Change) error {
	source, ok, err := tn.browserSource(a, site.Generator, ch)
	if err != nil || !ok {
		return err
	}
	auth := consoleAuth(a, site, source)
	if err := tn.copyTenantPages(a, source, auth, ch); err != nil {
		return err
	}
	if err := tn.editMenus(a, source, auth, ch); err != nil {
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
		ch.skipf("%s/angular.json has no project rooted over %s, so the browser was left as it was: the tenant pages (the tenant's sign-in page, the All %s page's configuration and the New %s dialog), the tenant menus and the tenant segment in the routes", w.Dir, rel, tn.manyLower(), tn.oneLower())

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

// copyTenantPages lays the tenant pages (tenantWebFiles) into the browser project, for
// the auth and in the record's words.
func (tn Tenancy) copyTenantPages(a *app.App, source, auth string, ch *Change) error {
	dst := path.Join(source, tenantSignInRoutesPath)
	if _, err := os.Stat(a.Abs(dst)); err == nil {
		ch.skipf("%s already exists and was left alone", dst)

		return nil
	}
	for _, f := range tenantWebFiles {
		reference, err := skeleton.FS(f.candidate)
		if err != nil {
			return err
		}
		src, err := fs.ReadFile(reference, "web/console/src/"+f.rel)
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		if err := writeNew(a, path.Join(source, f.rel), tn.inRecordWords(f.rel, forAuth(string(src), auth))); err != nil {
			return err
		}
	}
	renamed := ""
	if tn.Table != referenceTable {
		renamed = fmt.Sprintf(", with every name and word of the reference's Tenant record the %s record's", tn.Record())
	}
	ch.didf("%s, components/ui/new-tenant/ and configs/tenants.config.ts: the %s's sign-in page (who may enter the %s: its declared directories and whether they are the only way in, with its route and its page for the menu file in sign-in.routes.ts), the New %s dialog (an administrator holding Create on the record creates one) and the configuration of All %s, the library's list page over the record, from the reference%s; the library's frame carries the %s the URL names, the guard on the tenant segment, the refusal page and the place switcher",
		path.Dir(dst)+"/", tn.oneLower(), tn.oneLower(), tn.oneLower(), tn.manyLower(), renamed, tn.oneLower())

	return nil
}

// The words for the record: one and many as the screen shows them ("Service area",
// "Service areas"), and in lower case inside a sentence.
func (tn Tenancy) one() string       { return screenWords(tn.Record()) }
func (tn Tenancy) many() string      { return screenWords(tn.Table) }
func (tn Tenancy) oneLower() string  { return strings.ToLower(tn.one()) }
func (tn Tenancy) manyLower() string { return strings.ToLower(tn.many()) }

// screenWords spells a Go name as words with the first one capitalized, as the generator
// spells the tenant word: "ServiceArea" is "Service area".
func screenWords(name string) string {
	words := strcase.ToCase(name, strcase.LowerCase, ' ')
	if words == "" {
		return ""
	}

	return strings.ToUpper(words[:1]) + words[1:]
}

// recordRename is one name or word of the reference's record and the application's.
type recordRename struct {
	re   *regexp.Regexp
	with string
}

// recordRenames are the renames from the reference's record to the application's, in
// order: the quoted words the screen shows, then the names the generator derives from
// the record (its resources, the types of their bodies, the client's properties, the
// routes, the route parameter and the directories record's tenant field).
func (tn Tenancy) recordRenames() []recordRename {
	rec, table := tn.Record(), tn.Table
	r, t := lowerFirst(rec), lowerFirst(table)
	kr, kt := strcase.ToKebab(rec), strcase.ToKebab(table)
	quoted := func(old, with string) recordRename {
		return recordRename{regexp.MustCompile(regexp.QuoteMeta("'" + old + "'")), "'" + with + "'"}
	}

	return []recordRename{
		quoted("All tenants", "All "+tn.manyLower()),
		quoted("New tenant…", "New "+tn.oneLower()+"…"),
		quoted("New tenant", "New "+tn.oneLower()),
		quoted("Tenants", tn.many()),
		quoted("Tenant", tn.one()),
		quoted("tenants", kt),
		{regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*?)TenantRoles\b`), "${1}" + rec + "Roles"},
		{regexp.MustCompile(`-tenant-roles\b`), "-" + kr + "-roles"},
		{regexp.MustCompile(`\bTenantDirectories\b`), rec + "Directories"},
		{regexp.MustCompile(`\btenantDirectories\b`), r + "Directories"},
		{regexp.MustCompile(`\btenant-directories\b`), kr + "-directories"},
		{regexp.MustCompile(`\bTenantsCreate\b`), table + "Create"},
		{regexp.MustCompile(`\bTenants\b`), table},
		{regexp.MustCompile(`\.tenants\b`), "." + t},
		{regexp.MustCompile(`\{ tenants: `), "{ " + t + ": "},
		{regexp.MustCompile(`\btenants/`), kt + "/"},
		{regexp.MustCompile(`'/tenants'`), "'/" + kt + "'"},
		{regexp.MustCompile(`\btenantID\b`), r + "ID"},
		{regexp.MustCompile(`\btenantId\b`), r + "Id"},
	}
}

// inRecordWords renames a copied file's record from the reference's to the
// application's: every name the generator derives from the record, and in a template
// or a spec the record's word in the sentences the screen shows. The library's own
// words stay: a Microsoft Entra tenant and its tenant id are the directory's, and the
// copied components keep their names.
func (tn Tenancy) inRecordWords(rel, text string) string {
	if tn.Table == referenceTable {
		return text
	}
	for _, r := range tn.recordRenames() {
		text = r.re.ReplaceAllString(text, r.with)
	}
	if strings.HasSuffix(rel, ".html") || strings.HasSuffix(rel, ".spec.ts") {
		text = tn.proseInRecordWords(text)
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

// The anchors the menu edit works from: the menu function's opening and its array, the
// System menu the tenant menus go ahead of, and the feature flags entry, which belongs to
// the place outside every tenant once there are tenants.
var (
	menuFunctionRE  = regexp.MustCompile(`export function appMenus\(actions: FrameActions\): Menu\[\] \{`)
	menuReturnRE    = regexp.MustCompile(`\n(\s*)return \[\n`)
	featureFlagsRE  = regexp.MustCompile(`\n(\s*)run: \(\) => actions\.featureFlags\(\),\n(\s*)permission:`)
	importStatement = regexp.MustCompile(`(?m)^import [^;]*;\n`)
)

// editMenus writes the tenant menus into the menu file, ahead of its other menus: the
// record's menu (All, New…), People (the tenant's side of the auth's people) and the
// record's own menu (Sign-in settings), each showing in its place only, as the
// reference's menu file has them, with the imports they need and the dialog service.
func (tn Tenancy) editMenus(a *app.App, source, auth string, ch *Change) error {
	rel := path.Join(source, appMenusFile)
	data, mode, err := readFile(a, rel)
	if errors.Is(err, os.ErrNotExist) {
		ch.skipf("%s does not exist, so the tenant menus were not written: the %s menu (All %s, New %s…), People and %s (Sign-in settings)", rel, tn.many(), tn.manyLower(), tn.oneLower(), tn.one())

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
	opening := menuFunctionRE.FindStringIndex(text)
	if opening == nil {
		ch.skipf("%s does not declare the menus as the skeleton's menu file does (export function appMenus(actions: FrameActions): Menu[]), so the tenant menus were not written: put the %s menu (All %s, New %s…), People and %s (Sign-in settings) ahead of the others, as the reference's menu file does", rel, tn.many(), tn.manyLower(), tn.oneLower(), tn.one())

		return nil
	}
	body := text[opening[1]:]
	ret := menuReturnRE.FindStringSubmatchIndex(body)
	if ret == nil {
		ch.skipf("%s: appMenus returns no array literal, so the tenant menus were not written: put the %s menu (All %s, New %s…), People and %s (Sign-in settings) ahead of the others, as the reference's menu file does", rel, tn.many(), tn.manyLower(), tn.oneLower(), tn.one())

		return nil
	}
	reference, err := referenceText(tenancyAdminCandidate, appMenusFile)
	if err != nil {
		return err
	}
	menus, err := tenantMenus(reference)
	if err != nil {
		return err
	}
	menus = forAuth(menus, auth)
	menus = tn.inRecordWords(appMenusFile, menus)

	edited := text[:opening[1]]
	if !strings.Contains(text, "inject(MatDialog)") {
		edited += body[:ret[0]] + "\n" + body[ret[2]:ret[3]] + "const dialog = inject(MatDialog);" + body[ret[0]:ret[1]]
	} else {
		edited += body[:ret[1]]
	}
	edited += menus + body[ret[1]:]
	edited = featureFlagsRE.ReplaceAllString(edited, "\n${1}run: () => actions.featureFlags(),\n${1}place: 'outside',\n${2}permission:")
	edited = addImports(edited, importStatement.FindAllString(tn.inRecordWords(appMenusFile, forAuth(reference, auth)), -1))
	if err := os.WriteFile(a.Abs(rel), []byte(edited), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the tenant menus ahead of the others, as the reference's: %s (All %s and New %s…, which opens the copied dialog, in the place outside every %s under Create on the record), People (the %s's side of the auth's people) and %s (Sign-in settings), each showing in its place only; Feature flags… stays outside every %s", rel, tn.many(), tn.manyLower(), tn.oneLower(), tn.oneLower(), tn.oneLower(), tn.one(), tn.oneLower())

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

// addImports adds each import statement the file lacks after its last one.
func addImports(text string, imports []string) string {
	var missing strings.Builder
	for _, statement := range imports {
		if !strings.Contains(text, statement) {
			missing.WriteString(statement)
		}
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
	reference = forAuth(strings.Replace(reference, selfServiceSentence, outsideOnlySentence, 1), auth)
	if err := os.WriteFile(a.Abs(rel), []byte(tn.inRecordWords(appRoutesFile, reference)), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the routes in the tenant shape, as the reference's: the home guard at the root, the refusal page, the place outside every %s (the dashboard, All %s, the %s auth's people across the application and the library's pages) ahead of the %s segment (%s, the generated DomainRoutePath) with its guard (the dashboard, the %s's people, Sign-in settings and the library's pages)", rel, tn.oneLower(), tn.manyLower(), auth, tn.oneLower(), tn.Segment()+"/:"+lowerFirst(tn.Record())+"ID", tn.oneLower())

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
