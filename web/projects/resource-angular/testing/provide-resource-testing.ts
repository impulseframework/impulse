import { EnvironmentProviders, Provider } from '@angular/core';
import { provideRouter } from '@angular/router';
import { ApiDescriptor, ClientBase, createClient, Transport } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/** What provideResourceTesting builds the client from. Both are optional. */
export interface ResourceTestingOptions {
  /**
   * The transport the client rides. A spec that asserts what a component asked for, or
   * scripts the rows it reads, passes its own scriptedTransport and reads `requests`
   * from it afterwards. Absent, a fresh one answers every request with a 200 and no body.
   */
  transport?: ScriptedTransport;
  /**
   * Builds the client over the transport. An application passes its generated createApi,
   * `(transport) => createApi({ baseUrl: '/api', transport })`, so the component under test
   * reads the descriptor its pages read. Absent, the client is createClient over an empty
   * descriptor at baseUrl '/api': no resources, no methods, every route read whole.
   */
  client?: (transport: Transport) => ClientBase;
}

/** A descriptor that describes nothing: the default client's. */
const emptyDescriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

/**
 * The providers a spec of a component over the library needs: RESOURCE_CLIENT over a
 * scripted transport, so no request leaves the test and every request is on record, and
 * an empty router, since the components read the route and link to others. Use it in
 * the TestBed of a spec the way the library's own creation specs do:
 *
 *     const transport = scriptedTransport({ status: 200, body: [{ id: 'm1', name: 'Anvil run' }] });
 *     await TestBed.configureTestingModule({
 *       imports: [MissionsPageComponent],
 *       providers: [provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: '/api', transport: t }) })],
 *     }).compileComponents();
 *     // ... create the fixture, TestBed.tick(), await TestBed.inject(ApplicationRef).whenStable()
 *     expect(transport.requests.map((r) => r.url)).toEqual(['/api/missions?limit=all']);
 *
 * The client's error hook and error handler are not provided: a spec that wants the 401
 * redirect or the uncaught-error notice under test uses provideResourceClient with its own
 * transport. A spec that reads a component's tenant sets RESOURCE_DOMAIN beside this.
 */
export function provideResourceTesting(options: ResourceTestingOptions = {}): (Provider | EnvironmentProviders)[] {
  const transport = options.transport ?? scriptedTransport();
  const build =
    options.client ?? ((t: Transport): ClientBase => createClient(emptyDescriptor, { baseUrl: '/api', transport: t }));
  return [{ provide: RESOURCE_CLIENT, useValue: build(transport) }, provideRouter([])];
}
