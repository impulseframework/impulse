package check

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
)

// constantAnnotations verifies that every @manualAddResource and @manualAddPermission
// stands on a constant of the resources package, the directory a generator program names
// first. Generation reads the two off that package's constants alone: it loads the
// resources package and the few packages its options name, never the handlers'
// directory, so an annotation on a constant declared anywhere else (beside the RPC
// method it is for, or beside a hand-written route) registers or declares nothing, and
// the route it was written for is denied to every role without a word. The application
// scan reads every Go file but tests and generated files, so the rule is held here, once,
// for every package. A function's comment is not read: prose may start a line with the
// annotation.
type constantAnnotations struct{}

func (constantAnnotations) Name() string { return "constant-annotations" }

func (constantAnnotations) Describe() string {
	return "every @manualAddResource and @manualAddPermission stands on a constant of the resources package, where generation reads it"
}

func (c constantAnnotations) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.Generators) == 0 {
		return skip(c.Name(), "no generator program")
	}
	var dirs []string
	for _, g := range a.Generators {
		if g.ResourcePackageDir != "" && !slices.Contains(dirs, g.ResourcePackageDir) {
			dirs = append(dirs, g.ResourcePackageDir)
		}
	}
	if len(dirs) == 0 {
		return skip(c.Name(), "no generator program names its resources package")
	}
	slices.Sort(dirs)

	var details []string
	for _, annotation := range a.ConstantAnnotations {
		if slices.Contains(dirs, path.Dir(annotation.File)) {
			continue
		}
		does := "registers"
		if annotation.Keyword == "manualAddPermission" {
			does = "declares"
		}
		details = append(details, fmt.Sprintf(
			"%s:%d: constant %s carries @%s, which generation reads only on a constant of the resources package (%s), so here it %s nothing: declare the constant there",
			annotation.File, annotation.Line, annotation.Constant, annotation.Keyword, strings.Join(dirs, " or "), does,
		))
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d annotation(s) on a constant outside the resources package", len(details)), details...)
	}

	return pass(c.Name(), "no @manualAddResource or @manualAddPermission on a constant outside the resources package")
}
