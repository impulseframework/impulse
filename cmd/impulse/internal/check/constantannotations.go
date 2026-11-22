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
// for every package. A line that starts with either annotation in a comment that belongs
// to no declaration (gocomments.Loose: one a blank line parts from the constant below it)
// is read by nothing as well; outside the resources package it is reported here, where
// nothing else reads it, and inside it is generation's to refuse, so it is not said
// twice. A function's, a type's or a variable's own comment is read nowhere: prose may
// start a line with the annotation.
type constantAnnotations struct{}

func (constantAnnotations) Name() string {
	return "constant-annotations"
}

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
	resources := strings.Join(dirs, " or ")

	var details []string
	for _, annotation := range a.ConstantAnnotations {
		if slices.Contains(dirs, path.Dir(annotation.File)) {
			continue
		}
		details = append(details, fmt.Sprintf(
			"%s:%d: constant %s carries @%s, which generation reads only on a constant of the resources package (%s), so here it %s nothing: declare the constant there",
			annotation.File, annotation.Line, annotation.Constant, annotation.Keyword, resources, annotationDoes(annotation.Keyword),
		))
	}
	for _, annotation := range a.LooseAnnotations {
		if slices.Contains(dirs, path.Dir(annotation.File)) {
			continue
		}
		details = append(details, fmt.Sprintf(
			"%s:%d: the comment holds the line %q and is no declaration's, and generation reads @%s only from the comment directly above a constant of the resources package (%s), so here it %s nothing: write it there, above its constant, or, if the line is prose, start it with another word",
			annotation.File, annotation.Line, annotation.Text, annotation.Keyword, resources, annotationDoes(annotation.Keyword),
		))
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d annotation(s) outside the resources package, read by nothing", len(details)), details...)
	}

	return pass(c.Name(), "no @manualAddResource or @manualAddPermission outside the resources package, on a constant or in a comment of no declaration")
}

// annotationDoes is what the annotation does where generation reads it: @manualAddResource
// registers a permission on a resource, @manualAddPermission declares a permission.
func annotationDoes(keyword string) string {
	if keyword == "manualAddPermission" {
		return "declares"
	}

	return "registers"
}
