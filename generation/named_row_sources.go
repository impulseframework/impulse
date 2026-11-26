package generation

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
)

// The names a home's named-row sources are reached by: the application's method on its
// own type, which merges each auth's, and the exported method of an auth's Handlers.
const (
	namedRowSourcesAppMethod  = "namedRowSources"
	namedRowSourcesAuthMethod = "NamedRowSources"
)

// namedRowSourceData is one entry of a home's named-row sources: the resource a column is
// picked from, and its source as the home's handlers package names it. A view is its type
// (View); a computed list is its row type (Type), its list function (List) and the Go
// field of its one key (Key).
type namedRowSourceData struct {
	Resource string
	View     string
	Type     string
	List     string
	Key      string
}

// namedRowSourcesData renders one home's named-row sources file.
type namedRowSourcesData struct {
	Source  string
	Package string
	// ApplicationName and ReceiverName are the home's type and receiver: the
	// application's, or an auth's Handlers.
	ApplicationName string
	ReceiverName    string
	// Application says the file is the application's: its method merges each auth's.
	Application bool
	// Computed says an entry is a computed list's, whose function is named with a
	// context and an iterator.
	Computed bool
	Entries  []namedRowSourceData
	// Auths are the handlers packages of the auths' generated management, in the auths'
	// name order, whose sources the application's method merges.
	Auths []string
}

// namedRowSourcesOf is a home's named-row sources: an entry for each view and each
// computed list of the home that a column the run declares (NamedRows) is picked from,
// in resource order; and, for the application, the auths whose sources it merges.
func (r *resourceGenerator) namedRowSourcesOf(home *handlerHome) *namedRowSourcesData {
	data := &namedRowSourcesData{
		Source: home.resources.Dir(), Package: home.dir.Package(),
		ApplicationName: home.applicationName, ReceiverName: home.receiverName, Application: home.instance == nil,
	}
	if home.instance != nil {
		data.Source = fmt.Sprintf("Auth(%q)", home.instance.importPath)
	}
	seen := make(map[string]bool)
	for _, res := range r.resources {
		for _, row := range res.namedRows {
			if seen[row.Resource] {
				continue
			}
			switch {
			case row.view != nil && row.view.identity == home.instance:
				data.Entries = append(data.Entries, namedRowSourceData{
					Resource: row.Resource,
					View:     packageQualifier(home.dir, r.resourcesDirOf(row.view)) + row.view.Name(),
				})
			case row.list != nil && row.list.identity == home.instance:
				qualifier := packageQualifier(home.dir, home.computed)
				data.Entries = append(data.Entries, namedRowSourceData{
					Resource: row.Resource,
					Type:     qualifier + row.list.Name(),
					List:     qualifier + "List" + row.list.Name(),
					Key:      row.list.PrimaryKey().Name(),
				})
				data.Computed = true
			default:
				continue
			}
			seen[row.Resource] = true
		}
	}
	slices.SortFunc(data.Entries, func(a, b namedRowSourceData) int {
		return strings.Compare(a.Resource, b.Resource)
	})
	if home.instance == nil {
		instances := slices.Clone(r.identity)
		slices.SortFunc(instances, func(a, b *identityInstance) int {
			return strings.Compare(a.auth, b.auth)
		})
		for _, inst := range instances {
			data.Auths = append(data.Auths, inst.handlers.Package())
		}
	}

	return data
}

// namedRowSourcesCall names the named-row sources a home's decoders are handed: the
// application's method, which merges each auth's, or an auth's own.
func namedRowSourcesCall(home *handlerHome) string {
	if home.instance == nil {
		return home.receiverName + "." + namedRowSourcesAppMethod + "()"
	}

	return home.receiverName + "." + namedRowSourcesAuthMethod + "()"
}

// generateNamedRowSources writes each home's named-row sources, one file in the home's
// handlers package, always: the views and the computed lists a column picked from one
// names a row of, which the home's decoders hand the library (WithNamedRowSources). A
// home with none answers an empty map.
func (r *resourceGenerator) generateNamedRowSources() error {
	begin := time.Now()
	fileName := generatedGoFileName(namedRowSourcesOutputName)
	for _, home := range r.homes() {
		destinationFilePath := filepath.Join(home.dir.Dir(), fileName)
		if err := r.writeFormattedGoFile(destinationFilePath, "namedRowSourcesTemplate", namedRowSourcesTemplate, r.namedRowSourcesOf(home)); err != nil {
			return errors.Wrap(err, "writeFormattedGoFile()")
		}
		log.Printf("Generated named-row sources file in %s: %s", time.Since(begin), destinationFilePath)
	}

	return nil
}
