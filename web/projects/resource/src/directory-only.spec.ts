import { describe, expect, it } from 'bun:test';
import { Domain } from './brands';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { DirectoryOnlyConsequence, directoryOnlyConsequence, directoryOnlyState } from './directory-only';
import { ApiError } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// A tenant's directory-only switch is read under the tenant's own route: the switch as
// it stands, and what turning it on would do, answered with the sentence, the count and
// the people the server names; the server's refusal throws with its own sentence.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'tenants', param: 'tenantID' },
  resources: {},
  methods: {},
};

const consequence: DirectoryOnlyConsequence = {
  directory: 'Google Workspace acme.example',
  connection: 'acme.example',
  sentence:
    'Only people who sign in through acme.example will be able to enter this tenant. 1 person seated here signs in another way and will lose access.',
  count: 1,
  people: [{ userId: 'u-1', username: 'gale', name: 'Gale Ferris' }],
  confirm: 'Admit only people who sign in through acme.example',
};

describe('directoryOnlyState', () => {
  it("reads the switch as it stands under the tenant's route", async () => {
    const transport = scriptedTransport({ status: 200, body: { directoryOnly: true } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    expect(await directoryOnlyState(client, 'acme co' as Domain)).toEqual({ directoryOnly: true });
    expect(transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual(['GET /api/tenants/acme%20co/directory-only']);
  });

  it('refuses an API that serves no tenant route before any request is made', () => {
    const transport = scriptedTransport();
    const client = createClient({ ...descriptor, domainRoute: undefined }, { baseUrl: '/api', transport });

    expect(() => directoryOnlyState(client, 'acme' as Domain)).toThrow("served under a tenant's route");
    expect(transport.requests).toEqual([]);
  });
});

describe('directoryOnlyConsequence', () => {
  it('asks the server what the switch would do in the tenant, and answers its words, the count and the people it names', async () => {
    const transport = scriptedTransport({ status: 200, body: consequence });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    expect(await directoryOnlyConsequence(client, 'acme' as Domain)).toEqual(consequence);
    expect(transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual([
      'GET /api/tenants/acme/directory-only/consequence',
    ]);
  });

  it("throws the server's refusal with its sentence", async () => {
    const message = 'This tenant has no directory, so it cannot be directory only: nobody could enter it.';
    const transport = scriptedTransport({ status: 409, body: { message } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    let caught: unknown;
    try {
      await directoryOnlyConsequence(client, 'acme' as Domain);
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).status).toBe(409);
    expect((caught as ApiError).message).toBe(message);
  });
});
