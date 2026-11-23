package generation

import (
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// A method a row keeps off the person whose account the row is declares
// @notOnOwnAccount(Field) on its @rpc struct. The method names its row (@target or
// @transition), and Field is the field of the row's struct that holds the account's id,
// an impulse.UUID. The generated collection marks the method with the field's column
// (CollectionBuilder.SetMethodNotOnOwnAccount), and a row read with capabilities=Execute
// leaves it out where the column holds the caller's own account. The declaration shapes
// what a page offers and refuses nothing: the method's own code refuses the call.

// resolveNotOnOwnAccount reads the struct's @notOnOwnAccount against the row the method
// names, which resolveTransition has resolved: the one name is a field of the row's struct
// that is an impulse.UUID, and the method records the field's column.
func (c *client) resolveNotOnOwnAccount(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	if !annotations.Struct.Has(notOnOwnAccountKeyword) {
		return nil
	}
	if rpcMethod.Target == nil {
		return errors.Newf("struct %s: @%s says the row the method names does not offer it to the person whose account the row is, and the method names no row; declare @%s on the field that carries the row's key", pStruct.Name(), notOnOwnAccountKeyword, targetKeyword)
	}
	rootName := rpcMethod.Target.RootStruct
	arg := annotations.Struct.Get(notOnOwnAccountKeyword)
	written := strings.Join(slices.Collect(arg.Seq()), ", ")
	var names []string
	for part := range arg.Seq() {
		for name := range strings.SplitSeq(part, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	}
	if len(names) != 1 || names[0] == "" {
		return errors.Newf("struct %s: @%s(%s) names one field: the field of %s that holds the account's id, as @%s(ID)", pStruct.Name(), notOnOwnAccountKeyword, written, rootName, notOnOwnAccountKeyword)
	}
	name := names[0]
	root := c.resourceNamed(rootName)
	if root == nil {
		// Unreachable through the normal pipeline: resolveTarget found the root among the
		// same resources.
		return errors.Newf("struct %s: @%s: %s, the row the method names, is not a resource struct", pStruct.Name(), notOnOwnAccountKeyword, rootName)
	}
	i := slices.IndexFunc(root.Fields, func(f *resourceField) bool { return f.Name() == name })
	if i < 0 {
		return errors.Newf("struct %s: @%s(%s): %s has no field %s; name the field of the row that holds the account's id", pStruct.Name(), notOnOwnAccountKeyword, written, rootName, name)
	}
	field := root.Fields[i]
	if field.Type() != impulseUUIDGoType {
		return errors.Newf("struct %s: @%s(%s): %s.%s is %s; the field that holds an account's id is an %s", pStruct.Name(), notOnOwnAccountKeyword, written, rootName, name, field.Type(), impulseUUIDGoType)
	}
	rpcMethod.NotOnOwnAccount = fieldColumn(field)

	return nil
}
