type Brand<K, T> = K & { __brand: T };

/** A permission name (`Create`, `Read`, `Execute`, ...) as the ccc resource package spells it. */
export type Permission = Brand<string, 'Permission'>;
/** A resource name as registered on the server (plural PascalCase, e.g. `WorkOrders`). */
export type Resource = Brand<string, 'Resource'>;
/** A tenant partition identifier — the value of the domain route segment. */
export type Domain = Brand<string, 'Domain'>;
/** A JSON field name on a resource. */
export type FieldName = Brand<string, 'FieldName'>;
/** An RPC method name as registered on the server (PascalCase, e.g. `ScheduleWorkOrder`). */
export type Method = Brand<string, 'Method'>;

/**
 * One permission question: may the session user exercise `permission` on `resource`
 * (a resource, a dotted `Resource.field` name, or an RPC method)? `domain` names the
 * tenant partition the question is asked in; omit it for the global scope.
 */
export interface PermissionScope {
  resource: Resource | Method;
  permission: Permission;
  domain?: Domain;
}

export const CreatePermission = 'Create' as Permission;
export const DeletePermission = 'Delete' as Permission;
export const ExecutePermission = 'Execute' as Permission;
export const ListPermission = 'List' as Permission;
export const ReadPermission = 'Read' as Permission;
export const UpdatePermission = 'Update' as Permission;

/**
 * The scope a resource or method is permissioned in: `global` resources are asked of
 * the global digest and served without a domain segment; `domain` resources exist only
 * inside a tenant partition.
 */
export type ScopeKind = 'global' | 'domain';
