/**
 * The permission vocabulary lives in @impulseframework/resource, the framework-neutral client
 * for generated APIs; @impulseframework/resource-angular re-exports it so existing imports keep resolving.
 */
export {
  CreatePermission,
  DeletePermission,
  ExecutePermission,
  ListPermission,
  ReadPermission,
  UpdatePermission,
  CapabilitiesProperty,
  CapabilitiesQueryParam,
  rowCapabilities,
} from '@impulseframework/resource';
export type {
  Capability,
  Domain,
  FieldName,
  Method,
  Permission,
  PermissionDigest,
  PermissionDigestState,
  PermissionScope,
  Resource,
  RowCapabilities,
  ScopeKind,
  WithCapabilities,
} from '@impulseframework/resource';
