package generation

import (
	"go/types"
	"slices"
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
//
// A method that names no row declares the rows a page runs it from, @callerEnters(Row, ...):
// each is a resource struct that declares TenantsToEnter, the generated collection lists them
// (CollectionBuilder.SetMethodOfferedOn), and a row of one of them answers for the method as a
// row answers for a method that names it.

// tenantsToEnterMethodName is the method the row's struct declares.
const tenantsToEnterMethodName = "TenantsToEnter"

// resolveCallerEnters reads the struct's @callerEnters against the row the method names,
// which resolveTransition has resolved, and the row's TenantsToEnter: with no argument the
// method names its row, and a method that names none lists the rows that offer it.
func (c *client) resolveCallerEnters(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	if !annotations.Struct.Has(callerEntersKeyword) {
		return nil
	}
	arg := annotations.Struct.Get(callerEntersKeyword)
	if arg.Count() != 0 {
		written := strings.Join(slices.Collect(arg.Seq()), ", ")
		if rpcMethod.Target != nil {
			return errors.Newf("struct %s: @%s(%s) names the rows that offer a method that names no row, and this method names its row, %s; write @%s with no argument", pStruct.Name(), callerEntersKeyword, written, rpcMethod.Target.RootStruct, callerEntersKeyword)
		}

		return c.resolveOfferedOn(rpcMethod, pStruct, arg, written)
	}
	if rpcMethod.Target == nil {
		return errors.Newf("struct %s: @%s says a row offers the method only to a caller who enters the row's tenants, and the method names no row; declare @%s on the field that carries the row's key, or name the resource structs whose rows a page runs the method from, as @%s(Row)", pStruct.Name(), callerEntersKeyword, targetKeyword, callerEntersKeyword)
	}
	rootName := rpcMethod.Target.RootStruct
	root := c.resourceNamed(rootName)
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

// resolveOfferedOn reads @callerEnters(Row, ...) on a method that names no row: each name,
// split on commas as resolveOutlets reads its names, is a resource struct of the method's
// permission scope that declares TenantsToEnter, and the method is offered on their rows in
// the order written.
func (c *client) resolveOfferedOn(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, arg genlang.Arg, written string) error {
	var offeredOn []rpcOfferingRow
	for part := range arg.Seq() {
		for name := range strings.SplitSeq(part, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				return errors.Newf("struct %s: @%s(%s) holds an empty name; name each resource struct whose rows a page runs the method from", pStruct.Name(), callerEntersKeyword, written)
			}
			if slices.ContainsFunc(offeredOn, func(row rpcOfferingRow) bool { return row.RootStruct == name }) {
				return errors.Newf("struct %s: @%s names %s twice", pStruct.Name(), callerEntersKeyword, name)
			}
			root := c.resourceNamed(name)
			if root == nil {
				return errors.Newf("struct %s: @%s(%s): %s is not a resource struct; name the resource structs whose rows a page runs the method from", pStruct.Name(), callerEntersKeyword, written, name)
			}
			if scopeOrGlobal(rpcMethod.PermissionScope) != scopeOrGlobal(root.PermissionScope) {
				return errors.Newf("struct %s: @%s(%s): the method and %s are of different permission scopes; a row offers a method of its own scope", pStruct.Name(), callerEntersKeyword, written, name)
			}
			if err := checkTenantsToEnter(pStruct, root); err != nil {
				return err
			}
			offeredOn = append(offeredOn, rpcOfferingRow{RootStruct: name, RootResource: c.pluralize(name)})
		}
	}
	rpcMethod.OfferedOn = offeredOn

	return nil
}

// resourceNamed is the resource whose struct has the name, nil for none.
func (c *client) resourceNamed(name string) *resourceInfo {
	for _, res := range c.resources {
		if res.Name() == name {
			return res
		}
	}

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
