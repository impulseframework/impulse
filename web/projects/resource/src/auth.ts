import { ClientResponse, ResponseRequester } from './permissions';
import { Store } from './store';
import { retryAfterHeader, retryAfterSeconds } from './transport';
import { Warn, warnOnce } from './warnings';

/**
 * Where the generated router serves the login page's methods list, under the API prefix:
 * `GET <prefix>/user/methods`, public, before any session and without an XSRF token.
 */
export const loginMethodsRoute = 'user/methods';

/**
 * How a sign-in method starts: `password` is a form the page posts to the method's path
 * with `{ username, password }`; `redirect` is a navigation to the method's path that sends
 * the browser away to the identity provider and brings it back signed in, refused, or
 * waiting on a pending step.
 */
export type LoginMethodKind = 'password' | 'redirect';

/**
 * One sign-in method the server offers, as its methods list describes it. A page renders
 * from the list alone: it names no path, no label and no method of its own, so the browser
 * bundle is the same in every environment and a method that is not registered has no
 * button.
 */
export interface LoginMethod {
  /**
   * The name the method was registered under on the server's Auth: `password`, or the
   * application's own name for an external registration (`google`, `lakeside-entra`). Label
   * overrides are keyed by it.
   */
  readonly name: string;
  /** The library method behind the registration: `password`, `google`, `azure`, `workos`, or one a later server adds. */
  readonly method: string;
  readonly kind: LoginMethodKind;
  /** What the page shows for the method, in the application's words: the default a page may override by name. */
  readonly label: string;
  /**
   * Where the server mounted the method's login handler: a path in the application carrying
   * the API prefix, such as `/api/user/google/login`. The form posts to it; the redirect
   * goes to it.
   */
  readonly path: string;
}

/**
 * The paths of the pending sign-in's handlers: its status, the password confirmation, the
 * cancel, and the sign-in code's three, present on a server that sends mail: `sendCode`,
 * where the page has the code mailed; `verifyCode`, where it posts the code entered; and
 * `enrollEmail`, where a person whose account holds no email address gives one for the code.
 */
export interface PendingRoutes {
  readonly status: string;
  readonly confirm: string;
  readonly cancel: string;
  readonly sendCode?: string;
  readonly verifyCode?: string;
  readonly enrollEmail?: string;
}

/**
 * The invitation block of the methods list: the paths of the invitation handlers, and the
 * invitation the browser carries. `open` is where the page posts the token a mailed
 * invitation carried; `accept`, where a signed-in person accepts the invitation the browser
 * carries; and `signUp`, where the invited person creates an account with a password,
 * present when the server offers a password method. `standing` describes the invitation
 * the browser carries, present only while it carries a live one: a login page the browser
 * comes back to without the token, after a redirect method refused the sign-in, shows the
 * invitation again from it.
 */
export interface InvitationRoutes {
  readonly open: string;
  readonly accept: string;
  readonly signUp?: string;
  readonly standing?: StandingInvitation;
}

/**
 * The invitation the browser carries, as the methods list describes it: what opening the
 * link answered, less `signUp`, which the block's `signUp` route says. The list carries
 * none when the browser carries no live invitation.
 */
export interface StandingInvitation {
  /** The address the invitation was mailed to. */
  readonly email: string;
  /** The tenant the invitation seats the person in; empty for the application's global surface. */
  readonly tenant: string;
  /** When the invitation stops being accepted, as the server wrote it. */
  readonly expiresAt: string;
  /** An account created from the invitation has the address as its username; the person chooses one otherwise. */
  readonly usernameIsEmail: boolean;
  /** The registration names of the listed directory methods that could admit the address, as `OpenedInvitation.methods`; absent when the server did not say. */
  readonly methods?: readonly string[];
  /** An account already holds the invited address; absent when the server did not say (an older server). */
  readonly accountExists?: boolean;
  /** That account signs in with a password; absent when the server did not say. */
  readonly hasPassword?: boolean;
}

/** The paths of the email address handlers: `add`, where a signed-in person asks for a confirmation link, and `verify`, where the page posts the token it carried. */
export interface EmailRoutes {
  readonly add: string;
  readonly verify: string;
}

/** The paths of the sign-up handlers: `start`, where an address asks for its link; `open`, where the page posts the token; `complete`, where the account is created. */
export interface SignUpRoutes {
  readonly start: string;
  readonly open: string;
  readonly complete: string;
}

/**
 * What the methods route answers: the methods in the order the server lists them, the
 * pending sign-in's routes, and the routes of the mailed links the server serves.
 */
export interface LoginMethods {
  readonly methods: readonly LoginMethod[];
  /** The pending sign-in's routes; absent on a server that wired none, where no sign-in ever waits. */
  readonly pending?: PendingRoutes;
  /**
   * The path of the password method's change: where a signed-in person posts
   * `{ oldPassword, newPassword }`, including one whose operator-set password must change
   * before anything else is served. Absent on a server with no password method.
   */
  readonly changePassword?: string;
  /**
   * The password method's username is the person's email address: the form asks for the
   * address, and an account created from a mailed link takes no username of its own.
   * Absent, or false, where the username is a name of the person's choosing.
   */
  readonly usernameIsEmail?: boolean;
  /** The invitation routes, and the invitation the browser carries; absent on a server that serves no invitations. */
  readonly invitation?: InvitationRoutes;
  /** The email address routes; absent on a server that sends no mail. */
  readonly email?: EmailRoutes;
  /** The sign-up routes; absent on a server whose password method does not offer sign-up, or that has none. */
  readonly signUp?: SignUpRoutes;
}

/** An invitation as opening it describes it to the page. */
export interface OpenedInvitation {
  /** The address the invitation was mailed to; the account that accepts it holds it, verified, unless it already holds a verified one. */
  readonly email: string;
  /** The tenant the invitation seats the person in; empty for the application's global surface. */
  readonly tenant: string;
  /** When the invitation stops being accepted, as the server wrote it. */
  readonly expiresAt: string;
  /** The person may create an account with a password here (`signUpFromInvitation`); the server offers a password method. */
  readonly signUp: boolean;
  /** That account's username is the address; the person chooses one otherwise. */
  readonly usernameIsEmail: boolean;
  /**
   * The registration names of the listed directory methods that could admit the address,
   * each a listed method's `name`, so a page shows those buttons alone while the invitation
   * stands; the password method is never among them, `signUp` being its word. Possibly
   * empty; absent when the server did not say.
   */
  readonly methods?: readonly string[];
  /**
   * An account already holds the invited address, and whether it signs in with a password:
   * the page offers that account's password form, and offers to create an account only
   * where none holds the address. Absent when the server did not say (an older server),
   * where the page offers every path.
   */
  readonly accountExists?: boolean;
  readonly hasPassword?: boolean;
}

/**
 * What opening an invitation answers: `opened` with the invitation, the browser now
 * carrying it so the sign-in that follows accepts it; or the server's refusal, a 404 in
 * the server's words for a link that has expired or was already used.
 */
export type InvitationOpenResult = ({ readonly outcome: 'opened' } & OpenedInvitation) | SignInRefusal;

/** What accepting an invitation answers: `accepted`, the roles it carried granted; or the refusal, a 404 when the browser carries no live invitation. */
export type InvitationAcceptResult = { readonly outcome: 'accepted' } | SignInRefusal;

/**
 * What asking for a mailed link answers: `sent`, whatever the address's state, so the
 * screen reveals nothing about it (an address an account already holds is told so by
 * mail); or the refusal: not an address (400), too many links to one mailbox (429 with
 * `too_many_attempts` and the wait), or the server does not offer the step (404).
 */
export type MailLinkResult = { readonly outcome: 'sent' } | SignInRefusal;

/**
 * What verifying an address answers: `verified` with the address now on the signed-in
 * user; or the refusal: a link that has expired, was already used or is not this
 * account's own (404), or an address another account has taken meanwhile (409).
 */
export type EmailVerifyResult = { readonly outcome: 'verified'; readonly email: string } | SignInRefusal;

/** A sign-up as opening its link describes it: the address, and whether the account's username is the address or the person chooses one. */
export interface OpenedSignUp {
  readonly email: string;
  readonly usernameIsEmail: boolean;
}

/** What opening a sign-up link answers: `opened` with the sign-up, or the refusal, a 404 for a link that has expired or was already used. */
export type SignUpOpenResult = ({ readonly outcome: 'opened' } & OpenedSignUp) | SignInRefusal;

/**
 * What an account created from a mailed link takes: the password the person chose, and
 * the username where the password method does not declare the username as the address
 * (`usernameIsEmail`); the server ignores one where it does.
 */
export interface NewAccount {
  readonly password: string;
  readonly username?: string;
}

/**
 * What a password change answers: `changed`, the password is the new one and the session
 * continues (regenerated, so the page checks it again); or the server's refusal, resolved
 * rather than thrown, with its words: the old password did not match, the new one is the
 * old one or fails the server's rule, or the session is an impersonation.
 */
export type PasswordChangeResult = { readonly outcome: 'changed' } | SignInRefusal;

/** What the password method's form posts. */
export interface PasswordCredentials {
  readonly username: string;
  readonly password: string;
}

/**
 * The server's refusal of a sign-in step, resolved rather than thrown so a page renders it
 * where it belongs. `code` is the server's login refusal code, for the sentence the page
 * holds for it (`user_disabled`, `pending_expired`, `too_many_attempts`, an application's
 * own); it is undefined when the answer carried none, such as a wrong password or a step
 * the pending sign-in does not wait for, and `message` is then the server's words.
 * `retryAfter` is the wait the server asks for before the next attempt, in seconds: the
 * attempt limiter answers a locked name with 429, `too_many_attempts`, `retryAfter` in the
 * body and the `Retry-After` header, and the client reads the body's, else the header's.
 * It is undefined when the answer named no wait.
 */
export interface SignInRefusal {
  readonly outcome: 'refused';
  readonly status: number;
  readonly code?: string;
  readonly message: string;
  readonly retryAfter?: number;
}

/**
 * What a sign-in step answers. `session`: the session is established, and the page's next
 * request carries it. `pending`: the sign-in waits on a step, a password confirmation or
 * the application's MFA step; `pending()` says which, unless the server named a
 * `redirectUrl` of its own for the step, which the page goes to. `refused`: see
 * SignInRefusal.
 */
export type SignInResult = { readonly outcome: 'session' } | PendingSignInResult | SignInRefusal;

export interface PendingSignInResult {
  readonly outcome: 'pending';
  /** Where the server sends the browser for the step, when its pending hook chose a page; otherwise the page reads `pending()`. */
  readonly redirectUrl?: string;
}

/**
 * Why a sign-in waits: `confirmation`, the identity matches a user that has a password,
 * which must be entered before the two are linked; `mfa`, the sign-in policy requires a
 * second factor: the sign-in code this client drives, when the step carries `emailCode`,
 * or the application's own. A later server may add a reason.
 */
export type PendingReason = 'confirmation' | 'mfa' | (string & {});

/** The sign-in that waits, as the pending status route reports it. */
export interface PendingSignIn {
  readonly reason: PendingReason;
  /** The email address the identity asserted, for the page's words; empty when it asserted none. */
  readonly email: string;
  /** When the pending sign-in expires, as the server wrote it. */
  readonly expiresAt: string;
  /** The path in the application the sign-in returns to once it completes; empty when it had none. */
  readonly returnUrl: string;
  /** The methods the sign-in policy accepts for the MFA step; absent or empty for any. */
  readonly factors?: readonly string[];
  /**
   * Where the sign-in code stands, present while the sign-in waits for the second factor
   * and the policy accepts the mailed code: the step this client drives. Absent on a
   * confirmation, and on a second-factor step the application's own factors alone satisfy.
   */
  readonly emailCode?: EmailCodeStep;
}

/**
 * Where the sign-in code stands. `codeEmail` is the address the code goes to, masked by the
 * server (`p•••@example.com`), empty while the account holds none; `enrollEmail` says the
 * account holds none, and the page asks for one and posts it to `enrollEmail()` before a
 * code can be sent; `codeSent` says a code stands, mailed and not yet expired, used or out
 * of attempts, and `codeExpiresAt` is when it stops being accepted, as the server wrote it.
 */
export interface EmailCodeStep {
  readonly codeEmail: string;
  readonly enrollEmail: boolean;
  readonly codeSent: boolean;
  readonly codeExpiresAt?: string;
}

/**
 * What having the code mailed answers: `sent`, with the masked address it went to and when
 * it stops being accepted, or the server's refusal: 409 when the pending sign-in waits for
 * no second factor, the policy does not accept the mailed code, or the account holds no
 * address (on an enrollment, already holds one); `too_many_attempts` with the wait when
 * the address was sent three codes within fifteen minutes; 400 on an enrollment of what
 * is not an address; `pending_expired` when the sign-in is gone.
 */
export type EmailCodeResult = EmailCodeSent | SignInRefusal;

export interface EmailCodeSent {
  readonly outcome: 'sent';
  readonly codeEmail: string;
  readonly codeExpiresAt: string;
}

/**
 * What the pending status route answers: a sign-in waiting (`waiting`, with the pending
 * sign-in's fields), none for this browser (`none`), or the server's refusal, which is
 * `pending_expired` when the browser's pending sign-in expired or was already used.
 */
export type PendingStatus =
  | ({ readonly outcome: 'waiting' } & PendingSignIn)
  | { readonly outcome: 'none' }
  | SignInRefusal;

/** What createClient hands the sign-in client. */
/** What a redirect method's URL asks of the provider beyond the page to return to. */
export interface SignInUrlOptions {
  /**
   * Ask the provider for its account chooser (`prompt=select_account` on the method's
   * path), so a provider holding one signed-in account does not sign that account in
   * again without asking: the login page asks after a refusal, and under "Use a different
   * account". Any other prompt is the server's to refuse.
   */
  readonly selectAccount?: boolean;
  /**
   * The account the provider should take (`login_hint` on the method's path): the
   * person's email address, so a provider holding that account signed in takes it and
   * skips its chooser, and one not holding it pre-fills its sign-in form. The login page
   * sends the invited address while an invitation stands. A hint is never read back by
   * the server; the identity comes from the provider.
   */
  readonly loginHint?: string;
}

export interface AuthClientOptions {
  /** Issues the requests, resolving with the status; the client's own requester, so every request carries what the client's do. */
  request: ResponseRequester;
  /** The methods route under the API prefix: `loginMethodsRoute`. */
  route: string;
  /** Where a method the client cannot drive is announced, once per message; the client's hook, the console by default. */
  warn?: Warn;
}

/** The statuses a sign-in step resolves as a refusal rather than throws; 429 is the attempt limiter's. */
const signInRefusals: readonly number[] = [401, 403, 429];
const confirmRefusals: readonly number[] = [401, 403, 404, 409, 429];
/** A code mailed, or an address given for it: 400 for what is not an address, 404 and 409 for a sign-in that does not wait for the code, 429 for an address sent too many. */
const codeRefusals: readonly number[] = [400, 401, 403, 404, 409, 429];
const statusRefusals: readonly number[] = [401, 403];
const changeRefusals: readonly number[] = [400, 401, 403, 409];
/** A mailed link's token posted back: 404 for a link that is dead, 400 for a body that carries none. */
const linkRefusals: readonly number[] = [400, 404];
/** An acceptance by a signed-in person: 404 when the browser carries no live invitation. */
const acceptRefusals: readonly number[] = [401, 403, 404, 409];
/** An account created from a link: the sign-in's refusals, 400 for a short password or no username, 404 for a dead link, 409 for a taken name or address. */
const accountRefusals: readonly number[] = [400, 401, 403, 404, 409, 429];
/** A request for a mailed link: 400 for what is not an address, 404 where the step is not offered, 429 for a mailbox sent too many. */
const mailRefusals: readonly number[] = [400, 404, 429];
const verifyRefusals: readonly number[] = [400, 404, 409];

/**
 * The refusal code the server answers, with a 403, on every request of a session whose
 * operator-set password must change: everything but the password change, the session
 * read and the logout. A framework over the client treats such a refusal as it treats a
 * 401, returning the browser to the login page, where the change form is served.
 */
export const passwordChangeRequiredCode = 'password_change_required';
/**
 * The refusal code of a directory sign-in that carried an invitation mailed to another
 * address: the invitation is accepted only as the invited address, and still stands. A login
 * page names the invited address with it, which it knows from the invitation it carries.
 */
export const invitationAddressCode = 'invitation_address';

/** Whether a refusal is the server's answer to a session whose password must change: a 403 carrying the code. */
export function refusedForPasswordChange(refusal: { status: number; body: unknown }): boolean {
  if (refusal.status !== 403 || !refusal.body || typeof refusal.body !== 'object') {
    return false;
  }
  return (refusal.body as RefusalAnswer).code === passwordChangeRequiredCode;
}

/** The shape the methods route answers, before it is checked. */
interface MethodsAnswer {
  methods?: Partial<LoginMethod>[] | null;
  pending?: Partial<PendingRoutes> | null;
  changePassword?: string | null;
  usernameIsEmail?: boolean | null;
  invitation?: InvitationAnswer | null;
  email?: Partial<EmailRoutes> | null;
  signUp?: Partial<SignUpRoutes> | null;
}

/** The shape the invitation block answers, before it is checked. */
interface InvitationAnswer {
  open?: string | null;
  accept?: string | null;
  signUp?: string | null;
  standing?: Partial<StandingInvitation> | null;
}

/** The list as it is assembled from the answer, before it is handed out read-only. */
type ListDraft = { -readonly [K in keyof LoginMethods]: LoginMethods[K] };

/** The shape a sign-in step answers on 200. */
interface SignInAnswer {
  mfaIsRequired?: boolean;
  redirectUrl?: string;
}

/** The shape the send and enrollment routes answer on 200. */
interface EmailCodeAnswer {
  codeEmail?: unknown;
  codeExpiresAt?: unknown;
}

/** The shape a refusal answers: the server's words, its code when it has one, and the wait in seconds when it names one. */
interface RefusalAnswer {
  message?: unknown;
  code?: unknown;
  retryAfter?: unknown;
}

/**
 * The return address the server keeps, as a login page may follow it: `value` when it is a
 * path in this application in canonical form, undefined otherwise. The rule is the
 * server's own (the session library's `SanitizeReturnURL`, which a redirect method's
 * `returnUrl` must pass and the callback redirects to): one leading `/` (not `//` or
 * `/\`), printable ASCII with no backslash raw or as `%5C`, every `%` a valid escape, a
 * path of RFC 3986 path characters with no empty, `.` or `..` segment as written or
 * decoded, and a query and fragment that pass through. A value that is not such a path is
 * never a page in this application, so a login page drops it instead of sending it on and
 * being refused with 400, and an absolute URL never becomes a redirect to another origin.
 */
export function returnPath(value: string | null | undefined): string | undefined {
  if (!value || value[0] !== '/' || value[1] === '/' || value[1] === '\\' || !hasSafeBytes(value)) {
    return undefined;
  }
  const end = value.search(/[?#]/);
  const path = end < 0 ? value : value.slice(0, end);
  return isSafePath(path) ? value : undefined;
}

/** Whether `s` is printable ASCII without a backslash, and every `%` starts a valid escape that is not an encoded backslash. */
function hasSafeBytes(s: string): boolean {
  if (!/^[\x21-\x7e]*$/.test(s) || s.includes('\\')) {
    return false;
  }
  for (let i = s.indexOf('%'); i >= 0; i = s.indexOf('%', i + 1)) {
    const escape = s.slice(i + 1, i + 3);
    if (!/^[0-9a-fA-F]{2}$/.test(escape) || escape.toLowerCase() === '5c') {
      return false;
    }
  }
  return true;
}

/** Whether the path `p` is made of path characters and is canonical as written and decoded, with no control character once decoded. */
function isSafePath(p: string): boolean {
  if (!/^[A-Za-z0-9\-._~!$&'()*+,;=:@%/]*$/.test(p) || !isCanonicalPath(p)) {
    return false;
  }
  let decoded: string;
  try {
    decoded = decodeURIComponent(p);
  } catch {
    return false;
  }
  if (!isCanonicalPath(decoded)) {
    return false;
  }
  for (const character of decoded) {
    const code = character.charCodeAt(0);
    if (code < 0x20 || code === 0x7f || character === '\\') {
      return false;
    }
  }
  return true;
}

/** Whether `p`, which starts with `/`, has no empty, `.` or `..` segment (a trailing `/` aside) and is not scheme-relative. */
function isCanonicalPath(p: string): boolean {
  if (p === '/') {
    return true;
  }
  if (p.startsWith('//')) {
    return false;
  }
  const segments = p.slice(1).split('/');
  const last = segments.pop();
  return (
    last !== '.' && last !== '..' && segments.every((segment) => segment !== '' && segment !== '.' && segment !== '..')
  );
}

/**
 * AuthClient drives a sign-in from the browser against what the server offers. It is the
 * one place that knows the wire shape: the methods list and its route, the password
 * method's form, the redirect method's return parameter, the pending sign-in's status,
 * confirmation and cancel, the mailed links (an invitation, an address to confirm, a
 * sign-up) and the answers of each. A page over it renders the list, posts the form,
 * performs the redirect, renders the pending step and the opened link, naming no path of
 * its own.
 *
 * Every step that posts reads the methods list again first. The list's answer refreshes
 * the XSRF cookie the server's guard checks on a POST, so a login page left open past the
 * cookie's life signs in on the first click rather than being refused for a stale token
 * and succeeding on the second; the re-read also re-syncs the list, so a method the
 * server no longer offers is not posted to. The methods request is a page's first request
 * for the same reason: it primes the cookie.
 *
 * A refusal the server answers on a sign-in step (a wrong password, a disabled user, an
 * expired pending sign-in) resolves as `refused` with the server's code and words, so the
 * client's error hook never sees it: nothing sends the browser to the login page it is on,
 * and nothing raises a notice. Every other failure, a malformed request, a server fault,
 * no response, throws as the client's requests do.
 */
export class AuthClient {
  /** The last methods list read, for a page to render from; undefined until the first read answers. */
  readonly snapshot = new Store<LoginMethods | undefined>(undefined);
  private readonly warn: Warn;

  constructor(private readonly options: AuthClientOptions) {
    this.warn = options.warn ?? warnOnce();
  }

  /** The last methods list read, or undefined before the first read. */
  get(): LoginMethods | undefined {
    return this.snapshot.get();
  }

  subscribe(listener: (list: LoginMethods | undefined) => void): () => void {
    return this.snapshot.subscribe(listener);
  }

  /**
   * Reads the methods list: the sign-in methods the server offers, in its order, and the
   * pending sign-in's routes. The answer is kept in `snapshot`. A listed method of a kind
   * this client cannot drive is left out and announced once through the warn hook.
   */
  async methods(): Promise<LoginMethods> {
    const answer = await this.options.request<MethodsAnswer | null>('GET', this.options.route);
    const list = this.listOf(answer.body);
    this.snapshot.set(list);
    return list;
  }

  /**
   * The URL a redirect method's button sends the browser to: the method's path, naming
   * the page in the application the sign-in returns to as `returnUrl` when one is given,
   * asking the provider for its account chooser (`prompt=select_account`) when
   * `options.selectAccount` is set, and naming the account the provider should take
   * (`login_hint`) when `options.loginHint` is set. The server sanitizes the page to a
   * path in the application. Nothing is requested here: the page assigns the URL to the
   * browser's location.
   */
  signInUrl(method: LoginMethod, returnUrl?: string, options: SignInUrlOptions = {}): string {
    if (method.kind !== 'redirect') {
      throw new Error(
        `the ${method.name} sign-in method is a ${method.kind} method, not a redirect: post its form with signInWithPassword`,
      );
    }
    const query = [
      returnUrl ? `returnUrl=${encodeURIComponent(returnUrl)}` : '',
      options.selectAccount ? 'prompt=select_account' : '',
      options.loginHint ? `login_hint=${encodeURIComponent(options.loginHint)}` : '',
    ]
      .filter((part) => part !== '')
      .join('&');
    return query === '' ? method.path : `${method.path}?${query}`;
  }

  /**
   * Signs in through a password method: reads the methods list again, then posts the
   * credentials to the path the refreshed list names for the method. The result is the
   * session, a pending step, or the server's refusal: a wrong password answers 401 with
   * the server's words, a cause the policy forbids 403 with its code, and a name whose
   * failed attempts reached the server's limit 429 with `too_many_attempts` and the wait
   * (`retryAfter`). A method that is not a password method is refused before any request,
   * and one the refreshed list no longer offers throws, the list in `snapshot` being the
   * one to render again.
   */
  async signInWithPassword(method: LoginMethod, credentials: PasswordCredentials): Promise<SignInResult> {
    if (method.kind !== 'password') {
      throw new Error(
        `the ${method.name} sign-in method is a ${method.kind} method, not a password form: send the browser to signInUrl`,
      );
    }
    const list = await this.methods();
    const offered = list.methods.find((candidate) => candidate.name === method.name && candidate.kind === 'password');
    if (!offered) {
      throw new Error(`the server no longer offers the ${method.name} password sign-in: render the methods list again`);
    }
    const response = await this.options.request<SignInAnswer | RefusalAnswer | null>('POST', offered.path, {
      body: credentials,
      absolute: true,
      accept: signInRefusals,
    });
    return signInResultOf(response);
  }

  /**
   * Reads the pending sign-in's status: the step the browser's sign-in waits on, none, or
   * the server's refusal (`pending_expired`). It reads the methods list first when none
   * has been read, for the status route. A server whose list names no pending routes
   * never holds a sign-in, and the read throws saying so.
   */
  async pending(): Promise<PendingStatus> {
    const routes = await this.pendingRoutes(false);
    const response = await this.options.request<(Partial<PendingSignIn> & RefusalAnswer) | null>('GET', routes.status, {
      absolute: true,
      accept: [...statusRefusals, 404],
    });
    if (response.status === 404) {
      return { outcome: 'none' };
    }
    if (response.status >= 400) {
      return refusalOf(response);
    }
    const body = response.body ?? {};
    const emailCode = emailCodeOf(body.emailCode);
    return {
      outcome: 'waiting',
      reason: stringOf(body.reason),
      email: stringOf(body.email),
      expiresAt: stringOf(body.expiresAt),
      returnUrl: stringOf(body.returnUrl),
      ...(Array.isArray(body.factors) ? { factors: body.factors.map(String) } : {}),
      ...(emailCode ? { emailCode } : {}),
    };
  }

  /**
   * Confirms the pending sign-in with the password of the user it matched: reads the
   * methods list again, then posts the password to the confirmation route. The result is
   * the session, a further pending step (the policy's MFA after the link), or the
   * server's refusal: a wrong password answers 401 with the server's words and leaves the
   * pending sign-in in place, an expired one answers `pending_expired`, and a name whose
   * failed attempts reached the server's limit answers 429 with `too_many_attempts` and
   * the wait (`retryAfter`), the pending sign-in still in place.
   */
  async confirmPending(password: string): Promise<SignInResult> {
    const routes = await this.pendingRoutes(true);
    const response = await this.options.request<SignInAnswer | RefusalAnswer | null>('POST', routes.confirm, {
      body: { password },
      absolute: true,
      accept: confirmRefusals,
    });
    return signInResultOf(response);
  }

  /** Discards the browser's pending sign-in, if it has one: reads the methods list again, then posts to the cancel route. */
  async cancelPending(): Promise<void> {
    const routes = await this.pendingRoutes(true);
    await this.options.request('POST', routes.cancel, { absolute: true });
  }

  /**
   * Has the sign-in code mailed to the address the account holds, replacing a code
   * standing: reads the methods list again, then posts to the send route. The result is
   * `sent`, with the masked address and when the code stops being accepted, or the
   * server's refusal (see EmailCodeResult). A server whose pending routes name no send
   * route sends no mail, and the call throws saying so.
   */
  async sendCode(): Promise<EmailCodeResult> {
    const routes = await this.codeRoutes();
    const response = await this.options.request<EmailCodeAnswer | RefusalAnswer | null>('POST', routes.sendCode, {
      absolute: true,
      accept: codeRefusals,
    });
    return emailCodeResultOf(response);
  }

  /**
   * Gives the address of an account that holds none, and has the sign-in code mailed to
   * it: reads the methods list again, then posts the address to the enrollment route. The
   * address is written on the account, verified, once its code is entered; an address
   * another account holds is answered as sent, and that account is told by mail, so the
   * screen reveals nothing. The result is as sendCode's.
   */
  async enrollEmail(email: string): Promise<EmailCodeResult> {
    const routes = await this.codeRoutes();
    const response = await this.options.request<EmailCodeAnswer | RefusalAnswer | null>('POST', routes.enrollEmail, {
      body: { email },
      absolute: true,
      accept: codeRefusals,
    });
    return emailCodeResultOf(response);
  }

  /**
   * Enters the sign-in code: reads the methods list again, then posts the code to the
   * verification route, asking for the browser to be remembered when `rememberDevice` is
   * set, so this person's next sign-ins from it skip the code for the period the server
   * keeps a remembered browser. The result is the session, a further step, or the server's
   * refusal: a wrong code answers 401 with `invalid_code`, and the code stands with one
   * fewer attempt; a code entered while none stands (none sent, or the one sent expired,
   * was used or took its last attempt) answers 401 with `code_expired`, and the page has a
   * new one sent; an expired sign-in answers `pending_expired`.
   */
  async verifyCode(code: string, rememberDevice: boolean): Promise<SignInResult> {
    const routes = await this.codeRoutes();
    const response = await this.options.request<SignInAnswer | RefusalAnswer | null>('POST', routes.verifyCode, {
      body: { code, rememberDevice },
      absolute: true,
      accept: confirmRefusals,
    });
    return signInResultOf(response);
  }

  /**
   * Changes the signed-in person's password: reads the methods list again, then posts the
   * old and the new password to the change path the refreshed list names. The route is
   * the one a person whose operator-set password must change reaches before anything else
   * is served, so a login page that finds `passwordChangeRequired` on the session read
   * posts here, then checks the session again. The result is `changed`, or the server's
   * refusal with its words (the old password did not match, the new one is the old one).
   * A server whose list names no change path offers no password method, and the call
   * throws saying so.
   */
  async changePassword(oldPassword: string, newPassword: string): Promise<PasswordChangeResult> {
    const list = await this.methods();
    if (!list.changePassword) {
      throw new Error(
        'this server names no password change path in its methods list: it offers no password method to change',
      );
    }
    const response = await this.options.request<RefusalAnswer | null>('POST', list.changePassword, {
      body: { oldPassword, newPassword },
      absolute: true,
      accept: changeRefusals,
    });
    return response.status >= 400 ? refusalOf(response) : { outcome: 'changed' };
  }

  /**
   * Opens the invitation a mailed link carried: reads the methods list again, then posts
   * the token to the invitation's open route. The answer describes the invitation (the
   * address, the tenant, when it expires, whether an account with a password may be
   * created here and whether its username is the address), and the browser now carries
   * the invitation, so the sign-in that follows, by any method, or the sign-up, accepts
   * it. A link that has expired or was already used resolves `refused` with 404 and the
   * server's words. A server whose list names no invitation routes serves none, and the
   * call throws saying so.
   */
  async openInvitation(token: string): Promise<InvitationOpenResult> {
    const routes = await this.invitationRoutes();
    const response = await this.options.request<(Partial<OpenedInvitation> & RefusalAnswer) | null>('POST', routes.open, {
      body: { token },
      absolute: true,
      accept: linkRefusals,
    });
    if (response.status >= 400) {
      return refusalOf(response);
    }
    const body = response.body ?? {};
    const methods = methodNamesOf(body.methods);
    return {
      outcome: 'opened',
      email: stringOf(body.email),
      tenant: stringOf(body.tenant),
      expiresAt: stringOf(body.expiresAt),
      signUp: body.signUp === true,
      usernameIsEmail: body.usernameIsEmail === true,
      ...(methods === undefined ? {} : { methods }),
      ...accountFactsOf(body),
    };
  }

  /**
   * Accepts the invitation the browser carries for the signed-in person: reads the methods
   * list again, then posts to the accept route. The roles the invitation carried are
   * granted, and the address it was sent to is the person's when they hold no verified one.
   * A browser that carries no live invitation resolves `refused` with 404; an account that
   * holds another address than the invited one resolves `refused` with 403 and the server's
   * sentence, the invitation still standing.
   */
  async acceptInvitation(): Promise<InvitationAcceptResult> {
    const routes = await this.invitationRoutes();
    const response = await this.options.request<RefusalAnswer | null>('POST', routes.accept, {
      absolute: true,
      accept: acceptRefusals,
    });
    return response.status >= 400 ? refusalOf(response) : { outcome: 'accepted' };
  }

  /**
   * Creates the invited person's account with a password and signs them in, accepting the
   * invitation the browser carries: reads the methods list again, then posts the account to
   * the invitation's sign-up route. The account's address is the one the invitation was
   * sent to, verified; its username is that address when the list says `usernameIsEmail`
   * (none is posted), and the one given otherwise. The result is the sign-in's: the session, a pending step,
   * or the refusal: a taken username or address (409), a password under eight characters
   * or no username (400), no live invitation (404). A server whose list names no sign-up
   * route for invitations offers no password method, and the call throws saying so.
   */
  async signUpFromInvitation(account: NewAccount): Promise<SignInResult> {
    const routes = await this.invitationRoutes();
    if (!routes.signUp) {
      throw new Error(
        'this server names no sign-up route for invitations in its methods list: it offers no password method to create an account with',
      );
    }
    return this.createAccount(routes.signUp, accountBody(account));
  }

  /**
   * Asks for a confirmation link to be mailed to an address the signed-in person wants on
   * their account: reads the methods list again, then posts the address to the email add
   * route. Nothing is written until the link is followed (`verifyEmail`), and the answer is
   * `sent` whatever the address's state, so the screen reveals nothing about it: an address
   * another account holds is told so by mail, and one the person already holds verified gets
   * none. The refusal is not an address (400) or too many links to one mailbox (429 with
   * `too_many_attempts` and the wait). A server whose list names no email routes sends no
   * mail, and the call throws saying so.
   */
  async addEmail(email: string): Promise<MailLinkResult> {
    const routes = await this.emailRoutes();
    return this.requestLink(routes.add, { email });
  }

  /**
   * Verifies the address a mailed link confirms, for the signed-in person who asked for it:
   * reads the methods list again, then posts the token to the verify route. The address is
   * written on the user as verified, replacing whatever they held; where the username is the
   * address, the user is renamed to it. The result is `verified` with the address, or the
   * refusal: a link that has expired, was already used or is another account's (404), or an
   * address another account took meanwhile (409).
   */
  async verifyEmail(token: string): Promise<EmailVerifyResult> {
    const routes = await this.emailRoutes();
    const response = await this.options.request<(Partial<{ email: string }> & RefusalAnswer) | null>('POST', routes.verify, {
      body: { token },
      absolute: true,
      accept: verifyRefusals,
    });
    if (response.status >= 400) {
      return refusalOf(response);
    }
    return { outcome: 'verified', email: stringOf(response.body?.email) };
  }

  /**
   * Starts a sign-up: reads the methods list again, then posts the address to the sign-up
   * start route, which mails it a link to finish. The answer is `sent` whatever the
   * address's state, so the screen reveals nothing: an address an account already holds is
   * pointed at that account by mail. The refusal is not an address (400), too many links to
   * one mailbox (429 with `too_many_attempts` and the wait), or sign-up not offered (404). A
   * server whose list names no sign-up routes offers none, and the call throws saying so.
   */
  async startSignUp(email: string): Promise<MailLinkResult> {
    const routes = await this.signUpRoutes();
    return this.requestLink(routes.start, { email });
  }

  /**
   * Opens the sign-up a mailed link carried: reads the methods list again, then posts the
   * token to the sign-up open route. The answer is the address and whether the account's
   * username is the address, for the page to render the form; a link that has expired or was
   * already used resolves `refused` with 404.
   */
  async openSignUp(token: string): Promise<SignUpOpenResult> {
    const routes = await this.signUpRoutes();
    const response = await this.options.request<(Partial<OpenedSignUp> & RefusalAnswer) | null>('POST', routes.open, {
      body: { token },
      absolute: true,
      accept: linkRefusals,
    });
    if (response.status >= 400) {
      return refusalOf(response);
    }
    const body = response.body ?? {};
    return { outcome: 'opened', email: stringOf(body.email), usernameIsEmail: body.usernameIsEmail === true };
  }

  /**
   * Completes a sign-up: reads the methods list again, then posts the token and the account
   * to the sign-up complete route, which creates the account with the address verified and
   * signs the person in. The username is the address when the list says `usernameIsEmail`,
   * and the one given otherwise. The result is the sign-in's: the session, a pending step,
   * or the refusal: a taken username or address (409), a password under eight characters or
   * no username (400), a dead link (404).
   */
  async completeSignUp(token: string, account: NewAccount): Promise<SignInResult> {
    const routes = await this.signUpRoutes();
    return this.createAccount(routes.complete, { token, ...accountBody(account) });
  }

  /** Posts an account to the route that creates it and signs it in, resolving as a sign-in step does. */
  private async createAccount(route: string, body: object): Promise<SignInResult> {
    const response = await this.options.request<SignInAnswer | RefusalAnswer | null>('POST', route, {
      body,
      absolute: true,
      accept: accountRefusals,
    });
    return signInResultOf(response);
  }

  /** Posts a request for a mailed link, resolving `sent` or the refusal. */
  private async requestLink(route: string, body: object): Promise<MailLinkResult> {
    const response = await this.options.request<RefusalAnswer | null>('POST', route, {
      body,
      absolute: true,
      accept: mailRefusals,
    });
    return response.status >= 400 ? refusalOf(response) : { outcome: 'sent' };
  }

  /** The invitation routes of the list, read again; a list naming none throws. */
  private async invitationRoutes(): Promise<InvitationRoutes> {
    const list = await this.methods();
    if (!list.invitation) {
      throw new Error('this server names no invitation routes in its methods list: it serves no invitations');
    }
    return list.invitation;
  }

  /** The email routes of the list, read again; a list naming none throws. */
  private async emailRoutes(): Promise<EmailRoutes> {
    const list = await this.methods();
    if (!list.email) {
      throw new Error('this server names no email address routes in its methods list: it sends no mail');
    }
    return list.email;
  }

  /** The sign-up routes of the list, read again; a list naming none throws. */
  private async signUpRoutes(): Promise<SignUpRoutes> {
    const list = await this.methods();
    if (!list.signUp) {
      throw new Error('this server names no sign-up routes in its methods list: its password method does not offer sign-up');
    }
    return list.signUp;
  }

  /** The pending routes of the kept list, read again when asked or when none is kept. */
  private async pendingRoutes(refresh: boolean): Promise<PendingRoutes> {
    const kept = this.snapshot.get();
    const list = refresh || !kept ? await this.methods() : kept;
    if (!list.pending) {
      throw new Error(
        'this server names no pending sign-in routes in its methods list: no sign-in of its waits on a step',
      );
    }
    return list.pending;
  }

  /** The list as the page may render it: each method with its fields as strings, the kinds this client drives only. */
  /** The pending routes with the sign-in code's three, the list read again first; a server that names none sends no code. */
  private async codeRoutes(): Promise<Required<PendingRoutes>> {
    const routes = await this.pendingRoutes(true);
    if (!routes.sendCode || !routes.verifyCode || !routes.enrollEmail) {
      throw new Error(
        'this server names no sign-in code routes in its methods list: it sends no mail, and no sign-in of its waits on the code',
      );
    }
    return { ...routes, sendCode: routes.sendCode, verifyCode: routes.verifyCode, enrollEmail: routes.enrollEmail };
  }

  private listOf(answer: MethodsAnswer | null): LoginMethods {
    const methods: LoginMethod[] = [];
    for (const entry of answer?.methods ?? []) {
      const kind = entry.kind;
      if (kind !== 'password' && kind !== 'redirect') {
        this.warn(unknownKindWarning(stringOf(entry.name), stringOf(kind)));
        continue;
      }
      methods.push({
        name: stringOf(entry.name),
        method: stringOf(entry.method),
        kind,
        label: stringOf(entry.label),
        path: stringOf(entry.path),
      });
    }
    const list: ListDraft = { methods };
    const pending = answer?.pending;
    if (pending?.status && pending.confirm && pending.cancel) {
      list.pending = {
        status: pending.status,
        confirm: pending.confirm,
        cancel: pending.cancel,
        ...(pending.sendCode && pending.verifyCode && pending.enrollEmail
          ? { sendCode: pending.sendCode, verifyCode: pending.verifyCode, enrollEmail: pending.enrollEmail }
          : {}),
      };
    }
    const changePassword = answer?.changePassword;
    if (typeof changePassword === 'string' && changePassword !== '') {
      list.changePassword = changePassword;
    }
    if (answer?.usernameIsEmail === true) {
      list.usernameIsEmail = true;
    }
    const invitation = answer?.invitation;
    if (invitation?.open && invitation.accept) {
      const standing = standingOf(invitation.standing);
      list.invitation = {
        open: invitation.open,
        accept: invitation.accept,
        ...(invitation.signUp ? { signUp: invitation.signUp } : {}),
        ...(standing ? { standing } : {}),
      };
    }
    const email = answer?.email;
    if (email?.add && email.verify) {
      list.email = { add: email.add, verify: email.verify };
    }
    const signUp = answer?.signUp;
    if (signUp?.start && signUp.open && signUp.complete) {
      list.signUp = { start: signUp.start, open: signUp.open, complete: signUp.complete };
    }
    return list;
  }
}

/**
 * What the client announces, once, for a listed method whose kind it cannot drive: the
 * method is left out of the list, and a newer client is what would offer it.
 */
export function unknownKindWarning(name: string, kind: string): string {
  return (
    `the server lists the sign-in method ${name} as a ${kind} method, a kind this client does not drive: ` +
    'it is left out of the list; update @impulseframework/resource to a release that drives it'
  );
}

/** The body an account is posted as: the password, and the username only where one was given. */
function accountBody(account: NewAccount): { password: string; username?: string } {
  return account.username === undefined ? { password: account.password } : { password: account.password, username: account.username };
}

/** The result of a sign-in step's answer: a refusal for a status the step accepts, else the session or a pending step. */
function signInResultOf(response: ClientResponse<SignInAnswer | RefusalAnswer | null>): SignInResult {
  if (response.status >= 400) {
    return refusalOf(response);
  }
  const answer = (response.body ?? {}) as SignInAnswer;
  if (answer.mfaIsRequired) {
    return answer.redirectUrl ? { outcome: 'pending', redirectUrl: answer.redirectUrl } : { outcome: 'pending' };
  }
  return { outcome: 'session' };
}

/**
 * The server's refusal as the page renders it: its code when the answer carries one, its
 * words, and the wait it names, from the body's `retryAfter` when that is a number of
 * seconds, else from the `Retry-After` header.
 */
function refusalOf(response: ClientResponse<unknown>): SignInRefusal {
  const body: RefusalAnswer =
    response.body && typeof response.body === 'object' ? (response.body as RefusalAnswer) : {};
  const code = typeof body.code === 'string' && body.code !== '' ? body.code : undefined;
  const message = typeof body.message === 'string' && body.message !== '' ? body.message : `HTTP ${response.status}`;
  const retryAfter =
    typeof body.retryAfter === 'number' && Number.isFinite(body.retryAfter) && body.retryAfter >= 0
      ? body.retryAfter
      : retryAfterSeconds(response.headers[retryAfterHeader.toLowerCase()]);
  return {
    outcome: 'refused',
    status: response.status,
    ...(code === undefined ? {} : { code }),
    message,
    ...(retryAfter === undefined ? {} : { retryAfter }),
  };
}

/** The invitation the list says the browser carries, its fields as strings; undefined where the block names none. */
function standingOf(value: Partial<StandingInvitation> | null | undefined): StandingInvitation | undefined {
  if (!value || typeof value !== 'object') {
    return undefined;
  }
  const methods = methodNamesOf(value.methods);
  return {
    email: stringOf(value.email),
    tenant: stringOf(value.tenant),
    expiresAt: stringOf(value.expiresAt),
    usernameIsEmail: value.usernameIsEmail === true,
    ...(methods === undefined ? {} : { methods }),
    ...accountFactsOf(value),
  };
}

/**
 * The account facts an invitation answer carries, when it carries them: whether an account
 * holds the invited address and whether it signs in with a password. Both are read only
 * when the server said whether an account exists; an older server says nothing, and the
 * facts stay absent.
 */
function accountFactsOf(value: { accountExists?: unknown; hasPassword?: unknown }): {
  accountExists?: boolean;
  hasPassword?: boolean;
} {
  if (typeof value.accountExists !== 'boolean') {
    return {};
  }
  return { accountExists: value.accountExists, hasPassword: value.hasPassword === true };
}

/** The registration names an invitation answer lists, strings only; undefined where it lists none, or what it lists is not a list. */
function methodNamesOf(value: unknown): readonly string[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  return value.filter((name): name is string => typeof name === 'string');
}

function stringOf(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/** The result of having the code mailed: a refusal for a status the step accepts, else sent, with the address and the expiry as the server wrote them. */
function emailCodeResultOf(response: ClientResponse<EmailCodeAnswer | RefusalAnswer | null>): EmailCodeResult {
  if (response.status >= 400) {
    return refusalOf(response);
  }
  const answer = (response.body ?? {}) as EmailCodeAnswer;
  return { outcome: 'sent', codeEmail: stringOf(answer.codeEmail), codeExpiresAt: stringOf(answer.codeExpiresAt) };
}

/** The sign-in code's state as the status answers it, when it answers one; a field left out reads as empty or false. */
function emailCodeOf(value: unknown): EmailCodeStep | undefined {
  if (!value || typeof value !== 'object') {
    return undefined;
  }
  const state = value as Partial<Record<keyof EmailCodeStep, unknown>>;
  const codeExpiresAt = stringOf(state.codeExpiresAt);
  return {
    codeEmail: stringOf(state.codeEmail),
    enrollEmail: state.enrollEmail === true,
    codeSent: state.codeSent === true,
    ...(codeExpiresAt === '' ? {} : { codeExpiresAt }),
  };
}
