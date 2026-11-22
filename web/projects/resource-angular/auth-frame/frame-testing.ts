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

/** The parts of a methods answer the account page reads: the password change, the question about a new password's strength, and the removal, what the person signs in with, forgetting this browser and the person's name. */
export const passwordMethods = {
  methods: [],
  changePassword: '/api/user/password/change',
  passwordStrength: '/api/user/password/strength',
  removePassword: '/api/user/password/remove',
  signInMeans: '/api/user/sign-in-means',
  forgetBrowser: '/api/user/browser/forget',
  changeName: '/api/user/name',
};

/** What the spec's server answers: the session, the digests by tenant ('' global), the tenant list. */
export interface SpecServer {
  session?: Record<string, unknown>;
  digests?: Record<string, PermissionDigest>;
  domains?: readonly { domain: string; name: string; closed?: string }[];
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
    provideFrame({ name: 'Lodestar', home: 'dashboard', menus: [], ...config }),
  ];
}

/**
 * The class names of one of the frame's components that are not its own: those its elements
 * carry wherever they are drawn, the page and its pop-ups alike, and those its stylesheet
 * names. An application's stylesheet is global, so a rule it writes for its own `.row`,
 * `.label` or `.bar` lands on every element of the library that carries that class; a name
 * that starts with the component's own element name is one no application writes for
 * itself. `host` is the component's element, and `prefix` what its names start with.
 */
export function notNamedFor(prefix: string, host: Element): string[] {
  const hostMark = Array.from(host.attributes).find((attribute) => attribute.name.startsWith('_nghost'))?.name;
  if (!hostMark) {
    return ['(no stylesheet of the component is on the page to read)'];
  }
  const mark = hostMark.replace('_nghost', '_ngcontent');
  const drawn = Array.from(document.querySelectorAll(`[${mark}]`)).flatMap((element) => Array.from(element.classList));
  const styled = Array.from(document.querySelectorAll('style'))
    .map((style) => style.textContent ?? '')
    .filter((sheet) => sheet.includes(`[${mark}]`))
    .flatMap((sheet) => Array.from(sheet.matchAll(/\.(-?[A-Za-z_][\w-]*)/g), (match) => match[1]));
  // What Material, the CDK and Angular's forms put on an element are theirs to name; Material
  // marks a button whose animations are off, as they are in a spec, `_mat-animation-noopable`.
  const theirs = /^_?(mat|mdc|cdk|ng)-|^(notranslate|material-icons)$/;
  return [...new Set([...drawn, ...styled])].filter((name) => !name.startsWith(prefix) && !theirs.test(name)).sort();
}
