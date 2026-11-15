import { Route } from '@angular/router';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { featureMatch } from '@impulseframework/resource-angular/auth-feature';
import { Page, page, pageRoute } from '@impulseframework/resource-angular/auth-frame';
import { canDeactivateGuard } from '@impulseframework/resource-angular/guards';
import { registerResourcePage } from '@impulseframework/resource-angular/resource-nav';
import {
  keyFields,
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

/**
 * The page a config-driven resource's routes carry, for the application's menu file to
 * place: the config's label and icon at the page's route, needing List on the resource
 * (or the config's permission) and behind the resource's feature flag (or the config's).
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
  const declared = page({
    label: config.page.label,
    icon: config.page.icon,
    route: [config.routeData.route || meta.route],
    permission: config.page.permission ?? { resource, permission: ListPermission },
    feature: config.page.feature ?? meta.feature,
  });
  pages.set(config, declared);
  return declared;
}

/**
 * Builds the routes for a config-driven resource, carrying its page (resourcePage) when
 * the config names one, for the application's menu file to place.
 *
 * The routes are guarded by the resource's List permission, answered from the permission
 * digest by the AuthorizationGuard, and the page needs the same scope, so the menus leave
 * out what the person cannot open. The scope names no
 * domain: a global resource is asked in the global digest, and a domain-scoped one in
 * the selected tenant's (RESOURCE_DOMAIN), which AuthService settles. The row route
 * (`:uuid`) is guarded on Read as well, since a role may list a resource without reading
 * its rows — Read on the resource whose row the page opens: the listed resource, or its
 * table when the list is a view declaring one (the metadata's `rowsOf`), since a view
 * lists and never reads. A config's page may name another permission for the menus. A domain-scoped resource's metadata route carries the tenant parameter in
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

  const path = config.routeData.route || meta.route;
  const rowResource = writeResource(resource, meta);
  const keyless = keyFields(meta).length === 0;
  if (!keyless) {
    registerResourcePage(resource, path);
    registerResourcePage(rowResource, path);
  }

  const data = { config, scope } satisfies RouteResourceData;
  const viewData = { config, scope: { resource: rowResource, permission: ReadPermission } } satisfies RouteResourceData;

  if (config.routeData.route) {
    const baseRoute: Route = {
      path: config.routeData.route,
      data,
      canActivate: [AuthorizationGuard],
      children: [
        {
          path: '',
          loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.ResourceListCreateComponent),
          canDeactivate: [canDeactivateGuard],
        },
      ],
    };
    if (config.routeData.hasViewRoute !== false && !keyless) {
      baseRoute.children?.push({
        path: ':uuid',
        data: viewData,
        canActivate: [AuthorizationGuard],
        loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.CompoundResourceComponent),
        canDeactivate: [canDeactivateGuard],
      });
    }
    // A configured route stands with or without its row route; falling through would
    // discard it for the meta route and add the row route the config switched off.
    return gated(baseRoute);
  }

  const rowRoute: Route = {
    path: ':uuid',
    data: viewData,
    canActivate: [AuthorizationGuard],
    loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.CompoundResourceComponent),
    canDeactivate: [canDeactivateGuard],
  };
  const listRoute: Route = {
    path: '',
    loadComponent: () => import('@impulseframework/resource-angular/resource').then((mod) => mod.ResourceListCreateComponent),
    canDeactivate: [canDeactivateGuard],
  };

  return gated({
    path: meta.route,
    data,
    canActivate: [AuthorizationGuard],
    children: keyless ? [listRoute] : [rowRoute, listRoute],
  } satisfies Route);
};
