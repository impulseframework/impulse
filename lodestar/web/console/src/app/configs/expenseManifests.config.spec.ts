import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { methodMeta } from '@app/service/zz_gen_methods';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { METHOD_META, RESOURCE_DOMAIN, RESOURCE_META } from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { expenseManifestsConfig } from './expenseManifests.config';

// The expense manifests page is the library's list over the computed manifests. The server
// as the spec plays it answers the Purser in Anvil, who holds the manifests' fields
// outright: one row per mission, with its sorties and what they cost. The page draws the
// three columns, offers a sort on each, and draws the search box, which searches the
// mission's title. It answers the Flight Lead too, who holds the fields on the rows of the
// missions their squadrons fly: the page draws the same columns, offers no sort and no
// search box, and says why where the box would be.

const anvil = 'a1111111-1111-4111-8111-111111111111';

const manifests = [
  {
    missionId: '80000000-0000-4000-8000-000000000003',
    title: 'Escort the Meridian survey convoy',
    sorties: 1,
    expenses: '0',
  },
  {
    missionId: '80000000-0000-4000-8000-000000000005',
    title: 'Recover the Lantern cargo pod',
    sorties: 2,
    expenses: '1800',
  },
  {
    missionId: '80000000-0000-4000-8000-000000000002',
    title: 'Salvage the drifting Corvid wreck',
    sorties: 0,
    expenses: '0',
  },
];

/** A digest that lists the manifests and their four fields with one List state. */
const digestOf = (state: 'granted' | 'conditional'): PermissionDigest =>
  Object.fromEntries(
    [
      'ExpenseManifests',
      ...['title', 'sorties', 'expenses', 'assignedSquadronId'].map((f) => `ExpenseManifests.${f}`),
    ].map((name) => [name, { List: state }]),
  );

/** The Purser's digest in the sector: the manifests and every field, outright. */
const purser: PermissionDigest = digestOf('granted');

/**
 * The Flight Lead's digest in the sector: the manifests and every field under a condition
 * on the mission's squadron, which the server judges row by row.
 */
const flightLead: PermissionDigest = digestOf('conditional');

describe('the expense manifests page', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const manifestsPath = `${environment.apiUrl}/sectors/${anvil}/expense-manifests`;

  /** Builds the list component the page's route loads, over the console's generated client. */
  const create = async (digest: PermissionDigest, rows: unknown[]): Promise<void> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lodestar.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: url.searchParams.has('domain') ? digest : {} };
        case manifestsPath:
          return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
        default:
          return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: METHOD_META, useValue: methodMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(anvil as Domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: expenseManifestsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(anvil as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', expenseManifestsConfig.parentConfig);
    fixture.componentRef.setInput('hideCreateButton', false);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const headings = (): (string | undefined)[] =>
    Array.from(element().querySelectorAll('th .col-header')).map((h) => h.textContent?.trim());
  const pressable = (): (string | undefined)[] =>
    Array.from(element().querySelectorAll('th button.header-label .col-header')).map((h) => h.textContent?.trim());

  describe('for the Purser, who holds the fields outright', () => {
    it('draws the mission, its sorties and its expenses, and offers a sort on each column', async () => {
      await create(purser, manifests);

      expect(element().querySelector('h1.impulse-resource-list-title')?.textContent?.trim()).toBe(
        'Expense manifests (3)',
      );
      expect(headings()).toEqual(['Mission', 'Sorties', 'Expenses']);
      expect(pressable()).toEqual(['Mission', 'Sorties', 'Expenses']);
    });

    it('offers the search box, which searches the mission', async () => {
      await create(purser, manifests);

      const box = element().querySelector<HTMLInputElement>('impulse-search-box input');
      expect(box).not.toBeNull();
      expect(box?.placeholder).toBe('Search Mission');
      expect(element().querySelector('.impulse-resource-list-search-refused')).toBeNull();
    });

    it('says so when no mission has a manifest', async () => {
      await create(purser, []);

      expect(element().querySelector('tr.no-records-row')?.textContent?.trim()).toBe(
        'No mission in this sector has a manifest yet.',
      );
    });
  });

  describe('for the Flight Lead, who sees the fields on some rows only', () => {
    it('draws the three columns, offers no sort, and offers no search box but says why', async () => {
      await create(flightLead, manifests);

      expect(headings()).toEqual(['Mission', 'Sorties', 'Expenses']);
      expect(pressable()).toEqual([]);
      expect(element().querySelector('impulse-search-box')).toBeNull();
      expect(element().querySelector('.impulse-resource-list-search-refused')?.textContent?.trim()).toBe(
        'You cannot search this list: your role sees Mission on some rows only.',
      );
    });
  });
});
