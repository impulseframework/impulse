import { Domain } from './brands';
import { ApiDescriptor } from './descriptor';
import { Requester } from './permissions';

/**
 * A tenant's directory-only switch closes the tenant to every sign-in but one through
 * its own directory. The switch is the tenant's own: the method that turns it runs in
 * the tenant's scope (the generated `Set<Record>DirectoryOnly`, a method handle on
 * `api.domain(tenant)`), and the two reads here are served under the tenant's route and
 * asked under that method's grant, so the tenant's administrator, who does not read the
 * tenant record, sees the switch and what turning it on would do. The reads are the
 * library's on every application whose tenant record declares the switch. The server
 * refuses as the method refuses, in a sentence the person can act on: a tenant that has
 * no directory, and a person who is not signed in through the tenant's directory
 * themselves. A tenant that is directory only already has nothing to turn on, and the
 * consequence read says so.
 */

/** The route of the switch as it stands, under the tenant's route: `GET <tenants>/<tenant>/directory-only`. */
export const directoryOnlyRoute = 'directory-only';

/** The route of what turning the switch on would do: `GET <tenants>/<tenant>/directory-only/consequence`. */
export const directoryOnlyConsequenceRoute = 'directory-only/consequence';

/** What the two reads need of a client: its requester and the descriptor that names the tenant's route. */
export interface DirectoryOnlyClient {
  readonly request: Requester;
  readonly descriptor: Pick<ApiDescriptor, 'domainRoute'>;
}

/** A tenant's directory-only switch as it stands. */
export interface DirectoryOnlyState {
  directoryOnly: boolean;
}

/** A person seated in the tenant who has never signed in through its directory. */
export interface DirectoryOutsider {
  userId: string;
  username: string;
  /** The person's name as people read it, absent where the account holds none. */
  name?: string;
}

/**
 * What turning a tenant's directory-only switch on would do, in the server's words:
 * the tenant's directory as a sentence names it (`directory`, "Google Workspace
 * acme.example") and as its people know it (`connection`, "acme.example"), the sentence
 * a confirmation shows, how many people it keeps out (`count`), and what the confirming
 * button says it does (`confirm`, "Admit only people who sign in through acme.example").
 * `people` names them for a caller who may list them: the server names a person only to
 * someone who holds the tenant's list of its people, so the list holds fewer people than
 * `count` says, or none, for a caller who does not.
 */
export interface DirectoryOnlyConsequence {
  directory: string;
  connection: string;
  sentence: string;
  count: number;
  people: DirectoryOutsider[];
  confirm: string;
}

/** The tenant's route for one of the reads; an API with no tenant route serves neither. */
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

/**
 * Asks what turning the tenant's directory-only switch on would do, for a page to show
 * before it turns the switch on. It writes nothing. The server's refusal throws as every
 * request's does, with the server's sentence as the error's message.
 *
 *     const consequence = await directoryOnlyConsequence(api, tenant);
 */
export function directoryOnlyConsequence(
  client: DirectoryOnlyClient,
  tenant: Domain,
): Promise<DirectoryOnlyConsequence> {
  return client.request<DirectoryOnlyConsequence>('GET', tenantRoute(client, tenant, directoryOnlyConsequenceRoute));
}
