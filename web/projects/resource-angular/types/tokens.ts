import { InjectionToken, Provider, signal, Signal, WritableSignal } from '@angular/core';
import { ApiVersionError, ChangeFeed, MaintenanceError } from '@impulseframework/resource';
import { Domain, Permission, Resource } from './permissions';
import { MethodMeta, ResourceMeta } from './resource-meta';

/**
 * The base URL for API requests (e.g., 'https://api.example.com/').
 * @defaultValue '/'
 */
export const BASE_URL = new InjectionToken<string>('BASE_URL', { factory: () => '/' });

/**
 * The path to the frontend login page (e.g., '/login').
 * @defaultValue '/login'
 */
export const FRONTEND_LOGIN_PATH = new InjectionToken<string>('FRONTEND_LOGIN_PATH', { factory: () => '/login' });

/**
 * The path of the application's sign-out route (e.g., '/logout'): the page that ends the
 * session and forwards the browser to FRONTEND_LOGIN_PATH, composed from `<impulse-sign-out>`.
 * A sign-out control is a link to it, and the idle watch sends the browser there when the
 * session times out; the login page itself ends nothing on arrival.
 * @defaultValue '/logout'
 */
export const FRONTEND_SIGN_OUT_PATH = new InjectionToken<string>('FRONTEND_SIGN_OUT_PATH', {
  factory: () => '/logout',
});

/**
 * The path to the session endpoint (e.g., 'user/session').
 * @defaultValue 'user/session'
 */
export const SESSION_PATH = new InjectionToken<string>('SESSION_PATH', { factory: () => 'user/session' });

/**
 * The base URL for API requests (e.g., '/api').
 * @defaultValue '/api'
 */
export const API_URL = new InjectionToken<string>('API_URL', { factory: () => '/api' });

/**
 * The release this build of the application was made from (e.g., '1.5.0'), as the build
 * stamps it. `provideResourceClient` hands it to the client as `ClientOptions.apiVersion`,
 * and `apiVersionInterceptor` adds it to the application's own same-origin HttpClient
 * requests, so every request carries it in the version header and the server can refuse a
 * build older than the oldest release it answers. An empty value, or `dev`, the local
 * build's name, sends nothing and is never refused.
 * @defaultValue '' (no release: nothing is sent)
 */
export const API_VERSION = new InjectionToken<string>('API_VERSION', { factory: () => '' });

/**
 * What the client's error hook hands the server's refusal of this build's release to.
 * `provideAppUpdate()` registers `AppUpdateService` as it, which picks up the server's
 * build: by itself while the application is still starting, through a persistent notice
 * with Reload once a page is on screen, and by unregistering the service worker first when
 * the server runs an older release than this build.
 */
export interface VersionRefusalHandler {
  versionRefused(refusal: ApiVersionError): void;
}

/**
 * The handler of the server's refusal of this build's release; `provideAppUpdate()`
 * provides it. A build that names a release (API_VERSION) must have one: `provideResourceClient`
 * fails at startup otherwise, naming `provideAppUpdate()`.
 */
export const VERSION_REFUSAL_HANDLER = new InjectionToken<VersionRefusalHandler>('VERSION_REFUSAL_HANDLER');

/**
 * What the client's error hook hands the server's maintenance answer to: a 503 carrying
 * the maintenance marker, `MaintenanceError`. `provideAppUpdate()` registers
 * `MaintenanceService` as it, which raises the maintenance notice once per outage and
 * checks back until the server answers again.
 */
export interface MaintenanceHandler {
  maintenanceAnswered(answer: MaintenanceError): void;
}

/**
 * The handler of the server's maintenance answer; `provideAppUpdate()` provides it. Without
 * one the hook hands the answer to nobody, and `ResourceErrorHandler` raises no notice for
 * it either, so an application that wants the notice adds `provideAppUpdate()`.
 */
export const MAINTENANCE_HANDLER = new InjectionToken<MaintenanceHandler>('MAINTENANCE_HANDLER');

/**
 * The URL the browser returns to after the next login, and `AuthService.redirectUrl`.
 * Runtime state, not configuration: the login guard writes the route it turned away
 * from, the client's error hook writes BASE_URL plus the current route when a 401 sends
 * the browser to FRONTEND_LOGIN_PATH, and the login page reads it once and clears it.
 * It lives below AuthService so the client adapter can write it without depending on
 * the service that depends on the client.
 * @defaultValue a signal holding the empty string
 */
export const LOGIN_REDIRECT_URL = new InjectionToken<WritableSignal<string>>('LOGIN_REDIRECT_URL', {
  factory: () => signal(''),
});

/**
 * The available permissions in the system.
 * @defaultValue an empty array
 */
export const AVAILABLE_PERMISSIONS = new InjectionToken<{
  Create: Permission;
  Delete: Permission;
  List: Permission;
  Read: Permission;
  Update: Permission;
}>('AVAILABLE_PERMISSIONS');
export const AVAILABLE_DOMAINS = new InjectionToken<Record<string, Domain>[]>('AVAILABLE_DOMAINS', {
  factory: () => [],
});

/**
 * The selected tenant for the library's pages: a signal the application provides from
 * its tenant picker. Every request the store makes for a domain-scoped resource is
 * bound to it, and a permission question about a domain-scoped target whose scope
 * names no domain is asked in its digest. Undefined means no tenant is selected: a
 * domain-scoped request is refused by the client before it is sent, and a domain-scoped
 * permission question answers false.
 * @defaultValue a signal holding undefined
 */
export const RESOURCE_DOMAIN = new InjectionToken<Signal<Domain | undefined>>('RESOURCE_DOMAIN', {
  factory: () => signal<Domain | undefined>(undefined),
});

/**
 * The generated resourceMeta from the output of the [ccc package](https://github.com/impulseframework/impulse) typescript generator
 * Required to work with generated routes and resource metadata
 * @defaultValue a no-op function that returns an empty object
 */
export const RESOURCE_META = new InjectionToken<(resource: Resource) => ResourceMeta>('RESOURCE_META', {
  factory: () => () => ({}) as ResourceMeta,
});

/**
 * The generated methodMeta from the output of the [ccc package](https://github.com/impulseframework/impulse) typescript generator
 * Required to work with generated method data
 * @defaultValue a no-op function that returns an empty object
 */
export const METHOD_META = new InjectionToken<(method: string) => MethodMeta>('METHOD_META', {
  factory: () => () => ({}) as MethodMeta,
});

/**
 * The duration in seconds for the session to be considered idle.
 * @defaultValue 300 seconds
 */
export const IDLE_SESSION_DURATION = new InjectionToken<number>('IDLE_SESSION_DURATION', { factory: () => 300 });

/**
 * The duration in seconds for the warning to be shown to the user before the session times out due to inactivity.
 * @defaultValue 60 seconds
 */
export const IDLE_WARNING_DURATION = new InjectionToken<number>('IDLE_WARNING_DURATION', { factory: () => 60 });

/**
 * The duration in seconds for the keepalive ping to be sent to the server to keep the session alive.
 * @defaultValue 30 seconds
 */
export const IDLE_KEEPALIVE_DURATION = new InjectionToken<number>('IDLE_KEEPALIVE_DURATION', { factory: () => 30 });

/**
 * The change feed live pages listen to: `firestoreChangeFeed()` from
 * @impulseframework/resource-firestore, provided once in the application's providers. With it,
 * AuthService starts the client's live session when the session first authenticates
 * (fetching the feed's identity from the API) and stops it at logout, before the session
 * is logged out, and every live page stays current (a page on a resource whose changes are
 * published, unless its config says `live: false`). Without it no page is live, whatever
 * its config says.
 * @defaultValue none: live pages are off
 */
export const CHANGE_FEED = new InjectionToken<ChangeFeed>('CHANGE_FEED');

/**
 * A function to be called when the user logs out.
 * @defaultValue a no-op function that returns 0
 */
export const LOGOUT_ACTION = new InjectionToken<() => void>('LOGOUT_ACTION', { factory: () => () => 0 });

/**
 * A function to be called when the user is logged out due to inactivity.
 * @defaultValue a no-op function that returns 0
 */
export const IDLE_LOGOUT_ACTION = new InjectionToken<() => void>('IDLE_LOGOUT_ACTION', {
  factory: () => () => 0,
});

/**
 * Retired, with no effect: any activity (mouse movement, a key, a click, a scroll) resets the
 * idle timer and ends the warning, whatever this token says, since the application knows the
 * person is back from it and asks for nothing more. `IdleService.start()` logs once in the
 * console when an application still provides true. The token stays so a provider of it keeps
 * compiling; remove the provider.
 * @deprecated Activity always ends the idle warning; remove the provider.
 * @defaultValue false
 */
export const IDLE_TIMEOUT_REQUIRE_CONFIRMATION = new InjectionToken<boolean>('IDLE_TIMEOUT_REQUIRE_CONFIRMATION', {
  factory: () => false,
});

/**
 * The sentence a login page shows for each login refusal code the session module sends.
 * A refused login redirects to the login page with `?code=<code>` and nothing else; the
 * page renders the sentence it holds for the code and nothing for a code it does not
 * know, so no text that arrives in the URL is ever displayed. One entry per code in the
 * session module's "Login refusal codes" table. A refusal the server answers on a step
 * carries its code in the body, whatever its status: the attempt limiter's
 * `too_many_attempts` arrives with 429 and the wait in seconds (`retryAfter`), which
 * `UiCoreService.loginMessage(code, retryAfter)` renders after the sentence.
 */
export const DEFAULT_LOGIN_MESSAGES: Readonly<Record<string, string>> = {
  internal_error: 'Sign-in failed because of a problem on our side. Please try again.',
  // The login page's own code, never sent by a server: the arrival's reads got no answer it could show.
  unreachable: 'The server did not answer. Try again in a moment.',
  login_refused: 'Sign-in was refused for this account.',
  no_oidc_cookie: 'Sign-in could not be completed because the browser lost its sign-in cookie. Please try again.',
  invalid_state:
    'Sign-in could not be completed because it did not match the sign-in this browser started. Please try again.',
  invalid_pkce: 'Sign-in could not be completed because its verification code was missing. Please try again.',
  token_exchange_failed: 'The service you signed in with did not complete the sign-in. Please try again.',
  no_id_token: 'The service you signed in with did not say who you are. Please try again.',
  verify_id_token_failed: 'The answer from the service you signed in with could not be verified. Please try again.',
  parse_claims_failed: 'The answer from the service you signed in with could not be read. Please try again.',
  not_workspace_member:
    'This Google account is not one this application accepts. Sign in with the Google account your organization gave you.',
  email_not_verified: 'The email address of this account is not verified.',
  no_roles: 'This account has no role in this application. Please contact your administrator.',
  password_change_required: 'Your password was set by an administrator. Choose a new password to continue.',
  no_email_claim: 'The service you signed in with did not say which account signed in.',
  unlisted_tenant: 'This account belongs to an organization that cannot sign in here.',
  personal_account: 'Personal Microsoft accounts cannot sign in here. Use your work or school account.',
  organization_mismatch: 'This account belongs to a different organization than the one you signed in through.',
  identity_rejected: 'This account could not be matched to a user of this application.',
  not_invited: 'This account has not been invited to this application.',
  // The login page names the invited address itself while the invitation stands; this is the sentence without it.
  invitation_address: 'This invitation is for another account. Sign in with the account it was sent to.',
  guest_account: 'Guest accounts cannot sign in here. Use an account that is a member of the organization.',
  policy_denied: 'Signing in this way is not allowed for this account.',
  user_disabled: 'This account has been disabled. Please contact your administrator.',
  pending_expired: 'The sign-in took too long to complete. Please start again.',
  invalid_code: 'That code is not the one we sent. Check it and try again.',
  code_expired: 'That code is no longer valid. Ask for a new one.',
  no_email: 'The service you signed in with did not give an email address for this account.',
  too_many_attempts: 'Too many attempts.',
  no_tenant: 'This account is not a member of any organization in this application. Please contact your administrator.',
  connection_not_in_tenant:
    'Your organization does not sign in the way you just tried. Please sign in the way your organization has set up.',
  directory_only:
    'Your organization does not sign in with a password here. Please sign in the way your organization has set up.',
  invitation_only: 'Your organization signs people in by invitation. Ask your administrator to invite you.',
};

/**
 * The login refusal codes the application holds text for: the session module's codes with
 * the library's sentences, plus whatever `provideLoginMessages` adds. `UiCoreService`
 * reads it for `loginMessage` and `publishLoginError`.
 * @defaultValue DEFAULT_LOGIN_MESSAGES
 */
export const LOGIN_MESSAGES = new InjectionToken<Readonly<Record<string, string>>>('LOGIN_MESSAGES', {
  factory: () => DEFAULT_LOGIN_MESSAGES,
});

/**
 * Adds the application's own login refusal codes, or rewords a default: the library's
 * sentences come first and the application's entries after, so a repeated key wins. An
 * application whose custom session data resolver refuses with `not_provisioned` provides
 * `provideLoginMessages({ not_provisioned: 'Your account has not been set up yet.' })`.
 */
export function provideLoginMessages(messages: Record<string, string>): Provider {
  return { provide: LOGIN_MESSAGES, useValue: { ...DEFAULT_LOGIN_MESSAGES, ...messages } };
}
