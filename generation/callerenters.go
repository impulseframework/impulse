package generation

import (
	"go/types"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// A method a row offers only to a caller whose session enters every tenant the row names
// declares @callerEnters on its @rpc struct. The method names its row (@target or
// @transition), and the row's struct says which tenants those are in a method of its
// own,
//
//	func (Row) TenantsToEnter() resource.TenantsToEnter
//
// The generated collection marks the method (CollectionBuilder.SetMethodCallerEnters),
// and a row read with capabilities=Execute leaves it out for a caller whose session does
// not enter one of the row's tenants. The declaration shapes what a page offers and
// refuses nothing: the method's own code refuses the call.

// tenantsToEnterMethodName is the method the row's struct declares.
const tenantsToEnterMethodName = "TenantsToEnter"

// resolveCallerEnters reads the struct's @callerEnters against the row the method names,
// which resolveTransition has resolved, and the row's TenantsToEnter.
func (c *client) resolveCallerEnters(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	if !annotations.Struct.Has(callerEntersKeyword) {
		return nil
	}
	if annotations.Struct.Get(callerEntersKeyword).Count() != 0 {
		return errors.Newf("struct %s: @%s takes no argument; the row's struct says which tenants the caller must enter", pStruct.Name(), callerEntersKeyword)
	}
	if rpcMethod.Target == nil {
		return errors.Newf("struct %s: @%s says a row offers the method only to a caller who enters the row's tenants, and the method names no row; declare @%s on the field that carries the row's key, or remove @%s", pStruct.Name(), callerEntersKeyword, targetKeyword, callerEntersKeyword)
	}
	rootName := rpcMethod.Target.RootStruct
	var root *resourceInfo
	for _, res := range c.resources {
		if res.Name() == rootName {
			root = res

			break
		}
	}
	if root == nil {
		// Unreachable through the normal pipeline: resolveTarget found the root among the
		// same resources.
		return errors.Newf("struct %s: @%s: %s, the row the method names, is not a resource struct", pStruct.Name(), callerEntersKeyword, rootName)
	}
	if err := checkTenantsToEnter(pStruct, root); err != nil {
		return err
	}
	rpcMethod.CallerEnters = true

	return nil
}

// checkTenantsToEnter holds the row's struct to the one form of TenantsToEnter the
// statement reads off a value of the struct: a value receiver, no parameters, and a
// resource.TenantsToEnter.
func checkTenantsToEnter(pStruct *parser.Struct, root *resourceInfo) error {
	rootName := root.Name()
	undeclared := errors.Newf("struct %s: @%s: %s, the row the method names, does not say which tenants a caller must enter; write beside it: func (%s) %s() resource.TenantsToEnter", pStruct.Name(), callerEntersKeyword, rootName, rootName, tenantsToEnterMethodName)
	named, ok := types.Unalias(root.GoType()).(*types.Named)
	if !ok {
		return undeclared
	}
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(named), true, named.Obj().Pkg(), tenantsToEnterMethodName)
	fn, ok := obj.(*types.Func)
	if !ok {
		return undeclared
	}
	if tenantsToEnterForm(fn.Signature()) {
		return nil
	}

	return errors.Newf("struct %s: @%s: %s of %s is declared %s; write it as: func (%s) %s() resource.TenantsToEnter", pStruct.Name(), callerEntersKeyword, tenantsToEnterMethodName, rootName, declaredForm(named, fn), rootName, tenantsToEnterMethodName)
}

// tenantsToEnterForm reports whether the method is TenantsToEnter's one form.
func tenantsToEnterForm(sig *types.Signature) bool {
	if recv := sig.Recv(); recv != nil {
		if _, pointer := types.Unalias(recv.Type()).(*types.Pointer); pointer {
			return false
		}
	}
	if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return false
	}

	return isNamedType(types.Unalias(sig.Results().At(0).Type()), resourcePackagePath, "TenantsToEnter")
}

// declaredForm renders the method as it is declared, its receiver and the types of the
// row's own package unqualified: func (*Row) TenantsToEnter() string.
func declaredForm(named *types.Named, fn *types.Func) string {
	sig := fn.Signature()
	own := named.Obj().Pkg()
	qualifier := func(p *types.Package) string {
		if p == own {
			return ""
		}

		return p.Name()
	}
	recv := named.Obj().Name()
	if sig.Recv() != nil {
		recv = types.TypeString(sig.Recv().Type(), qualifier)
	}
	plain := types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic())

	return "func (" + recv + ") " + fn.Name() + strings.TrimPrefix(types.TypeString(plain, qualifier), "func")
}
