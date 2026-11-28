import { ApiDescriptor, Resource, ResourceDescriptor } from '@impulseframework/resource';
import { acrossTenantsLinkKey, refuseAcrossTenantsConfig, tenantLink, tenantWord } from './across-tenants';

// The pure parts of a list page across tenants: what a configuration is refused for,
// the word the tenant column is headed with, and the link a row opens by.

const resource = (name: string, extra: Partial<ResourceDescriptor> = {}): ResourceDescriptor => ({
  resource: name as Resource,
  property: name.toLowerCase(),
  route: name.toLowerCase(),
  scope: 'domain',
  consolidated: false,
  keys: ['id'],
  operations: ['list', 'read'],
  ...extra,
});

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'sectors', param: 'sectorID', word: { one: 'Sector', many: 'Sectors' } },
  resources: {
    Missions: resource('Missions', { acrossTenants: true }),
    Refits: resource('Refits'),
  },
  methods: {},
};

function client(): { descriptor: ApiDescriptor; warn: (message: string) => void; warnings: string[] } {
  const warnings: string[] = [];
  return { descriptor, warnings, warn: (message) => warnings.push(message) };
}

describe('refuseAcrossTenantsConfig', () => {
  it('passes a page that does not ask for a list across tenants', () => {
    const api = client();
    expect(() => refuseAcrossTenantsConfig('Refits' as Resource, { enableRowExpansion: true }, api)).not.toThrow();
    expect(api.warnings).toEqual([]);
  });

  it('passes a list across tenants over a resource declared for it', () => {
    const api = client();
    expect(() =>
      refuseAcrossTenantsConfig('Missions' as Resource, { acrossTenants: true, enableRowExpansion: false }, api),
    ).not.toThrow();
    expect(api.warnings).toEqual([]);
  });

  it('refuses one over a resource the API does not serve across tenants, naming it and the way out', () => {
    expect(() =>
      refuseAcrossTenantsConfig('Refits' as Resource, { acrossTenants: true, enableRowExpansion: false }, client()),
    ).toThrow(
      'Refits: acrossTenants is set, but the resource is not listed across tenants: declare @listAcrossTenants on its struct and regenerate, or remove acrossTenants and list it inside one tenant',
    );
    expect(() =>
      refuseAcrossTenantsConfig('Hangars' as Resource, { acrossTenants: true, enableRowExpansion: false }, client()),
    ).toThrow('Hangars: acrossTenants is set, but the resource is not listed across tenants');
  });

  it('refuses row expansion, since a row is read inside its own tenant', () => {
    expect(() =>
      refuseAcrossTenantsConfig('Missions' as Resource, { acrossTenants: true, enableRowExpansion: true }, client()),
    ).toThrow(
      "Missions: enableRowExpansion is set on a list across tenants, but a row is read inside its own tenant: remove enableRowExpansion, and the row's View link opens it there",
    );
  });

  it("builds a page over a declared resource and says nothing: whether it is live is the page's own choice", () => {
    const api = client();
    expect(() =>
      refuseAcrossTenantsConfig('Missions' as Resource, { acrossTenants: true, enableRowExpansion: false }, api),
    ).not.toThrow();
    expect(api.warnings).toEqual([]);
  });
});

describe('tenantWord', () => {
  it("is the application's word for one tenant", () => {
    expect(tenantWord(descriptor)).toBe('Sector');
  });

  it('is Tenant where the descriptor carries no word', () => {
    expect(tenantWord({ ...descriptor, domainRoute: { segment: 'sectors', param: 'sectorID' } })).toBe('Tenant');
    expect(tenantWord({ ...descriptor, domainRoute: undefined })).toBe('Tenant');
  });

  it("begins with a capital where the descriptor's word does not, and keeps the rest as written", () => {
    const worded = (one: string): ApiDescriptor => ({
      ...descriptor,
      domainRoute: { segment: 'outlets', param: 'outletID', word: { one, many: `${one}s` } },
    });
    expect(tenantWord(worded('outlet'))).toBe('Outlet');
    expect(tenantWord(worded('API client'))).toBe('API client');
  });
});

describe('tenantLink', () => {
  const anvil = 'a1111111-1111-4111-8111-111111111111';

  it("puts the row's tenant where the route names the tenant parameter, then the key", () => {
    expect(tenantLink('sectors/:sectorID/missions', 'sectorID', anvil, 'm1')).toBe(`/sectors/${anvil}/missions/m1`);
    expect(tenantLink('/sectors/:sectorID/missions', 'sectorID', 'b2', 'm 9')).toBe('/sectors/b2/missions/m%209');
  });

  it('draws no link to a route that names no tenant parameter: such a page is in no tenant', () => {
    expect(tenantLink('clients', 'sectorID', anvil, 'c1')).toBeUndefined();
    expect(tenantLink('clients', 'sectorID', undefined, 'c1')).toBeUndefined();
  });

  it('draws no link for a row with no key, or with no tenant', () => {
    expect(tenantLink('sectors/:sectorID/missions', 'sectorID', anvil, undefined)).toBeUndefined();
    expect(tenantLink('sectors/:sectorID/missions', 'sectorID', anvil, '')).toBeUndefined();
    expect(tenantLink('sectors/:sectorID/missions', 'sectorID', undefined, 'm1')).toBeUndefined();
    expect(tenantLink('sectors/:sectorID/missions', 'sectorID', '', 'm1')).toBeUndefined();
  });

  it('rides the display row under a key no field can take', () => {
    expect(acrossTenantsLinkKey).toContain(':');
  });
});
