import { signal, WritableSignal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { ColumnConfig, FieldName, RESOURCE_DOMAIN, rpcConfig } from '@impulseframework/resource-angular/types';
import { ApiDescriptor, ApiError, createClient, Domain, Method, Resource, Transport, TransportRequest } from '@impulseframework/resource';
import { firstValueFrom } from 'rxjs';
import { ResourceStore } from './resource-store.service';

// The store's whole-set funnel and the paged reader, on the read mode the descriptor's
// maximum page size decides: a source with no maximum is read whole in one limit=all
// request; a source with a maximum is read one server page at a time, looked up by
// key in `in` batches no larger than its maximum, and never read whole. The requests
// are pinned by URL against a scripted client.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {
    // No maximum: read whole.
    ShipClasses: {
      resource: 'ShipClasses' as Resource,
      property: 'shipClasses',
      route: 'ship-classes',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
    // A maximum and a declared order: paged, the server orders.
    Hangars: {
      resource: 'Hangars' as Resource,
      property: 'hangars',
      route: 'hangars',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 2 },
      order: [{ field: 'name', direction: 'asc' }],
    },
    // A maximum and no order: paged, and a request without a sort is the server's refusal.
    Berths: {
      resource: 'Berths' as Resource,
      property: 'berths',
      route: 'berths',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 10, max: 50 },
    },
  },
  methods: {
    // A tenant's method: posted under the selected tenant.
    OpenHangar: { method: 'OpenHangar' as Method, property: 'openHangar', route: 'open-hangar', scope: 'domain' },
    // A method of the whole application: posted at its own route.
    RetireShipClass: { method: 'RetireShipClass' as Method, property: 'retireShipClass', route: 'retire-ship-class', scope: 'global' },
  },
};

interface Scripted {
  transport: Transport;
  requests: TransportRequest[];
}

/** A transport scripted by URL: each entry answers one request with rows and headers, an unscripted URL is a 404. */
function scripted(pages: Record<string, { status?: number; rows: unknown; headers?: Record<string, string> }>): Scripted {
  const requests: TransportRequest[] = [];
  const transport: Transport = async (request) => {
    requests.push(request);
    const page = pages[request.url];
    if (!page) {
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
    return { status: page.status ?? 200, body: page.rows, headers: page.headers ?? {} };
  };
  return { transport, requests };
}

/** Runs change detection and lets pending microtasks land until `done` answers true, for at most `rounds` rounds. */
async function settle(done: () => boolean, rounds = 50): Promise<void> {
  for (let i = 0; i < rounds && !done(); i++) {
    TestBed.tick();
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  TestBed.tick();
}

function storeOver(script: Scripted, domain: WritableSignal<Domain | undefined> = signal('anvil' as Domain)): ResourceStore {
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      ResourceStore,
      { provide: RESOURCE_CLIENT, useValue: createClient(descriptor, { baseUrl: '/api', transport: script.transport }) },
      { provide: RESOURCE_DOMAIN, useValue: domain },
    ],
  });
  return TestBed.inject(ResourceStore);
}

describe('ResourceStore whole-set funnel', () => {
  const cases: {
    name: string;
    route: string;
    columns: string[];
    sorts: { field: FieldName; direction: 'asc' | 'desc' }[];
    script: Record<string, { status?: number; rows: unknown }>;
    wantUrls: string[];
    wantRows?: unknown[];
    wantRefusal?: string;
  }[] = [
    {
      name: 'a source with no maximum is read whole: one limit=all request',
      route: 'ship-classes',
      columns: ['id', 'designation'],
      sorts: [],
      script: { '/api/ship-classes?columns=id%2Cdesignation&limit=all': { rows: [{ id: 'c1', designation: 'Cutter' }] } },
      wantUrls: ['/api/ship-classes?columns=id%2Cdesignation&limit=all'],
      wantRows: [{ id: 'c1', designation: 'Cutter' }],
    },
    {
      name: 'a source with a maximum is never read whole: one server page at the default size, the declared order applying',
      route: 'sectors/{sectorID}/hangars',
      columns: ['id', 'name'],
      sorts: [],
      script: { '/api/sectors/anvil/hangars?columns=id%2Cname': { rows: [{ id: 'h1', name: 'Anvil Dock One' }, { id: 'h2', name: 'Quarantine Bay' }] } },
      wantUrls: ['/api/sectors/anvil/hangars?columns=id%2Cname'],
      wantRows: [{ id: 'h1', name: 'Anvil Dock One' }, { id: 'h2', name: 'Quarantine Bay' }],
    },
    {
      name: 'a bounded source with no order and no sort is the server\'s refusal, in its words',
      route: 'berths',
      columns: ['id', 'name'],
      sorts: [],
      script: {
        '/api/berths?columns=id%2Cname': { status: 400, rows: { message: 'Berths serves at most 50 rows per page and declares no order; add a sort' } },
      },
      wantUrls: ['/api/berths?columns=id%2Cname'],
      wantRefusal: 'Berths serves at most 50 rows per page and declares no order; add a sort',
    },
    {
      name: 'a configured sort pages a bounded source with no order',
      route: 'berths',
      columns: ['id', 'name'],
      sorts: [{ field: 'name' as FieldName, direction: 'asc' }],
      script: { '/api/berths?sort=name%3Aasc&columns=id%2Cname': { rows: [{ id: 'b1', name: 'Berth 1' }] } },
      wantUrls: ['/api/berths?sort=name%3Aasc&columns=id%2Cname'],
      wantRows: [{ id: 'b1', name: 'Berth 1' }],
    },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const script = scripted(tt.script);
      const store = storeOver(script);
      const ref = store.resourceList(signal(tt.route), signal(''), signal(tt.columns), signal(false), signal(tt.sorts));
      await settle(() => ref.status() === 'resolved' || ref.status() === 'error');
      expect(script.requests.map((r) => r.url)).toEqual(tt.wantUrls);
      if (tt.wantRefusal) {
        expect(ref.status()).toBe('error');
        const error = ref.error();
        expect(error instanceof ApiError && error.message).toBe(tt.wantRefusal);
      } else {
        expect(ref.value()).toEqual(tt.wantRows as never);
      }
    });
  }
});

describe('ResourceStore key lookup', () => {
  it('reads a source with no maximum whole once and ignores the keys', async () => {
    const script = scripted({ '/api/ship-classes?columns=id%2Cdesignation&limit=all': { rows: [{ id: 'c1', designation: 'Cutter' }, { id: 'c2', designation: 'Tug' }] } });
    const store = storeOver(script);
    const keys = signal(['c1']);
    const ref = store.resourceListByKeys(signal('ship-classes'), signal('id'), keys, signal(['id', 'designation']));
    await settle(() => ref.status() === 'resolved');
    expect(ref.value()).toEqual([{ id: 'c1', designation: 'Cutter' }, { id: 'c2', designation: 'Tug' }] as never);

    // Another page's keys ask nothing more: the whole list is already held.
    keys.set(['c2']);
    await settle(() => ref.status() === 'resolved');
    expect(script.requests.map((r) => r.url)).toEqual(['/api/ship-classes?columns=id%2Cdesignation&limit=all']);
  });

  it('asks a source with a maximum for one in page per batch no larger than the maximum, never a whole read', async () => {
    const first = '/api/sectors/anvil/hangars?filter=id%3Ain%3A%28h1%2Ch2%29&columns=id%2Cname&limit=2';
    const second = '/api/sectors/anvil/hangars?filter=id%3Ain%3A%28h3%29&columns=id%2Cname&limit=1';
    const script = scripted({
      [first]: { rows: [{ id: 'h1', name: 'Anvil Dock One' }, { id: 'h2', name: 'Quarantine Bay' }] },
      [second]: { rows: [{ id: 'h3', name: 'Bastion Slip' }] },
    });
    const store = storeOver(script);
    const ref = store.resourceListByKeys(signal('sectors/{sectorID}/hangars'), signal('id'), signal(['h1', 'h2', 'h3']), signal(['id', 'name']), 200);
    await settle(() => ref.status() === 'resolved');
    expect(script.requests.map((r) => r.url).sort()).toEqual([first, second].sort());
    expect((ref.value() as { id: string }[]).map((row) => row.id).sort()).toEqual(['h1', 'h2', 'h3']);
  });

  it('sorts a bounded source with no order by the key field, which its index serves', async () => {
    const url = '/api/berths?filter=id%3Ain%3A%28b1%29&sort=id%3Aasc&columns=id%2Cname&limit=1';
    const script = scripted({ [url]: { rows: [{ id: 'b1', name: 'Berth 1' }] } });
    const store = storeOver(script);
    const ref = store.resourceListByKeys(signal('berths'), signal('id'), signal(['b1']), signal(['id', 'name']));
    await settle(() => ref.status() === 'resolved');
    expect(script.requests.map((r) => r.url)).toEqual([url]);
  });
});

describe('ResourceStore paged reader', () => {
  it('asks for the first page with its count and turns it by the server\'s relations', async () => {
    const first = '/api/sectors/anvil/hangars?columns=id%2Cname&count=true';
    const second = '/api/sectors/anvil/hangars?columns=id%2Cname&cursor=v4.local.two';
    const script = scripted({
      [first]: { rows: [{ id: 'h1', name: 'Anvil Dock One' }, { id: 'h2', name: 'Quarantine Bay' }], headers: { 'total-count': '3', link: `<${second}>; rel="next"` } },
      [second]: { rows: [{ id: 'h3', name: 'Bastion Slip' }], headers: { link: `<${first}>; rel="prev"` } },
    });
    const store = storeOver(script);
    const pager = store.resourcePage(signal({ route: 'sectors/{sectorID}/hangars', columns: ['id', 'name'] }));
    await settle(() => pager.status() === 'resolved');
    expect(pager.rows()).toEqual([{ id: 'h1', name: 'Anvil Dock One' }, { id: 'h2', name: 'Quarantine Bay' }] as never);
    expect(pager.page()).toEqual({ offset: 0, hasPrev: false, hasNext: true, total: 3 });

    pager.turn('next');
    await settle(() => pager.status() === 'resolved' && pager.page().offset === 2);
    expect(pager.rows()).toEqual([{ id: 'h3', name: 'Bastion Slip' }] as never);
    expect(pager.page()).toEqual({ offset: 2, hasPrev: true, hasNext: false, total: 3 });

    pager.turn('prev');
    await settle(() => pager.status() === 'resolved' && pager.page().offset === 0);
    expect(pager.page()).toEqual({ offset: 0, hasPrev: false, hasNext: true, total: 3 });
    expect(script.requests.map((r) => r.url)).toEqual([first, second, first]);
    pager.destroy();
  });

  it('holds the server\'s refusal of a bounded source with no order and no sort', async () => {
    const url = '/api/berths?columns=id%2Cname&count=true';
    const script = scripted({ [url]: { status: 400, rows: { message: 'Berths serves at most 50 rows per page and declares no order; add a sort' } } });
    const store = storeOver(script);
    const pager = store.resourcePage(signal({ route: 'berths', columns: ['id', 'name'] }));
    await settle(() => pager.status() === 'error');
    const error = pager.error();
    expect(error instanceof ApiError && error.message).toBe('Berths serves at most 50 rows per page and declares no order; add a sort');
    expect(pager.rows()).toEqual([]);
    pager.destroy();
  });
});

describe('ResourceStore row reader', () => {
  it('is idle without a key, reads once per key, builds once, and holds the row through a reload', async () => {
    const c1 = '/api/ship-classes/c1?capabilities=Update%2CDelete';
    const c2 = '/api/ship-classes/c2?capabilities=Update%2CDelete';
    const script = scripted({
      [c1]: { rows: { id: 'c1', designation: 'Cutter' } },
      [c2]: { rows: { id: 'c2', designation: 'Tug' } },
    });
    const store = storeOver(script);
    store.resourceMeta.set({ route: 'ship-classes', fields: [] });

    // No key: the reader exists, idle, with no value and no request.
    store.buildStoreViewData();
    await settle(() => store.viewStatus() !== undefined);
    expect(store.viewStatus()).toBe('idle');
    expect(store.rowPresent()).toBe(false);
    expect(store.viewData()).toEqual({});
    expect(script.requests).toEqual([]);

    // The string a template renders for a missing value is no key either.
    store.uuid.set('undefined');
    await settle(() => store.viewStatus() === 'idle');
    expect(script.requests).toEqual([]);

    // A key: one read, and the row is present.
    store.uuid.set('c1');
    await settle(() => store.rowPresent());
    expect(script.requests.map((r) => r.url)).toEqual([c1]);
    expect(store.viewData()).toEqual({ id: 'c1', designation: 'Cutter' });

    // A second build adds no reader and no request.
    store.buildStoreViewData();
    await settle(() => store.rowPresent());
    expect(script.requests.map((r) => r.url)).toEqual([c1]);

    // A changed key reads again.
    store.uuid.set('c2');
    await settle(() => store.viewData()['id'] === 'c2');
    expect(script.requests.map((r) => r.url)).toEqual([c1, c2]);

    // A reload keeps the row on hand while it is in flight.
    store.reloadViewData();
    TestBed.tick();
    expect(store.viewStatus()).toBe('reloading');
    expect(store.rowPresent()).toBe(true);
    expect(store.viewData()).toEqual({ id: 'c2', designation: 'Tug' });
    await settle(() => store.viewStatus() === 'resolved');
    expect(script.requests.map((r) => r.url)).toEqual([c1, c2, c2]);
  });

  it('holds no row while the read is refused', async () => {
    const url = '/api/ship-classes/c9?capabilities=Update%2CDelete';
    const script = scripted({ [url]: { status: 403, rows: { message: 'no' } } });
    const store = storeOver(script);
    store.resourceMeta.set({ route: 'ship-classes', fields: [] });
    store.uuid.set('c9');
    store.buildStoreViewData();
    await settle(() => store.viewStatus() === 'error');
    expect(store.rowPresent()).toBe(false);
    expect(store.viewData()).toEqual({});
    expect(store.viewError() instanceof ApiError).toBe(true);
  });
});

describe('ResourceStore method calls', () => {
  // A page calls a method through the method's handle: a tenant's method is posted under
  // the selected tenant, a method of the whole application at its own route, and the body
  // is sent as it was given.
  const openHangar = rpcConfig({ label: 'Open', conditions: [], method: 'OpenHangar' as never });
  const retire = rpcConfig({ label: 'Retire', conditions: [], method: 'RetireShipClass' as never });

  it("posts a tenant's method under the selected tenant, and under the next one once it changes", async () => {
    const script = scripted({
      '/api/sectors/anvil/open-hangar': { rows: null },
      '/api/sectors/bastion/open-hangar': { rows: null },
    });
    const domain = signal<Domain | undefined>('anvil' as Domain);
    const store = storeOver(script, domain);

    await firstValueFrom(store.rpcCall(openHangar, { hangarId: 'h1', sealed: false, crew: 0 }));
    domain.set('bastion' as Domain);
    await firstValueFrom(store.rpcCall(openHangar, { hangarId: 'h1', sealed: true, crew: 4 }));

    expect(script.requests.map((r) => `${r.method} ${r.url}`)).toEqual([
      'POST /api/sectors/anvil/open-hangar',
      'POST /api/sectors/bastion/open-hangar',
    ]);
    // false and 0 are values of the request, sent as given.
    expect(script.requests[0].body).toEqual({ hangarId: 'h1', sealed: false, crew: 0 });
  });

  it('posts a method of the whole application at its own route, whatever tenant is selected', async () => {
    const script = scripted({ '/api/retire-ship-class': { rows: null } });
    const store = storeOver(script);

    await firstValueFrom(store.rpcCall(retire, { classId: 'c1' }));

    expect(script.requests.map((r) => `${r.method} ${r.url}`)).toEqual(['POST /api/retire-ship-class']);
  });

  it("refuses a tenant's method with no tenant selected, before anything is sent", () => {
    const script = scripted({});
    const store = storeOver(script, signal<Domain | undefined>(undefined));

    expect(() => store.rpcCall(openHangar, { hangarId: 'h1' })).toThrow('is domain-scoped');
    expect(() => store.methodHandle('LaunchNothing')).toThrow('LaunchNothing is no method of this API');
    expect(script.requests).toHaveLength(0);
  });
});

describe('ResourceStore tenant scoping', () => {
  // Every reader's request key carries the tenant only when the resource is domain-scoped:
  // a reader over a global resource asks once and a change of the tenant asks nothing
  // more, while a reader over a domain-scoped resource asks again, naming the new tenant.
  const hangars = 'sectors/{sectorID}/hangars';
  const shipClassRow = '/api/ship-classes/c1?capabilities=Update%2CDelete';
  const anvilHangarRow = '/api/sectors/anvil/hangars/h1?capabilities=Update%2CDelete';
  const bastionHangarRow = '/api/sectors/bastion/hangars/h1?capabilities=Update%2CDelete';
  const shipClassList = '/api/ship-classes?columns=id%2Cdesignation&limit=all';
  const anvilHangarList = '/api/sectors/anvil/hangars?columns=id%2Cname';
  const bastionHangarList = '/api/sectors/bastion/hangars?columns=id%2Cname';
  const shipClassPage = '/api/ship-classes?columns=designation&count=true';
  const anvilHangarPage = '/api/sectors/anvil/hangars?columns=name&count=true';
  const bastionHangarPage = '/api/sectors/bastion/hangars?columns=name&count=true';
  const shipClassPaged = '/api/ship-classes?columns=id%2Cdesignation&count=true';
  const anvilHangarPaged = '/api/sectors/anvil/hangars?columns=id%2Cname&count=true';
  const bastionHangarPaged = '/api/sectors/bastion/hangars?columns=id%2Cname&count=true';
  const anvilHangarKeys = '/api/sectors/anvil/hangars?filter=id%3Ain%3A%28h1%29&columns=id%2Cname&limit=1';
  const bastionHangarKeys = '/api/sectors/bastion/hangars?filter=id%3Ain%3A%28h1%29&columns=id%2Cname&limit=1';

  /** The store's list page over one resource, as the list component wires it. */
  const listPage = (store: ResourceStore, name: string, route: string, column: string): (() => boolean) => {
    store.resourceName.set(name as Resource);
    store.resourceMeta.set({ route, fields: [] });
    store.listColumns.set([{ id: column } as ColumnConfig]);
    store.buildStorePage();
    return () => store.pageStatus() === 'resolved';
  };

  const cases: {
    name: string;
    /** What each scripted URL answers. */
    script: Record<string, unknown>;
    /** Builds the reader over the store and answers whether it has settled. */
    build: (store: ResourceStore) => { settled: () => boolean; destroy?: () => void };
    /** The requests once the reader settles, and once the tenant has changed from anvil to bastion. */
    wantFirst: string[];
    wantAfter: string[];
  }[] = [
    {
      name: 'the row reader over a global resource reads once across a change of the tenant',
      script: { [shipClassRow]: { id: 'c1', designation: 'Cutter' } },
      build: (store) => {
        const ref = store.resourceView(signal('ship-classes'), signal('c1'));
        return { settled: () => ref.status() === 'resolved' };
      },
      wantFirst: [shipClassRow],
      wantAfter: [shipClassRow],
    },
    {
      name: 'the row reader over a domain-scoped resource reads again, naming the new tenant',
      script: { [anvilHangarRow]: { id: 'h1', name: 'Anvil Dock One' }, [bastionHangarRow]: { id: 'h1', name: 'Bastion Slip' } },
      build: (store) => {
        const ref = store.resourceView(signal(hangars), signal('h1'));
        return { settled: () => ref.status() === 'resolved' };
      },
      wantFirst: [anvilHangarRow],
      wantAfter: [anvilHangarRow, bastionHangarRow],
    },
    {
      name: 'the list reader over a global resource reads once across a change of the tenant',
      script: { [shipClassList]: [] },
      build: (store) => {
        const ref = store.resourceList(signal('ship-classes'), signal(''), signal(['id', 'designation']), signal(false), signal([]));
        return { settled: () => ref.status() === 'resolved' };
      },
      wantFirst: [shipClassList],
      wantAfter: [shipClassList],
    },
    {
      name: 'the list reader over a domain-scoped resource reads again, naming the new tenant',
      script: { [anvilHangarList]: [], [bastionHangarList]: [] },
      build: (store) => {
        const ref = store.resourceList(signal(hangars), signal(''), signal(['id', 'name']), signal(false), signal([]));
        return { settled: () => ref.status() === 'resolved' };
      },
      wantFirst: [anvilHangarList],
      wantAfter: [anvilHangarList, bastionHangarList],
    },
    {
      name: 'the list page over a global resource asks for its first page once across a change of the tenant',
      script: { [shipClassPage]: [] },
      build: (store) => ({ settled: listPage(store, 'ShipClasses', 'ship-classes', 'designation') }),
      wantFirst: [shipClassPage],
      wantAfter: [shipClassPage],
    },
    {
      name: 'the list page over a domain-scoped resource asks for a first page again, naming the new tenant',
      script: { [anvilHangarPage]: [], [bastionHangarPage]: [] },
      build: (store) => ({ settled: listPage(store, 'Hangars', hangars, 'name') }),
      wantFirst: [anvilHangarPage],
      wantAfter: [anvilHangarPage, bastionHangarPage],
    },
    {
      name: 'the paged reader over a global resource asks for its first page once across a change of the tenant',
      script: { [shipClassPaged]: [] },
      build: (store) => {
        const pager = store.resourcePage(signal({ route: 'ship-classes', columns: ['id', 'designation'] }));
        return { settled: () => pager.status() === 'resolved', destroy: () => pager.destroy() };
      },
      wantFirst: [shipClassPaged],
      wantAfter: [shipClassPaged],
    },
    {
      name: 'the paged reader over a domain-scoped resource asks for a first page again, naming the new tenant',
      script: { [anvilHangarPaged]: [], [bastionHangarPaged]: [] },
      build: (store) => {
        const pager = store.resourcePage(signal({ route: hangars, columns: ['id', 'name'] }));
        return { settled: () => pager.status() === 'resolved', destroy: () => pager.destroy() };
      },
      wantFirst: [anvilHangarPaged],
      wantAfter: [anvilHangarPaged, bastionHangarPaged],
    },
    {
      name: 'the key lookup over a global resource reads once across a change of the tenant',
      script: { [shipClassList]: [] },
      build: (store) => {
        const ref = store.resourceListByKeys(signal('ship-classes'), signal('id'), signal(['c1']), signal(['id', 'designation']));
        return { settled: () => ref.status() === 'resolved' };
      },
      wantFirst: [shipClassList],
      wantAfter: [shipClassList],
    },
    {
      name: 'the key lookup over a domain-scoped resource reads again, naming the new tenant',
      script: { [anvilHangarKeys]: [], [bastionHangarKeys]: [] },
      build: (store) => {
        const ref = store.resourceListByKeys(signal(hangars), signal('id'), signal(['h1']), signal(['id', 'name']));
        return { settled: () => ref.status() === 'resolved' };
      },
      wantFirst: [anvilHangarKeys],
      wantAfter: [anvilHangarKeys, bastionHangarKeys],
    },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const script = scripted(Object.fromEntries(Object.entries(tt.script).map(([url, rows]) => [url, { rows }])));
      const domain = signal<Domain | undefined>('anvil' as Domain);
      const store = storeOver(script, domain);
      const reader = tt.build(store);
      await settle(reader.settled);
      expect(script.requests.map((r) => r.url)).toEqual(tt.wantFirst);

      domain.set('bastion' as Domain);
      await settle(() => script.requests.length >= tt.wantAfter.length && reader.settled());
      // A few more rounds, so a request the change should not cause has every chance to show.
      await settle(() => false, 5);
      expect(script.requests.map((r) => r.url)).toEqual(tt.wantAfter);
      reader.destroy?.();
    });
  }
});
