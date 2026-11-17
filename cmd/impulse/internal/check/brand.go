package check

import (
	"context"
	"fmt"
	"strings"

	"github.com/impulseframework/impulse/cmd/impulse/internal/brand"
)

// brandIcons reminds an application whose browser applications still carry the
// skeleton's icon: the placeholder mark every new application starts with, in the
// favicon, the app icons, or the logo its brand declaration names. It never fails: a
// placeholder is a fine thing to develop under, and the reminder is there so that it is
// not what ships. impulse brand writes the application's own from the logo its
// brand.json names.
type brandIcons struct{}

func (brandIcons) Name() string {
	return "brand"
}

func (brandIcons) Describe() string {
	return "each browser application carries an icon of its own, not the skeleton's (a reminder, never a failure)"
}

func (c brandIcons) Run(_ context.Context, env *Env) Result {
	a := env.App
	apps, err := brand.BrowserApps(a)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	if len(apps) == 0 {
		return skip(c.Name(), "no browser apps")
	}
	dirs, files, declared, err := brand.SeedIcons(a)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	if len(dirs) == 0 {
		return pass(c.Name(), fmt.Sprintf("%d browser application(s) carry an icon of their own", len(apps)))
	}
	details := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		remedy := fmt.Sprintf("declare its brand in %s/%s (a name, a square PNG logo at least %d pixels a side, a primary color) and run impulse brand", dir, brand.DeclarationFile, brand.MinLogoSize)
		if declared[dir] {
			remedy = fmt.Sprintf("put the application's own logo where %s/%s names it (a square PNG at least %d pixels a side) and run impulse brand", dir, brand.DeclarationFile, brand.MinLogoSize)
		}
		details = append(details, fmt.Sprintf("%s: %s: %s", dir, theSkeletons(files[dir]), remedy))
	}

	return warn(c.Name(), fmt.Sprintf("%d of %d browser application(s) still carry the skeleton's icon", len(dirs), len(apps)), details...)
}

// theSkeletons says which files are the skeleton's.
func theSkeletons(files []string) string {
	if len(files) == 1 {
		return files[0] + " is the skeleton's"
	}

	return strings.Join(files, ", ") + " are the skeleton's"
}
