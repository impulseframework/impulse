import { computed } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Domain, Permission, PermissionScope, Resource } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';

// A permission question asked inside reactive code follows the digest (#569): the client
// reads a plain store, and the Angular client's store read hook makes each read a
// dependency, so nothing mirrors the store first.

const missions = { resource: 'Missions' as Resource, permission: 'List' as Permission } as PermissionScope;
const acme = 'acme' as Domain;

describe('a permission question inside computed()', () => {
  beforeEach(() => {
    const transport = scriptedTransport((request) =>
      request.url.includes('permission-digest')
        ? { status: 200, body: { Missions: { List: 'granted' } } }
        : { status: 200, body: [] },
    );
    TestBed.configureTestingModule({ providers: [provideResourceTesting({ transport })] });
  });

  it('answers no before the digest loads and yes once it has, with no mirror', async () => {
    const client = TestBed.inject(RESOURCE_CLIENT);
    const allowed = computed(() => client.permissions.can(missions));
    expect(allowed()).toBe(false);
    await client.permissions.loadDigest();
    expect(allowed()).toBe(true);
  });

  it("asks again when a tenant's digest loads after the page rendered", async () => {
    const client = TestBed.inject(RESOURCE_CLIENT);
    const allowed = computed(() => client.permissions.can({ ...missions, domain: acme }));
    expect(allowed()).toBe(false);
    await client.permissions.loadDigest(acme);
    expect(allowed()).toBe(true);
  });

  it('answers no again once the permissions are cleared at sign-out', async () => {
    const client = TestBed.inject(RESOURCE_CLIENT);
    const allowed = computed(() => client.permissions.can(missions));
    await client.permissions.loadDigest();
    expect(allowed()).toBe(true);
    client.permissions.clear();
    expect(allowed()).toBe(false);
  });
});
