package transition

import (
	"regexp"
	"strings"
)

// appRoutesFile is a browser project's routes, relative to its source root.
const appRoutesFile = "app/app.routes.ts"

// appMenusFile is a browser project's menu file, relative to its source root: the menus
// the library's frame draws, which place the pages the routes carry.
const appMenusFile = "app/app.menus.ts"

// The library's calls a browser application takes the pages of an auth's generated
// management through: identityRoutes among the routes, identityMenuPages among a menu
// group's entries in the menu file.
const (
	identityRoutesCall    = "identityRoutes"
	identityMenuPagesCall = "identityMenuPages"
)

// pagesFile is a file of a browser project that asks for the pages of an auth's
// management, by its path under the project's source root, and the call it spreads.
type pagesFile struct {
	file, call string
}

// pagesFiles are the routes and the menu file.
var pagesFiles = []pagesFile{{appRoutesFile, identityRoutesCall}, {appMenusFile, identityMenuPagesCall}}

// tsWordRE finds the identifiers of a run of TypeScript.
var tsWordRE = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)

// tsImportRE finds a named import, on one line or over several.
var tsImportRE = regexp.MustCompile(`(?m)^import\s*\{([^}]*)\}\s*from\s*'[^']*';\n`)

// dropIdentityRoutes returns a routes file or a menu file without the pages of an auth's
// generated management: every spread of identityRoutes among the routes, or of
// identityMenuPages among a menu group's entries, with the comment over it, then what the
// spreads alone used, the names of an import (the import when they were its only names)
// and a constant declared at the top of the file. A browser application whose site's
// program generates no management is described no such pages, and names nothing of them.
// A menu group left with no entries stays: the frame draws no empty group or menu.
func dropIdentityRoutes(text string) (edited string, found bool) {
	var removed []string
	for _, f := range pagesFiles {
		call := "..." + f.call + "("
		for {
			at := strings.Index(text, call)
			if at < 0 {
				break
			}
			start, end, ok := tsStatement(text, at, at+len(call)-1)
			if !ok {
				break
			}
			removed = append(removed, text[start:end])
			text = text[:start] + text[end:]
			found = true
		}
	}
	if !found {
		return text, false
	}
	// What the removed text named, until nothing removed names anything more.
	for len(removed) > 0 {
		words := tsWordRE.FindAllString(removed[0], -1)
		removed = removed[1:]
		for _, word := range words {
			var more string
			text, more = dropUnusedName(text, word)
			if more != "" {
				removed = append(removed, more)
			}
		}
	}
	// A removed import or constant leaves no blank line doubled, and none at the top.
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	return strings.TrimLeft(text, "\n"), true
}

// tsStatement returns the lines of the element that starts at the line of at and whose
// first parenthesis opens at open: through the matching parenthesis, the calls chained on
// it, and the end of that line, with the comment lines directly over it.
func tsStatement(text string, at, open int) (start, end int, ok bool) {
	start = strings.LastIndexByte(text[:at], '\n') + 1
	if strings.TrimSpace(text[start:at]) != "" {
		return 0, 0, false
	}
	end, ok = tsClose(text, open)
	if !ok {
		return 0, 0, false
	}
	// The calls chained on the result: .map(underTenant).
	for end < len(text) && text[end] == '.' {
		next := strings.IndexByte(text[end:], '(')
		if next < 0 || strings.ContainsAny(text[end:end+next], "\n,;") {
			break
		}
		closed, ok := tsClose(text, end+next)
		if !ok {
			break
		}
		end = closed
	}
	if newline := strings.IndexByte(text[end:], '\n'); newline >= 0 {
		end += newline + 1
	} else {
		end = len(text)
	}

	return tsCommentAbove(text, start), end, true
}

// tsClose returns the offset after the bracket matching the one at open, reading past
// the brackets nested in it and the string literals.
func tsClose(text string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case '\'', '"', '`':
			closed := strings.IndexByte(text[i+1:], text[i])
			if closed < 0 {
				return 0, false
			}
			i += closed + 1
		}
	}

	return 0, false
}

// tsCommentAbove returns where the run of comment lines directly over the line starting
// at start begins, or start when there is none.
func tsCommentAbove(text string, start int) int {
	for start > 0 {
		prev := strings.LastIndexByte(text[:start-1], '\n') + 1
		if !strings.HasPrefix(strings.TrimSpace(text[prev:start]), "//") {
			break
		}
		start = prev
	}

	return start
}

// dropUnusedName removes the declaration of a name nothing in the file uses any longer:
// its place in a named import, or the constant it names at the top of the file. Returns
// the file and the text of a removed constant, which may have been the only use of
// other names.
func dropUnusedName(text, name string) (edited, removed string) {
	word := regexp.MustCompile(`(^|[^A-Za-z0-9_$.])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_$])`)
	uses := word.FindAllStringSubmatchIndex(text, -1)
	if len(uses) != 1 {
		return text, ""
	}
	at := uses[0][3] // where the name starts, after what stands before it
	// A name of an import.
	for _, m := range tsImportRE.FindAllStringSubmatchIndex(text, -1) {
		if at < m[2] || at >= m[3] {
			continue
		}
		var kept []string
		for _, part := range strings.Split(text[m[2]:m[3]], ",") {
			part = strings.TrimSpace(part)
			local := part
			if _, alias, ok := strings.Cut(part, " as "); ok {
				local = strings.TrimSpace(alias)
			}
			if part != "" && local != name {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			return text[:m[0]] + text[m[1]:], ""
		}

		return text[:m[2]] + " " + strings.Join(kept, ", ") + " " + text[m[3]:], ""
	}
	// A constant declared at the top of the file.
	start := strings.LastIndexByte(text[:at], '\n') + 1
	if !strings.HasPrefix(text[start:], "const "+name+" ") {
		return text, ""
	}
	end := tsStatementEnd(text, start)
	if end < 0 {
		return text, ""
	}
	start = tsCommentAbove(text, start)
	// The blank line that set the declaration apart goes with it.
	if strings.HasPrefix(text[end:], "\n") && (start == 0 || strings.HasSuffix(text[:start], "\n\n")) {
		end++
	}

	return text[:start] + text[end:], text[start:end]
}

// tsStatementEnd returns the offset after the line that ends the statement starting at
// start: the first semicolon outside every bracket and string. -1 when there is none.
func tsStatementEnd(text string, start int) int {
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '\'', '"', '`':
			closed := strings.IndexByte(text[i+1:], text[i])
			if closed < 0 {
				return -1
			}
			i += closed + 1
		case ';':
			if depth == 0 {
				if newline := strings.IndexByte(text[i:], '\n'); newline >= 0 {
					return i + newline + 1
				}

				return len(text)
			}
		}
	}

	return -1
}
