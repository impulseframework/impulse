import { EnvironmentProviders, Provider } from '@angular/core';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { provideRouter, Routes } from '@angular/router';
import {
  ApiDescriptor,
  createClient,
  PermissionDigest,
  Resource,
  ResourceDescriptor,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT, trackStoreReads } from '@impulseframework/resource-angular/resource-client';
import { FrameConfig } from './frame';
import { provideFrame } from './provide-frame';

// What the frame's specs share: a descriptor with a tenant segment, a server the spec
// plays, and the providers. Not exported from the entry point.

const resource = (name: string, scope: 'global' | 'domain', route: string): ResourceDescriptor => ({
  resource: name as Resource,
  property: name.charAt(0).toLowerCase() + name.slice(1),
  route,
  scope,
  consolidated: false,
  keys: ['id'],
  operations: ['list', 'read'],
});

/** A tenanted API: a global FeatureFlags and Tenants, a tenant's Users and TenantDirectories. */
export const specDescriptor: ApiDescriptor = {
  resources: {
    FeatureFlags: resource('FeatureFlags', 'global', 'feature-flags'),
    Tenants: resource('Tenants', 'global', 'tenants'),
    Users: resource('Users', 'domain', 'users'),
    TenantDirectories: resource('TenantDirectories', 'domain', 'tenant-directories'),
  },
  methods: {},
  domainRoute: { segment: 'tenants', param: 'tenantID', word: { one: 'Customer', many: 'Customers' } },
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

/** The spec's descriptor for an application that issues API tokens. */
export const issuingDescriptor: ApiDescriptor = {
  ...specDescriptor,
  tokens: {
    route: 'tokens',
    revokeRoute: 'tokens/revoke',
    deviceRoute: 'tokens/device',
    approveRoute: 'tokens/approve',
    denyRoute: 'tokens/deny',
    outlets: [],
  },
};

/** The parts of a methods answer the account page reads: the password change and removal, forgetting this browser and the person's name. */
export const passwordMethods = {
  methods: [],
  changePassword: '/api/user/password/change',
  removePassword: '/api/user/password/remove',
  forgetBrowser: '/api/user/browser/forget',
  changeName: '/api/user/name',
};

/** What the spec's server answers: the session, the digests by tenant ('' global), the tenant list. */
export interface SpecServer {
  session?: Record<string, unknown>;
  digests?: Record<string, PermissionDigest>;
  domains?: readonly { domain: string; name: string }[];
  methods?: Record<string, unknown>;
  /** Any other route the spec plays; undefined falls through to the 404. */
  other?: (url: URL) => TransportResponse | undefined;
}

/** The scripted transport playing the server. */
export function specTransport(server: SpecServer): ScriptedTransport {
  return scriptedTransport((request: TransportRequest): TransportResponse => {
    const url = new URL(request.url, 'http://spec.test');
    switch (url.pathname) {
      case '/api/user/session':
        // Every read is an answer of its own, as a server's is: a spec that reads the
        // session twice sees what a page sees at the keepalive.
        return {
          status: 200,
          body: { ...(server.session ?? { authenticated: true, username: 'gale', email: 'gale@spec.example' }) },
        };
      case '/api/user/methods':
        return { status: 200, body: server.methods ?? { methods: [] } };
      case '/api/permission-digest':
        return { status: 200, body: server.digests?.[url.searchParams.get('domain') ?? ''] ?? {} };
      case '/api/user-domains':
        return { status: 200, body: server.domains ?? [] };
      case '/api/features':
        return { status: 200, body: { enabled: [] } };
      case '/api/feature-flags':
        return { status: 200, body: [], headers: { 'total-count': '0' } };
      default:
        return (
          server.other?.(url) ?? {
            status: 404,
            body: { message: `the spec's server has no ${request.method} ${url.pathname}` },
          }
        );
    }
  });
}

/** The frame's providers over the spec's server and descriptor, with the routes given. */
export function provideFrameTesting(
  transport: ScriptedTransport,
  config: Partial<FrameConfig> = {},
  routes: Routes = [],
  descriptor: ApiDescriptor = specDescriptor,
): (Provider | EnvironmentProviders)[] {
  trackStoreReads();
  return [
    { provide: RESOURCE_CLIENT, useValue: createClient(descriptor, { baseUrl: '/api', transport }) },
    provideRouter(routes),
    provideNoopAnimations(),
    provideFrame({ name: 'Redgrade', home: 'dashboard', menus: [], ...config }),
  ];
}
