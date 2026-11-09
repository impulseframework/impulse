import { describe, expect, it } from 'bun:test';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import {
  ApiError,
  MaintenanceError,
  maintenanceHeader,
  maintenanceHeaderValue,
  retryAfterHeader,
  retryAfterSeconds,
} from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The server's answer while it is down for maintenance: a 503 carrying the marker is
// thrown as MaintenanceError with the server's Retry-After in seconds, recognized before a
// caller's declared answers and observed by the hook first; a 503 without the marker, or
// the marker on another status, is judged as any other answer.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {},
};

const marker = maintenanceHeader.toLowerCase();
const retryAfter = retryAfterHeader.toLowerCase();

function maintenance(headers: Record<string, string> = {}): { status: number; body: unknown; headers: Record<string, string> } {
  return { status: 503, body: undefined, headers: { [marker]: maintenanceHeaderValue, ...headers } };
}

describe('a 503 carrying the marker', () => {
  it('is thrown as MaintenanceError with the Retry-After, the hook seeing it first', async () => {
    const transport = scriptedTransport(maintenance({ [retryAfter]: '30' }));
    const seen: ApiError[] = [];
    const client = createClient(descriptor, { baseUrl: '/api', transport, onError: (error) => seen.push(error) });
    const call = client.request('GET', 'missions');
    await expect(call).rejects.toBeInstanceOf(MaintenanceError);
    const error = (await call.catch((e: unknown) => e)) as MaintenanceError;
    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(503);
    expect(error.retryAfter).toBe(30);
    expect(error.message).toBe('HTTP 503');
    expect(seen).toEqual([error]);
    expect(transport.requests.length).toBe(1);
  });

  const retryAfterCases: { name: string; header: string | undefined; want: number | undefined }[] = [
    { name: 'a whole number of seconds is read as given', header: '30', want: 30 },
    { name: 'zero is read as zero', header: '0', want: 0 },
    { name: 'spaces around the number are ignored', header: ' 15 ', want: 15 },
    { name: 'no header is undefined', header: undefined, want: undefined },
    { name: 'a value that is neither seconds nor a date is undefined', header: 'soon', want: undefined },
  ];

  for (const tt of retryAfterCases) {
    it(tt.name, async () => {
      const transport = scriptedTransport(maintenance(tt.header === undefined ? {} : { [retryAfter]: tt.header }));
      const client = createClient(descriptor, { baseUrl: '/api', transport });
      const error = (await client.request('GET', 'missions').catch((e: unknown) => e)) as MaintenanceError;
      expect(error).toBeInstanceOf(MaintenanceError);
      expect(error.retryAfter).toBe(tt.want);
    });
  }

  it('an HTTP date is read as the seconds from now until it', () => {
    const inNinety = new Date(Date.now() + 90 * 1000).toUTCString();
    const seconds = retryAfterSeconds(inNinety) ?? -1;
    expect(seconds).toBeGreaterThanOrEqual(89);
    expect(seconds).toBeLessThanOrEqual(91);
    expect(retryAfterSeconds(new Date(Date.now() - 60 * 1000).toUTCString())).toBe(0);
  });

  it('is recognized before a declared 503 answer', async () => {
    const transport = scriptedTransport(maintenance());
    const client = createClient(descriptor, { baseUrl: '/api', transport });
    await expect(client.request('POST', 'hold-mission', { body: {}, accept: [503] })).rejects.toBeInstanceOf(
      MaintenanceError,
    );
  });
});

describe('an answer that is not maintenance', () => {
  const cases: { name: string; status: number; headers: Record<string, string> }[] = [
    { name: 'a 503 without the marker', status: 503, headers: {} },
    { name: 'a 503 with the marker at another value', status: 503, headers: { [marker]: '0' } },
    { name: 'a 503 with the marker empty', status: 503, headers: { [marker]: '' } },
  ];

  for (const tt of cases) {
    it(`${tt.name} is an ordinary error, and the method's own answer when declared`, async () => {
      const transport = scriptedTransport({ status: tt.status, body: { message: 'overloaded' }, headers: tt.headers });
      const client = createClient(descriptor, { baseUrl: '/api', transport });
      const undeclared = client.request('GET', 'missions');
      await expect(undeclared).rejects.toBeInstanceOf(ApiError);
      await undeclared.catch((e: unknown) => {
        expect(e).not.toBeInstanceOf(MaintenanceError);
        expect((e as ApiError).message).toBe('overloaded');
      });
      await expect(client.request('POST', 'hold-mission', { body: {}, accept: [503] })).resolves.toEqual({
        message: 'overloaded',
      });
    });
  }

  it('the marker on a success changes nothing', async () => {
    const transport = scriptedTransport({ status: 200, body: [{ id: 'm1' }], headers: { [marker]: maintenanceHeaderValue } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });
    await expect(client.request('GET', 'missions')).resolves.toEqual([{ id: 'm1' }]);
  });
});
