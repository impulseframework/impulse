import { describe, expect, it } from 'bun:test';
import { Domain, ListPermission, Resource } from './brands';
import { AnyResourceHandle, createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { ApiError, TransportResponse } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The server answers every request under a domain the session does not reach with 404, as
// it does for a record that is not there. The client reads the domain's digest again before
// the caller hears of a 404 under a domain it holds a digest of, so a domain the person has
// left is forgotten, and heard of, before anything draws the server's words.

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
      order: [{ field: 'id', direction: 'asc' }],
    },
  },
  methods: {},
};

interface Sector {
  missions: AnyResourceHandle<{ id: string }>;
}

const anvil = 'a1111111-1111-4111-8111-111111111111' as Domain;
const digestOf = (domain: Domain): string => `/api/permission-digest?domain=${domain}`;
const held = { Missions: { List: 'granted' } };
const unknown: TransportResponse = { status: 404, body: { message: 'This sector does not exist.' } };

/** A client over answers scripted by URL: an answer, or what answers each time the URL is asked. */
function clientOver(script: Record<string, TransportResponse | (() => TransportResponse)>) {
  const sent: string[] = [];
  const transport = scriptedTransport((request) => {
    sent.push(`${request.method} ${request.url}`);
    const answer = script[request.url];
    if (!answer) {
      return { status: 500, body: { message: `unscripted ${request.url}` } };
    }
    return typeof answer === 'function' ? answer() : answer;
  });
  const client = createClient<Record<never, never>, Sector>(descriptor, { baseUrl: '/api', transport });
  const heard: string[] = [];
  client.permissions.onUnread((domain, failure) =>
    heard.push(`${domain ?? 'global'} ${failure}, held: ${client.permissions.hasDigest(domain)}`),
  );
  return { client, sent, heard, script };
}

/** What a request ended as, with what had been heard of the domain by then. */
async function ended(request: Promise<unknown>, heard: readonly string[]): Promise<string> {
  try {
    await request;
    return `answered, heard by then: ${heard.join('; ')}`;
  } catch (error) {
    const status = error instanceof ApiError ? String(error.status) : String(error);
    return `refused ${status}, heard by then: ${heard.join('; ')}`;
  }
}

describe('a 404 under a domain the cache holds a digest of', () => {
  it('reads the digest again before its caller hears of it, and forgets a domain the person has left', async () => {
    const list = `/api/sectors/${anvil}/missions`;
    const { client, sent, heard, script } = clientOver({
      [digestOf(anvil)]: { status: 200, body: held },
      '/api/user-domains': { status: 200, body: [] },
      [list]: unknown,
    });
    await client.permissions.loadDigest(anvil);
    sent.length = 0;

    // The seat is taken away: the server answers the digest with no entries from here on.
    script[digestOf(anvil)] = { status: 200, body: {} };
    const outcome = await ended(client.domain(anvil).missions.list(), heard);

    // The caller is answered as the server answered, after the refusal was heard.
    expect(outcome).toBe(`refused 404, heard by then: ${anvil} refused, held: false`);
    expect(sent).toEqual([`GET ${list}`, `GET ${digestOf(anvil)}`, 'GET /api/user-domains']);
    expect(client.permissions.hasDigest(anvil)).toBe(false);
  });

  it('leaves a domain the person is still in as it was: the 404 is the record that is not there', async () => {
    const row = `/api/sectors/${anvil}/missions/m404`;
    const { client, sent, heard } = clientOver({
      [digestOf(anvil)]: { status: 200, body: held },
      [row]: { status: 404, body: { message: 'no such mission' } },
    });
    await client.permissions.loadDigest(anvil);
    sent.length = 0;

    const outcome = await ended(client.domain(anvil).missions.read(['m404']), heard);

    expect(outcome).toBe('refused 404, heard by then: ');
    expect(sent).toEqual([`GET ${row}`, `GET ${digestOf(anvil)}`]);
    expect(client.permissions.hasDigest(anvil)).toBe(true);
    expect(client.can(ListPermission, 'Missions' as Resource, anvil)).toBe(true);
  });

  it('reads the digest again for a page the server named by a link', async () => {
    const first = `/api/sectors/${anvil}/missions?sort=id%3Aasc&limit=1`;
    const second = `/api/sectors/${anvil}/missions?sort=id%3Aasc&limit=1&cursor=v4.local.aaa`;
    const { client, sent, heard, script } = clientOver({
      [digestOf(anvil)]: { status: 200, body: held },
      '/api/user-domains': { status: 200, body: [] },
      [first]: { status: 200, body: [{ id: 'm1' }], headers: { link: `<${second}>; rel="next"` } },
      [second]: unknown,
    });
    await client.permissions.loadDigest(anvil);
    const page = await client.domain(anvil).missions.page({ sort: { field: 'id', direction: 'asc' }, limit: 1 });
    sent.length = 0;

    script[digestOf(anvil)] = { status: 200, body: {} };
    const outcome = await ended(page.next!(), heard);

    expect(outcome).toBe(`refused 404, heard by then: ${anvil} refused, held: false`);
    expect(sent).toEqual([`GET ${second}`, `GET ${digestOf(anvil)}`, 'GET /api/user-domains']);
  });

  it('reads the digest again where the caller declared the 404 as an answer', async () => {
    const path = `sectors/${anvil}/missions/m1/launch`;
    const { client, sent, heard, script } = clientOver({
      [digestOf(anvil)]: { status: 200, body: held },
      '/api/user-domains': { status: 200, body: [] },
      [`/api/${path}`]: unknown,
    });
    await client.permissions.loadDigest(anvil);
    sent.length = 0;

    script[digestOf(anvil)] = { status: 200, body: {} };
    const outcome = await ended(client.requestResponse('POST', path, { accept: [404] }), heard);

    expect(outcome).toBe(`answered, heard by then: ${anvil} refused, held: false`);
    expect(sent).toEqual([`POST /api/${path}`, `GET ${digestOf(anvil)}`, 'GET /api/user-domains']);
  });

  it('answers the caller as the server answered where the digest cannot be read again', async () => {
    const list = `/api/sectors/${anvil}/missions`;
    const { client, heard, script } = clientOver({
      [digestOf(anvil)]: { status: 200, body: held },
      [list]: unknown,
    });
    await client.permissions.loadDigest(anvil);

    script[digestOf(anvil)] = { status: 500, body: { message: 'down' } };
    const outcome = await ended(client.domain(anvil).missions.list(), heard);

    // A read that failed forgets nothing; its listeners heard of it as of any failed read.
    expect(outcome).toBe(`refused 404, heard by then: ${anvil} failed, held: true`);
    expect(client.permissions.hasDigest(anvil)).toBe(true);
  });
});

describe('an answer that says nothing of the domain', () => {
  it('reads nothing for a domain the cache holds no digest of', async () => {
    const list = `/api/sectors/${anvil}/missions`;
    const { client, sent, heard } = clientOver({ [list]: unknown });

    const outcome = await ended(client.domain(anvil).missions.list(), heard);

    expect(outcome).toBe('refused 404, heard by then: ');
    expect(sent).toEqual([`GET ${list}`]);
  });

  it('reads nothing for a 404 under no domain', async () => {
    const { client, sent, heard } = clientOver({
      [digestOf(anvil)]: { status: 200, body: held },
      '/api/reports/r404': { status: 404, body: { message: 'no such report' } },
    });
    await client.permissions.loadDigest(anvil);
    sent.length = 0;

    const outcome = await ended(client.request('GET', 'reports/r404'), heard);

    expect(outcome).toBe('refused 404, heard by then: ');
    expect(sent).toEqual(['GET /api/reports/r404']);
  });

  it('reads nothing for any other refusal under the domain', async () => {
    for (const status of [400, 403, 409, 500]) {
      const list = `/api/sectors/${anvil}/missions`;
      const { client, sent, heard } = clientOver({
        [digestOf(anvil)]: { status: 200, body: held },
        [list]: { status, body: { message: 'refused' } },
      });
      await client.permissions.loadDigest(anvil);
      sent.length = 0;

      const outcome = await ended(client.domain(anvil).missions.list(), heard);

      expect(outcome).toBe(`refused ${status}, heard by then: `);
      expect(sent).toEqual([`GET ${list}`]);
    }
  });
});
