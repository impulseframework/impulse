import { describe, expect, it } from 'bun:test';
import { Method } from './brands';
import { createClient, MethodAnswer, MethodHandle, methodHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { ApiError, Transport, dryRunHeader } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The method handle's contract for a method that declares its statuses (@answers):
// a declared code resolves with { status, result }, an undeclared one throws, an
// answerless 204 resolves with nothing, and a dry run treats a declared 4xx as the
// refusal it is.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {
    CompleteMission: {
      method: 'CompleteMission' as Method,
      property: 'completeMission',
      route: 'complete-mission',
      scope: 'global',
      answers: true,
      statuses: [200, 409],
    },
    ArchiveMission: {
      method: 'ArchiveMission' as Method,
      property: 'archiveMission',
      route: 'archive-mission',
      scope: 'global',
      statuses: [204],
    },
    HoldMission: {
      method: 'HoldMission' as Method,
      property: 'holdMission',
      route: 'hold-mission',
      scope: 'global',
    },
  },
};

interface Settlement {
  fee: string;
  net: string;
}

interface Api {
  completeMission: MethodHandle<{ missionId: string }, MethodAnswer<200 | 409, Settlement>>;
  archiveMission: MethodHandle<{ missionId: string }>;
  holdMission: MethodHandle<{ missionId: string; reason: string }>;
}

function api(transport: Transport): Api {
  return createClient<Api, Record<string, never>>(descriptor, { baseUrl: '/api', transport }) as unknown as Api;
}

describe('a method that declares its statuses', () => {
  it('resolves a declared 2xx with the status and the typed result', async () => {
    const transport = scriptedTransport({ status: 200, body: { fee: '15000', net: '13500' } });
    const answer = await api(transport).completeMission.execute({ missionId: 'm1' });
    expect(answer).toEqual({ status: 200, result: { fee: '15000', net: '13500' } });
  });

  it('resolves a declared 4xx the same way: the method refused, and said why in its own type', async () => {
    const transport = scriptedTransport({ status: 409, body: { fee: '15000', net: '-6500' } });
    const answer = await api(transport).completeMission.execute({ missionId: 'm1' });
    expect(answer).toEqual({ status: 409, result: { fee: '15000', net: '-6500' } });
    expect(transport.requests[0].url).toBe('/api/complete-mission');
    expect(transport.requests[0].headers?.[dryRunHeader]).toBeUndefined();
  });

  it('throws ApiError for a status the method did not declare', async () => {
    const transport = scriptedTransport({
      status: 403,
      body: { message: 'user (lead) does not have (Execute) on CompleteMission' },
    });
    await expect(api(transport).completeMission.execute({ missionId: 'm1' })).rejects.toBeInstanceOf(ApiError);
  });

  it('throws ApiError for a 5xx even when 4xx answers are declared', async () => {
    const transport = scriptedTransport({ status: 500, body: { message: 'boom' } });
    await expect(api(transport).completeMission.execute({ missionId: 'm1' })).rejects.toBeInstanceOf(ApiError);
  });

  it('a dry run rejects a declared 4xx as the refusal it is, carrying the typed body', async () => {
    const transport = scriptedTransport({ status: 409, body: { fee: '15000', net: '-6500' } });
    const call = api(transport).completeMission.dryRun({ missionId: 'm1' });
    await expect(call).rejects.toBeInstanceOf(ApiError);
    await call.catch((e: ApiError) => expect(e.body).toEqual({ fee: '15000', net: '-6500' }));
    expect(transport.requests[0].headers?.[dryRunHeader]).toBe('true');
  });

  it('an answerless method declaring 204 resolves with nothing', async () => {
    const transport = scriptedTransport({ status: 204, body: undefined });
    await expect(api(transport).archiveMission.execute({ missionId: 'm1' })).resolves.toBeUndefined();
  });
});

describe('a method without declared statuses', () => {
  it('resolves an empty 200 with nothing', async () => {
    const transport = scriptedTransport({ status: 200, body: undefined });
    await expect(api(transport).holdMission.execute({ missionId: 'm1', reason: 'weather' })).resolves.toBeUndefined();
  });

  it('throws ApiError for every 4xx', async () => {
    const transport = scriptedTransport({ status: 409, body: { message: 'already on hold' } });
    await expect(api(transport).holdMission.execute({ missionId: 'm1', reason: 'weather' })).rejects.toBeInstanceOf(
      ApiError,
    );
  });
});

// A page that learns its methods at run time reaches one by its name: the same handle
// the typed client carries, bound to the tenant for a tenant's method.
describe('a method reached by its name', () => {
  const tenanted: ApiDescriptor = {
    ...descriptor,
    domainRoute: { segment: 'sectors', param: 'sectorID' },
    methods: {
      ...descriptor.methods,
      DisableCrewSectorUser: {
        method: 'DisableCrewSectorUser' as Method,
        property: 'disableCrewSectorUser',
        route: 'disable-crew-sector-user',
        scope: 'domain',
        statuses: [204],
      },
    },
  };
  const client = (transport: Transport) => createClient(tenanted, { baseUrl: '/api', transport });

  it("posts a tenant's method under the tenant's segment", async () => {
    const transport = scriptedTransport({ status: 204, body: null });
    await methodHandle(client(transport), 'DisableCrewSectorUser', 'anvil').execute({ userId: 'u1' });
    expect(transport.requests[0].url).toBe('/api/sectors/anvil/disable-crew-sector-user');
    expect(transport.requests[0].body).toEqual({ userId: 'u1' });
  });

  it("refuses a tenant's method with no tenant, before anything is sent", () => {
    const transport = scriptedTransport({ status: 204, body: null });
    expect(() => methodHandle(client(transport), 'DisableCrewSectorUser')).toThrow('is domain-scoped');
    expect(transport.requests).toHaveLength(0);
  });

  it('posts a global method at its own route, whatever tenant is given', async () => {
    const transport = scriptedTransport({ status: 200, body: null });
    await methodHandle(client(transport), 'HoldMission', 'anvil').execute({ missionId: 'm1', reason: 'weather' });
    expect(transport.requests[0].url).toBe('/api/hold-mission');
  });

  it('resolves like the typed handle: a declared status with its result', async () => {
    const transport = scriptedTransport({ status: 409, body: { fee: '1', net: '-1' } });
    const answer = await methodHandle(client(transport), 'CompleteMission').execute({ missionId: 'm1' });
    expect(answer).toEqual({ status: 409, result: { fee: '1', net: '-1' } });
  });

  it('refuses a name the API does not describe, naming it', () => {
    expect(() => methodHandle(client(scriptedTransport({ status: 200, body: null })), 'LaunchNothing')).toThrow(
      'LaunchNothing is no method of this API',
    );
  });
});
