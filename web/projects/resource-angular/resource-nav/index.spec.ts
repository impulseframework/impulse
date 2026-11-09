import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { ApiDescriptor, createClient, Domain } from '@impulseframework/resource';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { Resource, RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';

import { pageRouteResolver, registerResourcePage, resourcePageRoute } from './index';

// A registered route is the page's path as its config wrote it, and it may carry the
// tenant parameter as the router names it. The resolver puts the page's tenant in the
// parameter's place: the route's own value for it when the page is under the parameter,
// else the selected tenant; a route naming no parameter is returned as written, and one
// naming it with no tenant known is refused.

/** The generated descriptor of an API serving its tenants under sectors/{sectorID}. */
const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {},
  methods: {},
};

/** The same API declaring no domain route: nothing is served under a tenant. */
const globalDescriptor: ApiDescriptor = { ...descriptor, domainRoute: undefined };

const tenantRoute = 'sectors/:sectorID/missions';

describe('pageRouteResolver', () => {
  interface PageOptions {
    /** The page's own route parameters. */
    params?: Record<string, string>;
    /** The parameters of the routes above the page, root first, when the page has a path. */
    ancestors?: Record<string, string>[];
    domain?: Domain;
    api?: ApiDescriptor;
  }

  /** The resolver as a page with the given route parameters builds it, under the selected tenant. */
  const resolverOf = async (options: PageOptions): Promise<(route: string) => string> => {
    const snapshot: Record<string, unknown> = { data: {}, params: options.params ?? {}, queryParams: {} };
    if (options.ancestors) {
      snapshot['pathFromRoot'] = [...options.ancestors.map((params) => ({ params })), snapshot];
    }
    await TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          client: (t) => createClient(options.api ?? descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(options.domain) },
        { provide: ActivatedRoute, useValue: { snapshot } },
      ],
    }).compileComponents();
    return TestBed.runInInjectionContext(() => pageRouteResolver());
  };

  const cases: { name: string; route: string; page: PageOptions; want: string }[] = [
    {
      name: 'a route naming no parameter as written, with no tenant known',
      route: 'crew/clients',
      page: {},
      want: 'crew/clients',
    },
    {
      name: 'a route naming no parameter as written, under a tenant',
      route: 'crew/clients',
      page: { params: { sectorID: 'anvil' }, domain: 'forge' as Domain },
      want: 'crew/clients',
    },
    {
      name: "the route's tenant, when the page is under the parameter",
      route: tenantRoute,
      page: { params: { sectorID: 'anvil' } },
      want: 'sectors/anvil/missions',
    },
    {
      name: "the route's tenant from the segment above the page",
      route: tenantRoute,
      page: { params: {}, ancestors: [{}, { sectorID: 'anvil' }] },
      want: 'sectors/anvil/missions',
    },
    {
      name: "the route's tenant over the selected one, when both are known",
      route: tenantRoute,
      page: { params: { sectorID: 'anvil' }, domain: 'forge' as Domain },
      want: 'sectors/anvil/missions',
    },
    {
      name: 'the selected tenant, when the page is not under the parameter',
      route: tenantRoute,
      page: { domain: 'forge' as Domain },
      want: 'sectors/forge/missions',
    },
    {
      name: 'the parameter as a whole segment only',
      route: 'sectors/:sectorIDs/missions',
      page: { params: { sectorID: 'anvil' } },
      want: 'sectors/:sectorIDs/missions',
    },
    {
      name: 'a route as written on an API declaring no domain route',
      route: tenantRoute,
      page: { params: { sectorID: 'anvil' }, api: globalDescriptor },
      want: tenantRoute,
    },
  ];

  for (const tt of cases) {
    it(`resolves ${tt.name}`, async () => {
      const resolve = await resolverOf(tt.page);
      expect(resolve(tt.route)).toBe(tt.want);
    });
  }

  it('refuses a route naming the parameter when no tenant is known, naming the route and the parameter', async () => {
    const resolve = await resolverOf({});
    expect(() => resolve(tenantRoute)).toThrow(
      /^sectors\/:sectorID\/missions: the route names the tenant parameter :sectorID, and no tenant is known: the page is not under the parameter and no tenant is selected \(RESOURCE_DOMAIN\)$/,
    );
  });
});

describe('registerResourcePage', () => {
  it('records the route as the config wrote it, parameter and all, and keeps the first', () => {
    const missions = 'Missions' as Resource;
    registerResourcePage(missions, tenantRoute);
    registerResourcePage(missions, 'missions');
    expect(resourcePageRoute(missions)).toBe(tenantRoute);
  });
});
