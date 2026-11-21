// Demonstrates: list.across-tenants, list.across-tenants.live.
import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, ActivatedRouteSnapshot, Router, RouterStateSnapshot, UrlTree } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { Resources } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { MenuContext, shownMenus } from '@impulseframework/resource-angular/auth-frame';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { resourcePage, resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ListAcrossTenantsPermission, ListViewConfig, RESOURCE_META } from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { firstValueFrom, isObservable } from 'rxjs';
import { appMenus } from '../app.menus';
import { missionsConfig } from './missions.config';
import { missionsEverySectorConfig } from './missionsEverySector.config';

// Missions in every sector is the library's list over a resource listed across tenants.
// The server as the spec plays it answers the liaison: the list served without the
// sector's segment, each row naming its sector, Anvil's rows without the fee her Cadet
// role there does not show and Cinder's closed sweep with it. The page asks that one
// route, names each row's sector after its title, leaves the Fee cell empty where the
// server sent none, and opens a row on the mission itself inside its own sector, where
// her roles let her read a mission. The page is hers because she may list missions in a sector she
// reaches, which the server tells the global digest: its menu entry and its route ask
// that, and a person who may list missions in no sector is shown neither.

const anvil = 'a1111111-1111-4111-8111-111111111111';
const cinder = 'c3333333-3333-4333-8333-333333333333';

const sweep = '80000000-0000-4000-8000-000000000011';
const courier = '80000000-0000-4000-8000-000000000008';
const hauler = '80000000-0000-4000-8000-000000000001';

/** The liaison's first rows, in the declared deadline order. */
const liaisonRows = [
  {
    id: sweep,
    title: 'Cinder Verge salvage sweep',
    kindId: 'salvage',
    hazard: 5,
    statusId: 'completed',
    deadline: '2026-10-12T08:00:00Z',
    fee: '60000',
    zzTenant: cinder,
  },
  {
    id: courier,
    title: 'Quarantine relief courier',
    kindId: 'courier',
    hazard: 2,
    statusId: 'open',
    deadline: '2026-10-31T08:00:00Z',
    zzTenant: anvil,
  },
  {
    id: hauler,
    title: 'Stranded hauler off Anvil Reach',
    kindId: 'rescue',
    hazard: 2,
    statusId: 'open',
    deadline: '2026-12-01T08:00:00Z',
    zzTenant: anvil,
  },
];

describe('the Missions in every sector page', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  let rows: unknown[];
  /** The global digest the server answers: who the list across sectors is for. */
  let global: PermissionDigest;
  /** Her roles in each sector let her list missions and read one, each under a condition. */
  const inSector: PermissionDigest = { Missions: { List: 'conditional', Read: 'conditional' } };
  const liaisonDigest: PermissionDigest = { Missions: { ListAcrossTenants: 'granted' } };

  const server = (request: TransportRequest): TransportResponse => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/permission-digest`:
        return { status: 200, body: url.searchParams.has('domain') ? inSector : global };
      case `${environment.apiUrl}/user-domains`:
        return {
          status: 200,
          body: [
            { domain: anvil, name: 'Anvil' },
            { domain: cinder, name: 'Cinder' },
          ],
        };
      case `${environment.apiUrl}/missions`:
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };

  const page = (): ListViewConfig => missionsEverySectorConfig.parentConfig as ListViewConfig;

  /** Builds the list component the page's route loads, over the console's generated client. */
  const create = async (answer: unknown[], digest: PermissionDigest = liaisonDigest): Promise<void> => {
    rows = answer;
    global = digest;
    // The sector's own Missions page is where a mission opens, as the application's routes register it.
    resourceRoutes(missionsConfig, resourceMeta);
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: missionsEverySectorConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDomains();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', page());
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
    // The digests of the sectors on the page answer, and the rows' links are drawn.
    await TestBed.inject(ApplicationRef).whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };

  const listRequests = (): string[] =>
    transport.requests
      .filter((request) => request.url.split('?')[0] === `${environment.apiUrl}/missions`)
      .map((request) => `${request.method} ${request.url}`);

  describe('as the liaison', () => {
    beforeEach(async () => {
      await create(liaisonRows);
    });

    it("asks the one list without the sector's segment and no column list, and each sector's digest once for its rows' links", () => {
      expect(fixture.componentInstance.store.pageError()).toBeUndefined();
      expect(listRequests()).toEqual([`GET ${environment.apiUrl}/missions?count=true`]);
      expect(
        transport.requests.filter((request) => request.url.includes('permission-digest')).map((request) => request.url),
      ).toEqual([
        `${environment.apiUrl}/permission-digest?domain=${cinder}`,
        `${environment.apiUrl}/permission-digest?domain=${anvil}`,
      ]);
      expect(transport.requests.some((request) => request.url.includes('/sectors/'))).toBe(false);
    });

    it("names each row's sector after its title and leaves the fee empty where her roles in the sector hide it", () => {
      const element: HTMLElement = fixture.nativeElement;
      expect(Array.from(element.querySelectorAll('th .col-header')).map((th) => th.textContent?.trim())).toEqual([
        'Title',
        'Sector',
        'Kind',
        'Hazard',
        'Status',
        'Deadline',
        'Fee',
      ]);
      const cells = Array.from(element.querySelectorAll('tr.impulse-row')).map((row) =>
        Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim()),
      );
      expect(cells.map((row) => [row[0], row[1], row[6]])).toEqual([
        ['Cinder Verge salvage sweep', 'Cinder', '60000'],
        ['Quarantine relief courier', 'Anvil', ''],
        ['Stranded hauler off Anvil Reach', 'Anvil', ''],
      ]);
      expect(element.querySelector('.impulse-grid-pager .pager-rows')?.textContent?.trim()).toBe('1–3 of 3');
    });

    it('opens a row on the mission itself inside its own sector, where she may read a mission', () => {
      const links = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('td.action-col a')).map((link) =>
        link.getAttribute('href'),
      );
      expect(links).toEqual([
        `/sectors/${cinder}/missions/${sweep}`,
        `/sectors/${anvil}/missions/${courier}`,
        `/sectors/${anvil}/missions/${hauler}`,
      ]);
    });

    it('asks for live: one subscription over the sectors her list reads, whichever they are', () => {
      expect(fixture.componentInstance.store.live()).toBe(true);
      expect(TestBed.inject<ClientBase>(RESOURCE_CLIENT).live.subscriptions()).toEqual([
        { resource: Resources.Missions, acrossTenants: true },
      ]);
    });

    it('reads alone: a mission is booked inside its sector', () => {
      expect((fixture.nativeElement as HTMLElement).querySelector('.create-button')).toBeNull();
      expect(fixture.componentInstance.across()).toBe(true);
    });
  });

  it("is the list alone at its own route, with no row route, asking no one sector's List", () => {
    const route = resourceRoutes(missionsEverySectorConfig, resourceMeta);
    const listsSomewhere = { resource: Resources.Missions, permission: ListAcrossTenantsPermission };
    expect(route.path).toBe('missions');
    expect(route.children?.map((child) => child.path)).toEqual(['']);
    // The config names no permission: the route and the menus ask what the server tells
    // the global digest, whether the person may list missions in a sector they reach.
    expect(missionsEverySectorConfig.page?.permission).toBeUndefined();
    expect(route.data?.['scope']).toEqual(listsSomewhere);
    expect(route.canActivate).toEqual([AuthorizationGuard]);
    expect(resourcePage(missionsEverySectorConfig, resourceMeta).permission).toEqual(listsSomewhere);
  });

  describe('is for whoever may list missions in a sector they reach', () => {
    /** The console over a server answering the person's global digest, loaded as a sign-in loads it. */
    const signedIn = async (digest: PermissionDigest): Promise<ClientBase> => {
      global = digest;
      transport = scriptedTransport(server);
      await TestBed.configureTestingModule({
        providers: [
          provideResourceTesting({
            transport,
            client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }),
          }),
        ],
      }).compileComponents();
      const client = TestBed.inject<ClientBase>(RESOURCE_CLIENT);
      await client.permissions.loadDigest();
      return client;
    };
    /** Whether the application's menu file shows the page's entry, asked as the frame asks, in Headquarters. */
    const entryShown = (client: ClientBase): boolean => {
      const context: MenuContext = {
        allows: (scope) => client.can(scope.permission, scope.resource),
        enabled: () => true,
        inTenant: () => false,
        link: (route) => route,
      };
      const menus = TestBed.runInInjectionContext(() => appMenus());
      return JSON.stringify(shownMenus(menus, context)).includes('"Missions in every sector"');
    };
    /** What the page's route answers a navigation: true, or where it sends the person instead. */
    const routeAnswer = async (): Promise<true | string> => {
      const route = resourceRoutes(missionsEverySectorConfig, resourceMeta);
      const answer = TestBed.runInInjectionContext(() =>
        AuthorizationGuard({ data: route.data } as unknown as ActivatedRouteSnapshot, {} as RouterStateSnapshot),
      );
      const settled = isObservable(answer) ? await firstValueFrom(answer) : await answer;
      return settled instanceof UrlTree ? TestBed.inject(Router).serializeUrl(settled) : (settled as true);
    };

    it('the liaison sees the entry, and the route opens for her', async () => {
      const client = await signedIn(liaisonDigest);
      expect(entryShown(client)).toBe(true);
      await expect(routeAnswer()).resolves.toBe(true);
    });

    it('a person who may list missions in no sector does not see it, and the route turns them away', async () => {
      // The yeoman holds what every crew member holds outside the sectors, List on the
      // sectors among it, and may list missions nowhere: the server names her no mission list.
      const client = await signedIn({ Sectors: { List: 'granted', Read: 'granted' } });
      expect(entryShown(client)).toBe(false);
      await expect(routeAnswer()).resolves.toBe('/');
      // Nothing was asked of the list.
      expect(listRequests()).toEqual([]);
    });
  });
});
