import { describe, expect, it } from 'bun:test';
import { CreatePermission, Domain, Permission, Resource } from './brands';
import { createClient, ResourceHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { PermissionDigest } from './digest';
import { fieldPermissionStates, PermissionsSnapshot } from './permissions';

const incidents = 'IncidentReports' as Resource;
const memberships = 'TeamMemberships' as Resource;
const alpha = 'ws-alpha' as Domain;

// The dotted keys are field-level entries; IncidentReportsArchive guards the prefix
// scan (a longer resource name sharing the prefix must not bleed in), and
// TeamMemberships is the keys-only case: a base entry with no field entries.
const digest: PermissionDigest = {
  IncidentReports: { Create: 'granted', List: 'granted' },
  'IncidentReports.summary': { Create: 'granted', List: 'granted' },
  'IncidentReports.severity': { Create: 'conditional', List: 'granted' },
  'IncidentReports.reporterContact': { List: 'granted' },
  IncidentReportsArchive: { Create: 'granted' },
  'IncidentReportsArchive.notes': { Create: 'granted' },
  TeamMemberships: { Create: 'granted' },
};

function snapshotWith(entries: [string, PermissionDigest][]): PermissionsSnapshot {
  return { digests: new Map(entries), domains: [] };
}

describe('fieldPermissionStates', () => {
  const snapshot = snapshotWith([['', digest]]);

  it('maps each field-level entry to its state, denied fields absent', () => {
    expect(fieldPermissionStates(snapshot, { resource: incidents, permission: CreatePermission })).toEqual({
      summary: 'granted',
      severity: 'conditional',
    });
  });

  it('answers per permission', () => {
    expect(fieldPermissionStates(snapshot, { resource: incidents, permission: 'List' as Permission })).toEqual({
      summary: 'granted',
      severity: 'granted',
      reporterContact: 'granted',
    });
  });

  it('never matches a longer resource name sharing the prefix', () => {
    const states = fieldPermissionStates(snapshot, { resource: incidents, permission: CreatePermission });
    expect(Object.keys(states)).not.toContain('notes');
  });

  it('is empty for a keys-only resource and for an unloaded scope', () => {
    expect(fieldPermissionStates(snapshot, { resource: memberships, permission: CreatePermission })).toEqual({});
    expect(
      fieldPermissionStates(snapshot, { resource: incidents, permission: CreatePermission, domain: alpha }),
    ).toEqual({});
  });

  it('asks the domain digest when the scope names one', () => {
    const scoped = snapshotWith([[alpha, digest]]);
    expect(fieldPermissionStates(scoped, { resource: incidents, permission: CreatePermission, domain: alpha })).toEqual(
      { summary: 'granted', severity: 'conditional' },
    );
  });
});

const api: ApiDescriptor = {
  resources: {
    IncidentReports: {
      resource: incidents,
      property: 'incidentReports',
      route: 'incident-reports',
      scope: 'domain',
      consolidated: true,
      keys: ['id'],
      operations: ['list', 'create'],
    },
    TeamMemberships: {
      resource: memberships,
      property: 'teamMemberships',
      route: 'team-memberships',
      scope: 'global',
      consolidated: true,
      keys: ['teamId', 'staffId'],
      operations: ['list', 'create'],
    },
  },
  methods: {},
  domainRoute: { segment: 'waystations', param: 'waystationID' },
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

interface IncidentReport {
  id: string;
  summary: string;
  severity: string;
  reporterContact: string;
}
interface TeamMembership {
  teamId: string;
  staffId: string;
}
interface Global {
  teamMemberships: ResourceHandle<TeamMembership, [string, string], 'list' | 'create', TeamMembership>;
}
interface Station {
  incidentReports: ResourceHandle<IncidentReport, [string], 'list' | 'create', Omit<IncidentReport, 'id'>>;
}

function testClient() {
  const client = createClient<Global, Station>(api, {
    baseUrl: '/api',
    transport: () => Promise.reject(new Error('no requests in this test')),
  });
  client.permissions.snapshot.set({
    digests: new Map([
      ['', digest],
      [alpha, digest],
    ]),
    domains: [alpha],
  });
  return client;
}

describe('grantedFields', () => {
  it('a handle answers its own scope, sorted', () => {
    const station = testClient().domain(alpha);
    expect(station.incidentReports.grantedFields(CreatePermission)).toEqual(['severity', 'summary']);
  });

  it('undefined when the digest has no field-level entries: no information, not "no fields"', () => {
    expect(testClient().teamMemberships.grantedFields(CreatePermission)).toBeUndefined();
  });

  it('the client resolves scope from the descriptor; a domain-scoped target with no domain answers undefined', () => {
    const client = testClient();
    expect(client.grantedFields(CreatePermission, incidents, alpha)).toEqual(['severity', 'summary']);
    expect(client.grantedFields(CreatePermission, incidents)).toBeUndefined();
    expect(client.grantedFields(CreatePermission, memberships)).toBeUndefined();
  });
});
