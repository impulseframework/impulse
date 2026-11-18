import { TestBed } from '@angular/core/testing';
import { CanMatchFn, PartialMatchRouteSnapshot, Route } from '@angular/router';
import { createClient } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { pageDataKey } from '@impulseframework/resource-angular/auth-frame';
import { resourcePageRoute, rowPageRoute } from '@impulseframework/resource-angular/resource-nav';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ConfigPage,
  FieldMeta,
  ListAcrossTenantsPermission,
  ListPermission,
  listViewConfig,
  ReadPermission,
  Resource,
  ResourceMeta,
  rootConfig,
  RootRouteData,
  RouteResourceData,
} from '@impulseframework/resource-angular/types';
import { firstValueFrom, isObservable } from 'rxjs';

import { resourcePage, resourceRoutes } from './index';

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;
/** A resource keyed by `id`, as the generated metadata says it. */
const keyed = (route: string, extra: Partial<ResourceMeta> = {}): ResourceMeta =>
  ({ route, fields: [field('id', { primaryKey: { ordinalPosition: 0 } })], ...extra }) as ResourceMeta;

describe('resourceRoutes', () => {
  const clients = 'Clients' as Resource;
  const meta = (): ResourceMeta => keyed('clients');
  // routeData is spread over the helper's defaults, so an absent key (not an undefined
  // value) is what leaves the default in place.
  const config = (routeData?: RootRouteData) =>
    rootConfig({
      ...(routeData ? { routeData } : {}),
      page: { label: 'Clients', icon: 'groups' },
      parentConfig: listViewConfig({ primaryResource: clients, listColumns: [], elements: [] }),
    });
  const config_ = config;
  const scopeOf = (route: Route | undefined) => (route?.data as RouteResourceData | undefined)?.scope;
  const rowRoute = (route: Route) => route.children?.find((child) => child.path === ':uuid');
  const listRoute = (route: Route) => route.children?.find((child) => child.path === '');

  const cases: { name: string; routeData?: RootRouteData; wantPath: string; wantRowRoute: boolean }[] = [
    { name: 'under the meta route', routeData: undefined, wantPath: 'clients', wantRowRoute: true },
    {
      name: 'under a configured route',
      routeData: { route: 'crew/clients' },
      wantPath: 'crew/clients',
      wantRowRoute: true,
    },
    {
      name: 'with the row route switched off',
      routeData: { route: 'crew/clients', hasViewRoute: false },
      wantPath: 'crew/clients',
      wantRowRoute: false,
    },
  ];

  for (const tt of cases) {
    describe(tt.name, () => {
      const route = resourceRoutes(config(tt.routeData), meta);

      it('guards the list route on List', () => {
        expect(route.path).toBe(tt.wantPath);
        expect(listRoute(route)?.canActivate).toContain(AuthorizationGuard);
        expect(scopeOf(listRoute(route))).toEqual({ resource: clients, permission: ListPermission });
        expect(scopeOf(route)).toEqual({ resource: clients, permission: ListPermission });
      });

      if (tt.wantRowRoute) {
        it('guards the row route on Read alone: the guard of the list does not stand over the row', () => {
          const row = rowRoute(route);
          expect(row).toBeDefined();
          expect(row?.canActivate).toEqual([AuthorizationGuard]);
          expect(scopeOf(row)).toEqual({ resource: clients, permission: ReadPermission });
          // A guard on the page's own route would be asked before the row's.
          expect(route.canActivate).toBeUndefined();
        });
      } else {
        it('has no row route', () => {
          expect(rowRoute(route)).toBeUndefined();
        });
      }
    });
  }

  describe('for the menus', () => {
    it('carries the page the menu file places: its label and icon at its route, needing List', () => {
      const config = config_({ route: 'sectors/:sectorID/clients' });
      const route = resourceRoutes(config, meta);
      const page = resourcePage(config, meta);
      expect(route.data?.[pageDataKey]).toBe(page);
      expect(page).toEqual({
        label: 'Clients',
        icon: 'groups',
        route: ['sectors/:sectorID/clients'],
        permission: { resource: clients, permission: ListPermission },
        feature: undefined,
      });
    });

    it('answers the same page for the same config, made before or after the routes', () => {
      const config = config_();
      const before = resourcePage(config, meta);
      expect(resourceRoutes(config, meta).data?.[pageDataKey]).toBe(before);
      expect(resourcePage(config_(), meta)).not.toBe(before);
    });

    it("keeps the config's own permission for the menus", () => {
      const permission = { resource: 'Pilots' as Resource, permission: ListPermission };
      const config = rootConfig({
        page: { label: 'Clients', permission },
        parentConfig: listViewConfig({ primaryResource: clients, listColumns: [], elements: [] }),
      });
      expect(resourcePage(config, meta).permission).toBe(permission);
      // The route is still guarded on List of its own resource.
      expect(scopeOf(resourceRoutes(config, meta))).toEqual({ resource: clients, permission: ListPermission });
    });

    it('carries no page for a config that names none, and refuses to make one', () => {
      const config = rootConfig({
        parentConfig: listViewConfig({ primaryResource: clients, listColumns: [], elements: [] }),
      });
      expect(resourceRoutes(config, meta).data?.[pageDataKey]).toBeUndefined();
      expect(() => resourcePage(config, meta)).toThrow(
        'resourcePage: the config of Clients names no page (page: { label, icon }), so no menu can place it',
      );
    });
  });

  describe('over a view declaring its table', () => {
    const boards = 'CrewBoards' as Resource;
    const crew = 'Crew' as Resource;
    const metas: Record<string, ResourceMeta> = {
      [boards]: keyed('sectors/{sectorID}/crew-boards', { rowsOf: crew }),
      [crew]: keyed('sectors/{sectorID}/crew'),
    };
    const route = resourceRoutes(
      rootConfig({
        routeData: { route: 'sector/crew' },
        page: { label: 'Crew' },
        parentConfig: listViewConfig({ primaryResource: boards, listColumns: [], elements: [] }),
      }),
      (resource) => metas[resource],
    );

    it('guards the list route on List of the view', () => {
      expect(scopeOf(listRoute(route))).toEqual({ resource: boards, permission: ListPermission });
      expect(listRoute(route)?.canActivate).toEqual([AuthorizationGuard]);
    });

    it('guards the row route on Read of the table, which the row page opens, and not on List of the view', () => {
      expect(scopeOf(rowRoute(route))).toEqual({ resource: crew, permission: ReadPermission });
      expect(rowRoute(route)?.canActivate).toEqual([AuthorizationGuard]);
      expect(route.canActivate).toBeUndefined();
    });

    it('registers the page as where both the view and the table open', () => {
      expect(resourcePageRoute(boards)).toBe('sector/crew');
      expect(resourcePageRoute(crew)).toBe('sector/crew');
    });

    it("registers the page as where the table's rows open by a route of their own", () => {
      expect(rowPageRoute(crew)).toBe('sector/crew');
      expect(rowPageRoute(boards)).toBe('sector/crew');
    });
  });

  // A page under the tenant segment names the tenant parameter in its route; the route
  // is mounted and registered as written, and resolved into the tenant where a link is built.
  describe('under the tenant segment', () => {
    it('mounts and registers the page as the config wrote it, parameter and all', () => {
      const missions = 'Missions' as Resource;
      const route = resourceRoutes(
        rootConfig({
          routeData: { route: 'sectors/:sectorID/missions' },
          page: { label: 'Missions' },
          parentConfig: listViewConfig({ primaryResource: missions, listColumns: [], elements: [] }),
        }),
        () => keyed('sectors/{sectorID}/missions'),
      );
      expect(route.path).toBe('sectors/:sectorID/missions');
      expect(rowRoute(route)).toBeDefined();
      expect(resourcePageRoute(missions)).toBe('sectors/:sectorID/missions');
    });
  });

  // A page whose config switches the row route off still lists, and is where another
  // list's rows land, but no row opens on it by a route of its own.
  describe('with the row route switched off, for a link to a row', () => {
    const tenders = 'Tenders' as Resource;

    it('is registered as nowhere a row opens by its own route', () => {
      const route = resourceRoutes(
        rootConfig({
          routeData: { route: 'hq/tenders', hasViewRoute: false },
          parentConfig: listViewConfig({ primaryResource: tenders, listColumns: [], elements: [] }),
        }),
        () => keyed('tenders'),
      );
      expect(rowRoute(route)).toBeUndefined();
      expect(resourcePageRoute(tenders)).toBe('hq/tenders');
      expect(rowPageRoute(tenders)).toBeUndefined();
    });
  });

  // A listed resource with no key field is served whole and has no row: no row route
  // under either form of the page route, and no page registered as where a row opens.
  describe('over a key-less resource', () => {
    const orders = 'StandingOrders' as Resource;
    const ordersMeta = (): ResourceMeta =>
      ({
        route: 'standing-orders',
        readDisabled: true,
        fields: [field('section'), field('directive')],
      }) as ResourceMeta;
    const ordersConfig = (routeData?: RootRouteData) =>
      rootConfig({
        ...(routeData ? { routeData } : {}),
        page: { label: 'Standing Orders' },
        parentConfig: listViewConfig({ primaryResource: orders, listColumns: [], elements: [] }),
      });

    const cases: { name: string; routeData?: RootRouteData; wantPath: string }[] = [
      { name: 'under the meta route', routeData: undefined, wantPath: 'standing-orders' },
      { name: 'under a configured route', routeData: { route: 'hq/standing-orders' }, wantPath: 'hq/standing-orders' },
    ];

    for (const tt of cases) {
      describe(tt.name, () => {
        const route = resourceRoutes(ordersConfig(tt.routeData), ordersMeta);

        it('guards the list route on List', () => {
          expect(route.path).toBe(tt.wantPath);
          expect(listRoute(route)?.canActivate).toContain(AuthorizationGuard);
          expect(scopeOf(listRoute(route))).toEqual({ resource: orders, permission: ListPermission });
          expect(route.children?.map((child) => child.path)).toEqual(['']);
        });

        it('has no row route', () => {
          expect(rowRoute(route)).toBeUndefined();
        });

        it('registers no page as where a row opens', () => {
          expect(resourcePageRoute(orders)).toBeUndefined();
          expect(rowPageRoute(orders)).toBeUndefined();
        });
      });
    }
  });

  // A resource behind a feature flag is matched only while the flag is on: the route
  // carries a match guard from the metadata's feature, under either form of the page
  // route, and the page sits behind the flag so the menus leave it out. An ungated
  // resource carries neither.
  describe('behind a feature flag', () => {
    const debriefs = 'Debriefs' as Resource;
    const gatedMeta = (): ResourceMeta => keyed('debriefs', { feature: 'debriefs' });
    const gatedConfig = (routeData?: RootRouteData) =>
      rootConfig({
        ...(routeData ? { routeData } : {}),
        page: { label: 'Debriefs' },
        parentConfig: listViewConfig({ primaryResource: debriefs, listColumns: [], elements: [] }),
      });

    const cases: { name: string; routeData?: RootRouteData }[] = [
      { name: 'under the meta route', routeData: undefined },
      { name: 'under a configured route', routeData: { route: 'ops/debriefs' } },
    ];

    for (const tt of cases) {
      describe(tt.name, () => {
        it('carries a match guard and puts the page behind the flag', () => {
          const config = gatedConfig(tt.routeData);
          const route = resourceRoutes(config, gatedMeta);
          expect(route.canMatch).toHaveLength(1);
          expect(route.canActivate).toContain(AuthorizationGuard);
          expect(resourcePage(config, gatedMeta).feature).toBe('debriefs');
        });
      });
    }

    it('the guard answers from the enabled set, loading it first, so a dark URL falls to the wildcard', async () => {
      const route = resourceRoutes(gatedConfig(), gatedMeta);
      const [guard] = route.canMatch as CanMatchFn[];
      let enabled: string[] = [];
      const transport = scriptedTransport((request) =>
        request.url === '/api/features'
          ? { status: 200, body: { enabled } }
          : { status: 404, body: { message: `unscripted ${request.url}` } },
      );
      TestBed.configureTestingModule({
        providers: [
          provideResourceTesting({
            transport,
            client: (t) =>
              createClient(
                {
                  resources: {},
                  methods: {},
                  permissionDigestRoute: 'permission-digest',
                  userDomainsRoute: 'user-domains',
                  features: { route: 'features' },
                },
                { baseUrl: '/api', transport: t },
              ),
          }),
        ],
      });
      const answer = async (): Promise<unknown> => {
        // The guard reads the feature alone, so the snapshot of the match so far is empty.
        const result = TestBed.runInInjectionContext(() => guard(route, [], {} as PartialMatchRouteSnapshot));
        return isObservable(result) ? firstValueFrom(result) : result;
      };
      expect(await answer()).toBe(false);
      expect(transport.requests.map((r) => r.url)).toEqual(['/api/features']);

      enabled = ['debriefs'];
      await TestBed.inject(RESOURCE_CLIENT).features.refresh();
      expect(await answer()).toBe(true);
    });

    it("keeps a feature the config's page names", () => {
      const config = rootConfig({
        page: { label: 'Debriefs', feature: 'ops_console' },
        parentConfig: listViewConfig({ primaryResource: debriefs, listColumns: [], elements: [] }),
      });
      resourceRoutes(config, gatedMeta);
      expect(resourcePage(config, gatedMeta).feature).toBe('ops_console');
    });

    it('an ungated resource carries no match guard and no flag', () => {
      const config = config_();
      const route = resourceRoutes(config, meta);
      expect(route.canMatch).toBeUndefined();
      expect(resourcePage(config, meta).feature).toBeUndefined();
    });
  });

  // A page that lists its resource across tenants lives outside the tenant's segment and
  // has no one tenant: it is the list alone at the route its config names, it asks no
  // tenant's digest, and a row still opens on the tenant's own page.
  describe('over a list across tenants', () => {
    const dispatches = 'Dispatches' as Resource;
    const dispatchesMeta =
      (extra: Partial<ResourceMeta> = {}) =>
      (): ResourceMeta =>
        keyed('sectors/{sectorID}/dispatches', extra);
    const acrossConfig = (
      routeData: RootRouteData | undefined,
      page: ConfigPage = { label: 'Dispatches in every sector' },
    ) =>
      rootConfig({
        ...(routeData ? { routeData } : {}),
        page,
        parentConfig: listViewConfig({
          primaryResource: dispatches,
          acrossTenants: true,
          listColumns: [],
          elements: [],
        }),
      });

    it('mounts the list alone at the configured route, guarded by whether the person may list the resource in any tenant of theirs', () => {
      const route = resourceRoutes(acrossConfig({ route: 'hq/dispatches' }), dispatchesMeta());
      expect(route.path).toBe('hq/dispatches');
      expect(route.children?.map((child) => child.path)).toEqual(['']);
      expect(rowRoute(route)).toBeUndefined();
      // No one tenant's List: the global digest's answer for the list across tenants.
      expect(scopeOf(route)).toEqual({ resource: dispatches, permission: ListAcrossTenantsPermission });
      expect(route.canActivate).toEqual([AuthorizationGuard]);
    });

    it('registers no row route of its own: a row opens inside its tenant', () => {
      resourceRoutes(acrossConfig({ route: 'hq/dispatches' }), dispatchesMeta());
      expect(rowPageRoute(dispatches)).toBeUndefined();
    });

    it("leaves the tenant's own page as where the resource's rows open", () => {
      expect(resourcePageRoute(dispatches)).toBeUndefined();
      resourceRoutes(acrossConfig({ route: 'hq/dispatches' }), dispatchesMeta());
      expect(resourcePageRoute(dispatches)).toBeUndefined();

      const inTenant = rootConfig({
        routeData: { route: 'sectors/:sectorID/dispatches' },
        parentConfig: listViewConfig({ primaryResource: dispatches, listColumns: [], elements: [] }),
      });
      resourceRoutes(inTenant, dispatchesMeta());
      expect(resourcePageRoute(dispatches)).toBe('sectors/:sectorID/dispatches');
      expect(rowPageRoute(dispatches)).toBe('sectors/:sectorID/dispatches');
    });

    it('carries a page shown to whoever may list the resource in a tenant of theirs, at the configured route', () => {
      const config = acrossConfig(
        { route: 'hq/dispatches' },
        { label: 'Dispatches in every sector', icon: 'travel_explore' },
      );
      const route = resourceRoutes(config, dispatchesMeta());
      expect(route.data?.[pageDataKey]).toBe(resourcePage(config, dispatchesMeta()));
      expect(resourcePage(config, dispatchesMeta())).toEqual({
        label: 'Dispatches in every sector',
        icon: 'travel_explore',
        route: ['hq/dispatches'],
        permission: { resource: dispatches, permission: ListAcrossTenantsPermission },
        feature: undefined,
      });
    });

    it("keeps the config's own permission for the menus, and its own guard on the route", () => {
      const permission = { resource: 'Charters' as Resource, permission: ListPermission };
      const config = acrossConfig({ route: 'hq/dispatches' }, { label: 'Dispatches in every sector', permission });
      expect(resourcePage(config, dispatchesMeta()).permission).toBe(permission);
      expect(scopeOf(resourceRoutes(config, dispatchesMeta()))).toEqual({
        resource: dispatches,
        permission: ListAcrossTenantsPermission,
      });
    });

    it('sits behind the feature flag of its resource', () => {
      const config = acrossConfig({ route: 'hq/dispatches' });
      const route = resourceRoutes(config, dispatchesMeta({ feature: 'dispatches' }));
      expect(route.canMatch).toHaveLength(1);
      expect(resourcePage(config, dispatchesMeta({ feature: 'dispatches' })).feature).toBe('dispatches');
    });

    it("refuses a config that names no route, since the resource's own route is one tenant's list", () => {
      const refusal =
        "the config of Dispatches lists it across tenants and names no route: set routeData.route to where the page lives, outside the tenant's segment (the resource's own route is one tenant's list)";
      expect(() => resourceRoutes(acrossConfig(undefined), dispatchesMeta())).toThrow(refusal);
      expect(() => resourcePage(acrossConfig(undefined), dispatchesMeta())).toThrow(refusal);
    });
  });
});
