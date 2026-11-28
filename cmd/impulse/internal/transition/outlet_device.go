package transition

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// The device page of the console whose auth issues a machine outlet's tokens. A
// command-line client that asks to act as a person sends the person to the page the auth
// names (its DevicePage), so a console without the page left every such client at an
// address that showed nothing to approve. The command copies the page from the reference
// and declares its route where the console's routes open their signed-in pages as the
// skeletons write them; a console whose routes read otherwise is left as it is, and the
// brief says what to mount.

// deviceDir is where the reference keeps the device page, relative to a browser project's
// source root.
const deviceDir = "app/components/ui/device/"

// deviceFiles are the device page's files in the reference, the component first.
var deviceFiles = []string{"device.component.ts", "device.component.html", "device.component.scss"}

var (
	// frameChildrenRE matches where a browser project's routes open their signed-in
	// pages, as the skeletons write them: the children of the frame behind the sign-in
	// guard. The device page is one of them, so a signed-out person returns to it, code
	// and all, after signing in. It captures the indentation the children are under.
	frameChildrenRE = regexp.MustCompile(`(?m)^( *)canActivate: \[LoginAuthenticationGuard\],\n *children: \[\n`)
	// deviceRouteRE matches a route to the device page.
	deviceRouteRE = regexp.MustCompile(`path:\s*'device'`)
)

// deviceRouteLines is the device page's route as the reference declares it, by line: (1)
// the page's address and (2) the auth that names it.
var deviceRouteLines = []string{
	"// The device page, where a command-line client sends the person for approval",
	"// (%[1]s?userCode=..., the %[2]s auth's DevicePage). It is no menu's and",
	"// no place's: the link lands here whichever place the person works in.",
	"{",
	"  path: 'device',",
	"  loadComponent: () => import('./components/ui/device/device.component').then((comp) => comp.DeviceComponent),",
	"},",
}

// pageOutlet is the outlet whose browser application has the pages an auth names: the
// first that binds to the auth and serves one, which is the console where the console
// binds to it. It is nil where no outlet that binds to the auth serves one.
func pageOutlet(site *app.Site, importPath string) *app.Outlet {
	all := site.AllOutlets()
	for i := range all {
		if all[i].Auth != nil && all[i].Auth.ImportPath == importPath && all[i].WebApp != "" {
			return &all[i]
		}
	}

	return nil
}

// devicePage is the page where a signed-in person approves a command-line client: device
// under the mount path of the browser application bound to the auth. It is empty where no
// outlet that binds to the auth serves one.
func devicePage(site *app.Site, importPath string) string {
	outlet := pageOutlet(site, importPath)
	if outlet == nil {
		return ""
	}

	return path.Join(outlet.WebApp, "device")
}

// consoleSource is the source root of the default outlet's browser project, the one the
// generated client is written into. It is empty where the application has none the scan
// reads.
func consoleSource(a *app.App, g *app.Generator) (string, error) {
	target := defaultTarget(g)
	if target == nil {
		return "", nil
	}
	w, found := a.WebAppFor(target.Dir)
	if !found {
		return "", nil
	}
	projects, err := a.ReadAngular(w.Dir)
	if err != nil {
		return "", err
	}
	project, found := app.ProjectFor(projects, strings.TrimPrefix(strings.TrimPrefix(target.Dir, w.Dir), "/"))
	if !found || project.SourceRoot == "" {
		return "", nil
	}

	return path.Join(w.Dir, project.SourceRoot), nil
}

// mountDevicePage gives the console the page its auth names as the device page: the
// component, copied from the reference with the library's device approval component in
// it, and its route among the console's signed-in pages. A console that routes to a
// device page already is left as it is. Where the page is another browser application's,
// or the console's routes do not open their signed-in pages as the skeletons write them,
// nothing is written and the brief says what to mount.
func (o Outlet) mountDevicePage(a *app.App, site *app.Site, pkg *app.AuthPackage, ch *Change) error {
	outlet := pageOutlet(site, pkg.Path)
	if outlet == nil {
		// No browser application binds to the auth; the device page's setting says so.
		return nil
	}
	page := path.Join(outlet.WebApp, "device")
	left := fmt.Sprintf("mount the library's device approval page (DeviceApprovalComponent of @impulseframework/resource-angular/tokens) at %s, among the signed-in pages of the browser application that binds to the %s auth, as the reference's console does (%s%s and the device route of its %s)", page, pkg.Name, deviceDir, deviceFiles[0], appRoutesFile)
	if outlet.Name != site.Default.Name {
		ch.skipf("the device page is the %s outlet's browser application's, and the command mounts it in the default outlet's alone, so it is not mounted: %s", outlet.Name, left)

		return nil
	}
	source, err := consoleSource(a, site.Generator)
	if err != nil {
		return err
	}
	routesRel := path.Join(source, appRoutesFile)
	if source == "" || !exists(a, routesRel) {
		ch.skipf("the default outlet's browser project has no routes the command reads (%s under its source root), so the device page is not mounted: %s", appRoutesFile, left)

		return nil
	}
	routes, mode, err := readFile(a, routesRel)
	if err != nil {
		return err
	}
	if deviceRouteRE.Match(routes) {
		return nil
	}
	loc := frameChildrenRE.FindSubmatchIndex(routes)
	if loc == nil {
		ch.skipf("%s: the routes do not open their signed-in pages as the skeletons write them (the children of the frame behind LoginAuthenticationGuard), so the device page is not mounted: %s", routesRel, left)

		return nil
	}
	copied, err := o.copyDevicePage(a, source, pkg.Name)
	if err != nil {
		return err
	}
	indent := string(routes[loc[2]:loc[3]]) + "  "
	var route strings.Builder
	for _, line := range deviceRouteLines {
		route.WriteString(indent + line + "\n")
	}
	out := slices.Concat(routes[:loc[1]], fmt.Appendf(nil, route.String(), page, pkg.Name), routes[loc[1]:])
	if err := os.WriteFile(a.Abs(routesRel), out, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the console mounts the device page at %s, among its signed-in pages, where a command-line client sends the person who approves it%s", routesRel, page, copied)

	return nil
}

// copyDevicePage writes the device page's component into the project from the reference,
// for the auth, and says so for the change. A project that has one of the page's files
// already keeps what it has, and nothing is said.
func (o Outlet) copyDevicePage(a *app.App, source, auth string) (string, error) {
	if slices.ContainsFunc(deviceFiles, func(name string) bool {
		return exists(a, path.Join(source, deviceDir, name))
	}) {
		return "", nil
	}
	for _, name := range deviceFiles {
		text, err := referenceText(ReferenceCandidate, deviceDir+name)
		if err != nil {
			return "", err
		}
		if err := writeNew(a, path.Join(source, deviceDir, name), forAuth(text, auth)); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("; the page is %s, from the reference: the library's device approval component, which reads the code the client printed, shows which program asks and for which outlet, and posts the person's answer", path.Join(source, deviceDir)+"/"), nil
}
