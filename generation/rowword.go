package generation

import (
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// The struct-scope @rowWord: the word a person calls one row of a resource by. A page
// that lists the resource speaks of a row in three places, the button that adds one
// ("Add a hangar pass"), the question before one is deleted ("Delete this hangar
// pass?") and the message after ("Hangar pass deleted."), and it takes the word from
// the generated metadata (rowWords). A resource that states nothing goes by its name
// split into words: HangarPass is "hangar pass", and several are "hangar passes", the
// plural the generator names the collection by. The tenant record goes by the label it
// declares for one tenant (@tenant(label: ...)), and a view that declares its table
// (@rowsOf) by the table's words. A resource whose name is not what
// people call its rows states the word, and the word for several where the plural
// rule misses it: @rowWord(pass), @rowWord(person, plural: people). The word is
// written alone, in the case it has inside a sentence: a screen puts "a", "an" or
// "this" before it and capitalizes it where it opens a sentence.

// rowWordPluralArgKey names the word for several rows, where the plural rule misses it.
const rowWordPluralArgKey = "plural"

// rowWordArgSpec is @rowWord's argument shape: the word for one row, then plural:.
func rowWordArgSpec() *genlang.ArgSpec {
	return &genlang.ArgSpec{Positional: 1, Keys: []string{rowWordPluralArgKey}}
}

// rowWordsDecl records the words a resource's rows go by; every kind of resource
// carries one.
type rowWordsDecl struct {
	// RowWord is the word for one row as it reads inside a sentence ("hangar pass").
	RowWord string
	// RowWords is the word for several rows ("hangar passes").
	RowWords string
	// rowWordStated is whether the resource states its word (@rowWord); a view that
	// states none takes its table's (takeTableWords).
	rowWordStated bool
}

// takeTableWords gives a view that states no word the words of the table it declares
// (@rowsOf): a list of the view shows the table's rows, so a row goes by one word on
// both, and the view's own name ("mission board") is seldom what a row is called.
func (d *rowWordsDecl) takeTableWords(table *rowWordsDecl) {
	if d.rowWordStated {
		return
	}
	d.RowWord, d.RowWords = table.RowWord, table.RowWords
}

// TypescriptRowWords renders the words as the TypeScript metadata carries them:
// { one: 'hangar pass', many: 'hangar passes' }.
func (d *rowWordsDecl) TypescriptRowWords() string {
	return "{ one: " + typescriptString(d.RowWord) + ", many: " + typescriptString(d.RowWords) + " }"
}

// typescriptString renders text as a single-quoted TypeScript string literal.
func typescriptString(text string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(text) + "'"
}

// declareView records the struct-scope declarations of a view, a virtual or a computed
// resource: its @rowsOf and @changedBy (declareViewDeclarations) and the words its rows
// go by (declareRowWords). A view has no tenant label to default to.
func (c *client) declareView(pStruct *parser.Struct, annotations genlang.StructAnnotations, rowsOf *rowsOfDecl, publish *publishDecl, words *rowWordsDecl) error {
	declareViewDeclarations(annotations, rowsOf, publish)

	return c.declareRowWords(pStruct, annotations, "", words)
}

// declareRowWords records the words a resource's rows go by: the ones its @rowWord
// states, else the label a tenant record declares for one tenant, else its name split
// into words, as the tenant's own word is spelled (sentenceWord).
func (c *client) declareRowWords(pStruct *parser.Struct, annotations genlang.StructAnnotations, tenantLabel string, dest *rowWordsDecl) error {
	if !annotations.Struct.Has(rowWordKeyword) {
		tenantLabels := ""
		if tenantLabel != "" {
			tenantLabels = c.pluralize(tenantLabel)
		}
		dest.RowWord = sentenceWord(pStruct.Name(), tenantLabel)
		dest.RowWords = sentenceWord(c.pluralize(pStruct.Name()), tenantLabels)

		return nil
	}

	arg := annotations.Struct.Get(rowWordKeyword)
	written := strings.TrimSpace(string(arg))
	invocations, err := arg.ParseInvocations(rowWordArgSpec())
	if err != nil {
		return errors.Wrapf(err, "struct %s: @%s(%s): write the word for one row, and the word for several where the plural rule misses it, as in @%s(person, %s: people)", pStruct.Name(), rowWordKeyword, written, rowWordKeyword, rowWordPluralArgKey)
	}
	invocation := invocations[0]

	word := strings.Join(strings.Fields(invocation.Positional[0]), " ")
	if article := leadingArticle(word); article != "" {
		return errors.Newf("struct %s: @%s(%s): the word opens with %q; write the word alone, @%s(%s): a screen puts \"a\", \"an\" or \"this\" before it itself", pStruct.Name(), rowWordKeyword, written, article, rowWordKeyword, strings.TrimSpace(strings.TrimPrefix(word, article)))
	}
	plural := c.pluralize(word)
	if stated, ok := invocation.Named(rowWordPluralArgKey); ok {
		plural = strings.Join(strings.Fields(stated), " ")
		if article := leadingArticle(plural); article != "" {
			return errors.Newf("struct %s: @%s(%s): the plural opens with %q; write the word alone, %s: %s", pStruct.Name(), rowWordKeyword, written, article, rowWordPluralArgKey, strings.TrimSpace(strings.TrimPrefix(plural, article)))
		}
	}
	dest.RowWord, dest.RowWords, dest.rowWordStated = word, plural, true

	return nil
}

// rejectRowWord fails a struct of a kind that lists no rows: an RPC method.
func rejectRowWord(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string) error {
	if !annotations.Struct.Has(rowWordKeyword) {
		return nil
	}

	return errors.Newf("struct %s: @%s is only valid on a resource (@%s, @%s or @%s); this %s lists no rows to name", pStruct.Name(), rowWordKeyword, resourceKeyword, virtualKeyword, computedKeyword, kind)
}

// leadingArticle returns the article a phrase of several words opens with, as
// written, or nothing.
func leadingArticle(phrase string) string {
	first, _, more := strings.Cut(phrase, " ")
	if !more {
		return ""
	}
	switch strings.ToLower(first) {
	case "a", "an", "the", "this":
		return first
	default:
		return ""
	}
}
