import { ApiDescriptor, ClientBase, Resource } from '@impulseframework/resource';
import { ListViewConfig } from '@impulseframework/resource-angular/types';

// A list page across tenants (ListViewConfigOptions.acrossTenants): the parts that are
// plain functions. The page lists a resource declared for it (`@listAcrossTenants`) in
// every tenant the person reaches; each row carries its tenant, the table names it in a
// first column, and a row's link opens the row itself inside that tenant: the record's
// own row route there, drawn where the person may read the record in that tenant.

/**
 * The display-row key a row's own link rides under on a list across tenants. A generated
 * field name never carries a colon, so the key collides with none.
 */
export const acrossTenantsLinkKey = 'zzTenant:link';

/**
 * Refuses a list configuration asking for a list across tenants the page cannot draw,
 * when the page is built, naming the resource: one over a resource the API does not
 * serve across tenants, and one that also asks to expand a row in place, which reads
 * the row in the page's tenant when the page has none. A configuration that does not ask
 * for a list across tenants passes untouched.
 */
export function refuseAcrossTenantsConfig(
  resource: Resource,
  config: Pick<ListViewConfig, 'acrossTenants' | 'enableRowExpansion'>,
  client: Pick<ClientBase, 'descriptor'>,
): void {
  if (!config.acrossTenants) {
    return;
  }
  if (!client.descriptor.resources[resource]?.acrossTenants) {
    throw new Error(
      `${resource}: acrossTenants is set, but the resource is not listed across tenants: declare @listAcrossTenants on its struct and regenerate, or remove acrossTenants and list it inside one tenant`,
    );
  }
  if (config.enableRowExpansion) {
    throw new Error(
      `${resource}: enableRowExpansion is set on a list across tenants, but a row is read inside its own tenant: remove enableRowExpansion, and the row's View link opens it there`,
    );
  }
}

/** The application's word for one tenant, as the descriptor carries it; "Tenant" where it carries none. */
export function tenantWord(descriptor: ApiDescriptor): string {
  return descriptor.domainRoute?.word?.one ?? 'Tenant';
}

/**
 * The link of one row of a list across tenants: the route of the page whose row route
 * opens the record, with the row's tenant in the place of the tenant parameter
 * (`:param`), followed by the row's key. Undefined, and the row draws no link, when the
 * row carries no key or no tenant, and when the route does not name the tenant
 * parameter: such a page is in no tenant, so it cannot open the row inside its own.
 */
export function tenantLink(route: string, param: string, tenant: unknown, key: unknown): string | undefined {
  if (key === undefined || key === null || key === '') {
    return undefined;
  }
  const marker = `:${param}`;
  const segments = route.split('/').filter((segment) => segment !== '');
  if (!segments.includes(marker) || typeof tenant !== 'string' || tenant === '') {
    return undefined;
  }
  const path = segments.map((segment) => (segment === marker ? encodeURIComponent(String(tenant)) : segment));
  return `/${[...path, encodeURIComponent(String(key))].join('/')}`;
}
