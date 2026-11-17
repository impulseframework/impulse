// Demonstrates: list.across-tenants.
import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { resourcePage, resourceRoutes } from '@impulseframework/resource-angular/resource-route-generator';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ListViewConfig, RESOURCE_META } from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { missionsConfig } from './missions.config';
import { missionsEverySectorConfig } from './missionsEverySector.config';

// Missions in every sector is the library's list over a resource listed across tenants.
// The server as the spec plays it answers the liaison: the list served without the
// sector's segment, each row naming its sector, Anvil's rows without the fee her Cadet
// role there does not show and Cinder's closed sweep with it. The page asks that one
// route, names each row's sector first, leaves the Fee cell empty where the server sent
// none, and opens a row on the mission's page inside its own sector.

const anvil = 'a1111111-1111-4111-8111-111111111111';
const cinder = 'c3333333-3333-4333-8333-333333333333';

const sweep = '80000000-0000-4000-8000-000000000011';
const courier = '80000000-0000-4000-8000-000000000008';
const hauler = '80000000-0000-4000-8000-000000000001';

/** The liaison's first rows, in the declared deadline order. */
const liaisonRows = [
  { id: sweep, title: 'Cinder Verge salvage sweep', kindId: 'salvage', hazard: 5, statusId: 'completed', deadline: '2026-10-12T08:00:00Z', fee: '60000', zzTenant: cinder },
  { id: courier, title: 'Quarantine relief courier', kindId: 'courier', hazard: 2, statusId: 'open', deadline: '2026-10-31T08:00:00Z', zzTenant: anvil },
  { id: hauler, title: 'Stranded hauler off Anvil Reach', kindId: 'rescue', hazard: 2, statusId: 'open', deadline: '2026-12-01T08:00:00Z', zzTenant: anvil },
];

describe('the Missions in every sector page', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  let rows: unknown[];

  const server = (request: TransportRequest): TransportResponse => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
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
  const create = async (answer: unknown[]): Promise<void> => {
    rows = answer;
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
  };

  const listRequests = (): string[] =>
    transport.requests
      .filter((request) => request.url.split('?')[0] === `${environment.apiUrl}/missions`)
      .map((request) => `${request.method} ${request.url}`);

  describe('as the liaison', () => {
    beforeEach(async () => {
      await create(liaisonRows);
    });

    it("asks the one list without the sector's segment: no column list, no sector's digest", () => {
      expect(fixture.componentInstance.store.pageError()).toBeUndefined();
      expect(listRequests()).toEqual([`GET ${environment.apiUrl}/missions?count=true`]);
      expect(transport.requests.some((request) => request.url.includes('permission-digest'))).toBe(false);
      expect(transport.requests.some((request) => request.url.includes('/sectors/'))).toBe(false);
    });

    it("names each row's sector first and leaves the fee empty where her roles in the sector hide it", () => {
      const element: HTMLElement = fixture.nativeElement;
      expect(Array.from(element.querySelectorAll('th .col-header')).map((th) => th.textContent?.trim())).toEqual([
        'Sector',
        'Title',
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
        ['Cinder', 'Cinder Verge salvage sweep', '60000'],
        ['Anvil', 'Quarantine relief courier', ''],
        ['Anvil', 'Stranded hauler off Anvil Reach', ''],
      ]);
      expect(element.querySelector('.impulse-grid-pager .pager-rows')?.textContent?.trim()).toBe('1–3 of 3');
    });

    it('opens a row on the mission page inside its own sector', () => {
      const links = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('td.action-col a')).map((link) =>
        link.getAttribute('href'),
      );
      expect(links).toEqual([
        `/sectors/${cinder}/missions/${sweep}`,
        `/sectors/${anvil}/missions/${courier}`,
        `/sectors/${anvil}/missions/${hauler}`,
      ]);
    });

    it('reads alone: a mission is booked inside its sector', () => {
      expect((fixture.nativeElement as HTMLElement).querySelector('.create-button')).toBeNull();
      expect(fixture.componentInstance.across()).toBe(true);
    });
  });

  it('is an empty list for a person who may list missions in no sector', async () => {
    await create([]);
    expect(fixture.componentInstance.store.pageError()).toBeUndefined();
    expect(listRequests()).toEqual([`GET ${environment.apiUrl}/missions?count=true`]);
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row').length).toBe(0);
  });

  it("is the list alone at its own route, with no row route and no one sector's List guard", () => {
    const route = resourceRoutes(missionsEverySectorConfig, resourceMeta);
    expect(route.path).toBe('missions');
    expect(route.children?.map((child) => child.path)).toEqual(['']);
    expect(route.data?.['scope']).toBeUndefined();
    // The menus show the page to whoever may list the sectors.
    expect(resourcePage(missionsEverySectorConfig, resourceMeta).permission).toEqual({
      resource: Resources.Sectors,
      permission: Permissions.List,
    });
  });
});
