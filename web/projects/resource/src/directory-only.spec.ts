import { describe, expect, it } from 'bun:test';
import { Domain, Method } from './brands';
import { createClient, methodHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { directoryOnlyState } from './directory-only';
import { ApiError, warningHeader } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// A tenant's directory-only switch is read as it stands under the tenant's own route.
// What turning it on would do is the warning of the method that turns it on, asked of
// the method's own handle in the tenant: the server's sentence, the people it names and
// the words for the button, or the server's refusal with its own sentence.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'tenants', param: 'tenantID' },
  resources: {},
  methods: {
    MakeTenantDirectoryOnly: {
      method: 'MakeTenantDirectoryOnly' as Method,
      property: 'makeTenantDirectoryOnly',
      route: 'make-tenant-directory-only',
      scope: 'domain',
    },
  },
};

const warning = {
  consequence:
    'Only people who sign in through acme.example will be able to enter this tenant. 1 person seated here signs in another way and will lose access.',
  names: ['Gale Ferris (gale)'],
  confirm: 'Admit only people who sign in through acme.example',
};

describe('directoryOnlyState', () => {
  it("reads the switch as it stands under the tenant's route", async () => {
    const transport = scriptedTransport({ status: 200, body: { directoryOnly: true } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    expect(await directoryOnlyState(client, 'acme co' as Domain)).toEqual({ directoryOnly: true });
    expect(transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual([
      'GET /api/tenants/acme%20co/directory-only',
    ]);
  });

  it('refuses an API that serves no tenant route before any request is made', () => {
    const transport = scriptedTransport();
    const client = createClient({ ...descriptor, domainRoute: undefined }, { baseUrl: '/api', transport });

    expect(() => directoryOnlyState(client, 'acme' as Domain)).toThrow("served under a tenant's route");
    expect(transport.requests).toEqual([]);
  });
});

describe("the warning of the method that turns a tenant's switch on", () => {
  const handle = (client: ReturnType<typeof createClient>) =>
    methodHandle<Record<string, never>>(client, 'MakeTenantDirectoryOnly', 'acme' as Domain);

  it('asks the server what the switch would do in the tenant, and answers its words, the people it names and the button', async () => {
    const transport = scriptedTransport({ status: 200, body: warning });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    expect(await handle(client).warning({})).toEqual(warning);
    expect(transport.requests.map((r) => `${r.method} ${r.url} ${r.headers?.[warningHeader] ?? ''}`)).toEqual([
      'POST /api/tenants/acme/make-tenant-directory-only true',
    ]);
  });

  it("throws the server's refusal with its sentence", async () => {
    const message = 'This tenant has no directory, so it cannot be directory only: nobody could enter it.';
    const transport = scriptedTransport({ status: 409, body: { message } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    let caught: unknown;
    try {
      await handle(client).warning({});
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).status).toBe(409);
    expect((caught as ApiError).message).toBe(message);
  });
});
