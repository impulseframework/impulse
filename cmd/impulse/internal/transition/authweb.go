package transition

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// appConfigFile is the application configuration of a browser project, relative to its
// source root. Two files of a browser application are bound to an auth: this one, whose
// HttpClient names the auth's XSRF cookie, and the routes (appRoutesFile), which ask the
// library for the pages of the auth's generated management by the auth's name
// (identityRoutes with auth: '<Auth>').
const appConfigFile = "app/app.config.ts"

// baseSourceRoot is the console project's source root inside the base candidate, where
// the templates of the bound files are read from.
const baseSourceRoot = "web/console/src"

// xsrfCookieRE finds the auth whose XSRF cookie an application configuration names.
var xsrfCookieRE = regexp.MustCompile(`cookieName:\s*'([a-z][a-z0-9]*)-xsrf'`)

// webOutcome is what rebinding one browser file did.
type webOutcome int

const (
	// webAbsent is a file the project does not have.
	webAbsent webOutcome = iota
	// webUntouched is a file naming nothing of the auth it was bound to.
	webUntouched
	// webRendered is a file written again from the skeleton's template for the new auth,
	// since it was the template as rendered for the auth it was bound to.
	webRendered
	// webRewritten is a file edited since the skeleton rendered it, rewritten in place.
	webRewritten
)

// rebindWeb binds the browser application the outlet serves to the new auth, in the
// sites layout: a site's browser application starts as a copy of the console's, bound to
// the console's auth, so its HttpClient echoes that auth's XSRF cookie, and its routes
// may ask the library for the pages of that auth's management, which the API description
// no longer carries once the outlet names another auth (the library refuses the request
// when the application loads, which no build sees). managed says whether the outlet's
// program generates the new auth's management: where it does the routes name the new
// auth, and where it does not the pages are taken out. A file the skeleton rendered and
// nobody edited is rendered again from the template for the new auth; a file edited since
// is rewritten in place. The flat layout is left as it was: an additional outlet's
// project there is the agent's, and the brief says what to name.
func (au Auth) rebindWeb(a *app.App, choice *OutletChoice, src *authSource, managed bool, ch *Change) error {
	if a.Profile().Layout != app.LayoutSites {
		return nil
	}
	for _, t := range outletTargets(choice.Site.Generator, &choice.Outlet) {
		source, ok, err := projectSource(a, t)
		if err != nil {
			return err
		}
		if !ok {
			continue // outlet-wired reports a target outside a browser project
		}
		base := au.webBaseAuth(a, choice, source, src)
		if base == au.Name {
			continue
		}
		if err := au.rebindProject(a, source, base, managed, ch); err != nil {
			return err
		}
	}

	return nil
}

// outletTargets lists the GenerateTypescript targets addressed to the outlet: those
// naming it with ForOutlet, or those naming no outlet for the default.
func outletTargets(g *app.Generator, o *app.Outlet) []app.TSTarget {
	var targets []app.TSTarget
	for _, t := range g.TypescriptTargets() {
		if (o.Name == app.DefaultOutletName && t.Outlet == "") || t.Outlet == o.Name {
			targets = append(targets, t)
		}
	}

	return targets
}

// projectSource finds the source root of the browser project receiving a generated
// client, root-relative, or false when no browser project holds the target.
func projectSource(a *app.App, t app.TSTarget) (source string, found bool, err error) {
	w, ok := a.WebAppFor(t.Dir)
	if !ok {
		return "", false, nil
	}
	projects, err := a.ReadAngular(w.Dir)
	if err != nil {
		return "", false, err
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(t.Dir, w.Dir), "/")
	project, ok := app.ProjectFor(projects, rel)
	if !ok {
		return "", false, nil
	}
	source = project.SourceRoot
	if source == "" {
		source = path.Join(project.Root, "src")
	}

	return path.Join(w.Dir, source), true, nil
}

// webBaseAuth is the auth the project's files are bound to before the change: the auth
// the outlet declared, else the one whose XSRF cookie the application configuration
// names (under a hand-written router the declaration carries none), else the source auth.
func (au Auth) webBaseAuth(a *app.App, choice *OutletChoice, source string, src *authSource) string {
	if choice.Outlet.Auth != nil {
		return authName(a, choice.Outlet.Auth.ImportPath)
	}
	if data, _, err := readFile(a, path.Join(source, appConfigFile)); err == nil {
		if m := xsrfCookieRE.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}

	return src.Name
}

// rebindProject rebinds one project's files from the base auth to the new one: the
// routes, whose requests for the pages of an auth's management name the auth, and the
// application configuration's XSRF lines alone, since the rest of that file is the
// application's own providers.
func (au Auth) rebindProject(a *app.App, source, base string, managed bool, ch *Change) error {
	templates, err := skeleton.FS(skeleton.Base)
	if err != nil {
		return err
	}
	routesRel := path.Join(source, appRoutesFile)
	outcome, err := au.rebindRoutes(a, templates, routesRel, base, managed)
	if err != nil {
		return err
	}
	au.reportRoutes(routesRel, base, outcome, managed, ch)

	configRel := path.Join(source, appConfigFile)
	outcome, err = au.rebindFile(a, templates, configRel, appConfigFile, base, xsrfRewrite)
	if err != nil {
		return err
	}
	au.reportAppConfig(configRel, base, outcome, ch)

	return nil
}

// rebindRoutes rebinds a project's routes. Where the outlet's program generates the new
// auth's management, the requests for the base auth's pages name the new auth. Where it
// generates none, the API description carries no management on this outlet any longer,
// and the requests are taken out (webRewritten), since the library refuses one it cannot
// answer when the application loads.
func (au Auth) rebindRoutes(a *app.App, templates fs.FS, rel, base string, managed bool) (webOutcome, error) {
	if managed {
		return au.rebindFile(a, templates, rel, appRoutesFile, base, routesRewrite)
	}
	if _, err := os.Stat(a.Abs(rel)); errors.Is(err, os.ErrNotExist) {
		return webAbsent, nil
	}
	data, mode, err := readFile(a, rel)
	if err != nil {
		return webAbsent, err
	}
	edited, found := dropIdentityRoutes(string(data))
	if !found {
		return webUntouched, nil
	}
	if err := os.WriteFile(a.Abs(rel), []byte(edited), mode); err != nil {
		return webAbsent, errors.Wrap(err, "os.WriteFile()")
	}

	return webRewritten, nil
}

// routesRewrite renames the auth in a routes file's requests for the pages of an auth's
// management alone: the auth option of identityRoutes and identityPages, and the comment
// naming the auth.
func routesRewrite(text, from, to string) string {
	for _, r := range []rewrite{
		{"auth: '" + names.Pascal(from) + "'", "auth: '" + names.Pascal(to) + "'"},
		{"the " + from + " auth", "the " + to + " auth"},
		{"The " + from + "'s accounts", "The " + to + "'s accounts"},
	} {
		text = strings.ReplaceAll(text, r.old, r.new)
	}

	return text
}

// rebindFile rewrites one file from the base auth to the new one. The file is rendered
// again from the skeleton's template when it is that template as rendered for the base
// auth; otherwise rewrite edits it in place, and a file it leaves as it was names nothing
// of the base auth.
func (au Auth) rebindFile(a *app.App, templates fs.FS, rel, templateRel, base string, rewrite func(text, from, to string) string) (webOutcome, error) {
	if _, err := os.Stat(a.Abs(rel)); errors.Is(err, os.ErrNotExist) {
		return webAbsent, nil
	}
	data, mode, err := readFile(a, rel)
	if err != nil {
		return webAbsent, err
	}
	template, err := fs.ReadFile(templates, path.Join(baseSourceRoot, templateRel))
	if err != nil {
		return webAbsent, errors.Wrapf(err, "fs.ReadFile(): %s", templateRel)
	}
	text := string(data)
	if text == names.Rename(string(template), skeleton.PlaceholderAuth, base) {
		rendered := names.Rename(string(template), skeleton.PlaceholderAuth, au.Name)
		if err := os.WriteFile(a.Abs(rel), []byte(rendered), mode); err != nil {
			return webAbsent, errors.Wrap(err, "os.WriteFile()")
		}

		return webRendered, nil
	}
	edited := rewrite(text, base, au.Name)
	if edited == text {
		return webUntouched, nil
	}
	if err := os.WriteFile(a.Abs(rel), []byte(edited), mode); err != nil {
		return webAbsent, errors.Wrap(err, "os.WriteFile()")
	}

	return webRewritten, nil
}

// xsrfRewrite renames the auth in an application configuration's XSRF lines alone: the
// cookie name HttpClient echoes and the comment naming the auth and its package.
func xsrfRewrite(text, from, to string) string {
	for _, r := range []rewrite{
		{"'" + from + "-xsrf'", "'" + to + "-xsrf'"},
		{"pkg/auth/" + from, "pkg/auth/" + to},
		{"the " + from + " auth", "the " + to + " auth"},
	} {
		text = strings.ReplaceAll(text, r.old, r.new)
	}

	return text
}

// reportRoutes records what rebinding the routes did, or what is left where they ask
// for no page of the base auth's.
func (au Auth) reportRoutes(rel, base string, outcome webOutcome, managed bool, ch *Change) {
	request := fmt.Sprintf("identityRoutes(apiDescriptor, resourceMeta, { auth: '%s', scope: 'global' })", au.Pascal())
	if !managed {
		if outcome == webRewritten {
			ch.didf("%s: without the pages of the %s auth's management (the identityRoutes spreads and what they alone used): the outlet's program generates no management of the %s auth, so its API description names no such page, and the library refuses a request for one when the application loads", rel, base, au.Name)
		}

		return
	}
	switch outcome {
	case webRendered:
		ch.didf("%s: rendered again from the skeleton's for the %s auth: it asks the library for the pages of the %s auth's management (%s) in place of the %s auth's", rel, au.Name, au.Name, request, base)
	case webRewritten:
		ch.didf("%s: its requests for the pages of an auth's management name the %s auth (auth: '%s') in place of the %s auth's; the rest of the file, edited since the skeleton rendered it, is as it was", rel, au.Name, au.Pascal(), base)
	case webUntouched:
		ch.skipf("%s: asks the library for no pages of the %s auth's management, so nothing was rewritten and the %s auth's people have no page; spread %s among the routes behind the login guard, as the base skeleton's routes do: the library builds the accounts, the invitations, the sessions and the failed sign-ins from the API description", rel, base, au.Name, request)
	case webAbsent:
		ch.skipf("%s: no routes file to ask for the pages of the %s auth's management in; spread %s among the project's routes behind the login guard", rel, au.Name, request)
	}
}

// reportAppConfig records what rebinding the application configuration did, or what is
// left when it names no XSRF cookie of the base auth.
func (au Auth) reportAppConfig(rel, base string, outcome webOutcome, ch *Change) {
	switch outcome {
	case webRendered:
		ch.didf("%s: rendered again from the skeleton's: its HttpClient names the %s auth's XSRF cookie (%s-xsrf) in place of the %s auth's", rel, au.Name, au.Name, base)
	case webRewritten:
		ch.didf("%s: its HttpClient names the %s auth's XSRF cookie (%s-xsrf) in place of the %s auth's; the rest of the file, edited since the skeleton rendered it, is as it was", rel, au.Name, au.Name, base)
	case webUntouched:
		ch.skipf("%s: names no XSRF cookie of the %s auth, so nothing was rewritten; name the %s auth's (%s-xsrf) in withXsrfConfiguration, since the browser echoes that cookie in X-XSRF-TOKEN and the server verifies the echo", rel, base, au.Name, au.Name)
	case webAbsent:
		ch.skipf("%s: no application configuration to name the %s auth's XSRF cookie (%s-xsrf) in; name it in the project's withXsrfConfiguration", rel, au.Name, au.Name)
	}
}

// webPlan is the sentence the brief carries about the browser application, for the
// Meaning: what the transition did in the sites layout, what is the agent's in the flat.
func webPlan(outlet string) string {
	return fmt.Sprintf("In the sites layout the site's browser application is rebound with the auth: its HttpClient names the auth's XSRF cookie, and its routes ask the library for the pages of the auth's generated management by the auth's name (identityRoutes), or for none where the outlet's program generates no management of the auth; each file is rendered again from the skeleton's when nobody edited it and rewritten in place otherwise (the did lines say which), and the web-wired check holds the result. In the flat layout the %s outlet's project is yours to rebind the same way.", outlet)
}
