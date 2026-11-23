import { describe, expect, it } from 'bun:test';
import { Domain, Method } from './brands';
import { createClient, methodHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { TenantSignInSettings, tenantSignInSettings } from './directory-only';
import { ApiError, warningHeader } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// A tenant's sign-in settings, its directory-only switch among them, are read as they
// stand under the tenant's own route.
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

describe('tenantSignInSettings', () => {
  it("reads the sign-in settings as they stand under the tenant's route", async () => {
    const transport = scriptedTransport({ status: 200, body: { directoryOnly: true } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    expect(await tenantSignInSettings(client, 'acme co' as Domain)).toEqual({ directoryOnly: true });
    expect(transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual([
      'GET /api/tenants/acme%20co/sign-in-settings',
    ]);
  });

  it("answers the second factor's part, with what saying the word takes of the person asking", async () => {
    const settings: TenantSignInSettings = {
      directoryOnly: false,
      directory: 'acme.example',
      secondFactor: {
        vouched: false,
        asked: true,
        says: 'People who sign in through acme.example are asked for a code mailed to their address, as everyone is.',
        conditions: [
          {
            sentence: 'Saying so is for a person signed in through acme.example.',
            met: false,
            toDo: 'Sign in through acme.example, then try again.',
          },
        ],
      },
    };
    const transport = scriptedTransport({ status: 200, body: settings });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    const read = await tenantSignInSettings(client, 'acme' as Domain);
    expect(read).toEqual(settings);
    expect(read.secondFactor?.conditions?.map((condition) => condition.met)).toEqual([false]);
  });

  it("answers the count auth by auth, each auth's share with its label and its list of people", async () => {
    const counted: TenantSignInSettings = {
      directoryOnly: false,
      people: {
        seated: 17,
        outside: 3,
        shares: [
          { label: 'Crew', people: 'CrewTenantUsers', seated: 12, outside: 2 },
          { label: 'Staff', people: 'StaffTenantUsers', seated: 5, outside: 1 },
        ],
      },
    };
    const transport = scriptedTransport({ status: 200, body: counted });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    const state = await tenantSignInSettings(client, 'acme' as Domain);
    expect(state).toEqual(counted);
    expect(state.people?.shares?.map((share) => share.label)).toEqual(['Crew', 'Staff']);
  });

  it('refuses an API that serves no tenant route before any request is made', () => {
    const transport = scriptedTransport();
    const client = createClient({ ...descriptor, domainRoute: undefined }, { baseUrl: '/api', transport });

    expect(() => tenantSignInSettings(client, 'acme' as Domain)).toThrow("served under a tenant's route");
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
