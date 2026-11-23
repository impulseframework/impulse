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
 *
 * Turning the switch on takes two things of the person who turns it: they are signed in
 * through the tenant's directory, and they have a password where their accounts have
 * one. The read says both for the person asking (`conditions`), each in the sentence the
 * method's refusal opens with, so a page says them before the person tries and holds no
 * copy of the words.
 */

/** The route of the switch as it stands, under the tenant's route: `GET <tenants>/<tenant>/directory-only`. */
export const directoryOnlyRoute = 'directory-only';

/** What the read needs of a client: its requester and the descriptor that names the tenant's route. */
export interface DirectoryOnlyClient {
  readonly request: Requester;
  readonly descriptor: Pick<ApiDescriptor, 'domainRoute'>;
}

/**
 * One thing turning a tenant directory only takes of the person who turns it, as the
 * server says it for the person asking. The method's refusal of a person who does not
 * meet it is `sentence`, a space, and `toDo`.
 */
export interface DirectoryOnlyCondition {
  /** The condition and why it is asked, as a sentence. */
  sentence: string;
  /** Whether the person asking meets it. */
  met: boolean;
  /** What the person does to meet it, as sentences; absent where they meet it. */
  toDo?: string;
}

/**
 * How many people a tenant's list of its people holds (`seated`: who holds a role there,
 * and who signs in through its directory), and how many of them the tenant's
 * directory-only switch keeps out (`outside`): the people who have never signed in
 * through the tenant's directory, counted as the warning that turns the switch on counts
 * them.
 */
export interface DirectoryOnlyCount {
  seated: number;
  outside: number;
}

/** A tenant's directory-only switch as it stands, and what turning it on takes of the person asking. */
export interface DirectoryOnlyState {
  directoryOnly: boolean;
  /** The tenant's directory as people know it, on or off; absent for a tenant with no directory. */
  directory?: string;
  /**
   * The role a person gets the first time they sign in through the tenant's directory, in
   * words; absent where the tenant admits the directory's people by invitation only.
   */
  defaultRole?: string;
  /**
   * Whether the accounts of the person asking have a password method: turning the switch
   * on asks them for a password, and an administrator of the tenant who has one still
   * enters while it is on.
   */
  passwords?: boolean;
  /** A sentence that says how the person asking signed in. */
  signedIn?: string;
  /** The organization's directory the person asking is signed in through, as people know it. */
  sessionDirectory?: string;
  /** How many people the tenant's list holds and how many of them the switch keeps out. */
  people?: DirectoryOnlyCount;
  /**
   * What turning the switch on takes of the person asking, in the order the server
   * checks. Absent while the switch is on and for a tenant with no directory, which is
   * refused before anything is asked of the person.
   */
  conditions?: DirectoryOnlyCondition[];
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
