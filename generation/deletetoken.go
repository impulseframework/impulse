package generation

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// A method that deletes a record with rows under it on a person's word declares the field
// that carries the token the delete's question gave: @deleteToken on a field named Token,
// of type string, of an @rpc struct whose Execute runs in the transaction form. Its
// generated handler asks the delete's question before it opens the transaction while the
// field is empty (resource.SpannerClient.ExecuteDeleteFunc), and stamps the field into the
// context (resource.WithDeleteToken), where the method's delete reads it
// (resource.DeleteTokenFrom) and hands it to its patch (resource.PatchSet.DeleteWithToken).

// deleteTokenFieldName is the name of the field that carries a delete's token: the delete
// dialog sends the token back as "token". deleteTokenFieldType is its type: the token is
// text.
const (
	deleteTokenFieldName = "Token"
	deleteTokenFieldType = "string"
)

// resolveDeleteToken reads and validates the struct's @deleteToken field.
func resolveDeleteToken(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	var declared []int
	for i := range pStruct.Fields() {
		if annotations.Fields[i].Has(deleteTokenKeyword) {
			declared = append(declared, i)
		}
	}
	switch len(declared) {
	case 0:
		return nil
	case 1:
	default:
		return errors.Newf("struct %s: @%s appears on fields %s and %s; a method deletes one record, so one field carries its token", pStruct.Name(), deleteTokenKeyword, pStruct.Fields()[declared[0]].Name(), pStruct.Fields()[declared[1]].Name())
	}
	field := pStruct.Fields()[declared[0]]
	switch {
	case field.Type() != deleteTokenFieldType:
		return errors.Newf("struct %s field %s: @%s is on a %s; the token a delete's question gives is text, so the field is a string", pStruct.Name(), field.Name(), deleteTokenKeyword, field.Type())
	case field.Name() != deleteTokenFieldName:
		return errors.Newf("struct %s field %s: the field that carries a delete's token is named %s; the delete dialog sends it back as \"token\"", pStruct.Name(), field.Name(), deleteTokenFieldName)
	case !rpcMethod.IsTxnForm():
		return errors.Newf("struct %s: @%s requires the transaction form of Execute (second parameter resource.ReadWriteTransaction); the handler asks the delete's question before it opens that transaction", pStruct.Name(), deleteTokenKeyword)
	case rpcMethod.takesFiles:
		return errors.Newf("struct %s: @%s on an upload; a delete's question is asked with the request alone, so declare it on a method that takes no files", pStruct.Name(), deleteTokenKeyword)
	case annotations.Struct.Has(scheduleKeyword):
		return errors.Newf("struct %s: @%s with @%s: a token is given to a person's session, and a scheduled call has none; drop one of them", pStruct.Name(), deleteTokenKeyword, scheduleKeyword)
	}
	rpcMethod.DeleteToken = field.Name()

	return nil
}
