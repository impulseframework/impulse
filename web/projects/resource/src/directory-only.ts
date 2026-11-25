import { Domain } from './brands';
import { ApiDescriptor } from './descriptor';
import { Requester } from './permissions';

/**
 * A tenant's directory-only switch closes the tenant to every sign-in but one through
 * its own directory. The switch is the tenant's own: the two methods that turn it run
 * in the tenant's scope (the generated `Make<Record>DirectoryOnly` and
 * `End<Record>DirectoryOnly`, method handles on `api.domain(tenant)`), and the read
 * here, of the tenant's sign-in settings as they stand, is served under the tenant's
 * route and asked under the grant of any of the tenant's own sign-in methods, so the
 * tenant's administrator, who does not read the tenant record, sees the switch as it
 * stands. The read is the library's on every application whose tenant record declares
 * a directories record.
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

/** The route of a tenant's sign-in settings as they stand, under the tenant's route: `GET <tenants>/<tenant>/sign-in-settings`. */
export const tenantSignInSettingsRoute = 'sign-in-settings';

/** What the read needs of a client: its requester and the descriptor that names the tenant's route. */
export interface TenantSignInSettingsClient {
  readonly request: Requester;
  readonly descriptor: Pick<ApiDescriptor, 'domainRoute'>;
}

/**
 * One thing a change of a tenant's sign-in settings takes of the person who makes it, as
 * the server says it for the person asking. The call's refusal of a person who does not
 * meet it is `sentence`, a space, and `toDo`, so a page that shows a condition and the
 * call that refuses on it say the same words.
 */
export interface TenantSignInCondition {
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
  /** The count auth by auth; the two numbers above are their sums. */
  shares?: DirectoryOnlyShare[];
}

/**
 * One auth's part of a tenant's count: what a page calls the auth's people (`label`), the
 * tenant's list of them as the description names it (`people`), how many people that
 * list holds (`seated`) and how many of those the switch keeps out (`outside`).
 */
export interface DirectoryOnlyShare {
  label: string;
  people: string;
  seated: number;
  outside: number;
}

/**
 * The second factor's part of a tenant's sign-in settings: whether the tenant says its
 * directory asks its people for a second factor of its own (`vouched`), whether the
 * auth of the person asking asks everyone for one now (`asked`; true where no auth
 * answers), the one sentence a page shows for the two (`says`), and, while the tenant
 * has not said so and the application asks, what saying so takes of the person asking
 * (`conditions`).
 */
export interface DirectorySecondFactor {
  vouched: boolean;
  says: string;
  asked: boolean;
  /** The one thing saying the word takes of the person asking; absent while the tenant vouches or the application asks nobody. */
  conditions?: TenantSignInCondition[];
}

/**
 * How a tenant's people sign in, as it stands, said for the person asking: the tenant's
 * directory, the role its people get, whether it is directory only and whom that keeps
 * out, whether the tenant vouches for its directory's second factor, and what each change
 * takes of the person asking.
 */
export interface TenantSignInSettings {
  /** Whether the tenant is directory only; false where the tenant record declares no switch. */
  directoryOnly: boolean;
  /** The tenant's directory as people know it, on or off; absent for a tenant with no directory. */
  directory?: string;
  /**
   * The role a person gets when they sign in through the tenant's directory and hold
   * nothing in the tenant, in words; absent where the tenant admits the directory's people
   * by invitation only.
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
   * checks. Absent while the switch is on, for a tenant with no directory, which is
   * refused before anything is asked of the person, and where the tenant record declares
   * no switch.
   */
  conditions?: TenantSignInCondition[];
  /**
   * Whether the tenant vouches for its directory's second factor; absent for a tenant with
   * no directory and where the directories record carries no word for it.
   */
  secondFactor?: DirectorySecondFactor;
}

/** The tenant's route for the read; an API with no tenant route does not serve it. */
function tenantRoute(client: TenantSignInSettingsClient, tenant: Domain, route: string): string {
  const domainRoute = client.descriptor.domainRoute;
  if (!domainRoute) {
    throw new Error(`${route} is served under a tenant's route, and the API declares none`);
  }
  return `${domainRoute.segment}/${encodeURIComponent(tenant)}/${route}`;
}

/**
 * Reads the tenant's sign-in settings as they stand, for a person who may change one of
 * them. The server's refusal throws as every request's does.
 *
 *     const { directoryOnly, secondFactor } = await tenantSignInSettings(api, tenant);
 */
export function tenantSignInSettings(
  client: TenantSignInSettingsClient,
  tenant: Domain,
): Promise<TenantSignInSettings> {
  return client.request<TenantSignInSettings>('GET', tenantRoute(client, tenant, tenantSignInSettingsRoute));
}
