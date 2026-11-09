import { describe, expect, it } from 'bun:test';
import { Method } from './brands';
import { createClient, MethodAnswer, MethodHandle } from './client';
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
