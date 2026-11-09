import { describe, expect, it } from 'bun:test';
import { Resource } from './brands';
import { createClient, ResourceHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { Operation, OperationRoute, operationRoute } from './operations';
import { Transport } from './transport';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';

// client.batch delivers operations to the endpoint that can commit them: the
// consolidated route for consolidated resources, a standalone resource's own PATCH
// route for its operations (the generator's consolidated handler does not know that
// resource, so its path is the key alone and would read as a resource name there), and
// a refusal when one batch mixes the two.

const descriptor: ApiDescriptor = {
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  consolidatedRoute: 'resources',
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    Missions: {
      resource: 'Missions' as Resource,
      property: 'missions',
      route: 'missions',
      scope: 'domain',
      consolidated: true,
      keys: ['id'],
      operations: ['list', 'read', 'create', 'patch', 'remove'],
      patchable: ['notes'],
    },
    Clients: {
      resource: 'Clients' as Resource,
      property: 'clients',
      route: 'clients',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read', 'create', 'patch', 'remove'],
      patchable: ['name', 'trusted'],
    },
    Hangars: {
      resource: 'Hangars' as Resource,
      property: 'hangars',
      route: 'hangars',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read', 'create', 'patch', 'remove'],
      patchable: ['name'],
    },
  },
  methods: {},
};

interface Client {
  id: string;
  name: string;
  trusted: boolean;
}
interface Mission {
  id: string;
  notes: string;
}
interface Hangar {
  id: string;
  name: string;
}

// The handles as the generator types them: every operation, a Create without the
// generated key, and a Patch over the patchable fields.
type Ops = 'list' | 'read' | 'create' | 'patch' | 'remove' | 'batch';
interface Global {
  clients: ResourceHandle<Client, [string], Ops, Omit<Client, 'id'>, Partial<Pick<Client, 'name' | 'trusted'>>>;
}
interface Sector {
  missions: ResourceHandle<Mission, [string], Ops, Omit<Mission, 'id'>, Partial<Pick<Mission, 'notes'>>>;
  hangars: ResourceHandle<Hangar, [string], Ops, Omit<Hangar, 'id'>, Partial<Pick<Hangar, 'name'>>>;
}

/** A server that commits whatever it is sent: every request a 200 with an empty result. */
function recording(): ScriptedTransport {
  return scriptedTransport({ status: 200, body: {} });
}

function api(transport: Transport) {
  return createClient<Global, Sector>(descriptor, { baseUrl: '/api', transport });
}

describe('client.batch', () => {
  it('sends a consolidated resource to the consolidated endpoint with the full path', async () => {
    const transport = recording();
    const client = api(transport);
    await client.batch([client.domain('anvil').missions.ops.patch(['m1'], { notes: 'called twice' })]);
    expect(transport.requests).toHaveLength(1);
    expect(transport.requests[0].url).toBe('/api/resources');
    expect(transport.requests[0].body).toEqual([
      { op: 'patch', path: '/sectors/anvil/missions/m1', value: { notes: 'called twice' } },
    ]);
  });

  it('sends a standalone resource to its own route with the key as the path', async () => {
    const transport = recording();
    const client = api(transport);
    await client.batch([client.clients.ops.patch(['c1'], { trusted: true })]);
    expect(transport.requests).toHaveLength(1);
    expect(transport.requests[0].url).toBe('/api/clients');
    expect(transport.requests[0].body).toEqual([{ op: 'patch', path: '/c1', value: { trusted: true } }]);
  });

  it('sends a domain-scoped standalone resource to its scoped route', async () => {
    const transport = recording();
    const client = api(transport);
    await client.batch([client.domain('anvil').hangars.ops.add({ name: 'Bay 3' })]);
    expect(transport.requests[0].url).toBe('/api/sectors/anvil/hangars');
    expect(transport.requests[0].body).toEqual([{ op: 'add', path: '/', value: { name: 'Bay 3' } }]);
  });

  it('keeps several operations of one standalone resource in one request', async () => {
    const transport = recording();
    const client = api(transport);
    await client.batch([client.clients.ops.patch(['c1'], { trusted: true }), client.clients.ops.remove(['c2'])]);
    expect(transport.requests).toHaveLength(1);
    expect(transport.requests[0].url).toBe('/api/clients');
  });

  it('refuses operations for two endpoints before sending anything', async () => {
    const transport = recording();
    const client = api(transport);
    const sector = client.domain('anvil');
    await expect(
      client.batch([
        sector.missions.ops.patch(['m1'], { notes: 'x' }),
        client.clients.ops.patch(['c1'], { trusted: true }),
      ]),
    ).rejects.toThrow(/resources, clients/);
    expect(transport.requests).toHaveLength(0);
  });

  it('binds the route so that it survives a spread and stays off the wire', async () => {
    const transport = recording();
    const client = api(transport);
    const copy: Operation = { ...client.clients.ops.patch(['c1'], { trusted: true }) };
    expect(operationRoute(copy)).toBe('clients');
    expect(copy[OperationRoute]).toBe('clients');
    expect(JSON.parse(JSON.stringify(copy))).toEqual({ op: 'patch', path: '/c1', value: { trusted: true } });
    await client.batch([copy]);
    expect(transport.requests[0].url).toBe('/api/clients');
  });

  it('leaves a consolidated operation unbound', () => {
    const client = api(recording());
    expect(operationRoute(client.domain('anvil').missions.ops.remove(['m1']))).toBeUndefined();
  });

  it("routes the handle's own patch the same way", async () => {
    const transport = recording();
    const client = api(transport);
    await client.clients.patch(['c1'], { name: 'Halvard Freight' });
    expect(transport.requests[0].url).toBe('/api/clients');
    expect(transport.requests[0].body).toEqual([{ op: 'patch', path: '/c1', value: { name: 'Halvard Freight' } }]);
  });
});

// A create answers the key the server generated for the new row: a consolidated route
// keys the new keys by resource, a standalone route answers them under `ids`, and a server
// from before the key changed sent `iDs`, which the client still reads.
describe("a create's new key", () => {
  const answering = (body: unknown): ScriptedTransport => scriptedTransport({ status: 200, body });

  it('is read by the resource name from a consolidated route', async () => {
    const client = api(answering({ missions: ['m-9'] }));
    expect(await client.domain('anvil').missions.create({ notes: 'first light' })).toBe('m-9');
  });

  it('is read under ids from a standalone route', async () => {
    const client = api(answering({ ids: ['c-9'] }));
    expect(await client.clients.create({ name: 'Anvil', trusted: false })).toBe('c-9');
  });

  it('is read under iDs from a server that still sends it, ids first where both stand', async () => {
    const client = api(answering({ iDs: ['c-9'] }));
    expect(await client.clients.create({ name: 'Anvil', trusted: false })).toBe('c-9');
    const both = api(answering({ ids: ['c-10'], iDs: ['c-9'] }));
    expect(await both.clients.create({ name: 'Anvil', trusted: false })).toBe('c-10');
  });

  it('is undefined when the answer names none', async () => {
    const client = api(answering({}));
    expect(await client.clients.create({ name: 'Anvil', trusted: false })).toBeUndefined();
  });
});
