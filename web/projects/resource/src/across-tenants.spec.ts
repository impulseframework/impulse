import { describe, expect, it } from 'bun:test';
import { Domain, ListAcrossTenantsPermission, ListPermission, Resource } from './brands';
import { AcrossTenantsHandle, AnyResourceHandle, InTenant, acrossTenantsHandle, createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { scriptedTransport } from '@impulseframework/resource/testing';

// Missions is declared for the list across tenants; Refits is a tenant's alone.
const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  live: { renewRoute: 'live/renew', unsubscribeRoute: 'live/unsubscribe', tokenRoute: 'live/token' },
  resources: {
    Missions: {
      resource: 'Missions' as Resource,
      property: 'missions',
      route: 'missions',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
      order: [{ field: 'deadline', direction: 'asc' }],
      acrossTenants: true,
    },
    Refits: {
      resource: 'Refits' as Resource,
      property: 'refits',
      route: 'refits',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
  },
  methods: {},
};

interface Mission {
  id: string;
  title: string;
  fee?: string;
}

interface Sector {
  missions: AnyResourceHandle<Mission>;
  refits: AnyResourceHandle<{ id: string }>;
}
interface Across {
  missions: AcrossTenantsHandle<Mission, [string]>;
}

interface Sent {
  method: string;
  url: string;
  headers: Record<string, string>;
  body: unknown;
}

/** A client over a transport that records each request and answers the scripted rows by URL. */
function clientOver(pages: Record<string, { rows: unknown[]; headers?: Record<string, string> }>) {
  const sent: Sent[] = [];
  const warnings: string[] = [];
  const transport = scriptedTransport((request) => {
    sent.push({ method: request.method, url: request.url, headers: request.headers ?? {}, body: request.body });
    const page = pages[request.url];
    if (!page) {
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
    return { status: 200, body: page.rows, headers: page.headers ?? {} };
  });
  const client = createClient<Record<never, never>, Sector, Across>(descriptor, {
    baseUrl: '/api',
    transport,
    warn: (message) => warnings.push(message),
  });
  return { client, sent, warnings };
}

const anvil = 'a1111111-1111-4111-8111-111111111111' as Domain;
const bastion = 'b2222222-2222-4222-8222-222222222222' as Domain;

describe('a list across tenants', () => {
  it('is served at the resource route without the tenant segment, each row naming its tenant', async () => {
    const { client, sent } = clientOver({
      '/api/missions': {
        rows: [
          { id: 'm1', title: 'Hauler', fee: '8000', zzTenant: anvil },
          { id: 'm9', title: 'Beacon', zzTenant: bastion },
        ],
      },
    });

    const rows = await client.acrossTenants.missions.list();

    expect(sent.map((request) => `${request.method} ${request.url}`)).toEqual(['GET /api/missions']);
    expect(rows.map((row) => client.acrossTenants.missions.tenantOf(row))).toEqual([anvil, bastion]);
    expect(client.acrossTenants.missions.keyOf(rows[1])).toEqual(['m9']);
    expect(client.acrossTenants.missions.url()).toBe('/api/missions');
    // The tenant's own list stays under the tenant's segment.
    expect(client.domain(anvil).missions.url()).toBe(`/api/sectors/${anvil}/missions`);
  });

  it('narrows the set with domains beside the list parameters', async () => {
    const target = `/api/missions?filter=title%3Acontains%3Abeacon&sort=deadline%3Adesc&limit=10&count=true&domains=${anvil}%2C${bastion}`;
    const { client, sent } = clientOver({ [target]: { rows: [], headers: { 'total-count': '0' } } });

    const page = await client.acrossTenants.missions.page({
      filter: { field: 'title', op: 'contains', value: 'beacon' },
      sort: { field: 'deadline' as never, direction: 'desc' },
      limit: 10,
      count: true,
      domains: [anvil, bastion],
    });

    expect(sent.map((request) => request.url)).toEqual([target]);
    expect(page.total).toBe(0);
    expect(page.rows).toEqual([]);
  });

  it('sends an empty domains as written, a set of no tenant', async () => {
    const { client, sent } = clientOver({ '/api/missions?domains=': { rows: [] } });

    // The server refuses an empty entry; the client does not turn "no tenant" into
    // "every tenant" by dropping the parameter.
    await client.acrossTenants.missions.list({ domains: [] });

    expect(sent.map((request) => request.url)).toEqual(['/api/missions?domains=']);
  });

  it('follows next and prev as the server named them, the narrowing in the links', async () => {
    const first = `/api/missions?limit=1&domains=${anvil}`;
    const second = `/api/missions?domains=${anvil}&limit=1&cursor=v4.local.two`;
    const { client, sent } = clientOver({
      [first]: { rows: [{ id: 'm1', title: 'Hauler', zzTenant: anvil }], headers: { link: `<${second}>; rel="next"` } },
      [second]: { rows: [{ id: 'm2', title: 'Corvid', zzTenant: anvil }], headers: { link: `<${first}>; rel="prev"` } },
    });

    const page = await client.acrossTenants.missions.page({ limit: 1, domains: [anvil] });
    const next = await page.next?.();
    const back = await next?.prev?.();

    expect(next?.rows.map((row) => row.id)).toEqual(['m2']);
    expect(back?.rows.map((row) => row.id)).toEqual(['m1']);
    expect(sent.map((request) => request.url)).toEqual([first, second, first]);
  });

  it('carries a sensitive filter in the body and the narrowing in the URL', async () => {
    const target = `/api/missions?domains=${bastion}`;
    const { client, sent } = clientOver({ [target]: { rows: [] } });

    await client.acrossTenants.missions.list({ filter: 'title:contains:beacon', sensitiveFilter: true, domains: [bastion] });

    expect(sent).toHaveLength(1);
    expect(sent[0].method).toBe('POST');
    expect(sent[0].url).toBe(target);
    expect(sent[0].body).toEqual({ filter: 'title:contains:beacon' });
  });

  it('is plain: a list nobody asked to be live carries no subscription, and none is announced', async () => {
    const { client, sent, warnings } = clientOver({ '/api/missions': { rows: [] } });

    await client.acrossTenants.missions.list();
    // Asked for live while no live session runs, the list is read plain, as a tenant's
    // list is (live.spec.ts pins the live list across tenants).
    await client.acrossTenants.missions.page({ live: true });

    expect(warnings).toEqual([]);
    for (const request of sent) {
      expect(request.url).toBe('/api/missions');
      expect(Object.keys(request.headers).map((name) => name.toLowerCase())).not.toContain('x-subscribe');
    }
  });

  it('offers a handle for a declared resource alone', () => {
    const { client } = clientOver({});

    expect(Object.keys(client.acrossTenants)).toEqual(['missions']);
    expect(acrossTenantsHandle(client, 'Missions').url()).toBe('/api/missions');
    expect(() => acrossTenantsHandle(client, 'Refits')).toThrow(
      'Refits is not listed across tenants: declare @listAcrossTenants on its struct and regenerate, or list it inside one tenant with client.domain(...)',
    );
    expect(() => acrossTenantsHandle(client, 'Hangars')).toThrow(
      'this API declares no Hangars resource; it cannot be listed across tenants from here',
    );
  });

  it('asks the global digest who the list is for, whatever tenant is open', async () => {
    // The global digest answers the list across tenants; each tenant's own digest answers
    // that tenant's list and says nothing of the other question.
    const digests: Record<string, unknown> = {
      '/api/permission-digest': { Missions: { ListAcrossTenants: 'granted' } },
      [`/api/permission-digest?domain=${anvil}`]: { Missions: { List: 'granted' }, Refits: { List: 'granted' } },
    };
    const client = createClient<Record<never, never>, Sector, Across>(descriptor, {
      baseUrl: '/api',
      transport: scriptedTransport((request) => ({ status: 200, body: digests[request.url] ?? {} })),
    });

    // Fail closed until the global digest has loaded.
    expect(client.acrossTenants.missions.can()).toBe(false);
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(anvil);

    expect(client.acrossTenants.missions.can()).toBe(true);
    expect(client.can(ListAcrossTenantsPermission, 'Missions' as Resource)).toBe(true);
    expect(client.can(ListAcrossTenantsPermission, 'Missions' as Resource, anvil)).toBe(true);
    // A resource the server did not answer for, and the tenant's own questions as ever.
    expect(client.can(ListAcrossTenantsPermission, 'Refits' as Resource, anvil)).toBe(false);
    expect(client.can(ListPermission, 'Missions' as Resource, anvil)).toBe(true);
    expect(client.can(ListPermission, 'Missions' as Resource)).toBe(false);
  });

  it('answers nobody the list is for when the global digest names no tenant of theirs', async () => {
    const client = createClient<Record<never, never>, Sector, Across>(descriptor, {
      baseUrl: '/api',
      transport: scriptedTransport(() => ({ status: 200, body: {} })),
    });
    await client.permissions.loadDigest();

    expect(client.acrossTenants.missions.can()).toBe(false);
  });

  it('is empty on an API that declares no list across tenants', () => {
    const plain: ApiDescriptor = {
      ...descriptor,
      resources: { Refits: descriptor.resources['Refits'] },
    };
    const client = createClient<Record<never, never>, Sector>(plain, { baseUrl: '/api', transport: scriptedTransport(() => ({ status: 200, body: [] })) });

    expect(client.acrossTenants).toEqual({});
  });
});

// The query type: no capability envelope on a list across tenants.
const typed = undefined as unknown as AcrossTenantsHandle<Mission, [string]>;
export async function acrossTenantsTypes(): Promise<void> {
  const rows: InTenant<Mission>[] = await typed.list({ columns: ['title'], domains: ['x'] });
  void rows;
  // @ts-expect-error capabilities is asked on the row's page inside its tenant
  await typed.list({ capabilities: ['Update'] });
}
