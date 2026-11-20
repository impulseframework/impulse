import { describe, expect, it } from 'bun:test';
import { CreatePermission, Domain, Permission, Resource } from './brands';
import { createClient, ResourceHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { PermissionDigest } from './digest';
import {
  digestFailure,
  fieldPermissionStates,
  PermissionsSnapshot,
  PermissionStore,
  Requester,
  UserDomain,
} from './permissions';
import { ApiError } from './transport';

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

describe('the tenant list', () => {
  // A requester that answers the user-domains route with the list and records each path.
  const answering = (listed: unknown): { request: Requester; paths: string[] } => {
    const paths: string[] = [];
    const request = (async (_method: string, path: string) => {
      paths.push(path);
      return listed;
    }) as Requester;
    return { request, paths };
  };

  it('loads each domain with its name: domains() lists the ids, domainName() names each', async () => {
    const { request, paths } = answering([
      { domain: 'ridgeline', name: 'Ridgeline County' },
      { domain: 'bighorn', name: 'bighorn' },
    ]);
    const store = new PermissionStore(request, { digest: 'permission-digest', domains: 'user-domains' });
    expect(await store.loadDomains()).toEqual(['ridgeline' as Domain, 'bighorn' as Domain]);
    expect(paths).toEqual(['user-domains']);
    expect(store.domains()).toEqual(['ridgeline' as Domain, 'bighorn' as Domain]);
    expect(store.domainName('ridgeline' as Domain)).toBe('Ridgeline County');
    expect(store.domainName('bighorn' as Domain)).toBe('bighorn');
  });

  it('names a domain the list does not hold, and every domain before the list loads, by the domain itself', async () => {
    const store = new PermissionStore(answering([{ domain: 'ridgeline', name: 'Ridgeline County' }]).request, {
      digest: 'permission-digest',
      domains: 'user-domains',
    });
    expect(store.domainName('ridgeline' as Domain)).toBe('ridgeline');
    await store.loadDomains();
    expect(store.domainName('elsewhere' as Domain)).toBe('elsewhere');
  });

  it("keeps a tenant that is closed to this sign-in apart from the domains, with its name and the server's reason", async () => {
    const reason = 'Bastion admits only people who sign in through its own directory. You signed in with a password.';
    const store = new PermissionStore(
      answering([
        { domain: 'anvil', name: 'Anvil' },
        { domain: 'bastion', name: 'Bastion', closed: reason },
      ]).request,
      { digest: 'permission-digest', domains: 'user-domains' },
    );
    expect([...store.closedDomains()]).toEqual([]);
    expect(await store.loadDomains()).toEqual(['anvil' as Domain]);
    expect(store.domains()).toEqual(['anvil' as Domain]);
    expect([...store.closedDomains()]).toEqual([['bastion' as Domain, reason]]);
    expect(store.domainName('bastion' as Domain)).toBe('Bastion');
    store.clear();
    expect([...store.closedDomains()]).toEqual([]);
  });

  it('an empty answer, or none, lists nothing; clear() forgets the names', async () => {
    const store = new PermissionStore(answering(null).request, {
      digest: 'permission-digest',
      domains: 'user-domains',
    });
    expect(await store.loadDomains()).toEqual([]);
    const named = new PermissionStore(answering([{ domain: 'ridgeline', name: 'Ridgeline County' }]).request, {
      digest: 'permission-digest',
      domains: 'user-domains',
    });
    await named.loadDomains();
    named.clear();
    expect(named.domains()).toEqual([]);
    expect(named.domainName('ridgeline' as Domain)).toBe('ridgeline');
  });
});

// A read of a digest that fails is not an answer. The cache holds no failure as an empty
// digest, which would say the person holds nothing: a scope never read stays unloaded, a
// scope read before keeps its last answer. Whoever asked sees the rejection, and every
// listener hears which scope it was and whether the server refused the read.
describe('a digest read that fails', () => {
  const list = { resource: incidents, permission: 'List' as Permission };

  /** A server that answers each tenant's digest as the script says: a digest, or what the request throws. */
  const serving = (script: Record<string, PermissionDigest | Error>): PermissionStore => {
    const request = (async (_method: string, path: string, options?: { query?: URLSearchParams }) => {
      if (path === 'user-domains') {
        return [{ domain: alpha, name: 'Alpha' }];
      }
      const answer = script[options?.query?.get('domain') ?? ''];
      if (answer instanceof Error) {
        throw answer;
      }
      return answer;
    }) as Requester;
    return new PermissionStore(request, { digest: 'permission-digest', domains: 'user-domains' });
  };

  const refusal = (status: number): ApiError => new ApiError('GET', 'permission-digest', status, { message: 'no' });

  it('leaves a scope that was never read unloaded: nothing is known, which is not an empty digest', async () => {
    const store = serving({ [alpha]: refusal(500) });
    await expect(store.loadDigest(alpha)).rejects.toBeInstanceOf(ApiError);
    expect(store.hasDigest(alpha)).toBe(false);
    expect(store.digest(alpha)).toBeUndefined();
  });

  it('keeps the last answer of a scope that was read before, for a failure and for a refusal', async () => {
    for (const status of [500, 403, 404]) {
      const script: Record<string, PermissionDigest | Error> = { [alpha]: digest };
      const store = serving(script);
      await store.loadDigest(alpha);
      script[alpha] = refusal(status);
      await expect(store.loadDigest(alpha)).rejects.toBeInstanceOf(ApiError);
      expect(store.can({ ...list, domain: alpha })).toBe(true);
      expect(store.digest(alpha)).toEqual(digest);
    }
  });

  it("keeps every scope's last answer when a refresh cannot read one of them", async () => {
    const script: Record<string, PermissionDigest | Error> = { '': digest, [alpha]: digest };
    const store = serving(script);
    await Promise.all([store.loadDigest(), store.loadDigest(alpha)]);
    script[alpha] = new Error('the request never arrived');
    await expect(store.refresh()).rejects.toBeInstanceOf(Error);
    expect(store.can(list)).toBe(true);
    expect(store.can({ ...list, domain: alpha })).toBe(true);
  });

  // The server's 403 and 404 are a refusal; anything else is a failure.
  const cases: { name: string; thrown: Error; want: string }[] = [
    { name: 'a 403', thrown: refusal(403), want: 'refused' },
    { name: 'a 404', thrown: refusal(404), want: 'refused' },
    { name: 'a 500', thrown: refusal(500), want: 'failed' },
    { name: 'a 401', thrown: refusal(401), want: 'failed' },
    { name: 'a request that never arrived', thrown: new Error('the request never arrived'), want: 'failed' },
  ];
  for (const tt of cases) {
    it(`tells each listener the scope and that the read ${tt.want}, for ${tt.name}`, async () => {
      const store = serving({ '': tt.thrown, [alpha]: tt.thrown });
      const heard: string[] = [];
      const stop = store.onUnread((domain, failure) => heard.push(`${domain ?? 'global'} ${failure}`));
      await expect(store.loadDigest(alpha)).rejects.toBe(tt.thrown);
      await expect(store.loadDigest()).rejects.toBe(tt.thrown);
      expect(heard).toEqual([`${alpha} ${tt.want}`, `global ${tt.want}`]);
      // A listener that was stopped hears nothing more.
      stop();
      await expect(store.loadDigest(alpha)).rejects.toBe(tt.thrown);
      expect(heard).toHaveLength(2);
    });
  }

  it('tells no listener of a read that answers, an empty digest among them', async () => {
    const store = serving({ [alpha]: {} });
    const heard: string[] = [];
    store.onUnread((domain, failure) => heard.push(`${domain ?? 'global'} ${failure}`));
    expect(await store.loadDigest(alpha)).toEqual({});
    expect(store.hasDigest(alpha)).toBe(true);
    expect(heard).toEqual([]);
  });

  it('asks the server again on the next read: a failure is not remembered as an answer', async () => {
    const script: Record<string, PermissionDigest | Error> = { [alpha]: refusal(500) };
    const store = serving(script);
    await expect(store.ensure({ ...list, domain: alpha })).rejects.toBeInstanceOf(ApiError);
    script[alpha] = digest;
    expect(await store.ensure({ ...list, domain: alpha })).toBe(true);
  });
});

// The server answers a digest with no entries for a domain the session does not reach (a
// seat taken away, a domain that is not there), the same answer it gives for a domain where
// the person holds nothing. The person's list of domains tells the two apart, so the store
// reads the list again before it keeps a domain's empty digest: a domain that has left the
// list is a refusal, and one the list still names is held as empty, which is then true.
describe("a domain's digest that reads empty", () => {
  const list = { resource: incidents, permission: 'List' as Permission, domain: alpha };
  const named: UserDomain[] = [{ domain: alpha, name: 'Alpha' }];

  /** A server that answers alpha's digest, the global one and the person's list as the script says when asked. */
  const serving = (script: {
    digest: PermissionDigest | Error;
    global?: PermissionDigest;
    listed: UserDomain[] | Error;
  }): { store: PermissionStore; paths: string[]; heard: string[] } => {
    const paths: string[] = [];
    const request = (async (_method: string, path: string, options?: { query?: URLSearchParams }) => {
      paths.push(path);
      const tenant = options?.query?.get('domain');
      const answer = path === 'user-domains' ? script.listed : tenant ? script.digest : (script.global ?? {});
      if (answer instanceof Error) {
        throw answer;
      }
      return answer;
    }) as Requester;
    const store = new PermissionStore(request, { digest: 'permission-digest', domains: 'user-domains' });
    const heard: string[] = [];
    store.onUnread((domain, failure) => heard.push(`${domain ?? 'global'} ${failure}`));
    return { store, paths, heard };
  };

  /** What a read of alpha's digest throws. */
  const thrownBy = async (store: PermissionStore): Promise<unknown> =>
    store.loadDigest(alpha).then(
      () => undefined,
      (error: unknown) => error,
    );

  it('is a refusal for a domain that has left the list, and the scope keeps its last answer', async () => {
    const script = { digest: digest as PermissionDigest | Error, listed: named as UserDomain[] | Error };
    const { store, heard } = serving(script);
    await store.loadDomains();
    await store.loadDigest(alpha);

    // The seat is taken away: the server lists the domain no more, and answers its digest empty.
    script.digest = {};
    script.listed = [];
    const thrown = await thrownBy(store);

    expect((thrown as Error | undefined)?.name).toBe('DomainUnlistedError');
    expect(digestFailure(thrown)).toBe('refused');
    expect(heard).toEqual([`${alpha} refused`]);
    // Nothing drawn from the scope is drawn again as a person who holds nothing.
    expect(store.can(list)).toBe(true);
    expect(store.digest(alpha)).toEqual(digest);
    expect(store.domains()).toEqual([]);
  });

  it('is a refusal for a domain that was never read and the list does not name, which stays unloaded', async () => {
    const { store, heard } = serving({ digest: {}, listed: [] });
    expect(digestFailure(await thrownBy(store))).toBe('refused');
    expect(heard).toEqual([`${alpha} refused`]);
    expect(store.hasDigest(alpha)).toBe(false);
  });

  it('is a refusal for a domain that is closed to this sign-in, though the list names it', async () => {
    const { store, heard } = serving({
      digest: {},
      listed: [{ domain: alpha, name: 'Alpha', closed: 'Alpha admits only its own directory.' }],
    });
    expect(digestFailure(await thrownBy(store))).toBe('refused');
    expect(heard).toEqual([`${alpha} refused`]);
    expect(store.hasDigest(alpha)).toBe(false);
  });

  it('is an answer for a domain the list still names: the person holds nothing there', async () => {
    const { store, paths, heard } = serving({ digest: {}, listed: named });
    expect(await store.loadDigest(alpha)).toEqual({});
    // The list was read again, after the digest, before the digest was kept.
    expect(paths).toEqual(['permission-digest', 'user-domains']);
    expect(store.hasDigest(alpha)).toBe(true);
    expect(store.can(list)).toBe(false);
    expect(heard).toEqual([]);
  });

  it('fails the read where the list cannot be read: nothing says which of the two the empty digest was', async () => {
    const script = { digest: {} as PermissionDigest | Error, listed: new Error('the request never arrived') };
    const { store, heard } = serving(script);
    const thrown = await thrownBy(store);
    expect(thrown).toBe(script.listed);
    expect(digestFailure(thrown)).toBe('failed');
    expect(heard).toEqual([`${alpha} failed`]);
    expect(store.hasDigest(alpha)).toBe(false);
  });

  it('reads no list for a digest with entries, or for what the person holds outside every domain', async () => {
    const { store, paths, heard } = serving({ digest, global: {}, listed: [] });
    await store.loadDigest(alpha);
    expect(await store.loadDigest()).toEqual({});
    expect(paths).toEqual(['permission-digest', 'permission-digest']);
    expect(store.hasDigest()).toBe(true);
    expect(heard).toEqual([]);
  });

  it('tells a refresh of the domain that left as a refusal, and every other scope keeps what the refresh read', async () => {
    const script = {
      digest: digest as PermissionDigest | Error,
      global: { TeamMemberships: { Create: 'granted' } } as PermissionDigest,
      listed: named as UserDomain[] | Error,
    };
    const { store, heard } = serving(script);
    await Promise.all([store.loadDomains(), store.loadDigest(), store.loadDigest(alpha)]);

    script.digest = {};
    script.listed = [];
    script.global = {};
    const thrown = await store.refresh().then(
      () => undefined,
      (error: unknown) => error,
    );

    expect(digestFailure(thrown)).toBe('refused');
    expect(heard).toEqual([`${alpha} refused`]);
    expect(store.can(list)).toBe(true);
    expect(store.can({ resource: memberships, permission: CreatePermission })).toBe(false);
  });
});
