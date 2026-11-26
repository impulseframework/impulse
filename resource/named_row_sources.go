package resource

import (
	"maps"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// NamedRowSources are the views the rows a write names are asked of, by resource: a
// column picked from a view (NamedRow.View) is set only to a row the writer's list of the
// view shows, and the write reads that row from the view's query, which it is handed
// here. The generated handlers hand the application's to every decoder
// (WithNamedRowSources); a caller a decode answers carries them on to a write a body
// arms (PatchSet.Enforce).
type NamedRowSources map[accesstypes.Resource]NamedRowSource

// NamedRowSource is one view a named row is asked of. Its zero value is no source.
type NamedRowSource struct {
	// view is the view whose query the named row is read from.
	view View
}

// ViewRows is the source of a view's rows: a named row is read from the view's query
// (Subquery), as every statement over the view reads it.
func ViewRows(view View) NamedRowSource {
	return NamedRowSource{view: view}
}

// MergeNamedRowSources returns one map holding every entry of its arguments, a later
// argument's entry replacing an earlier one's. It is never nil.
func MergeNamedRowSources(sets ...NamedRowSources) NamedRowSources {
	merged := make(NamedRowSources)
	for _, set := range sets {
		maps.Copy(merged, set)
	}

	return merged
}

// namedViewItem renders the view a column is picked from as the FROM item the row the
// write names is read from: the view's query under its resource's name, as viewFromItem
// writes it for every statement over the view, with the WITH clause that leads the
// statement and the query's own parameters joined to params. A parameter the statement
// already binds is refused, as a gate's read of a view's row refuses it (targetRow). A
// write handed no source of the view is an error for the log: the application did not
// hand the decoder its sources.
func (p *PatchSet[Resource]) namedViewItem(row *NamedRow, params map[string]any) (withClause, item string, err error) {
	source := p.querySet.namedRowSources[row.Resource]
	if source.view == nil {
		return "", "", noNamedRowSource(p.Resource(), row)
	}
	withClause, item, viewParams, err := viewFromItem(row.Resource, source.view)
	if err != nil {
		return "", "", errors.Wrapf(err, "the row a write of %s names", p.Resource())
	}
	for name, value := range viewParams {
		if _, ok := params[name]; ok {
			return "", "", errors.Newf("named parameter collision: %s query and the read of the row a write of %s names both contain named parameter %q", row.Resource, p.Resource(), name)
		}
		params[name] = value
	}
	if withClause != "" {
		withClause += "\n"
	}

	return withClause, item, nil
}

// noNamedRowSource says a write names a row of a view it was handed no source of.
func noNamedRowSource(res accesstypes.Resource, row *NamedRow) error {
	return errors.Newf("resource %s field %s names a row of the view %s, and the write was handed no source of that name: hand the application's named-row sources to the decoder (WithNamedRowSources), which the generated handlers do; regenerate them", res, row.Field, row.Resource)
}
