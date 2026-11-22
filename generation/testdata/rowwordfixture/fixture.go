// Package rowwordfixture holds the structs the struct-scope @rowWord tests and the test
// of a delete's words read: resources that take their words from their names, resources
// that state them, the ways a statement is refused, and the kind that has no rows to
// name.
package rowwordfixture

import "github.com/impulseframework/impulse"

type (
	// HangarPass states no word: its rows go by its name, split into words.
	//
	// @resource
	HangarPass struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// PilotID states no word, and its name ends in an acronym: several keep the
	// acronym's letters.
	//
	// @resource
	PilotID struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// ShipClass states no word, and its plural takes "es".
	//
	// @resource
	ShipClass struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// DockingPermit states the word for one row, and takes the plural from it.
	//
	// @resource
	// @rowWord(permit)
	DockingPermit struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// CrewMember states both words, since the plural is none a rule would find.
	//
	// @resource
	// @rowWord(person, plural: people)
	CrewMember struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// ShipLog states a word of several, with an apostrophe in it.
	//
	// @resource
	// @rowWord(ship's log)
	ShipLog struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// WithArticle states the word with its article, which the screens add themselves.
	//
	// @resource
	// @rowWord(a permit)
	WithArticle struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// PluralWithArticle states the plural with an article.
	//
	// @resource
	// @rowWord(permit, plural: the permits)
	PluralWithArticle struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// TwoWords states two words where one is taken.
	//
	// @resource
	// @rowWord(permit, pass)
	TwoWords struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// MethodWithRowWord is a method stating a word, which only a resource may.
	//
	// @rpc
	// @rowWord(permit)
	MethodWithRowWord struct {
		Input string
	}
)
