import { describe, expect, it } from 'bun:test';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { DeviceRequest, IssuedToken, isTokenRefusal, TokenRow, userTokens } from './tokens';
import { ApiError, TransportRequest, TransportResponse } from './transport';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';

// The person's tokens over the client: the descriptor's tokens block names the routes
// and the machine outlets; the handle lists, issues and revokes the person's own tokens
// and looks up, approves and denies a device request, each request under the API prefix
// with the body the server takes. A refusal the server answers with its words (a bad
// name, lifetime or outlet; no live token; no pending device code) resolves as
// `refused`, so a page renders it in place; a 401 or 403 throws as every request does.
// A descriptor without the block issues no tokens: the handle is undefined.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  resources: {},
  methods: {},
  tokens: {
    route: 'user/tokens',
    revokeRoute: 'user/tokens/revoke',
    deviceRoute: 'user/device',
    approveRoute: 'user/device/approve',
    denyRoute: 'user/device/deny',
    outlets: [
      { name: 'machines', prefix: '/machines' },
      { name: 'exports', prefix: '/exports' },
    ],
  },
};

/** The descriptor an older generator wrote, before tokens. */
const older: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  resources: {},
  methods: {},
};

const rows: TokenRow[] = [
  {
    id: 't-3',
    kind: 'refresh',
    outlet: 'machines',
    clientId: 'impulse on hollis-laptop',
    createdAt: '2026-10-24T10:00:00Z',
    expiresAt: '2026-11-23T10:00:00Z',
  },
  {
    id: 't-2',
    kind: 'access',
    outlet: 'machines',
    clientId: 'impulse on hollis-laptop',
    createdAt: '2026-10-24T10:00:00Z',
    expiresAt: '2026-10-24T11:00:00Z',
    lastUsedAt: '2026-10-24T10:05:00Z',
  },
  {
    id: 't-1',
    kind: 'application',
    name: 'nightly export',
    outlet: 'exports',
    createdAt: '2026-10-01T09:00:00Z',
    expiresAt: '2027-10-01T09:00:00Z',
  },
];

const issued: IssuedToken = {
  id: 't-4',
  token: 'secret-shown-once',
  name: 'nightly export',
  outlet: 'exports',
  expiresAt: '2027-10-25T09:00:00Z',
};

const waiting: DeviceRequest = {
  clientId: 'impulse on hollis-laptop',
  outlet: 'machines',
  expiresAt: '2026-10-25T10:10:00Z',
};

/** Every route answering by its path and method; a spec overrides one answer to script a refusal. */
function server(overrides: Partial<Record<string, TransportResponse>> = {}): ScriptedTransport {
  return scriptedTransport((request: TransportRequest) => {
    const path = request.url.replace(/\?.*$/, '');
    const key = `${request.method} ${path}`;
    const scripted = overrides[key];
    if (scripted) {
      return scripted;
    }
    switch (key) {
      case 'GET /api/user/tokens':
        return { status: 200, body: rows };
      case 'POST /api/user/tokens':
        return { status: 200, body: issued };
      case 'POST /api/user/tokens/revoke':
        return { status: 200, body: {} };
      case 'GET /api/user/device':
        return { status: 200, body: waiting };
      case 'POST /api/user/device/approve':
      case 'POST /api/user/device/deny':
        return { status: 200, body: {} };
      default:
        return { status: 404, body: { message: `unscripted ${key}` } };
    }
  });
}

function over(transport: ScriptedTransport, api: ApiDescriptor = descriptor) {
  const client = createClient(api, { baseUrl: '/api', transport });
  const tokens = userTokens(client);
  if (!tokens) {
    throw new Error('the descriptor carries the tokens block: the handle is expected');
  }
  return tokens;
}

const sent = (transport: ScriptedTransport): string[] => transport.requests.map((r) => `${r.method} ${r.url}`);

describe('the tokens block', () => {
  it('binds no handle on a descriptor from a generator that predates tokens', () => {
    const client = createClient(older, { baseUrl: '/api', transport: server() });
    expect(userTokens(client)).toBeUndefined();
  });

  it('lists the machine outlets the Auth issues tokens for', () => {
    expect(over(server()).outlets).toEqual([
      { name: 'machines', prefix: '/machines' },
      { name: 'exports', prefix: '/exports' },
    ]);
  });
});

describe('the refusal guard', () => {
  it('tells a refusal from a result, and from nothing', async () => {
    const refusing = server({ 'POST /api/user/tokens': { status: 400, body: { message: 'the user is disabled' } } });
    expect(isTokenRefusal(await over(refusing).issue('nightly export', 'exports'))).toBe(true);
    expect(isTokenRefusal(await over(server()).issue('nightly export', 'exports'))).toBe(false);
    expect(isTokenRefusal(await over(server()).device('BCDF-GHJK'))).toBe(false);
    expect(isTokenRefusal(await over(server()).revoke('t-1'))).toBe(false);
    expect(isTokenRefusal(undefined)).toBe(false);
    expect(isTokenRefusal(null)).toBe(false);
  });
});

describe('the list', () => {
  it('reads the tokens route under the API prefix and hands the rows out as served, newest first', async () => {
    const transport = server();
    expect(await over(transport).list()).toEqual(rows);
    expect(sent(transport)).toEqual(['GET /api/user/tokens']);
  });

  it('is empty when the server answers no body', async () => {
    const transport = server({ 'GET /api/user/tokens': { status: 200, body: null } });
    expect(await over(transport).list()).toEqual([]);
  });

  it('throws without a session, so the error hook sends the person to sign in', async () => {
    const transport = server({ 'GET /api/user/tokens': { status: 401, body: { message: 'Unauthorized' } } });
    await expect(over(transport).list()).rejects.toBeInstanceOf(ApiError);
  });
});

describe('issuing a token', () => {
  it('posts the name and the outlet to the tokens route and answers the token, shown this once', async () => {
    const transport = server();
    expect(await over(transport).issue('nightly export', 'exports')).toEqual(issued);
    expect(sent(transport)).toEqual(['POST /api/user/tokens']);
    expect(transport.requests[0].body).toEqual({ name: 'nightly export', outlet: 'exports' });
  });

  it('posts the lifetime in seconds when one is given', async () => {
    const transport = server();
    await over(transport).issue('nightly export', 'exports', 3600);
    expect(transport.requests[0].body).toEqual({ name: 'nightly export', outlet: 'exports', expiresIn: 3600 });
  });

  const refusals: { name: string; message: string }[] = [
    { name: 'an outlet the application has not', message: '"vault" is not a machine outlet of this application' },
    { name: 'a name over 200 characters', message: 'a token needs a name of at most 200 characters' },
    { name: 'a lifetime over the maximum', message: 'a token lives at most 8760h0m0s' },
    { name: 'a disabled user', message: 'the user is disabled' },
  ];
  for (const tt of refusals) {
    it(`resolves refused with the server's words for ${tt.name}`, async () => {
      const transport = server({ 'POST /api/user/tokens': { status: 400, body: { message: tt.message } } });
      expect(await over(transport).issue('nightly export', 'vault')).toEqual({
        outcome: 'refused',
        status: 400,
        message: tt.message,
      });
    });
  }

  it('resolves refused with the status as its words when the refusal carries none', async () => {
    const transport = server({ 'POST /api/user/tokens': { status: 400, body: undefined } });
    expect(await over(transport).issue('nightly export', 'exports')).toEqual({
      outcome: 'refused',
      status: 400,
      message: 'HTTP 400',
    });
  });

  it('throws under the password mark, so the error hook sends the person to the change form', async () => {
    const transport = server({
      'POST /api/user/tokens': {
        status: 403,
        body: { message: 'Change your password to continue', code: 'password_change_required' },
      },
    });
    await expect(over(transport).issue('nightly export', 'exports')).rejects.toBeInstanceOf(ApiError);
  });
});

describe('revoking a token', () => {
  it('posts the id to the revoke route and resolves with nothing', async () => {
    const transport = server();
    expect(await over(transport).revoke('t-1')).toBeUndefined();
    expect(sent(transport)).toEqual(['POST /api/user/tokens/revoke']);
    expect(transport.requests[0].body).toEqual({ id: 't-1' });
  });

  it("resolves refused with the server's words when no live token of the person's answers to the id", async () => {
    const transport = server({ 'POST /api/user/tokens/revoke': { status: 404, body: { message: 'no live token' } } });
    expect(await over(transport).revoke('t-9')).toEqual({ outcome: 'refused', status: 404, message: 'no live token' });
  });
});

describe('a device request', () => {
  it('is looked up by the code the program printed, passed through untouched in the query', async () => {
    const transport = server();
    expect(await over(transport).device('BCDF-GHJK')).toEqual(waiting);
    expect(sent(transport)).toEqual(['GET /api/user/device?userCode=BCDF-GHJK']);
  });

  it('is looked up with the code as the person typed it, in any case and spacing; the server reads it', async () => {
    const transport = server();
    await over(transport).device('bcdf ghjk');
    expect(sent(transport)).toEqual(['GET /api/user/device?userCode=bcdf+ghjk']);
  });

  it('resolves refused in the one sentence the server uses for an unknown, expired, decided or consumed code', async () => {
    const transport = server({ 'GET /api/user/device': { status: 404, body: { message: 'no pending device code' } } });
    expect(await over(transport).device('BCDF-GHJK')).toEqual({
      outcome: 'refused',
      status: 404,
      message: 'no pending device code',
    });
  });

  it('is approved by posting the code to the approve route', async () => {
    const transport = server();
    expect(await over(transport).approve('BCDF-GHJK')).toBeUndefined();
    expect(sent(transport)).toEqual(['POST /api/user/device/approve']);
    expect(transport.requests[0].body).toEqual({ userCode: 'BCDF-GHJK' });
  });

  it('is denied by posting the code to the deny route', async () => {
    const transport = server();
    expect(await over(transport).deny('BCDF-GHJK')).toBeUndefined();
    expect(sent(transport)).toEqual(['POST /api/user/device/deny']);
    expect(transport.requests[0].body).toEqual({ userCode: 'BCDF-GHJK' });
  });

  const decisions: { name: string; route: string; decide: (code: string) => Promise<unknown> }[] = [
    {
      name: 'an approval',
      route: 'POST /api/user/device/approve',
      decide: (code) => over(serverRefusing('POST /api/user/device/approve')).approve(code),
    },
    {
      name: 'a denial',
      route: 'POST /api/user/device/deny',
      decide: (code) => over(serverRefusing('POST /api/user/device/deny')).deny(code),
    },
  ];
  for (const tt of decisions) {
    it(`${tt.name} of a code no request waits on resolves refused`, async () => {
      expect(await tt.decide('BCDF-GHJK')).toEqual({
        outcome: 'refused',
        status: 404,
        message: 'no pending device code',
      });
    });
  }

  it('throws without a session on every step, so the error hook sends the person to sign in', async () => {
    const unauthorized: TransportResponse = { status: 401, body: { message: 'Unauthorized' } };
    const transport = server({
      'GET /api/user/device': unauthorized,
      'POST /api/user/device/approve': unauthorized,
      'POST /api/user/device/deny': unauthorized,
    });
    const tokens = over(transport);
    await expect(tokens.device('BCDF-GHJK')).rejects.toBeInstanceOf(ApiError);
    await expect(tokens.approve('BCDF-GHJK')).rejects.toBeInstanceOf(ApiError);
    await expect(tokens.deny('BCDF-GHJK')).rejects.toBeInstanceOf(ApiError);
  });
});

/** A server refusing one route with the device code's 404. */
function serverRefusing(route: string): ScriptedTransport {
  return server({ [route]: { status: 404, body: { message: 'no pending device code' } } });
}
