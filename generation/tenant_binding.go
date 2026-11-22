package generation

import "fmt"

// TenantBindingLiteral renders the resource's way to its tenant as the
// resource.TenantBinding its generated TenantBinding method returns, by which the
// library writes the tenant on every change record of a row: the field the resource's
// @domain leaves through, with the path from it to the tenant's key, or, for the tenant
// record, its own key. A view, which nothing writes, and a resource that belongs to no
// tenant have none, and the literal is empty.
func (r *resourceInfo) TenantBindingLiteral() string {
	switch {
	case r.IsVirtual:
		return ""
	case r.DomainBinding != nil:
		return fmt.Sprintf("resource.TenantBinding{Field: %q%s}", r.DomainBinding.Anchor.Name(), bindingHopsLiteral(collectionHops(r.DomainBinding.Path)))
	case r.IsTenant:
		return fmt.Sprintf("resource.TenantBinding{Field: %q}", r.PrimaryKey().Name())
	default:
		return ""
	}
}
