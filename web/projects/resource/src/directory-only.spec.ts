import { describe, expect, it } from 'bun:test';
import { Domain } from './brands';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { DirectoryOnlyConsequence, directoryOnlyConsequence } from './directory-only';
import { ApiError } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// What turning a tenant's directory-only switch on would do is asked of the server
// before a page saves the switch: one read under the API prefix that names the tenant,
// answered with the sentence and the people it names; the server's refusal throws with
// its own sentence.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  resources: {},
  methods: {},
};

const consequence: DirectoryOnlyConsequence = {
  directory: 'Google Workspace acme.example',
  connection: 'acme.example',
  sentence:
    'Only people who sign in through acme.example will be able to enter this tenant. 1 person seated here signs in another way and will lose access.',
  people: [{ userId: 'u-1', username: 'gale', name: 'Gale Ferris' }],
  confirm: 'Admit only people who sign in through acme.example',
};

describe('directoryOnlyConsequence', () => {
  it('asks the server what the switch would do in the tenant, and answers its words and the people it names', async () => {
    const transport = scriptedTransport({ status: 200, body: consequence });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    expect(await directoryOnlyConsequence(client.request, 'acme' as Domain)).toEqual(consequence);
    expect(transport.requests.map((r) => `${r.method} ${r.url}`)).toEqual(['GET /api/directory-only?domain=acme']);
  });

  it("throws the server's refusal with its sentence", async () => {
    const message = 'This tenant has no directory, so it cannot be directory only: nobody could enter it.';
    const transport = scriptedTransport({ status: 409, body: { message } });
    const client = createClient(descriptor, { baseUrl: '/api', transport });

    const refused = directoryOnlyConsequence(client.request, 'acme' as Domain);
    await expect(refused).rejects.toBeInstanceOf(ApiError);
    await expect(refused).rejects.toMatchObject({ status: 409, message });
  });
});
