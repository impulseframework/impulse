import { ApiDescriptor, ClientBase, Resource } from '@impulseframework/resource';
import { ListViewConfig } from '@impulseframework/resource-angular/types';

// A list page across tenants (ListViewConfigOptions.acrossTenants): the parts that are
// plain functions. The page lists a resource declared for it (`@listAcrossTenants`) in
// every tenant the person reaches; each row carries its tenant, the table names it in a
// first column, and a row's link opens the row inside that tenant's routes.

/**
 * The display-row key a row's own link rides under on a list across tenants. A generated
 * field name never carries a colon, so the key collides with none.
 */
export const acrossTenantsLinkKey = 'zzTenant:link';

/**
 * Refuses a list configuration asking for a list across tenants the page cannot draw,
 * when the page is built, naming the resource: one over a resource the API does not
 * serve across tenants, and one that also asks to expand a row in place, which reads
 * the row in the page's tenant when the page has none. A page that asks for live is
 * told, once, that the list is plain. A configuration that does not ask for a list
 * across tenants passes untouched.
 */
export function refuseAcrossTenantsConfig(
  resource: Resource,
  config: Pick<ListViewConfig, 'acrossTenants' | 'enableRowExpansion' | 'live'>,
  client: Pick<ClientBase, 'descriptor' | 'warn'>,
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
  if (config.live === true) {
    client.warn(
      `${resource} is listed across tenants, and a list across tenants is plain: the page asked for live and is read without a subscription. Remove live from the page's configuration`,
    );
  }
}

/** The application's word for one tenant, as the descriptor carries it; "Tenant" where it carries none. */
export function tenantWord(descriptor: ApiDescriptor): string {
  return descriptor.domainRoute?.word?.one ?? 'Tenant';
}

/**
 * The link of one row of a list across tenants: the registered route of the page the
 * row opens on, with the row's tenant in the place of the tenant parameter (`:param`),
 * followed by the row's key. A route that names no tenant parameter is the same for
 * every row. Undefined, and the row draws no link, when the row carries no key, or the
 * route names the parameter and the row carries no tenant.
 */
export function tenantLink(route: string, param: string, tenant: unknown, key: unknown): string | undefined {
  if (key === undefined || key === null || key === '') {
    return undefined;
  }
  const marker = `:${param}`;
  const segments = route.split('/').filter((segment) => segment !== '');
  if (segments.includes(marker) && (typeof tenant !== 'string' || tenant === '')) {
    return undefined;
  }
  const path = segments.map((segment) => (segment === marker ? encodeURIComponent(String(tenant)) : segment));
  return `/${[...path, encodeURIComponent(String(key))].join('/')}`;
}
