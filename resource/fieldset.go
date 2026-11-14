package resource

import (
	"slices"

	"github.com/impulseframework/impulse/accesstypes"
)

type fieldSet struct {
	data   map[accesstypes.Field]any
	fields []accesstypes.Field
}

func newFieldSet() *fieldSet {
	return &fieldSet{
		data: make(map[accesstypes.Field]any),
	}
}

func (p *fieldSet) Set(field accesstypes.Field, value any) {
	if _, found := p.data[field]; !found {
		p.fields = append(p.fields, field)
	}
	p.data[field] = value
}

// unset removes a field, so the set reads as if the field had never been set.
func (p *fieldSet) unset(field accesstypes.Field) {
	if _, found := p.data[field]; !found {
		return
	}
	delete(p.data, field)
	p.fields = slices.DeleteFunc(p.fields, func(f accesstypes.Field) bool {
		return f == field
	})
}

func (p *fieldSet) Get(field accesstypes.Field) any {
	return p.data[field]
}

func (p *fieldSet) KeySet() KeySet {
	var keys KeySet
	for _, field := range p.fields {
		keys = keys.Add(field, p.data[field])
	}

	return keys
}

func (p *fieldSet) IsSet(field accesstypes.Field) bool {
	_, found := p.data[field]

	return found
}
