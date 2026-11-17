package generation

import (
	"go/types"
	"regexp"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// A method whose effect a person should read before they cause it declares a warning:
// @warning(typed: open) on the @rpc struct, and beside its Execute a Warning method
// that writes the warning for one call,
//
//	Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error)
//
// The declaration and the method go together; generation refuses one without the
// other, naming the struct. The generated frame answers the warning to a request that
// asks for it (resource.WarningHeader) in place of running the call, inside the
// handler's transaction, which it rolls back, so the method runs in the transaction
// form; the metadata carries the typed word, which the dialog a page runs the method
// through holds its confirm button for.

// warningTypedArgKey is the named argument of @warning: the word a person types before
// the confirm button works.
const warningTypedArgKey = "typed"

// warningMethodName is the method a @warning struct declares beside Execute.
const warningMethodName = "Warning"

// warningForm is the shape every refusal quotes, so the message says what to write.
const warningForm = "Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error)"

// typedWord is what a warning's typed word may be: one lowercase word, so the dialog
// can say it in a sentence and a person can type it without a shift key.
var typedWord = regexp.MustCompile(`^[a-z]{2,24}$`)

// rpcWarning is a method's @warning declaration.
type rpcWarning struct {
	// Typed is the word a person types before the confirm button works.
	Typed string
}

// resolveWarning reads and validates the struct's @warning against its Warning method.
func resolveWarning(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	declares := pStruct.Method(warningMethodName) != nil
	if !annotations.Struct.Has(warningKeyword) {
		if declares {
			return errors.Newf("struct %s: the method declares %s but no @%s; declare the word a person types before the call is confirmed, for example @%s(%s: open)", pStruct.Name(), warningMethodName, warningKeyword, warningKeyword, warningTypedArgKey)
		}

		return nil
	}

	invocations, err := annotations.Struct.Get(warningKeyword).ParseInvocations(&genlang.ArgSpec{
		Keys:     []string{warningTypedArgKey},
		Required: []string{warningTypedArgKey},
	})
	if err != nil {
		return errors.Wrapf(err, "@%s on %s", warningKeyword, pStruct.Name())
	}
	typedText, _ := invocations[0].Named(warningTypedArgKey)
	typed := strings.TrimSpace(typedText)
	if !typedWord.MatchString(typed) {
		return errors.Newf("struct %s: @%s(%s: %s): the typed word is one lowercase word of 2 to 24 letters, such as open or delete", pStruct.Name(), warningKeyword, warningTypedArgKey, typedText)
	}

	switch {
	case rpcMethod.SuppressHandler:
		return errors.Newf("struct %s: @%s is answered by the generated handler, which @%s suppresses; remove one of them", pStruct.Name(), warningKeyword, suppressKeyword)
	case !rpcMethod.IsTxnForm():
		return errors.Newf("struct %s: @%s requires the transaction form of Execute (second parameter resource.ReadWriteTransaction); the warning is read inside the handler's transaction, which is rolled back", pStruct.Name(), warningKeyword)
	case rpcMethod.takesFiles:
		return errors.Newf("struct %s: @%s on an upload; a warning is asked with the request alone, before any file is chosen, so declare it on a method that takes no files", pStruct.Name(), warningKeyword)
	case !declares:
		return errors.Newf("struct %s: @%s is declared but the method has no %s; write the warning for the call beside Execute: %s", pStruct.Name(), warningKeyword, warningMethodName, warningForm)
	}
	if err := checkWarningSignature(pStruct); err != nil {
		return err
	}
	rpcMethod.Warning = &rpcWarning{Typed: typed}

	return nil
}

// checkWarningSignature holds the struct's Warning method to its one form: the
// transaction form's parameters, the client Execute takes, and a *resource.Warning
// with an error.
func checkWarningSignature(pStruct *parser.Struct) error {
	refuse := func(got string) error {
		return errors.Newf("struct %s: %s is %s; a @%s method declares %s", pStruct.Name(), warningMethodName, got, warningKeyword, warningForm)
	}
	sig, ok := pStruct.Method(warningMethodName).Type().(*types.Signature)
	if !ok {
		return refuse("not a method")
	}
	params, results := sig.Params(), sig.Results()
	if params.Len() != 3 || sig.Variadic() {
		return refuse("declared taking " + paramsString(sig))
	}
	if !isNamedType(params.At(0).Type(), "context", "Context") || !isNamedType(params.At(1).Type(), resourcePackagePath, "ReadWriteTransaction") {
		return refuse("declared taking " + paramsString(sig))
	}
	if !types.Identical(params.At(2).Type(), executeClientType(pStruct)) {
		return refuse("declared taking " + paramsString(sig) + ", whose client is not the one Execute takes")
	}
	errType := types.Universe.Lookup("error").Type()
	if results.Len() != 2 || !types.Identical(results.At(1).Type(), errType) {
		return refuse("declared returning " + typeTupleString(results))
	}
	ptr, ok := types.Unalias(results.At(0).Type()).(*types.Pointer)
	if !ok || !isNamedType(types.Unalias(ptr.Elem()), resourcePackagePath, "Warning") {
		return refuse("declared returning " + typeTupleString(results))
	}

	return nil
}

// executeClientType is the type of the client parameter of the struct's Execute in the
// transaction form, its third: the type a Warning takes too. The struct's Execute was
// classified before its warning is read, so the method and the parameter exist.
func executeClientType(pStruct *parser.Struct) types.Type {
	sig, ok := pStruct.Method("Execute").Type().(*types.Signature)
	if !ok || sig.Params().Len() < 3 {
		return types.Typ[types.Invalid]
	}

	return sig.Params().At(2).Type()
}
