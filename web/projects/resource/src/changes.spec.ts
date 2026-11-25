import { describe, expect, it } from 'bun:test';
import { Resource } from './brands';
import { ChangeHistoryPage } from './changes';
import { createClient, ResourceHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { ApiError } from './transport';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';

// A record's history as the client reads it: a GET of the record's read route followed by
// /changes, in the record's scope, with after and field sent only when given, and the
// server's refusal thrown as the client's own error, as a read's is.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
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
    },
    Statements: {
      resource: 'Statements' as Resource,
      property: 'statements',
      route: 'statements',
      scope: 'global',
      consolidated: false,
      keys: ['clientId', 'period'],
      operations: ['list', 'read'],
      page: { default: 50 },
    },
  },
  methods: {},
};

const page: ChangeHistoryPage = {
  changes: [
    {
      at: '2026-11-23T10:15:00Z',
      sequence: 1,
      operation: 'update',
      source: 'gale',
      fields: [{ field: 'status', old: 'planned', new: 'underway' }],
    },
  ],
  next: '2026-11-23T10:15:00Z_1',
};

interface Mission {
  id: string;
  status: string;
}
interface Statement {
  clientId: string;
  period: string;
}

// The handles as the generator types a resource it serves reads of.
interface Global {
  statements: ResourceHandle<Statement, [string, string], 'list' | 'read'>;
}
interface Sector {
  missions: ResourceHandle<Mission, [string], 'list' | 'read'>;
}

function handles(transport: ScriptedTransport): {
  missions: Sector['missions'];
  statements: Global['statements'];
} {
  const client = createClient<Global, Sector>(descriptor, { baseUrl: '/api', transport });
  return { missions: client.domain('anvil').missions, statements: client.statements };
}

describe('changes', () => {
  it("asks the record's read route followed by /changes, with no parameter where none is given", async () => {
    const transport = scriptedTransport({ status: 200, body: page });
    const { missions } = handles(transport);

    expect(await missions.changes(['0193e2a7'])).toEqual(page);
    expect(transport.requests.map((r) => [r.method, r.url])).toEqual([
      ['GET', '/api/sectors/anvil/missions/0193e2a7/changes'],
    ]);
  });

  it('sends after and field each alone and both together', async () => {
    const transport = scriptedTransport({ status: 200, body: { changes: [] } });
    const { missions } = handles(transport);

    await missions.changes(['0193e2a7'], { after: '2026-11-23T10:15:00Z_1' });
    await missions.changes(['0193e2a7'], { field: 'status' });
    await missions.changes(['0193e2a7'], { after: '2026-11-23T10:15:00Z_1', field: 'status' });

    expect(transport.requests.map((r) => r.url)).toEqual([
      '/api/sectors/anvil/missions/0193e2a7/changes?after=2026-11-23T10%3A15%3A00Z_1',
      '/api/sectors/anvil/missions/0193e2a7/changes?field=status',
      '/api/sectors/anvil/missions/0193e2a7/changes?after=2026-11-23T10%3A15%3A00Z_1&field=status',
    ]);
  });

  it('spells a compound key as the read route does', async () => {
    const transport = scriptedTransport({ status: 200, body: { changes: [] } });
    const { statements } = handles(transport);

    await statements.changes(['c1', '2026-09']);

    expect(transport.requests.map((r) => r.url)).toEqual(['/api/statements/c1/2026-09/changes']);
  });

  it("throws a 404 as the client's ApiError, as a read's", async () => {
    const transport = scriptedTransport({ status: 404, body: { message: 'Missions keeps no change history' } });
    const { missions } = handles(transport);

    const refused = await missions.changes(['0193e2a7']).catch((error: unknown) => error);

    expect(refused).toBeInstanceOf(ApiError);
    expect((refused as ApiError).status).toBe(404);
    expect((refused as ApiError).message).toBe('Missions keeps no change history');
    expect((refused as ApiError).url).toBe('/api/sectors/anvil/missions/0193e2a7/changes');
  });
});
