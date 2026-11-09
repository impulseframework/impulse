import { describe, expect, it } from 'bun:test';
import { createClient, olderServerRetryDelays } from './client';
import { ApiDescriptor } from './descriptor';
import { ApiError, ApiVersionError, apiVersionHeader, timeZoneHeader } from './transport';
import { compareReleases, releaseVersion } from './versions';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The release header and the server's refusal of it: a build that names a release sends it
// on every request, a build that names none or `dev` sends nothing, a 412 carrying the
// server's release is the refusal and is recognized before a caller's declared answers,
// a server older than the build is retried after each configured pause before the refusal
// is thrown, and a build older than the server is refused at once.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {},
};

const header = apiVersionHeader.toLowerCase();

function refusal(serverVersion: string): { status: number; body: unknown; headers: Record<string, string> } {
  return { status: 412, body: { message: 'this release is no longer answered' }, headers: { [header]: serverVersion } };
}

describe('the release header', () => {
  const cases: { name: string; apiVersion: string | undefined; want: string | undefined }[] = [
    { name: 'a release is sent as given', apiVersion: '1.5.0', want: '1.5.0' },
    { name: 'a release with a leading v is sent as given', apiVersion: 'v1.5.0', want: 'v1.5.0' },
    { name: 'no release sends nothing', apiVersion: undefined, want: undefined },
    { name: 'an empty release sends nothing', apiVersion: '', want: undefined },
    { name: 'the dev build sends nothing', apiVersion: 'dev', want: undefined },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const transport = scriptedTransport({ status: 200, body: [] });
      const client = createClient(descriptor, { baseUrl: '/api', transport, apiVersion: tt.apiVersion });
      await client.request('GET', 'missions');
      expect(transport.requests[0].headers?.[apiVersionHeader]).toBe(tt.want);
    });
  }

  it('rides beside the request\'s own headers', async () => {
    const transport = scriptedTransport({ status: 200, body: undefined });
    const client = createClient(descriptor, { baseUrl: '/api', transport, apiVersion: '1.5.0', timeZone: 'America/New_York' });
    await client.request('POST', 'complete-mission', { body: {}, headers: { 'X-Dry-Run': 'true' } });
    expect(transport.requests[0].headers).toEqual({
      'X-Dry-Run': 'true',
      [apiVersionHeader]: '1.5.0',
      [timeZoneHeader]: 'America/New_York',
    });
  });
});

describe('the refusal', () => {
  it('a 412 carrying the server\'s release is thrown as ApiVersionError, the hook seeing it first', async () => {
    const transport = scriptedTransport(refusal('1.6.0'));
    const seen: ApiError[] = [];
    const client = createClient(descriptor, {
      baseUrl: '/api',
      transport,
      apiVersion: '1.5.0',
      onError: (error) => seen.push(error),
    });
    const call = client.request('GET', 'missions');
    await expect(call).rejects.toBeInstanceOf(ApiVersionError);
    const error = (await call.catch((e: unknown) => e)) as ApiVersionError;
    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(412);
    expect(error.serverVersion).toBe('1.6.0');
    expect(error.appVersion).toBe('1.5.0');
    expect(error.serverOlder).toBe(false);
    expect(error.message).toBe('this release is no longer answered');
    expect(seen).toEqual([error]);
    expect(transport.requests.length).toBe(1);
  });

  it('is recognized before a declared 412 answer', async () => {
    const transport = scriptedTransport(refusal('1.6.0'));
    const client = createClient(descriptor, { baseUrl: '/api', transport, apiVersion: '1.5.0' });
    await expect(client.request('POST', 'hold-mission', { body: {}, accept: [412] })).rejects.toBeInstanceOf(
      ApiVersionError,
    );
  });

  it('a 412 without the server\'s release is the method\'s own answer', async () => {
    const transport = scriptedTransport({ status: 412, body: { reason: 'stale' } });
    const client = createClient(descriptor, { baseUrl: '/api', transport, apiVersion: '1.5.0' });
    await expect(client.request('POST', 'hold-mission', { body: {}, accept: [412] })).resolves.toEqual({ reason: 'stale' });
    const undeclared = client.request('POST', 'hold-mission', { body: {} });
    await expect(undeclared).rejects.toBeInstanceOf(ApiError);
    await undeclared.catch((e: unknown) => expect(e).not.toBeInstanceOf(ApiVersionError));
  });

  it('a build that sends no release is still told when a server refuses it, as this build being the older', async () => {
    const transport = scriptedTransport(refusal('1.6.0'));
    const client = createClient(descriptor, { baseUrl: '/api', transport });
    const error = (await client.request('GET', 'missions').catch((e: unknown) => e)) as ApiVersionError;
    expect(error).toBeInstanceOf(ApiVersionError);
    expect(error.appVersion).toBe('');
    expect(error.serverOlder).toBe(false);
    expect(transport.requests.length).toBe(1);
  });
});

describe('a server older than the build', () => {
  it('is retried after each configured pause, then refused with serverOlder', async () => {
    const transport = scriptedTransport(refusal('1.4.0'));
    const client = createClient(descriptor, {
      baseUrl: '/api',
      transport,
      apiVersion: '1.5.0',
      olderServerRetryDelays: [1, 1, 1],
    });
    const error = (await client.request('GET', 'missions').catch((e: unknown) => e)) as ApiVersionError;
    expect(error).toBeInstanceOf(ApiVersionError);
    expect(error.serverOlder).toBe(true);
    expect(error.serverVersion).toBe('1.4.0');
    expect(transport.requests.length).toBe(4);
    expect(transport.requests.every((r) => r.headers?.[apiVersionHeader] === '1.5.0')).toBe(true);
  });

  it('a retry the new instance answers resolves as if nothing happened', async () => {
    let answered = 0;
    const transport = scriptedTransport(() => (answered++ < 2 ? refusal('1.4.0') : { status: 200, body: [{ id: 'm1' }] }));
    const client = createClient(descriptor, {
      baseUrl: '/api',
      transport,
      apiVersion: '1.5.0',
      olderServerRetryDelays: [1, 1, 1],
    });
    await expect(client.request('GET', 'missions')).resolves.toEqual([{ id: 'm1' }]);
    expect(transport.requests.length).toBe(3);
  });

  it('a build older than the server is not retried', async () => {
    const transport = scriptedTransport(refusal('1.6.0'));
    const client = createClient(descriptor, {
      baseUrl: '/api',
      transport,
      apiVersion: '1.5.0',
      olderServerRetryDelays: [1, 1, 1],
    });
    await expect(client.request('GET', 'missions')).rejects.toBeInstanceOf(ApiVersionError);
    expect(transport.requests.length).toBe(1);
  });

  it('the default schedule is three retries over ten seconds', () => {
    expect(olderServerRetryDelays).toEqual([1000, 3000, 6000]);
  });
});

describe('releases', () => {
  const releaseCases: { value: string | undefined; want: string | undefined }[] = [
    { value: undefined, want: undefined },
    { value: '', want: undefined },
    { value: '  ', want: undefined },
    { value: 'dev', want: undefined },
    { value: '1.5.0', want: '1.5.0' },
    { value: ' v1.5.0 ', want: 'v1.5.0' },
  ];

  for (const tt of releaseCases) {
    it(`releaseVersion(${JSON.stringify(tt.value)}) is ${JSON.stringify(tt.want)}`, () => {
      expect(releaseVersion(tt.value)).toBe(tt.want);
    });
  }

  const compareCases: { a: string; b: string; want: 'older' | 'same' | 'newer' }[] = [
    { a: '1.4.0', b: '1.5.0', want: 'older' },
    { a: '1.5.0', b: '1.5.0', want: 'same' },
    { a: 'v1.5.0', b: '1.5.0', want: 'same' },
    { a: '1.5.1', b: '1.5.0', want: 'newer' },
    { a: '1.10.0', b: '1.9.0', want: 'newer' },
    { a: '2.0.0', b: '1.99.99', want: 'newer' },
    { a: '1.5.0-rc.1', b: '1.5.0', want: 'older' },
    { a: '1.5.0-rc.2', b: '1.5.0-rc.1', want: 'newer' },
    { a: '1.5.0-rc.10', b: '1.5.0-rc.9', want: 'newer' },
    { a: '1.5.0-alpha', b: '1.5.0-beta', want: 'older' },
    { a: '1.5.0+build.7', b: '1.5.0', want: 'same' },
    { a: 'garbage', b: '0.0.1', want: 'older' },
    { a: 'garbage', b: 'dev', want: 'same' },
  ];

  for (const tt of compareCases) {
    it(`${tt.a} is ${tt.want} than/as ${tt.b}`, () => {
      const got = compareReleases(tt.a, tt.b);
      expect(got < 0 ? 'older' : got > 0 ? 'newer' : 'same').toBe(tt.want);
    });
  }
});

describe("the server's release", () => {
  it('is kept from every answer that names one, and told once per change', async () => {
    const transport = scriptedTransport({ status: 200, body: [], headers: { [header]: 'v2.0.0' } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });
    expect(client.serverRelease.get()).toBeUndefined();
    const seen: (string | undefined)[] = [];
    client.serverRelease.subscribe((release) => seen.push(release));
    await client.request('GET', 'user/methods');
    await client.request('GET', 'user/methods');
    expect(client.serverRelease.get()).toBe('v2.0.0');
    expect(seen).toEqual(['v2.0.0']);
  });

  it('is kept from a refusal too', async () => {
    const transport = scriptedTransport(refusal('2.0.0'));
    const client = createClient(descriptor, { baseUrl: '/api', transport, apiVersion: '1.0.0' });
    await expect(client.request('GET', 'missions')).rejects.toBeInstanceOf(ApiVersionError);
    expect(client.serverRelease.get()).toBe('2.0.0');
  });

  it('stays as it was when an answer names none', async () => {
    const transport = scriptedTransport((request) =>
      request.url.endsWith('/user/methods') ? { status: 200, body: {}, headers: { [header]: 'dev' } } : { status: 200, body: {} },
    );
    const client = createClient(descriptor, { baseUrl: '/api', transport });
    await client.request('GET', 'user/methods');
    await client.request('GET', 'missions');
    expect(client.serverRelease.get()).toBe('dev');
  });
});
