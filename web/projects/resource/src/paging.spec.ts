import { describe, expect, it } from 'bun:test';
import { Resource } from './brands';
import { createClient, resolveAbsolute, AnyResourceHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { listSearchParams, parseLinkHeader } from './query';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    Missions: {
      resource: 'Missions' as Resource,
      property: 'missions',
      route: 'missions',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
    },
    Sectors: {
      resource: 'Sectors' as Resource,
      property: 'sectors',
      route: 'sectors',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 50 },
    },
    // A declared @order: the server pages a sort-less list by it.
    Consignments: {
      resource: 'Consignments' as Resource,
      property: 'consignments',
      route: 'consignments',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 20, max: 100 },
      order: [{ field: 'expiresOn', direction: 'asc' }],
    },
    // A key-less resource (no @primarykey): served whole on every request, list alone,
    // no read, no page.
    StandingOrders: {
      resource: 'StandingOrders' as Resource,
      property: 'standingOrders',
      route: 'standing-orders',
      scope: 'global',
      consolidated: false,
      keys: [],
      operations: ['list'],
      page: { default: 50 },
    },
  },
  methods: {},
};

interface Row {
  id: string;
}

/** A transport scripted by URL: each entry answers one request with rows and headers, an unscripted URL is a 404. */
function scripted(pages: Record<string, { rows: Row[]; headers?: Record<string, string> }>): ScriptedTransport {
  return scriptedTransport((request) => {
    const page = pages[request.url];
    if (!page) {
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
    return { status: 200, body: page.rows, headers: page.headers ?? {} };
  });
}

describe('parseLinkHeader', () => {
  it('reads each relation and its URL exactly as written', () => {
    const header =
      '</api/missions?sort=deadline&limit=4&cursor=v4.local.aaa>; rel="next", </api/missions?sort=deadline&limit=4&cursor=v4.local.bbb>; rel="prev"';
    expect(parseLinkHeader(header)).toEqual({
      next: '/api/missions?sort=deadline&limit=4&cursor=v4.local.aaa',
      prev: '/api/missions?sort=deadline&limit=4&cursor=v4.local.bbb',
    });
  });

  it('answers nothing for an absent header', () => {
    expect(parseLinkHeader(undefined)).toEqual({});
  });
});

describe('listSearchParams paging', () => {
  it('sends limit, all, cursor, and count, and never offset', () => {
    expect(listSearchParams<Row>({ limit: 'all' }).toString()).toBe('limit=all');
    expect(listSearchParams<Row>({ limit: 10, cursor: 'v4.local.x', count: true }).toString()).toBe(
      'limit=10&cursor=v4.local.x&count=true',
    );
    expect(listSearchParams<Row>({ count: false }).toString()).toBe('');
  });
});

describe('resolveAbsolute', () => {
  it('joins a path-absolute reference to the base origin', () => {
    expect(resolveAbsolute('https://host.example/api', '/api/missions?cursor=x')).toBe(
      'https://host.example/api/missions?cursor=x',
    );
  });

  it('passes a same-origin reference and a complete URL through', () => {
    expect(resolveAbsolute('/api', '/api/missions?cursor=x')).toBe('/api/missions?cursor=x');
    expect(resolveAbsolute('/api', 'https://other.example/api/missions')).toBe('https://other.example/api/missions');
  });
});

describe('page', () => {
  it('reads the rows, the total, and follows next and prev as the server named them', async () => {
    const first = '/api/missions?sort=deadline&limit=2&count=true';
    const second = '/api/missions?sort=deadline&limit=2&cursor=v4.local.two';
    const backToFirst = '/api/missions?sort=deadline&limit=2&cursor=v4.local.one';
    const transport = scripted({
      [first]: {
        rows: [{ id: 'a' }, { id: 'b' }],
        headers: { 'total-count': '3', link: `<${second}>; rel="next"` },
      },
      [second]: {
        rows: [{ id: 'c' }],
        headers: { link: `<${backToFirst}>; rel="prev"` },
      },
      [backToFirst]: {
        rows: [{ id: 'a' }, { id: 'b' }],
        headers: { link: `<${second}>; rel="next"` },
      },
    });
    const api = createClient<{ missions: AnyResourceHandle<Row> }, unknown>(descriptor, { baseUrl: '/api', transport });

    const page = await api.missions.page({ sort: { field: 'deadline' as never }, limit: 2, count: true });
    expect(page.rows).toEqual([{ id: 'a' }, { id: 'b' }]);
    expect(page.total).toBe(3);
    expect(page.prev).toBeUndefined();
    expect(page.next).toBeDefined();

    const next = await page.next!();
    expect(next.rows).toEqual([{ id: 'c' }]);
    expect(next.total).toBeUndefined();
    expect(next.next).toBeUndefined();

    const back = await next.prev!();
    expect(back.rows).toEqual([{ id: 'a' }, { id: 'b' }]);
    expect(transport.requests.map((r) => r.url)).toEqual([first, second, backToFirst]);
    expect(transport.requests.every((r) => r.method === 'GET')).toBe(true);
  });
});

describe('page reload', () => {
  it('repeats the request that produced the page: the first page by its parameters, a later one by its cursor', async () => {
    const first = '/api/missions?sort=deadline&count=true';
    const second = '/api/missions?sort=deadline&cursor=v4.local.two';
    const transport = scripted({
      [first]: { rows: [{ id: 'a' }], headers: { link: `<${second}>; rel="next"`, 'total-count': '2' } },
      [second]: { rows: [{ id: 'b' }], headers: { link: `<${first}>; rel="prev"` } },
    });
    const api = createClient<{ missions: AnyResourceHandle<Row> }, unknown>(descriptor, { baseUrl: '/api', transport });
    const page = await api.missions.page({ sort: { field: 'deadline' as never }, count: true });
    const again = await page.reload();
    expect(again.rows).toEqual([{ id: 'a' }]);
    expect(again.total).toBe(2);
    const turned = await again.next!();
    const turnedAgain = await turned.reload();
    expect(turnedAgain.rows).toEqual([{ id: 'b' }]);
    expect(transport.requests.map((r) => r.url)).toEqual([first, first, second, second]);
  });
});

describe('sensitive filter', () => {
  it('posts the filter in the body and follows the walk with the same body', async () => {
    const first = '/api/missions?sort=deadline&limit=1';
    const second = '/api/missions?sort=deadline&limit=1&cursor=v4.local.two';
    const transport = scripted({
      [first]: { rows: [{ id: 'a' }], headers: { link: `<${second}>; rel="next"` } },
      [second]: { rows: [{ id: 'b' }] },
    });
    const api = createClient<{ missions: AnyResourceHandle<Row> }, unknown>(descriptor, { baseUrl: '/api', transport });

    const page = await api.missions.page({
      filter: 'contactEmail:eq:cleo@halvard.example',
      sensitiveFilter: true,
      sort: { field: 'deadline' as never },
      limit: 1,
    });
    const next = await page.next!();
    expect(next.rows).toEqual([{ id: 'b' }]);
    expect(transport.requests.map((r) => [r.method, r.url, r.body])).toEqual([
      ['POST', first, { filter: 'contactEmail:eq:cleo@halvard.example' }],
      ['POST', second, { filter: 'contactEmail:eq:cleo@halvard.example' }],
    ]);
  });

  it('stays a GET when the query carries no filter', async () => {
    const transport = scripted({ '/api/sectors': { rows: [] } });
    const api = createClient<{ sectors: AnyResourceHandle<Row> }, unknown>(descriptor, { baseUrl: '/api', transport });
    await api.sectors.list({ sensitiveFilter: true });
    expect(transport.requests[0].method).toBe('GET');
  });
});

describe('whole read', () => {
  it('is the explicit limit: all on a resource with no maximum, one request and no Link', async () => {
    const transport = scripted({
      '/api/sectors?limit=all': { rows: [{ id: 'a' }, { id: 'b' }] },
    });
    const api = createClient<{ sectors: AnyResourceHandle<Row> }, unknown>(descriptor, { baseUrl: '/api', transport });
    expect(await api.sectors.list({ limit: 'all' })).toEqual([{ id: 'a' }, { id: 'b' }]);
    expect(transport.requests).toHaveLength(1);
  });
});

describe('key-less resource', () => {
  it('lists the whole resource with no limit and no cursor, whatever the query carries', async () => {
    const whole = '/api/standing-orders';
    const sorted = '/api/standing-orders?sort=section';
    const transport = scripted({
      [whole]: { rows: [{ id: 'a' }, { id: 'b' }] },
      [sorted]: { rows: [{ id: 'b' }, { id: 'a' }] },
    });
    const api = createClient<{ standingOrders: AnyResourceHandle<Row> }, unknown>(descriptor, {
      baseUrl: '/api',
      transport,
    });

    // AnyResourceHandle types its key as string[], so a limit passes the type here; the
    // generated handle's empty key tuple refuses it at compile time (keyless.types.spec.ts).
    expect(await api.standingOrders.list({ limit: 10, cursor: 'v4.local.anything' })).toEqual([
      { id: 'a' },
      { id: 'b' },
    ]);
    expect(await api.standingOrders.list({ sort: { field: 'section' as never }, limit: 'all' })).toEqual([
      { id: 'b' },
      { id: 'a' },
    ]);
    expect(transport.requests.map((r) => r.url)).toEqual([whole, sorted]);
  });

  it('answers the whole list as one page with no neighbors', async () => {
    const whole = '/api/standing-orders';
    const transport = scripted({ [whole]: { rows: [{ id: 'a' }, { id: 'b' }] } });
    const api = createClient<{ standingOrders: AnyResourceHandle<Row> }, unknown>(descriptor, {
      baseUrl: '/api',
      transport,
    });

    const page = await api.standingOrders.page({ limit: 10 });
    expect(page.rows).toEqual([{ id: 'a' }, { id: 'b' }]);
    expect(page.next).toBeUndefined();
    expect(page.prev).toBeUndefined();
    expect(transport.requests.map((r) => r.url)).toEqual([whole]);
  });
});
