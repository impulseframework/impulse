// Package gocomments holds the one definition of a comment that belongs to no
// declaration: a loose comment, one that is neither the doc nor the line comment of the
// file, a declaration, a spec or a field, and that lies in no function's body. A comment
// a blank line parts from the declaration below it is one, inside a const ( ... ) group or
// outside it, and so is a comment no declaration follows. An annotation is read from the
// comment of what it stands above, so one written in a loose comment is read by nothing.
// Generation, which refuses the constant annotations in a loose comment of the resources
// package, and the application scan behind impulse check, which reports them in one
// anywhere else, both ask here, so the two agree on which comment is loose.
package gocomments

import (
	"go/ast"
	"slices"
)

// Loose returns, in source order, the comment groups of a file parsed with its comments
// that belong to no declaration.
func Loose(file *ast.File) []*ast.CommentGroup {
	var loose []*ast.CommentGroup
	owned, bodies := ownedComments(file)
	for _, group := range file.Comments {
		if owned[group] || slices.ContainsFunc(bodies, func(body *ast.BlockStmt) bool {
			return body.Pos() <= group.Pos() && group.End() <= body.End()
		}) {
			continue
		}
		loose = append(loose, group)
	}

	return loose
}

// ownedComments returns the comments a node of the file owns, the doc and line comments
// of the file, its declarations, specs and fields, and the bodies of its functions,
// whose comments are the code's.
func ownedComments(file *ast.File) (owned map[*ast.CommentGroup]bool, bodies []*ast.BlockStmt) {
	owned = make(map[*ast.CommentGroup]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.File:
			owned[n.Doc] = true
		case *ast.GenDecl:
			owned[n.Doc] = true
		case *ast.FuncDecl:
			owned[n.Doc] = true
			if n.Body != nil {
				bodies = append(bodies, n.Body)
			}
		case *ast.FuncLit:
			bodies = append(bodies, n.Body)
		case *ast.ImportSpec:
			owned[n.Doc], owned[n.Comment] = true, true
		case *ast.ValueSpec:
			owned[n.Doc], owned[n.Comment] = true, true
		case *ast.TypeSpec:
			owned[n.Doc], owned[n.Comment] = true, true
		case *ast.Field:
			owned[n.Doc], owned[n.Comment] = true, true
		}

		return true
	})

	return owned, bodies
}
