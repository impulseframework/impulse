import { Domain } from './brands';
import { ApiDescriptor } from './descriptor';
import { Requester } from './permissions';

/**
 * A tenant's directory-only switch closes the tenant to every sign-in but one through
 * its own directory. The switch is the tenant's own: the two methods that turn it run
 * in the tenant's scope (the generated `Make<Record>DirectoryOnly` and
 * `End<Record>DirectoryOnly`, method handles on `api.domain(tenant)`), and the read
 * here is served under the tenant's route and asked under the grant of either method,
 * so the tenant's administrator, who does not read the tenant record, sees the switch
 * as it stands. The read is the library's on every application whose tenant record
 * declares the switch.
 *
 * What turning the switch on would do is the method's own warning, in the server's
 * words: `api.domain(tenant).make<Record>DirectoryOnly.warning({})` answers the
 * sentence, the people it names for a caller who may list them, and the words for the
 * button, or throws the refusal the call itself would answer (a tenant that has no
 * directory, a person who is not signed in through the tenant's directory themselves, a
 * tenant that is directory only already). The dialog a page runs the method through
 * shows the warning and holds its button until the person has typed the method's word.
 */

/** The route of the switch as it stands, under the tenant's route: `GET <tenants>/<tenant>/directory-only`. */
export const directoryOnlyRoute = 'directory-only';

/** What the read needs of a client: its requester and the descriptor that names the tenant's route. */
export interface DirectoryOnlyClient {
  readonly request: Requester;
  readonly descriptor: Pick<ApiDescriptor, 'domainRoute'>;
}

/** A tenant's directory-only switch as it stands. */
export interface DirectoryOnlyState {
  directoryOnly: boolean;
}

/** The tenant's route for the read; an API with no tenant route does not serve it. */
function tenantRoute(client: DirectoryOnlyClient, tenant: Domain, route: string): string {
  const domainRoute = client.descriptor.domainRoute;
  if (!domainRoute) {
    throw new Error(`${route} is served under a tenant's route, and the API declares none`);
  }
  return `${domainRoute.segment}/${encodeURIComponent(tenant)}/${route}`;
}

/**
 * Reads the tenant's directory-only switch as it stands, for the person who may turn it.
 * The server's refusal throws as every request's does.
 *
 *     const { directoryOnly } = await directoryOnlyState(api, tenant);
 */
export function directoryOnlyState(client: DirectoryOnlyClient, tenant: Domain): Promise<DirectoryOnlyState> {
  return client.request<DirectoryOnlyState>('GET', tenantRoute(client, tenant, directoryOnlyRoute));
}
