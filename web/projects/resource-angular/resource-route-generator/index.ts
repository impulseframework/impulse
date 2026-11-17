import { Route } from '@angular/router';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { featureMatch } from '@impulseframework/resource-angular/auth-feature';
import { Page, page, pageRoute } from '@impulseframework/resource-angular/auth-frame';
import { canDeactivateGuard } from '@impulseframework/resource-angular/guards';
import { registerResourcePage, registerRowPage } from '@impulseframework/resource-angular/resource-nav';
import {
  keyFields,
  ListAcrossTenantsPermission,
  ListPermission,
  PermissionScope,
  ReadPermission,
  Resource,
  ResourceMeta,
  RootConfig,
  RouteResourceData,
  writeResource,
} from '@impulseframework/resource-angular/types';

/** The page each config makes, so the route and the menu file hold the same one. */
const pages = new WeakMap<RootConfig, Page>();

/** Whether the config's page lists its resource across tenants (ListViewConfig.acrossTenants). */
function listsAcrossTenants(config: RootConfig): boolean {
  return config.parentConfig.type === 'ListView' && config.parentConfig.acrossTenants === true;
}

/**
 * The route of a page that lists its resource across tenants: the one its config names.
 * The resource's own route carries the tenant's segment, which is where one tenant's
 * list lives, so a config that names no route is refused, naming the resource.
 */
function acrossTenantsRoute(config: RootConfig, resource: Resource): string {
  if (!config.routeData.route) {
    throw new Error(
      `the config of ${resource} lists it across tenants and names no route: set routeData.route to where the page lives, outside the tenant's segment (the resource's own route is one tenant's list)`,
    );
  }
  return config.routeData.route;
}

/**
 * What a page that lists its resource across tenants is shown by, and its route opened
 * by: the global digest's answer that the person may list the resource in at least one
 * tenant the session reaches. The server works it out, so the application names no
 * permission of its own to stand in for it.
 */
function acrossTenantsScope(resource: Resource): PermissionScope {
  return { resource, permission: ListAcrossTenantsPermission };
}

/**
 * The page a config-driven resource's routes carry, for the application's menu file to
 * place: the config's label and icon at the page's route, needing List on the resource
 * (or the config's permission) and behind the resource's feature flag (or the config's).
 * A page that lists its resource across tenants needs the list to have something to show
 * the person instead (acrossTenantsScope).
 * The same config answers the same page, made once, so the place in the menu file and the
 * page on the route are one, as `unplacedPages` checks. Refused, naming what is missing: a
 * config with no page, and a resource the metadata does not carry.
 */
export function resourcePage(config: RootConfig, resourceMeta: (resource: Resource) => ResourceMeta): Page {
  const made = pages.get(config);
  if (made) {
    return made;
  }
  const resource = config.parentConfig.primaryResource as Resource;
  if (!config.page) {
    throw new Error(`resourcePage: the config of ${resource} names no page (page: { label, icon }), so no menu can place it`);
  }
  const meta = resourceMeta(resource) as ResourceMeta | undefined;
  if (!meta) {
    throw new Error(`resourcePage: the generated metadata carries no resource ${resource}`);
  }
  const across = listsAcrossTenants(config);
  const declared = page({
    label: config.page.label,
    icon: config.page.icon,
    route: [across ? acrossTenantsRoute(config, resource) : config.routeData.route || meta.route],
    // A list across tenants has no one tenant whose digest could answer List for it: the
    // global digest says whether the person may list the resource in any tenant of theirs.
    permission: config.page.permission ?? (across ? acrossTenantsScope(resource) : { resource, permission: ListPermission }),
    feature: config.page.feature ?? meta.feature,
  });
  pages.set(config, declared);
  return declared;
}

/**
 * Builds the routes for a config-driven resource, carrying its page (resourcePage) when
 * the config names one, for the application's menu file to place.
 *
 * The list route is guarded by the resource's List permission, answered from the
 * permission digest by the AuthorizationGuard, and the page needs the same scope, so the
 * menus leave out what the person cannot open. The scope names no
 * domain: a global resource is asked in the global digest, and a domain-scoped one in
 * the selected tenant's (RESOURCE_DOMAIN), which AuthService settles. The row route
 * (`:uuid`) is guarded on Read alone, since a role may list a resource without reading
 * its rows and may read a row of a table whose view it does not list — Read on the
 * resource whose row the page opens: the listed resource, or its table when the list is
 * a view declaring one (the metadata's `rowsOf`), since a view lists and never reads. The
 * list's guard sits on the list, not over the row: a link to a row a person may read
 * opens it, whatever they may list. The page is registered as where the resource's rows
 * open by a route of their own (rowPageRoute) when it carries the row route. A config's
 * page may name another permission for the menus. A domain-scoped resource's metadata route carries the tenant parameter in
 * braces, so such a page sets `routeData.route` to the path it should live at, which may
 * carry the tenant parameter as the router names it (`sectors/:sectorID/missions`) when
 * the page lives under the tenant segment. The page is registered as where the
 * resource's rows open (resourcePageRoute), for the listed resource and for its table,
 * so another list's row route lands here; the route is recorded as written, and every
 * link built from it (the list's View column, the after-create navigation) puts the
 * page's tenant in the parameter's place (pageRouteResolver). A listed resource
 * with no key field (a `@computed` or `@virtual` struct with no `@primarykey`) is served
 * whole and has no row: its page gets no row route and is registered as nowhere a row
 * opens, as `hasViewRoute: false` says for a keyed one. A resource behind a feature flag
 * (the metadata's `feature`) gets a match guard from it: while the flag is off the router
 * passes over the page and a dark URL falls to the application's wildcard, and the page
 * sits behind the flag too, so the menus leave it out while the flag is off.
 *
 * A page that lists its resource across tenants (`acrossTenants` on the list config) is
 * mounted at `routeData.route`, which it must name: the resource's own route is one
 * tenant's list. It gets no row route and is not registered as where the resource's rows
 * open, since a row opens inside its tenant. No one tenant's digest speaks for it: its
 * route is guarded by, and its page needs, the global digest's answer that the person
 * may list the resource in at least one tenant the session reaches
 * (ListAcrossTenantsPermission), so the menus show the page to those people and nobody
 * else, and the route turns anyone else away. A `page.permission` on the config is what
 * the menus ask instead; the route's guard stays. The feature flag gates it as it gates
 * any page.
 */
export const resourceRoutes = (config: RootConfig, resourceMeta: (resource: Resource) => ResourceMeta): Route => {
  const resource = config.parentConfig.primaryResource as Resource;
  const meta = resourceMeta(resource);
  if (!meta) {
    return {} as Route;
  }

  const scope: PermissionScope = { resource, permission: ListPermission };
  // The page is matched only while its flag is on; an ungated page is matched as any other.
  // The route carries the page the menu file places, when the config names one.
  const gated = (route: Route): Route => {
    const matched = meta.feature ? { ...route, canMatch: [featureMatch(meta.feature)] } : route;
    return config.page ? pageRoute(resourcePage(config, resourceMeta), matched) : matched;
  };

  if (listsAcrossTenants(config)) {
    // The page reads and opens no row of its own: a row's link leads into its tenant's
    // routes. The server answers who may list what, tenant by tenant, and tells the
    // global digest whether that is anything at all for this person, which the route asks.
    return gated({
      path: acrossTenantsRoute(config, resource),
      data: { config, scope: acrossTenantsScope(resource) } satisfies RouteResourceData,
      canActivate: [AuthorizationGuard],
      children: [
        {
          path: '',
          loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.ResourceListCreateComponent),
          canDeactivate: [canDeactivateGuard],
        },
      ],
    });
  }

  const path = config.routeData.route || meta.route;
  const rowResource = writeResource(resource, meta);
  const keyless = keyFields(meta).length === 0;
  if (!keyless) {
    registerResourcePage(resource, path);
    registerResourcePage(rowResource, path);
  }

  const data = { config, scope } satisfies RouteResourceData;
  const viewData = { config, scope: { resource: rowResource, permission: ReadPermission } } satisfies RouteResourceData;

  // The list asks List and the row asks Read, each of its own route: the list's guard
  // does not stand over the row, so a row a person may read opens from wherever a link
  // to it was drawn.
  const listRoute: Route = {
    path: '',
    data,
    canActivate: [AuthorizationGuard],
    loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.ResourceListCreateComponent),
    canDeactivate: [canDeactivateGuard],
  };
  const rowRoute: Route = {
    path: ':uuid',
    data: viewData,
    canActivate: [AuthorizationGuard],
    loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.CompoundResourceComponent),
    canDeactivate: [canDeactivateGuard],
  };
  // A configured route stands with or without its row route: the config may switch the
  // row route off, which the meta route never does.
  const opensRows = !keyless && (!config.routeData.route || config.routeData.hasViewRoute !== false);
  if (opensRows) {
    registerRowPage(resource, path);
    registerRowPage(rowResource, path);
  }

  // Each path matches one child alone, so their order decides nothing; a configured route
  // names its list first and the metadata's route its row first, as each always has.
  const withRows = config.routeData.route ? [listRoute, rowRoute] : [rowRoute, listRoute];

  return gated({
    path,
    data,
    children: opensRows ? withRows : [listRoute],
  } satisfies Route);
};
