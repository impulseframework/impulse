import { describe, expect, it } from 'bun:test';
import { Domain, Method, Resource } from './brands';
import { createClient } from './client';
import { ApiDescriptor, MethodDescriptor, ResourceDescriptor, UserSurfaceDescriptor } from './descriptor';
import { TransportRequest } from './transport';
import { surfaceResources, userPartition, userSurfaces } from './users';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';

// The user surfaces' contract: the descriptor's block says which resources and methods
// make up each Auth's surface; a partition bound on the client hands out the client's own
// handles over those entries (global under the API prefix, tenant under the domain pair),
// posts the two methods to their routes, and answers what the partition may do from the
// digest folded with what the Auth's methods give it. A descriptor from a generator that
// predates the surfaces carries none.

function resource(name: string, route: string, scope: 'global' | 'domain', keys: string[], operations: string[]): ResourceDescriptor {
  return {
    resource: name as Resource,
    property: name.charAt(0).toLowerCase() + name.slice(1),
    route,
    scope,
    consolidated: false,
    keys,
    operations: operations as ResourceDescriptor['operations'],
    page: { default: 50 },
  };
}

function method(name: string, route: string, scope: 'global' | 'domain'): MethodDescriptor {
  return { method: name as Method, property: name.charAt(0).toLowerCase() + name.slice(1), route, scope };
}

const crew: UserSurfaceDescriptor = {
  auth: 'Crew',
  label: 'Crew',
  password: true,
  usernameIsEmail: false,
  openSignUp: false,
  registrations: [{ name: 'google', kind: 'google', label: 'Google', invited: false, syncsRoles: true }],
  partitions: [
    {
      scope: 'global',
      users: 'CrewUsers',
      seats: 'CrewSeats',
      roles: 'CrewRoles',
      invitations: 'CrewInvitations',
      setPassword: 'SetCrewUserPassword',
      unlinkIdentity: 'UnlinkCrewUserIdentity',
      resetFactors: 'ResetCrewUserFactors',
    },
    {
      scope: 'domain',
      users: 'CrewSectorUsers',
      seats: 'CrewSectorSeats',
      roles: 'CrewSectorRoles',
      invitations: 'CrewSectorInvitations',
      setPassword: 'SetCrewSectorUserPassword',
      unlinkIdentity: 'UnlinkCrewSectorUserIdentity',
      resetFactors: 'ResetCrewSectorUserFactors',
    },
  ],
};

/** An Auth with a password method alone: a create, a set-password, and no unlink; its descriptor predates the second factor, so no reset. */
const kiosk: UserSurfaceDescriptor = {
  auth: 'Kiosk',
  label: 'Kiosk',
  password: true,
  registrations: [],
  partitions: [{ scope: 'global', users: 'KioskUsers', seats: 'KioskSeats', roles: 'KioskRoles', setPassword: 'SetKioskUserPassword' }],
};

/** An Auth with a directory alone: no create, no set-password, an unlink. */
const members: UserSurfaceDescriptor = {
  auth: 'Members',
  label: 'Members',
  password: false,
  registrations: [{ name: 'directory', kind: 'google', label: 'Company directory', invited: false, syncsRoles: true }],
  partitions: [
    {
      scope: 'global',
      users: 'MembersUsers',
      seats: 'MembersSeats',
      roles: 'MembersRoles',
      unlinkIdentity: 'UnlinkMembersUserIdentity',
      resetFactors: 'ResetMembersUserFactors',
    },
  ],
};

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {
    CrewUsers: resource('CrewUsers', 'crew-users', 'global', ['id'], ['list', 'read', 'create', 'patch', 'remove']),
    CrewSeats: resource('CrewSeats', 'crew-seats', 'global', ['user', 'role'], ['list', 'read', 'create', 'remove']),
    CrewRoles: resource('CrewRoles', 'crew-roles', 'global', ['name'], ['list', 'read']),
    CrewSectorUsers: resource('CrewSectorUsers', 'crew-sector-users', 'domain', ['id'], ['list', 'read', 'create', 'patch', 'remove']),
    CrewSectorSeats: resource('CrewSectorSeats', 'crew-sector-seats', 'domain', ['user', 'role'], ['list', 'read', 'create', 'remove']),
    CrewSectorRoles: resource('CrewSectorRoles', 'crew-sector-roles', 'domain', ['name'], ['list', 'read']),
    CrewInvitations: resource('CrewInvitations', 'crew-invitations', 'global', ['id'], ['list', 'read', 'create', 'remove']),
    CrewSectorInvitations: resource('CrewSectorInvitations', 'crew-sector-invitations', 'domain', ['id'], ['list', 'read', 'create', 'remove']),
    KioskUsers: resource('KioskUsers', 'kiosk-users', 'global', ['id'], ['list', 'read', 'create', 'patch', 'remove']),
    KioskSeats: resource('KioskSeats', 'kiosk-seats', 'global', ['user', 'role'], ['list', 'read', 'create', 'remove']),
    KioskRoles: resource('KioskRoles', 'kiosk-roles', 'global', ['name'], ['list', 'read']),
    MembersUsers: resource('MembersUsers', 'members-users', 'global', ['id'], ['list', 'read', 'patch', 'remove']),
    MembersSeats: resource('MembersSeats', 'members-seats', 'global', ['user', 'role'], ['list', 'read', 'create', 'remove']),
    MembersRoles: resource('MembersRoles', 'members-roles', 'global', ['name'], ['list', 'read']),
  },
  methods: {
    SetCrewUserPassword: method('SetCrewUserPassword', 'set-crew-user-password', 'global'),
    UnlinkCrewUserIdentity: method('UnlinkCrewUserIdentity', 'unlink-crew-user-identity', 'global'),
    SetCrewSectorUserPassword: method('SetCrewSectorUserPassword', 'set-crew-sector-user-password', 'domain'),
    UnlinkCrewSectorUserIdentity: method('UnlinkCrewSectorUserIdentity', 'unlink-crew-sector-user-identity', 'domain'),
    SetKioskUserPassword: method('SetKioskUserPassword', 'set-kiosk-user-password', 'global'),
    UnlinkMembersUserIdentity: method('UnlinkMembersUserIdentity', 'unlink-members-user-identity', 'global'),
    ResetCrewUserFactors: method('ResetCrewUserFactors', 'reset-crew-user-factors', 'global'),
    ResetCrewSectorUserFactors: method('ResetCrewSectorUserFactors', 'reset-crew-sector-user-factors', 'domain'),
    ResetMembersUserFactors: method('ResetMembersUserFactors', 'reset-members-user-factors', 'global'),
  },
  userSurfaces: [crew, kiosk, members],
};

/** The digest every scope answers: the crew's global partition granted whole, the sector's seats alone. */
const digest = {
  CrewUsers: { List: 'granted', Read: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  CrewSeats: { List: 'granted', Create: 'granted', Delete: 'granted' },
  SetCrewUserPassword: { Execute: 'granted' },
  UnlinkCrewUserIdentity: { Execute: 'conditional' },
  ResetCrewUserFactors: { Execute: 'granted' },
  CrewSectorSeats: { List: 'granted', Create: 'conditional' },
  CrewInvitations: { List: 'granted', Create: 'conditional' },
  CrewSectorInvitations: { List: 'granted', Create: 'granted', Delete: 'granted' },
  KioskUsers: { Create: 'granted' },
  MembersUsers: { List: 'granted' },
  MembersSeats: { Create: 'granted' },
};

function clientOver(): { client: ReturnType<typeof createClient>; transport: ScriptedTransport } {
  const transport = scriptedTransport((request: TransportRequest) => {
    const path = request.url.replace(/\?.*$/, '');
    if (path === '/api/permission-digest') {
      return { status: 200, body: digest };
    }
    if (request.method === 'GET') {
      return { status: 200, body: [] };
    }
    return { status: 200, body: undefined };
  });
  return { client: createClient(descriptor, { baseUrl: '/api', transport }), transport };
}

const sent = (transport: ScriptedTransport): string[] => transport.requests.map((r) => `${r.method} ${r.url}`);

describe('the surfaces block', () => {
  it('lists the surfaces the descriptor carries, in its order', () => {
    expect(userSurfaces(descriptor).map((s) => s.auth)).toEqual(['Crew', 'Kiosk', 'Members']);
  });

  it('is empty on a descriptor from a generator that predates the surfaces', () => {
    const older = Object.fromEntries(Object.entries(descriptor).filter(([key]) => key !== 'userSurfaces')) as ApiDescriptor;
    expect(userSurfaces(older)).toEqual([]);
  });

  it('names the resources of a surface, both partitions, the invitations where the partition has them', () => {
    expect(surfaceResources(crew)).toEqual([
      'CrewUsers',
      'CrewSeats',
      'CrewRoles',
      'CrewInvitations',
      'CrewSectorUsers',
      'CrewSectorSeats',
      'CrewSectorRoles',
      'CrewSectorInvitations',
    ]);
    expect(surfaceResources(kiosk)).toEqual(['KioskUsers', 'KioskSeats', 'KioskRoles']);
  });
});

describe('a partition bound on the client', () => {
  it('hands out the handles of the global partition under the API prefix', () => {
    const { client } = clientOver();
    const partition = userPartition(client, crew, crew.partitions[0]);
    expect(partition.users.url()).toBe('/api/crew-users');
    expect(partition.users.url(['u1'])).toBe('/api/crew-users/u1');
    expect(partition.seats.url(['u1', 'Pilot'])).toBe('/api/crew-seats/u1/Pilot');
    expect(partition.roles.url()).toBe('/api/crew-roles');
    expect(partition.invitations?.url(['inv-1'])).toBe('/api/crew-invitations/inv-1');
    expect(partition.domain).toBeUndefined();
  });

  it('hands out no invitations handle for a partition whose descriptor names none, and answers false for all of it', () => {
    const { client } = clientOver();
    const partition = userPartition(client, kiosk, kiosk.partitions[0]);
    expect(partition.invitations).toBeUndefined();
    expect(partition.canListInvitations()).toBe(false);
    expect(partition.canInvite()).toBe(false);
    expect(partition.canRevokeInvitations()).toBe(false);
  });

  it('invites through the invitations resource, the address and the roles in the operation, and revokes by id', async () => {
    const { client, transport } = clientOver();
    const partition = userPartition(client, crew, crew.partitions[1], 'anvil' as Domain);
    await partition.invitations?.create({ email: 'cass@fleet.example', roles: ['Harbormaster'] });
    await partition.invitations?.remove(['inv-1']);
    expect(sent(transport)).toEqual(['PATCH /api/sectors/anvil/crew-sector-invitations', 'PATCH /api/sectors/anvil/crew-sector-invitations']);
    expect(transport.requests[0].body).toEqual([{ op: 'add', path: '/', value: { email: 'cass@fleet.example', roles: ['Harbormaster'] } }]);
    expect(transport.requests[1].body).toEqual([{ op: 'remove', path: '/inv-1' }]);
  });

  it('hands out the handles of the tenant partition under the domain pair', () => {
    const { client } = clientOver();
    const partition = userPartition(client, crew, crew.partitions[1], 'anvil' as Domain);
    expect(partition.users.url()).toBe('/api/sectors/anvil/crew-sector-users');
    expect(partition.seats.url(['u1', 'Harbormaster'])).toBe('/api/sectors/anvil/crew-sector-seats/u1/Harbormaster');
    expect(partition.domain).toBe('anvil' as Domain);
  });

  it('refuses the tenant partition without a tenant, and the global one with one', () => {
    const { client } = clientOver();
    expect(() => userPartition(client, crew, crew.partitions[1])).toThrow(/needs the tenant/);
    expect(() => userPartition(client, crew, crew.partitions[0], 'anvil' as Domain)).toThrow(/takes no tenant/);
  });

  it('refuses a partition naming a resource the descriptor does not carry', () => {
    const { client } = clientOver();
    const stray = { ...crew, partitions: [{ ...crew.partitions[0], roles: 'CrewRanks' }] };
    expect(() => userPartition(client, stray, stray.partitions[0])).toThrow(/CrewRanks, which the descriptor does not carry/);
  });

  it('seats through the seats resource, the key in the operation path', async () => {
    const { client, transport } = clientOver();
    const partition = userPartition(client, crew, crew.partitions[0]);
    await partition.seats.create({ user: 'u1', role: 'Pilot' });
    expect(sent(transport)).toEqual(['PATCH /api/crew-seats']);
    const [operation] = transport.requests[0].body as { op: string; path: string; value: Record<string, unknown> }[];
    expect(operation.op).toBe('add');
    expect(operation.path).toBe('/u1/Pilot');
    expect(operation.value).toEqual({});
  });

  it('posts the two methods to their routes, global and under the tenant', async () => {
    const { client, transport } = clientOver();
    await userPartition(client, crew, crew.partitions[0]).setPassword('u1', 'temporary');
    await userPartition(client, crew, crew.partitions[0]).unlinkIdentity('u1', 'i1');
    await userPartition(client, crew, crew.partitions[1], 'anvil' as Domain).setPassword('u1', 'temporary');
    expect(sent(transport)).toEqual([
      'POST /api/set-crew-user-password',
      'POST /api/unlink-crew-user-identity',
      'POST /api/sectors/anvil/set-crew-sector-user-password',
    ]);
    expect(transport.requests[0].body).toEqual({ user: 'u1', password: 'temporary' });
    expect(transport.requests[1].body).toEqual({ user: 'u1', identity: 'i1' });
  });

  it('posts the reset to its route, global and under the tenant, and refuses it on a descriptor that predates the second factor', async () => {
    const { client, transport } = clientOver();
    await userPartition(client, crew, crew.partitions[0]).resetFactors('u1');
    await userPartition(client, crew, crew.partitions[1], 'anvil' as Domain).resetFactors('u1');
    expect(sent(transport)).toEqual(['POST /api/reset-crew-user-factors', 'POST /api/sectors/anvil/reset-crew-sector-user-factors']);
    expect(transport.requests[0].body).toEqual({ user: 'u1' });
    expect(transport.requests[1].body).toEqual({ user: 'u1' });
    await expect(userPartition(client, kiosk, kiosk.partitions[0]).resetFactors('u1')).rejects.toThrow(/predates the second factor/);
  });

  it('refuses a method the partition does not have', async () => {
    const { client } = clientOver();
    await expect(userPartition(client, kiosk, kiosk.partitions[0]).unlinkIdentity('u1', 'i1')).rejects.toThrow(/no external registration/);
    await expect(userPartition(client, members, members.partitions[0]).setPassword('u1', 'x')).rejects.toThrow(/no password method/);
  });

  it('answers what the partition may do from the digest and the Auth together', async () => {
    const { client } = clientOver();
    const crewGlobal = userPartition(client, crew, crew.partitions[0]);
    expect(crewGlobal.canCreateUsers()).toBe(false);
    await client.permissions.loadDigest();
    expect(crewGlobal.canCreateUsers()).toBe(true);
    expect(crewGlobal.canSeat()).toBe(true);
    expect(crewGlobal.canUnseat()).toBe(true);
    expect(crewGlobal.canResetFactors()).toBe(true);
    expect(userPartition(client, kiosk, kiosk.partitions[0]).canResetFactors()).toBe(false);
    expect(userPartition(client, members, members.partitions[0]).canResetFactors()).toBe(false);
    expect(crewGlobal.canSetPassword()).toBe(true);
    expect(crewGlobal.canUnlinkIdentity()).toBe(true);
    expect(crewGlobal.canListInvitations()).toBe(true);
    expect(crewGlobal.canInvite()).toBe(true);
    expect(crewGlobal.canRevokeInvitations()).toBe(false);

    const kioskGlobal = userPartition(client, kiosk, kiosk.partitions[0]);
    expect(kioskGlobal.canCreateUsers()).toBe(true);
    expect(kioskGlobal.canSetPassword()).toBe(false);
    expect(kioskGlobal.canUnlinkIdentity()).toBe(false);

    // The members' users resource takes no create: nobody is created without a way to sign in.
    const membersGlobal = userPartition(client, members, members.partitions[0]);
    expect(membersGlobal.canCreateUsers()).toBe(false);
    expect(membersGlobal.canSeat()).toBe(true);
    expect(membersGlobal.canUnseat()).toBe(false);

    await client.permissions.loadDigest('anvil' as Domain);
    const crewSector = userPartition(client, crew, crew.partitions[1], 'anvil' as Domain);
    expect(crewSector.canCreateUsers()).toBe(false);
    expect(crewSector.canSeat()).toBe(true);
    expect(crewSector.canSetPassword()).toBe(false);
    expect(crewSector.canInvite()).toBe(true);
    expect(crewSector.canRevokeInvitations()).toBe(true);
  });
});
