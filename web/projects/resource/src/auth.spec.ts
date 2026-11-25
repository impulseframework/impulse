import { describe, expect, it } from 'bun:test';
import {
  AuthClient,
  LoginMethod,
  LoginMethods,
  loginMethodsRoute,
  MAILED_LINK_KINDS,
  mailedLink,
  passwordChangeRequiredCode,
  refusedForPasswordChange,
  returnPath,
  SELF_SERVICE_RESET_OFF,
  unknownKindWarning,
} from './auth';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { ApiError, TransportRequest, TransportResponse } from './transport';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';

// The sign-in client's contract: the methods list is read from the route the generated
// router serves and kept for a page to render from, with a method of a kind the client
// cannot drive left out and announced once; a password sign-in reads the list again before
// it posts the credentials to the path the refreshed list names, so the POST carries a live
// XSRF token and never goes to a method the server no longer offers; a redirect method's
// URL names the page to return to; the pending sign-in's status, confirmation and cancel
// ride the routes the list names; and a refusal the server answers on a step resolves
// with its code and words, through no error hook, while every other failure throws.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {},
};

const password: LoginMethod = {
  name: 'password',
  method: 'password',
  kind: 'password',
  label: 'Password',
  path: '/console/api/user/password/login',
};
const google: LoginMethod = {
  name: 'google',
  method: 'google',
  kind: 'redirect',
  label: 'Google',
  path: '/console/api/user/google/login',
};
const pendingRoutes = {
  status: '/console/api/user/pending',
  confirm: '/console/api/user/pending/confirm',
  cancel: '/console/api/user/pending/cancel',
};
/** The pending routes of a server that sends mail: the sign-in code's three beside the other three. */
const codeRoutes = {
  ...pendingRoutes,
  sendCode: '/console/api/user/pending/code',
  verifyCode: '/console/api/user/pending/code/verify',
  enrollEmail: '/console/api/user/pending/email',
};
const codeMailed = { codeEmail: 'n•••@fleet.example', codeSent: true, codeExpiresAt: '2026-10-17T12:10:00Z' };
const changePath = '/console/api/user/password/change';
const consoleList: LoginMethods = { methods: [password, google], pending: pendingRoutes, changePassword: changePath };
const strengthPath = '/console/api/user/password/strength';
/** The list of a server that answers how strong a password is. */
const strengthList: LoginMethods = { ...consoleList, passwordStrength: strengthPath };
const invitationRoutes = {
  open: '/console/api/user/invitation',
  accept: '/console/api/user/invitation/accept',
  signUp: '/console/api/user/invitation/sign-up',
};
const emailRoutes = { add: '/console/api/user/email', verify: '/console/api/user/email/verify' };
const emailConfirmRoute = '/console/api/user/email/confirm';
const resetRoutes = {
  request: '/console/api/user/password-reset',
  open: '/console/api/user/password-reset/open',
  complete: '/console/api/user/password-reset/complete',
};
const signUpRoutes = {
  start: '/console/api/user/sign-up',
  open: '/console/api/user/sign-up/open',
  complete: '/console/api/user/sign-up/complete',
};
/** The list of a server that sends mail: invitations, the address step and an open sign-up, the username being the address. */
const mailingList: LoginMethods = {
  ...consoleList,
  usernameIsEmail: true,
  invitation: invitationRoutes,
  email: emailRoutes,
  signUp: signUpRoutes,
};
/** What opening an invitation answers for a person nobody holds an account for, whom no directory could admit. */
const opened = {
  email: 'ensign@fleet.example',
  name: 'Nova Ensign',
  tenantId: '5d1c8e2a-7b3f-4a9e-b6d0-2c4f8a1e9b37',
  tenantName: 'Anvil',
  expiresAt: '2026-10-30T12:00:00Z',
  signUp: true,
  usernameIsEmail: true,
  methods: [],
  accountExists: false,
  hasPassword: false,
};
const dead = { status: 404, body: { message: 'That link has expired or was already used. Ask for a new one.' } };
/** The list of a server whose people reset their own password and confirm an address an administrator set. */
const resettingList: LoginMethods = {
  ...mailingList,
  email: { ...emailRoutes, confirm: emailConfirmRoute },
  passwordReset: resetRoutes,
};

/** What the server answers each route with; a spec changes an entry to script a step's answer. */
interface Server {
  methods: unknown;
  login: TransportResponse;
  status: TransportResponse;
  confirm: TransportResponse;
  cancel: TransportResponse;
  sendCode: TransportResponse;
  verifyCode: TransportResponse;
  enrollEmail: TransportResponse;
  change: TransportResponse;
  strength: TransportResponse;
  remove: TransportResponse;
  firstPasswordCode: TransportResponse;
  firstPasswordSet: TransportResponse;
  means: TransportResponse;
  name: TransportResponse;
  look: TransportResponse;
  username: TransportResponse;
  usernameFree: TransportResponse;
  ownSessions: TransportResponse;
  endOwnSession: TransportResponse;
  endOtherSessions: TransportResponse;
  forgetBrowsers: TransportResponse;
  ownTenants: TransportResponse;
  leaveTenant: TransportResponse;
  invitationOpen: TransportResponse;
  invitationAccept: TransportResponse;
  invitationSignUp: TransportResponse;
  emailAdd: TransportResponse;
  emailVerify: TransportResponse;
  emailConfirm: TransportResponse;
  resetRequest: TransportResponse;
  resetOpen: TransportResponse;
  resetComplete: TransportResponse;
  signUpStart: TransportResponse;
  signUpOpen: TransportResponse;
  signUpComplete: TransportResponse;
}

function server(overrides: Partial<Server> = {}): Server {
  return {
    methods: consoleList,
    login: { status: 200, body: { mfaIsRequired: false } },
    status: { status: 404, body: { message: 'no pending sign-in' } },
    confirm: { status: 200, body: { mfaIsRequired: false } },
    cancel: { status: 200, body: undefined },
    sendCode: { status: 200, body: codeMailed },
    verifyCode: { status: 200, body: { mfaIsRequired: false } },
    enrollEmail: { status: 200, body: codeMailed },
    change: { status: 200, body: undefined },
    strength: { status: 200, body: { strongEnough: true, strength: 62, minimum: 50 } },
    remove: { status: 200, body: undefined },
    firstPasswordCode: {
      status: 200,
      body: { codeEmail: 'p•••@fleet.example', codeExpiresAt: '2026-11-17T12:10:00Z' },
    },
    firstPasswordSet: { status: 200, body: {} },
    means: { status: 200, body: { password: true, directories: [{ name: 'google', label: 'Fleet Google' }] } },
    name: { status: 200, body: { name: 'Nova Example' } },
    look: { status: 200, body: { scheme: 'dark', palette: 'workspace' } },
    username: { status: 200, body: { username: 'nova' } },
    usernameFree: { status: 200, body: { free: true } },
    ownSessions: { status: 200, body: { sessions: [], rememberedBrowsers: 0 } },
    endOwnSession: { status: 200, body: undefined },
    endOtherSessions: { status: 200, body: { ended: 2 } },
    forgetBrowsers: { status: 200, body: undefined },
    ownTenants: { status: 200, body: { tenants: [] } },
    leaveTenant: { status: 200, body: undefined },
    invitationOpen: { status: 200, body: opened },
    invitationAccept: { status: 200, body: { accepted: true } },
    // The link proves nothing of who holds it: the sign-up makes nobody and waits for the code.
    invitationSignUp: { status: 200, body: { mfaIsRequired: true } },
    emailAdd: { status: 200, body: {} },
    emailVerify: { status: 200, body: { email: 'pax@fleet.example' } },
    emailConfirm: { status: 200, body: { email: 'pax@fleet.example' } },
    resetRequest: { status: 200, body: {} },
    resetOpen: { status: 200, body: { username: 'pax', name: 'Pax Example', email: 'pax@fleet.example' } },
    resetComplete: { status: 200, body: { username: 'pax' } },
    signUpStart: { status: 200, body: {} },
    signUpOpen: { status: 200, body: { email: 'rookie@fleet.example', usernameIsEmail: true } },
    signUpComplete: { status: 200, body: { mfaIsRequired: false } },
    ...overrides,
  };
}

/** The server over a scripted transport, answering by method and path. */
function transportOf(state: Server): ScriptedTransport {
  return scriptedTransport((request: TransportRequest) => {
    const path = request.url.replace(/^https?:\/\/[^/]+/, '').replace(/\?.*$/, '');
    switch (`${request.method} ${path}`) {
      case 'GET /console/api/user/methods':
        return { status: 200, body: state.methods };
      case 'POST /console/api/user/password/login':
        return state.login;
      case 'GET /console/api/user/pending':
        return state.status;
      case 'POST /console/api/user/pending/confirm':
        return state.confirm;
      case 'POST /console/api/user/pending/cancel':
        return state.cancel;
      case 'POST /console/api/user/pending/code':
        return state.sendCode;
      case 'POST /console/api/user/pending/code/verify':
        return state.verifyCode;
      case 'POST /console/api/user/pending/email':
        return state.enrollEmail;
      case 'POST /console/api/user/password/change':
        return state.change;
      case 'POST /console/api/user/password/strength':
        return state.strength;
      case 'POST /console/api/user/password/remove':
        return state.remove;
      case 'POST /console/api/user/first-password/code':
        return state.firstPasswordCode;
      case 'POST /console/api/user/first-password':
        return state.firstPasswordSet;
      case 'GET /console/api/user/sign-in-means':
        return state.means;
      case 'POST /console/api/user/browser/forget':
        return { status: 200, body: undefined };
      case 'POST /console/api/user/name':
        return state.name;
      case 'POST /console/api/user/look':
        return state.look;
      case 'POST /console/api/user/username':
        return state.username;
      case 'POST /console/api/user/username/free':
        return state.usernameFree;
      case 'GET /console/api/user/sessions':
        return state.ownSessions;
      case 'POST /console/api/user/sessions/end':
        return state.endOwnSession;
      case 'POST /console/api/user/sessions/end-others':
        return state.endOtherSessions;
      case 'POST /console/api/user/browser/forget-all':
        return state.forgetBrowsers;
      case 'GET /console/api/user/tenants':
        return state.ownTenants;
      case 'POST /console/api/user/tenants/leave':
        return state.leaveTenant;
      case 'POST /console/api/user/invitation':
        return state.invitationOpen;
      case 'POST /console/api/user/invitation/accept':
        return state.invitationAccept;
      case 'POST /console/api/user/invitation/sign-up':
        return state.invitationSignUp;
      case 'POST /console/api/user/email':
        return state.emailAdd;
      case 'POST /console/api/user/email/verify':
        return state.emailVerify;
      case 'POST /console/api/user/email/confirm':
        return state.emailConfirm;
      case 'POST /console/api/user/password-reset':
        return state.resetRequest;
      case 'POST /console/api/user/password-reset/open':
        return state.resetOpen;
      case 'POST /console/api/user/password-reset/complete':
        return state.resetComplete;
      case 'POST /console/api/user/sign-up':
        return state.signUpStart;
      case 'POST /console/api/user/sign-up/open':
        return state.signUpOpen;
      case 'POST /console/api/user/sign-up/complete':
        return state.signUpComplete;
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  });
}

/** The client's sign-in client over the server, with the errors its hook saw and the warnings it raised. */
function authOver(
  state: Server,
  baseUrl = '/console/api',
): { auth: AuthClient; transport: ScriptedTransport; errors: ApiError[]; warnings: string[] } {
  const transport = transportOf(state);
  const errors: ApiError[] = [];
  const warnings: string[] = [];
  const client = createClient(descriptor, {
    baseUrl,
    transport,
    onError: (error) => errors.push(error),
    warn: (message) => warnings.push(message),
  });
  return { auth: client.auth, transport, errors, warnings };
}

const sent = (transport: ScriptedTransport): string[] => transport.requests.map((r) => `${r.method} ${r.url}`);

describe('the methods list', () => {
  it('is read from the methods route under the API prefix and kept for the page', async () => {
    const { auth, transport } = authOver(server());
    expect(auth.get()).toBeUndefined();
    const list = await auth.methods();
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`]);
    expect(list).toEqual(consoleList);
    expect(auth.get()).toEqual(consoleList);
  });

  it('tells its subscribers when it is read again', async () => {
    const { auth } = authOver(server());
    const seen: (LoginMethods | undefined)[] = [];
    auth.subscribe((list) => seen.push(list));
    await auth.methods();
    expect(seen).toEqual([consoleList]);
  });

  it('leaves out a method of a kind this client does not drive, announcing it once', async () => {
    const passkey = {
      name: 'passkey',
      method: 'passkey',
      kind: 'webauthn',
      label: 'Passkey',
      path: '/console/api/user/passkey/login',
    };
    const { auth, warnings } = authOver(server({ methods: { methods: [password, passkey, google] } }));
    const list = await auth.methods();
    await auth.methods();
    expect(list).toEqual({ methods: [password, google] });
    expect(warnings).toEqual([unknownKindWarning('passkey', 'webauthn')]);
  });

  it('reads a list without pending routes, and an empty answer, as a list', async () => {
    const { auth } = authOver(
      server({ methods: { methods: [google], pending: { status: '/console/api/user/pending' } } }),
    );
    expect(await auth.methods()).toEqual({ methods: [google] });
    const noCancel = authOver(
      server({
        methods: { methods: [google], pending: { status: pendingRoutes.status, confirm: pendingRoutes.confirm } },
      }),
    ).auth;
    expect(await noCancel.methods()).toEqual({ methods: [google] });
    const empty = authOver(server({ methods: null })).auth;
    expect(await empty.methods()).toEqual({ methods: [] });
  });
});

describe('a redirect method', () => {
  it('has a URL naming the page to return to, and one without when none is given', () => {
    const { auth } = authOver(server());
    expect(auth.signInUrl(google, '/console/missions?sector=anvil')).toBe(
      '/console/api/user/google/login?returnUrl=%2Fconsole%2Fmissions%3Fsector%3Danvil',
    );
    expect(auth.signInUrl(google)).toBe('/console/api/user/google/login');
  });

  it('asks the provider for its account chooser when told to, beside the page or alone', () => {
    const { auth } = authOver(server());
    expect(auth.signInUrl(google, '/console/missions', { selectAccount: true })).toBe(
      '/console/api/user/google/login?returnUrl=%2Fconsole%2Fmissions&prompt=select_account',
    );
    expect(auth.signInUrl(google, undefined, { selectAccount: true })).toBe(
      '/console/api/user/google/login?prompt=select_account',
    );
    expect(auth.signInUrl(google, '/console/missions', { selectAccount: false })).toBe(
      '/console/api/user/google/login?returnUrl=%2Fconsole%2Fmissions',
    );
  });

  it('names the account the provider should take, as its login hint, beside the chooser or alone', () => {
    const { auth } = authOver(server());
    expect(auth.signInUrl(google, '/console/missions', { loginHint: 'ensign@fleet.example' })).toBe(
      '/console/api/user/google/login?returnUrl=%2Fconsole%2Fmissions&login_hint=ensign%40fleet.example',
    );
    expect(auth.signInUrl(google, undefined, { selectAccount: true, loginHint: 'ensign@fleet.example' })).toBe(
      '/console/api/user/google/login?prompt=select_account&login_hint=ensign%40fleet.example',
    );
  });

  it('is refused as a URL when it is a password method', () => {
    const { auth } = authOver(server());
    expect(() => auth.signInUrl(password)).toThrow('not a redirect');
  });
});

describe('the return address', () => {
  it('keeps a path in this application in canonical form, its query and fragment passing through', () => {
    for (const path of [
      '/',
      '/persona/pick/req-1',
      '/settings/sso?test=done',
      '/reports#x',
      '/a%20b/',
      '/x:y@z',
      '/console/missions?sector=anvil&page=2',
    ]) {
      expect(returnPath(path)).toBe(path);
    }
  });

  it('answers undefined for anything else: nothing, another origin, a scheme-relative URL, a backslash raw or encoded, a dot or empty segment as written or decoded, a bad escape, a control character or a character outside the path set', () => {
    for (const value of [
      '',
      undefined,
      null,
      'persona/pick/req-1',
      'https://evil.example/pick',
      '//evil.example/pick',
      '/\\evil.example',
      '/a\\b',
      '/a%5Cb',
      '/a%5cb',
      '/a/../b',
      '/./a',
      '/a/.',
      '/a/..',
      '/a//b',
      '/a/%2e%2e/b',
      '/a/%2E/b',
      '/a%zz',
      '/a%2',
      '/a b',
      '/a\tb',
      '/a%00b',
      '/a%7fb',
      '/ä',
      '/a?b\\c',
      '/a<b>',
    ]) {
      expect([value, returnPath(value)]).toEqual([value, undefined]);
    }
  });
});

describe('the mailed link a page was opened with', () => {
  it('is read from the fragment, with or without its #, for every kind of link the server mails', () => {
    for (const kind of MAILED_LINK_KINDS) {
      expect(mailedLink(`#${kind}=tok_9-Z`)).toEqual({ kind, token: 'tok_9-Z' });
      expect(mailedLink(`${kind}=tok_9-Z`)).toEqual({ kind, token: 'tok_9-Z' });
    }
    expect([...MAILED_LINK_KINDS]).toEqual(['invitation', 'signUp', 'reset', 'address', 'verify']);
  });

  it('is nothing for an address that carries no link: no fragment, an empty token, or a fragment that names something else', () => {
    for (const fragment of [undefined, null, '', '#', '#invitation=', '#section-2', '#returnUrl=%2Fhome']) {
      expect([fragment, mailedLink(fragment)]).toEqual([fragment, undefined]);
    }
  });

  it('is read from the fragment alone: the function takes the fragment and nothing else of the address, so nothing in a query reaches it', () => {
    expect(mailedLink.length).toBe(1);
    expect(mailedLink('#returnUrl=%2Fhome&invitation=tok')).toEqual({ kind: 'invitation', token: 'tok' });
  });
});

describe('a password sign-in', () => {
  it('reads the list again, then posts the credentials to the path the list names, and resolves the session', async () => {
    const { auth, transport, errors } = authOver(server());
    const result = await auth.signInWithPassword(password, { username: 'overseer', password: 'lodestar' });
    expect(result).toEqual({ outcome: 'session' });
    expect(sent(transport)).toEqual(['GET /console/api/user/methods', 'POST /console/api/user/password/login']);
    expect(transport.requests[1].body).toEqual({ username: 'overseer', password: 'lodestar' });
    expect(errors).toEqual([]);
  });

  it('posts to the path as the server wrote it under a base URL with an origin', async () => {
    const { auth, transport } = authOver(server(), 'https://ops.example.test/console/api');
    await auth.signInWithPassword(password, { username: 'overseer', password: 'lodestar' });
    expect(sent(transport)).toEqual([
      'GET https://ops.example.test/console/api/user/methods',
      'POST https://ops.example.test/console/api/user/password/login',
    ]);
  });

  it('resolves a pending step, with the page the server chose for it when it chose one', async () => {
    const held = authOver(server({ login: { status: 200, body: { mfaIsRequired: true } } }));
    expect(await held.auth.signInWithPassword(password, { username: 'overseer', password: 'lodestar' })).toEqual({
      outcome: 'pending',
    });
    const sentOn = authOver(
      server({ login: { status: 200, body: { mfaIsRequired: true, redirectUrl: '/console/mfa' } } }),
    );
    expect(await sentOn.auth.signInWithPassword(password, { username: 'overseer', password: 'lodestar' })).toEqual({
      outcome: 'pending',
      redirectUrl: '/console/mfa',
    });
  });

  const refusals: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: 'a refusal with a code resolves with the code and the words',
      answer: { status: 401, body: { message: 'User disabled', code: 'user_disabled' } },
      want: { outcome: 'refused', status: 401, code: 'user_disabled', message: 'User disabled' },
    },
    {
      name: 'a wrong password resolves with the words and no code',
      answer: {
        status: 401,
        body: { message: 'The username or password is wrong. Check what you entered and try again' },
      },
      want: {
        outcome: 'refused',
        status: 401,
        message: 'The username or password is wrong. Check what you entered and try again',
      },
    },
    {
      name: 'a forbidden sign-in resolves as refused',
      answer: { status: 403, body: { message: 'Sign-in is not allowed for this account', code: 'policy_denied' } },
      want: {
        outcome: 'refused',
        status: 403,
        code: 'policy_denied',
        message: 'Sign-in is not allowed for this account',
      },
    },
    {
      name: 'a refusal with no body resolves with the status as its words',
      answer: { status: 401, body: undefined },
      want: { outcome: 'refused', status: 401, message: 'HTTP 401' },
    },
    {
      name: 'a locked name resolves as refused with the code and the wait the 429 body carries',
      answer: {
        status: 429,
        body: { message: 'Too many sign-in attempts', code: 'too_many_attempts', retryAfter: 840 },
        headers: { 'retry-after': '840' },
      },
      want: {
        outcome: 'refused',
        status: 429,
        code: 'too_many_attempts',
        message: 'Too many sign-in attempts',
        retryAfter: 840,
      },
    },
    {
      name: 'a wait the body does not carry is read from the Retry-After header',
      answer: {
        status: 429,
        body: { message: 'Too many sign-in attempts', code: 'too_many_attempts' },
        headers: { 'retry-after': '45' },
      },
      want: {
        outcome: 'refused',
        status: 429,
        code: 'too_many_attempts',
        message: 'Too many sign-in attempts',
        retryAfter: 45,
      },
    },
    {
      name: 'a wait that is not a number of seconds in the body nor a header the client reads is none',
      answer: {
        status: 429,
        body: { message: 'Too many sign-in attempts', code: 'too_many_attempts', retryAfter: '840' },
      },
      want: { outcome: 'refused', status: 429, code: 'too_many_attempts', message: 'Too many sign-in attempts' },
    },
  ];
  for (const tt of refusals) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(server({ login: tt.answer }));
      const result = await auth.signInWithPassword(password, { username: 'overseer', password: 'wrong' });
      expect(result).toEqual(tt.want);
      expect(Object.keys(result).sort()).toEqual(Object.keys(tt.want as object).sort());
      expect(errors).toEqual([]);
    });
  }

  it('throws the server fault a step does not declare, the hook seeing it', async () => {
    const { auth, errors } = authOver(server({ login: { status: 500, body: { message: 'store unavailable' } } }));
    await expect(
      auth.signInWithPassword(password, { username: 'overseer', password: 'lodestar' }),
    ).rejects.toBeInstanceOf(ApiError);
    expect(errors.map((error) => error.status)).toEqual([500]);
  });

  it('is refused before any request for a redirect method', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.signInWithPassword(google, { username: 'overseer', password: 'lodestar' })).rejects.toThrow(
      'not a password form',
    );
    expect(transport.requests).toHaveLength(0);
  });

  it('throws without posting when the refreshed list no longer offers the method, the kept list being the new one', async () => {
    const { auth, transport } = authOver(server({ methods: { methods: [google], pending: pendingRoutes } }));
    await expect(auth.signInWithPassword(password, { username: 'overseer', password: 'lodestar' })).rejects.toThrow(
      'no longer offers the password password sign-in',
    );
    expect(sent(transport)).toEqual(['GET /console/api/user/methods']);
    expect(auth.get()).toEqual({ methods: [google], pending: pendingRoutes });
  });
});

describe('the pending sign-in', () => {
  const waiting = {
    reason: 'confirmation',
    email: 'nova@fleet.example',
    expiresAt: '2026-10-17T12:10:00Z',
    returnUrl: '/console/dashboard',
  };

  it('reports the step a sign-in waits on, reading the list first for the route', async () => {
    const { auth, transport } = authOver(server({ status: { status: 200, body: waiting } }));
    expect(await auth.pending()).toEqual({ outcome: 'waiting', ...waiting });
    expect(sent(transport)).toEqual(['GET /console/api/user/methods', 'GET /console/api/user/pending']);
  });

  it('reads the status from the kept list without reading the list again', async () => {
    const { auth, transport } = authOver(
      server({ status: { status: 200, body: { ...waiting, reason: 'mfa', factors: ['totp'] } } }),
    );
    await auth.methods();
    expect(await auth.pending()).toEqual({ outcome: 'waiting', ...waiting, reason: 'mfa', factors: ['totp'] });
    expect(sent(transport)).toEqual(['GET /console/api/user/methods', 'GET /console/api/user/pending']);
  });

  it('reports none when the browser has no pending sign-in', async () => {
    const { auth, errors } = authOver(server());
    expect(await auth.pending()).toEqual({ outcome: 'none' });
    expect(errors).toEqual([]);
  });

  it('reports the expiry as the server refuses it', async () => {
    const { auth, errors } = authOver(
      server({
        status: { status: 401, body: { message: 'the pending sign-in has expired', code: 'pending_expired' } },
      }),
    );
    expect(await auth.pending()).toEqual({
      outcome: 'refused',
      status: 401,
      code: 'pending_expired',
      message: 'the pending sign-in has expired',
    });
    expect(errors).toEqual([]);
  });

  it('throws when the list names no pending routes', async () => {
    const { auth } = authOver(server({ methods: { methods: [password] } }));
    await expect(auth.pending()).rejects.toThrow('names no pending sign-in routes');
    await expect(auth.confirmPending('lodestar')).rejects.toThrow('names no pending sign-in routes');
    await expect(auth.cancelPending()).rejects.toThrow('names no pending sign-in routes');
  });

  it('keeps the pending routes of a server with no password sign-in, which names no confirmation route, and posts no confirmation there', async () => {
    const withoutConfirm = { status: pendingRoutes.status, cancel: pendingRoutes.cancel };
    const held = { ...waiting, reason: 'mfa' };
    const { auth, transport } = authOver(
      server({ methods: { methods: [google], pending: withoutConfirm }, status: { status: 200, body: held } }),
    );
    expect(await auth.methods()).toEqual({ methods: [google], pending: withoutConfirm });
    expect(await auth.pending()).toEqual({ outcome: 'waiting', ...held });
    await expect(auth.confirmPending('lodestar')).rejects.toThrow(
      'names no route that confirms a sign-in with a password',
    );
    await auth.cancelPending();
    expect(sent(transport)).toEqual([
      'GET /console/api/user/methods',
      'GET /console/api/user/pending',
      'GET /console/api/user/methods',
      'GET /console/api/user/methods',
      'POST /console/api/user/pending/cancel',
    ]);
  });

  it('is confirmed with the password after the list is read again, and resolves the session', async () => {
    const { auth, transport } = authOver(server());
    await auth.methods();
    expect(await auth.confirmPending('lodestar')).toEqual({ outcome: 'session' });
    expect(sent(transport)).toEqual([
      'GET /console/api/user/methods',
      'GET /console/api/user/methods',
      'POST /console/api/user/pending/confirm',
    ]);
    expect(transport.requests[2].body).toEqual({ password: 'lodestar' });
  });

  const confirmations: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: 'a wrong confirmation password resolves with the words and no code',
      answer: {
        status: 401,
        body: { message: 'The username or password is wrong. Check what you entered and try again' },
      },
      want: {
        outcome: 'refused',
        status: 401,
        message: 'The username or password is wrong. Check what you entered and try again',
      },
    },
    {
      name: 'an expired pending sign-in resolves with its code',
      answer: { status: 401, body: { message: 'the pending sign-in has expired', code: 'pending_expired' } },
      want: { outcome: 'refused', status: 401, code: 'pending_expired', message: 'the pending sign-in has expired' },
    },
    {
      name: 'a step the pending sign-in does not wait for resolves as refused',
      answer: { status: 409, body: { message: 'the pending sign-in does not wait for a password confirmation' } },
      want: {
        outcome: 'refused',
        status: 409,
        message: 'the pending sign-in does not wait for a password confirmation',
      },
    },
    {
      name: 'no pending sign-in resolves as refused',
      answer: { status: 404, body: { message: 'no pending sign-in' } },
      want: { outcome: 'refused', status: 404, message: 'no pending sign-in' },
    },
    {
      name: 'a locked name resolves as refused with the code and the wait the 429 body carries',
      answer: {
        status: 429,
        body: { message: 'Too many sign-in attempts', code: 'too_many_attempts', retryAfter: 840 },
        headers: { 'retry-after': '840' },
      },
      want: {
        outcome: 'refused',
        status: 429,
        code: 'too_many_attempts',
        message: 'Too many sign-in attempts',
        retryAfter: 840,
      },
    },
    {
      name: 'the policy asking for MFA after the link resolves a pending step',
      answer: { status: 200, body: { mfaIsRequired: true } },
      want: { outcome: 'pending' },
    },
  ];
  for (const tt of confirmations) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(server({ confirm: tt.answer }));
      expect(await auth.confirmPending('lodestar')).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('is cancelled after the list is read again', async () => {
    const { auth, transport } = authOver(server());
    await auth.cancelPending();
    expect(sent(transport)).toEqual(['GET /console/api/user/methods', 'POST /console/api/user/pending/cancel']);
  });
});

describe('the sign-in code', () => {
  const mailing = (overrides: Partial<Server> = {}): Server =>
    server({ methods: { ...consoleList, pending: codeRoutes }, ...overrides });
  const codeStep = {
    reason: 'mfa',
    email: 'nova@fleet.example',
    expiresAt: '2026-10-17T12:10:00Z',
    returnUrl: '/console/missions',
    factors: ['email-code'],
  };

  it('is reported on the status with where it stands, the fields left out reading as empty or false', async () => {
    const { auth } = authOver(
      mailing({
        status: { status: 200, body: { ...codeStep, emailCode: { codeEmail: 'n•••@fleet.example', codeSent: false } } },
      }),
    );
    expect(await auth.pending()).toEqual({
      outcome: 'waiting',
      ...codeStep,
      emailCode: { codeEmail: 'n•••@fleet.example', enrollEmail: false, codeSent: false, rememberOffered: false },
    });
  });

  it('is reported standing, with its expiry, and as waiting for an address', async () => {
    const standing = authOver(mailing({ status: { status: 200, body: { ...codeStep, emailCode: codeMailed } } }));
    expect(await standing.auth.pending()).toEqual({
      outcome: 'waiting',
      ...codeStep,
      emailCode: {
        codeEmail: 'n•••@fleet.example',
        enrollEmail: false,
        codeSent: true,
        codeExpiresAt: '2026-10-17T12:10:00Z',
        rememberOffered: false,
      },
    });
    const enrolling = authOver(
      mailing({ status: { status: 200, body: { ...codeStep, emailCode: { enrollEmail: true, codeSent: false } } } }),
    );
    expect(await enrolling.auth.pending()).toEqual({
      outcome: 'waiting',
      ...codeStep,
      emailCode: { codeEmail: '', enrollEmail: true, codeSent: false, rememberOffered: false },
    });
  });

  it('says the browser may be remembered only where the server says true', async () => {
    for (const [given, want] of [
      [true, true],
      [false, false],
      ['yes', false],
    ] as const) {
      const { auth } = authOver(
        mailing({
          status: { status: 200, body: { ...codeStep, emailCode: { ...codeMailed, rememberOffered: given } } },
        }),
      );
      const status = await auth.pending();
      expect(status.outcome === 'waiting' ? status.emailCode?.rememberOffered : undefined).toBe(want);
    }
  });

  it('is absent from a step that carries none, and from a status whose emailCode is not an object', async () => {
    const { auth } = authOver(mailing({ status: { status: 200, body: { ...codeStep, emailCode: 'soon' } } }));
    expect(await auth.pending()).toEqual({ outcome: 'waiting', ...codeStep });
  });

  it('keeps the three routes the list names, and leaves them off a list that names them in part', async () => {
    const mailingList = authOver(mailing());
    expect((await mailingList.auth.methods()).pending).toEqual(codeRoutes);
    const partial = authOver(
      server({ methods: { ...consoleList, pending: { ...pendingRoutes, sendCode: codeRoutes.sendCode } } }),
    );
    expect((await partial.auth.methods()).pending).toEqual(pendingRoutes);
  });

  it('is sent after the list is read again, posting nothing, and answers where it went and when it expires', async () => {
    const { auth, transport } = authOver(mailing());
    await auth.methods();
    expect(await auth.sendCode()).toEqual({
      outcome: 'sent',
      codeEmail: 'n•••@fleet.example',
      codeExpiresAt: '2026-10-17T12:10:00Z',
    });
    expect(sent(transport)).toEqual([
      'GET /console/api/user/methods',
      'GET /console/api/user/methods',
      'POST /console/api/user/pending/code',
    ]);
    expect(transport.requests[2].body).toBeUndefined();
  });

  it('takes the address of an account that holds none after the list is read again, and answers as a sending does', async () => {
    const { auth, transport } = authOver(mailing());
    expect(await auth.enrollEmail('nova@fleet.example')).toEqual({
      outcome: 'sent',
      codeEmail: 'n•••@fleet.example',
      codeExpiresAt: '2026-10-17T12:10:00Z',
    });
    expect(sent(transport)).toEqual(['GET /console/api/user/methods', 'POST /console/api/user/pending/email']);
    expect(transport.requests[1].body).toEqual({ email: 'nova@fleet.example' });
  });

  const sendings: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: 'a sign-in that does not wait for the code resolves as refused',
      answer: {
        status: 409,
        body: { message: 'the pending sign-in waits for a password confirmation, not a second factor' },
      },
      want: {
        outcome: 'refused',
        status: 409,
        message: 'the pending sign-in waits for a password confirmation, not a second factor',
      },
    },
    {
      name: 'an address sent too many codes resolves as refused with the code and the wait',
      answer: { status: 429, body: { message: 'too many codes', code: 'too_many_emails', retryAfter: 600 } },
      want: { outcome: 'refused', status: 429, code: 'too_many_emails', message: 'too many codes', retryAfter: 600 },
    },
    {
      name: 'an expired pending sign-in resolves with its code',
      answer: { status: 401, body: { message: 'the pending sign-in has expired', code: 'pending_expired' } },
      want: { outcome: 'refused', status: 401, code: 'pending_expired', message: 'the pending sign-in has expired' },
    },
    {
      name: 'no pending sign-in resolves as refused',
      answer: { status: 404, body: { message: 'no pending sign-in' } },
      want: { outcome: 'refused', status: 404, message: 'no pending sign-in' },
    },
  ];
  for (const tt of sendings) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(mailing({ sendCode: tt.answer, enrollEmail: tt.answer }));
      expect(await auth.sendCode()).toEqual(tt.want);
      expect(await auth.enrollEmail('nova@fleet.example')).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('resolves what is not an address as the refusal of an enrollment, through no error hook', async () => {
    const { auth, errors } = authOver(
      mailing({ enrollEmail: { status: 400, body: { message: 'not an email address' } } }),
    );
    expect(await auth.enrollEmail('nova')).toEqual({
      outcome: 'refused',
      status: 400,
      message: 'not an email address',
    });
    expect(errors).toEqual([]);
  });

  it('is entered after the list is read again, posting the code and the remembering asked for, and resolves the session', async () => {
    const { auth, transport } = authOver(mailing());
    expect(await auth.verifyCode('123456', true)).toEqual({ outcome: 'session' });
    expect(await auth.verifyCode('123456', false)).toEqual({ outcome: 'session' });
    expect(sent(transport)).toEqual([
      'GET /console/api/user/methods',
      'POST /console/api/user/pending/code/verify',
      'GET /console/api/user/methods',
      'POST /console/api/user/pending/code/verify',
    ]);
    expect(transport.requests[1].body).toEqual({ code: '123456', rememberDevice: true });
    expect(transport.requests[3].body).toEqual({ code: '123456', rememberDevice: false });
  });

  const entries: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: 'a wrong code resolves with its code',
      answer: { status: 401, body: { message: 'the code is not the one sent', code: 'invalid_code' } },
      want: { outcome: 'refused', status: 401, code: 'invalid_code', message: 'the code is not the one sent' },
    },
    {
      name: 'a code entered while none stands resolves with its code',
      answer: { status: 401, body: { message: 'no sign-in code stands: ask for a new one', code: 'code_expired' } },
      want: {
        outcome: 'refused',
        status: 401,
        code: 'code_expired',
        message: 'no sign-in code stands: ask for a new one',
      },
    },
    {
      name: 'a sign-in that does not wait for the code resolves as refused',
      answer: {
        status: 409,
        body: { message: 'the pending sign-in waits for a password confirmation, not a second factor' },
      },
      want: {
        outcome: 'refused',
        status: 409,
        message: 'the pending sign-in waits for a password confirmation, not a second factor',
      },
    },
    {
      name: 'the policy asking for a further step resolves a pending step',
      answer: { status: 200, body: { mfaIsRequired: true } },
      want: { outcome: 'pending' },
    },
  ];
  for (const tt of entries) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(mailing({ verifyCode: tt.answer }));
      expect(await auth.verifyCode('000000', false)).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose pending routes name no code routes', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.sendCode()).rejects.toThrow('names no sign-in code routes');
    await expect(auth.enrollEmail('nova@fleet.example')).rejects.toThrow('names no sign-in code routes');
    await expect(auth.verifyCode('123456', false)).rejects.toThrow('names no sign-in code routes');
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe('forgetting this browser', () => {
  const forgetPath = '/console/api/user/browser/forget';

  it('reads the list again, then posts to the forget path it names', async () => {
    const { auth, transport, errors } = authOver(server({ methods: { ...consoleList, forgetBrowser: forgetPath } }));
    await auth.forgetBrowser();
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${forgetPath}`]);
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no forget path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.forgetBrowser()).rejects.toThrow(/names no forget-this-browser path/);
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe("changing the person's own name", () => {
  const namePath = '/console/api/user/name';
  const named = (overrides: Partial<Server> = {}): Server =>
    server({ methods: { ...consoleList, changeName: namePath }, ...overrides });

  it('reads the list again, then posts the name to the change path it names, and resolves the name the server keeps', async () => {
    const { auth, transport, errors } = authOver(named());
    expect(await auth.changeName('  Nova Example ')).toEqual({ outcome: 'changed', name: 'Nova Example' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${namePath}`]);
    expect(transport.requests[1].body).toEqual({ name: '  Nova Example ' });
    expect(errors).toEqual([]);
  });

  it('resolves no name when the server answers none: the name was taken off', async () => {
    const { auth } = authOver(named({ name: { status: 200, body: {} } }));
    expect(await auth.changeName('')).toEqual({ outcome: 'changed' });
  });

  const refusals: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: "a name too long to keep resolves with the server's words",
      answer: { status: 400, body: { message: 'a name is at most 200 characters' } },
      want: { outcome: 'refused', status: 400, message: 'a name is at most 200 characters' },
    },
    {
      name: 'an impersonated session resolves as refused',
      answer: {
        status: 403,
        body: { message: 'You are acting as someone else, and this session cannot change their account.' },
      },
      want: {
        outcome: 'refused',
        status: 403,
        message: 'You are acting as someone else, and this session cannot change their account.',
      },
    },
  ];
  for (const tt of refusals) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(named({ name: tt.answer }));
      expect(await auth.changeName('Nova')).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose list names no change path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.changeName('Nova')).rejects.toThrow(/names no change-your-name path/);
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

const usernamePath = '/console/api/user/username';
const usernameFreePath = '/console/api/user/username/free';
const ownSessionsRoutes = {
  list: '/console/api/user/sessions',
  end: '/console/api/user/sessions/end',
  endOthers: '/console/api/user/sessions/end-others',
};
const forgetAllPath = '/console/api/user/browser/forget-all';
/** The list of a server that serves a person's own username, sessions and remembered browsers. */
const ownAccountList: LoginMethods = {
  ...consoleList,
  changeUsername: usernamePath,
  usernameFree: usernameFreePath,
  ownSessions: ownSessionsRoutes,
  forgetBrowsers: forgetAllPath,
};
const ownAccount = (overrides: Partial<Server> = {}): Server => server({ methods: ownAccountList, ...overrides });

describe("the paths of the person's own username, sessions and remembered browsers", () => {
  it('are kept as the list names them', async () => {
    expect(await authOver(ownAccount()).auth.methods()).toEqual(ownAccountList);
  });

  it('are absent from a list that names none, or names them empty', async () => {
    const empty = {
      ...consoleList,
      changeUsername: '',
      usernameFree: '',
      ownSessions: { list: '', end: '', endOthers: '' },
      forgetBrowsers: '',
    };
    const list = await authOver(server({ methods: empty })).auth.methods();
    expect(list).toEqual(consoleList);
    expect(Object.keys(list)).not.toContain('ownSessions');
  });

  it('leave the sessions out where the list names some of their paths and not all', async () => {
    const some = { ...consoleList, ownSessions: { list: ownSessionsRoutes.list, end: ownSessionsRoutes.end } };
    const list = await authOver(server({ methods: some })).auth.methods();
    expect(list).toEqual(consoleList);
    expect(Object.keys(list)).not.toContain('ownSessions');
  });
});

describe("changing the person's own username", () => {
  it('reads the list again, then posts the username to the path it names, and resolves the username the server keeps', async () => {
    const { auth, transport, errors } = authOver(ownAccount());
    expect(await auth.changeUsername(' nova ')).toEqual({ outcome: 'changed', username: 'nova' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${usernamePath}`]);
    expect(transport.requests[1].body).toEqual({ username: ' nova ' });
    expect(errors).toEqual([]);
  });

  const refusals: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: "a username someone else holds resolves with the server's words",
      answer: { status: 409, body: { message: 'Someone else has that username.' } },
      want: { outcome: 'refused', status: 409, message: 'Someone else has that username.' },
    },
    {
      name: "a username that is the address resolves with the server's words",
      answer: {
        status: 409,
        body: { message: 'You sign in with your email address, so your username changes when you change the address.' },
      },
      want: {
        outcome: 'refused',
        status: 409,
        message: 'You sign in with your email address, so your username changes when you change the address.',
      },
    },
    {
      name: 'an empty username resolves as refused',
      answer: { status: 400, body: { message: 'Type a username.' } },
      want: { outcome: 'refused', status: 400, message: 'Type a username.' },
    },
    {
      name: 'an impersonated session resolves as refused',
      answer: {
        status: 403,
        body: { message: 'You are acting as someone else, and this session cannot change their account.' },
      },
      want: {
        outcome: 'refused',
        status: 403,
        message: 'You are acting as someone else, and this session cannot change their account.',
      },
    },
  ];
  for (const tt of refusals) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(ownAccount({ username: tt.answer }));
      expect(await auth.changeUsername('nova')).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose list names no path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.changeUsername('nova')).rejects.toThrow(
      'this server names no change-your-username path in its methods list: it does not serve one',
    );
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe('asking whether a username is free', () => {
  it('posts to the path of the list last read, reading the list only when none was read', async () => {
    const { auth, transport } = authOver(ownAccount());
    expect(await auth.usernameFree('nova')).toEqual({ outcome: 'answered', free: true });
    expect(await auth.usernameFree('novae')).toEqual({ outcome: 'answered', free: true });
    expect(sent(transport)).toEqual([
      `GET /console/api/${loginMethodsRoute}`,
      `POST ${usernameFreePath}`,
      `POST ${usernameFreePath}`,
    ]);
    expect(transport.requests[1].body).toEqual({ username: 'nova' });
  });

  it('answers not free for anything but free: true', async () => {
    for (const body of [{ free: false }, { free: 'yes' }, {}, null]) {
      const { auth } = authOver(ownAccount({ usernameFree: { status: 200, body } }));
      expect(await auth.usernameFree('nova')).toEqual({ outcome: 'answered', free: false });
    }
  });

  for (const status of [400, 401, 403, 409]) {
    it(`resolves a ${status} as refused, with the server's words, handed to no error hook`, async () => {
      const message = 'A username is text on one line, with no tab or other control character.';
      const { auth, errors } = authOver(ownAccount({ usernameFree: { status, body: { message } } }));
      expect(await auth.usernameFree('nova')).toEqual({ outcome: 'refused', status, message });
      expect(errors).toEqual([]);
    });
  }

  it('asks quietly: any other answer that is an error is thrown and handed to no error hook', async () => {
    const { auth, errors } = authOver(ownAccount({ usernameFree: { status: 500, body: { message: 'boom' } } }));
    await expect(auth.usernameFree('nova')).rejects.toBeInstanceOf(ApiError);
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.usernameFree('nova')).rejects.toThrow(
      'this server names no is-this-name-free path in its methods list: it does not serve one',
    );
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe('where the person is signed in', () => {
  const chrome = {
    id: '7c1e4a52-3b9f-4d2e-8a61-5f0c9b2d7e14',
    browser: 'Chrome on Android',
    phone: true,
    signedInAt: '2026-11-20T09:00:00Z',
    lastUsedAt: '2026-11-22T08:30:00Z',
    current: true,
  };
  const firefox = {
    id: '2a9d6f13-8e4b-4c7a-9d05-6b3e1f8c2a47',
    phone: false,
    signedInAt: '2026-11-01T09:00:00Z',
    lastUsedAt: '2026-11-21T17:45:00Z',
    current: false,
  };

  it('reads the list again, then the sessions from the path it names', async () => {
    const { auth, transport, errors } = authOver(
      ownAccount({ ownSessions: { status: 200, body: { sessions: [chrome, firefox], rememberedBrowsers: 2 } } }),
    );
    expect(await auth.ownSessions()).toEqual({ sessions: [chrome, firefox], rememberedBrowsers: 2 });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `GET ${ownSessionsRoutes.list}`]);
    expect(errors).toEqual([]);
  });

  it('drops an entry without an id, reads phone and current as true only for true, and counts no remembered browser that is not a number', async () => {
    const body = {
      sessions: [{ ...firefox, phone: 'yes', current: 1, browser: 7 }, { ...chrome, id: 12 }, null],
      rememberedBrowsers: '2',
    };
    const { auth } = authOver(ownAccount({ ownSessions: { status: 200, body } }));
    const read = await auth.ownSessions();
    expect(read).toEqual({ sessions: [{ ...firefox, phone: false, current: false }], rememberedBrowsers: 0 });
    expect(Object.keys(read.sessions[0])).not.toContain('browser');
    const empty = authOver(ownAccount({ ownSessions: { status: 200, body: null } })).auth;
    expect(await empty.ownSessions()).toEqual({ sessions: [], rememberedBrowsers: 0 });
  });

  it('signs the person out of one browser, and resolves gone for one already signed out', async () => {
    const { auth, transport, errors } = authOver(ownAccount());
    expect(await auth.endOwnSession(firefox.id)).toBe('ended');
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${ownSessionsRoutes.end}`]);
    expect(transport.requests[1].body).toEqual({ sessionId: firefox.id });
    const gone = authOver(
      ownAccount({ endOwnSession: { status: 404, body: { message: 'That browser is already signed out.' } } }),
    );
    expect(await gone.auth.endOwnSession(firefox.id)).toBe('gone');
    expect(errors).toEqual([]);
    expect(gone.errors).toEqual([]);
  });

  it('signs the person out of every other browser, and resolves how many', async () => {
    const { auth, transport } = authOver(ownAccount());
    expect(await auth.endOtherSessions()).toBe(2);
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${ownSessionsRoutes.endOthers}`]);
    const unsaid = authOver(ownAccount({ endOtherSessions: { status: 200, body: {} } })).auth;
    expect(await unsaid.endOtherSessions()).toBe(0);
  });

  it('throws on a server whose list names no paths, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    const thrown = 'this server names no where-you-are-signed-in paths in its methods list: it does not serve one';
    await expect(auth.ownSessions()).rejects.toThrow(thrown);
    await expect(auth.endOwnSession(firefox.id)).rejects.toThrow(thrown);
    await expect(auth.endOtherSessions()).rejects.toThrow(thrown);
    expect(sent(transport).filter((request) => !request.endsWith(loginMethodsRoute))).toEqual([]);
  });
});

describe('forgetting every remembered browser', () => {
  it('reads the list again, then posts to the path it names', async () => {
    const { auth, transport, errors } = authOver(ownAccount());
    await auth.forgetBrowsers();
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${forgetAllPath}`]);
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.forgetBrowsers()).rejects.toThrow(
      'this server names no forget-all-remembered-browsers path in its methods list: it does not serve one',
    );
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

const ownTenantsRoutes = { list: '/console/api/user/tenants', leave: '/console/api/user/tenants/leave' };
/** The list of a server that serves a person's own tenants. */
const ownTenantsList: LoginMethods = { ...consoleList, ownTenants: ownTenantsRoutes };
const ownTenantsServer = (overrides: Partial<Server> = {}): Server => server({ methods: ownTenantsList, ...overrides });

describe("the paths of the person's own tenants", () => {
  it('are kept as the list names them', async () => {
    expect(await authOver(ownTenantsServer()).auth.methods()).toEqual(ownTenantsList);
  });

  it('are absent from a list that names none, names them empty, or names one and not the other', async () => {
    for (const ownTenants of [
      { list: '', leave: '' },
      { list: ownTenantsRoutes.list },
      { leave: ownTenantsRoutes.leave },
    ]) {
      const list = await authOver(server({ methods: { ...consoleList, ownTenants } })).auth.methods();
      expect(list).toEqual(consoleList);
      expect(Object.keys(list)).not.toContain('ownTenants');
    }
  });
});

describe("the person's own tenants", () => {
  const anvil = {
    domain: 'a1111111-1111-4111-8111-111111111111',
    name: 'Anvil',
    roles: ['Pilot', 'Sector crew'],
  };
  const cinder = {
    domain: 'c3333333-3333-4333-8333-333333333333',
    name: 'Cinder',
    roles: ['Sector marshal'],
    refusal: 'You are the last administrator of this sector. Give another person the role before you leave.',
  };

  it('reads the list again, then the tenants from the path it names', async () => {
    const { auth, transport, errors } = authOver(
      ownTenantsServer({ ownTenants: { status: 200, body: { tenants: [anvil, cinder] } } }),
    );
    expect(await auth.ownTenants()).toEqual([anvil, cinder]);
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `GET ${ownTenantsRoutes.list}`]);
    expect(errors).toEqual([]);
  });

  it('drops an entry without a domain, names a tenant by its domain where the server sends no name, and keeps only what is so', async () => {
    const body = {
      tenants: [
        { ...anvil, name: '', roles: ['Pilot', 4, null], refusal: '' },
        { ...cinder, name: 9, roles: 'Sector marshal', refusal: 3 },
        { ...anvil, domain: 12 },
        null,
      ],
    };
    const { auth } = authOver(ownTenantsServer({ ownTenants: { status: 200, body } }));
    const read = await auth.ownTenants();
    expect(read).toEqual([
      { domain: anvil.domain, name: anvil.domain, roles: ['Pilot'] },
      { domain: cinder.domain, name: cinder.domain, roles: [] },
    ]);
    expect(Object.keys(read[0])).not.toContain('refusal');
    expect(Object.keys(read[1])).not.toContain('refusal');
    const empty = authOver(ownTenantsServer({ ownTenants: { status: 200, body: null } })).auth;
    expect(await empty.ownTenants()).toEqual([]);
  });

  it('reads the list again, then posts the tenant, and nothing else, to the path it names', async () => {
    const { auth, transport, errors } = authOver(ownTenantsServer());
    expect(await auth.leaveTenant(anvil.domain)).toEqual({ outcome: 'left' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${ownTenantsRoutes.leave}`]);
    expect(transport.requests[1].body).toEqual({ domain: anvil.domain });
    expect(Object.keys(transport.requests[1].body as object)).toEqual(['domain']);
    expect(errors).toEqual([]);
  });

  const refusals: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: "a tenant's last administrator resolves with the server's words",
      answer: { status: 403, body: { message: cinder.refusal } },
      want: { outcome: 'refused', status: 403, message: cinder.refusal },
    },
    {
      name: "a tenant the person is not in resolves with the server's words",
      answer: { status: 404, body: { message: 'You hold no role in that sector.' } },
      want: { outcome: 'refused', status: 404, message: 'You hold no role in that sector.' },
    },
  ];
  for (const tt of refusals) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(ownTenantsServer({ leaveTenant: tt.answer }));
      expect(await auth.leaveTenant(cinder.domain)).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose list names no paths, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    const thrown = 'this server names no your-tenants paths in its methods list: it does not serve one';
    await expect(auth.ownTenants()).rejects.toThrow(thrown);
    await expect(auth.leaveTenant(anvil.domain)).rejects.toThrow(thrown);
    expect(sent(transport).filter((request) => !request.endsWith(loginMethodsRoute))).toEqual([]);
  });
});

describe('keeping the look the person chose', () => {
  const lookPath = '/console/api/user/look';
  const kept = (overrides: Partial<Server> = {}): Server =>
    server({ methods: { ...consoleList, changeLook: lookPath }, ...overrides });

  it('reads the list again, then posts the look to the path it names, and resolves the look the server keeps', async () => {
    const { auth, transport, errors } = authOver(kept());
    expect(await auth.changeLook({ scheme: 'dark', palette: ' workspace ' })).toEqual({
      outcome: 'changed',
      look: { scheme: 'dark', palette: 'workspace' },
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${lookPath}`]);
    expect(transport.requests[1].body).toEqual({ scheme: 'dark', palette: ' workspace ' });
    expect(errors).toEqual([]);
  });

  it('posts a half the person did not choose as empty, which takes it off', async () => {
    const { auth, transport } = authOver(kept({ look: { status: 200, body: { scheme: 'light' } } }));
    expect(await auth.changeLook({ scheme: 'light' })).toEqual({ outcome: 'changed', look: { scheme: 'light' } });
    expect(transport.requests[1].body).toEqual({ scheme: 'light', palette: '' });
  });

  it("resolves the look that follows the person's device as the server keeps it", async () => {
    const { auth, transport } = authOver(kept({ look: { status: 200, body: { scheme: 'device' } } }));
    expect(await auth.changeLook({ scheme: 'device' })).toEqual({ outcome: 'changed', look: { scheme: 'device' } });
    expect(transport.requests[1].body).toEqual({ scheme: 'device', palette: '' });
  });

  const answers: { name: string; body: unknown; want: unknown }[] = [
    { name: 'an empty answer is a look of nothing', body: {}, want: {} },
    { name: 'no body is a look of nothing', body: undefined, want: {} },
    {
      name: 'a scheme that is none of the three is not kept',
      body: { scheme: 'sepia', palette: 'blue' },
      want: { palette: 'blue' },
    },
    { name: 'an empty palette is none', body: { scheme: 'dark', palette: '' }, want: { scheme: 'dark' } },
  ];
  for (const tt of answers) {
    it(tt.name, async () => {
      const { auth } = authOver(kept({ look: { status: 200, body: tt.body } }));
      expect(await auth.changeLook({})).toEqual({ outcome: 'changed', look: tt.want });
    });
  }

  const refusals: { name: string; answer: TransportResponse; want: unknown }[] = [
    {
      name: "a scheme that is not light, dark or device resolves with the server's words",
      answer: { status: 400, body: { message: 'a scheme is light, dark or device' } },
      want: { outcome: 'refused', status: 400, message: 'a scheme is light, dark or device' },
    },
    {
      name: 'an impersonated session resolves as refused',
      answer: {
        status: 403,
        body: { message: 'You are acting as someone else, and this session cannot change their account.' },
      },
      want: {
        outcome: 'refused',
        status: 403,
        message: 'You are acting as someone else, and this session cannot change their account.',
      },
    },
  ];
  for (const tt of refusals) {
    it(tt.name, async () => {
      const { auth, errors } = authOver(kept({ look: tt.answer }));
      expect(await auth.changeLook({ scheme: 'dark' })).toEqual(tt.want);
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose list names no such path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.changeLook({ scheme: 'dark' })).rejects.toThrow(/names no keep-your-look path/);
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe('the password change', () => {
  it('reads the list again, then posts the old and the new password to the change path it names', async () => {
    const { auth, transport } = authOver(server());
    const result = await auth.changePassword('old-secret', 'new-secret');
    expect(result).toEqual({ outcome: 'changed' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${changePath}`]);
    expect(transport.requests[1].body).toEqual({ oldPassword: 'old-secret', newPassword: 'new-secret' });
  });

  it("resolves the refusal of a wrong or an unchanged password with the server's words, through no error hook", async () => {
    const { auth, errors } = authOver(
      server({ change: { status: 400, body: { message: 'the new password is the old one' } } }),
    );
    const result = await auth.changePassword('secret', 'secret');
    expect(result).toEqual({ outcome: 'refused', status: 400, message: 'the new password is the old one' });
    expect(errors).toEqual([]);
  });

  it('resolves the refusal of a name at the limit of wrong old passwords with its code and the wait, through no error hook', async () => {
    const { auth, errors } = authOver(
      server({
        change: {
          status: 429,
          body: { message: 'Too many sign-in attempts', code: 'too_many_attempts', retryAfter: 600 },
        },
      }),
    );
    expect(await auth.changePassword('secret', 'another')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_attempts',
      message: 'Too many sign-in attempts',
      retryAfter: 600,
    });
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no change path', async () => {
    const { auth } = authOver(server({ methods: { methods: [google], pending: pendingRoutes } }));
    await expect(auth.changePassword('old', 'new')).rejects.toThrow(/names no password change path/);
  });

  it('keeps the change path off a list that does not name one', async () => {
    const { auth } = authOver(server({ methods: { methods: [google] } }));
    expect(await auth.methods()).toEqual({ methods: [google] });
  });
});

describe('the password strength question', () => {
  const judged = (body: unknown): Server => server({ methods: strengthList, strength: { status: 200, body } });

  it('posts once to the path the list names, reading the list only when none was read, and never again per question', async () => {
    const read = authOver(server({ methods: strengthList }));
    await read.auth.methods();
    await read.auth.passwordStrength('correct horse');
    await read.auth.passwordStrength('correct horse battery');
    expect(sent(read.transport)).toEqual([
      `GET /console/api/${loginMethodsRoute}`,
      `POST ${strengthPath}`,
      `POST ${strengthPath}`,
    ]);
    const unread = authOver(server({ methods: strengthList }));
    await unread.auth.passwordStrength('correct horse');
    await unread.auth.passwordStrength('correct horse battery');
    expect(sent(unread.transport)).toEqual([
      `GET /console/api/${loginMethodsRoute}`,
      `POST ${strengthPath}`,
      `POST ${strengthPath}`,
    ]);
  });

  it('posts the password and only the details that are not empty', async () => {
    const { auth, transport } = authOver(server({ methods: strengthList }));
    await auth.passwordStrength('correct horse', { username: 'pax', name: '', email: 'pax@fleet.example' });
    await auth.passwordStrength('correct horse');
    await auth.passwordStrength('correct horse', { username: 'pax', name: 'Pax Example', email: 'pax@fleet.example' });
    expect(transport.requests[1].body).toEqual({
      password: 'correct horse',
      username: 'pax',
      email: 'pax@fleet.example',
    });
    expect(transport.requests[2].body).toEqual({ password: 'correct horse' });
    expect(transport.requests[3].body).toEqual({
      password: 'correct horse',
      username: 'pax',
      name: 'Pax Example',
      email: 'pax@fleet.example',
    });
  });

  it("resolves the server's judgment as it answers it, with no reason where it gives none and 0 for a number it does not give", async () => {
    const short = authOver(
      judged({ strongEnough: false, strength: 24, minimum: 50, reason: 'Add another word or two.' }),
    ).auth;
    expect(await short.passwordStrength('pax')).toEqual({
      outcome: 'judged',
      strongEnough: false,
      strength: 24,
      minimum: 50,
      reason: 'Add another word or two.',
    });
    const enough = await authOver(
      judged({ strongEnough: true, strength: 62, minimum: 50, reason: '' }),
    ).auth.passwordStrength('correct horse battery');
    expect(enough).toEqual({ outcome: 'judged', strongEnough: true, strength: 62, minimum: 50 });
    expect(Object.keys(enough)).not.toContain('reason');
    const unsaid = authOver(judged({ strongEnough: 'yes', strength: '62', minimum: null })).auth;
    expect(await unsaid.passwordStrength('correct horse')).toEqual({
      outcome: 'judged',
      strongEnough: false,
      strength: 0,
      minimum: 0,
    });
  });

  it('resolves a browser that asked too often as the refusal with its code and the wait, through no error hook', async () => {
    const { auth, errors } = authOver(
      server({
        methods: strengthList,
        strength: {
          status: 429,
          body: {
            message: 'Too many passwords have been checked from here',
            code: 'too_many_attempts',
            retryAfter: 300,
          },
        },
      }),
    );
    expect(await auth.passwordStrength('correct horse')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_attempts',
      message: 'Too many passwords have been checked from here',
      retryAfter: 300,
    });
    expect(errors).toEqual([]);
  });

  it('asks quietly: an answer that is an error is thrown and handed to no error hook', async () => {
    const { auth, errors } = authOver(
      server({ methods: strengthList, strength: { status: 500, body: { message: 'the server failed' } } }),
    );
    await expect(auth.passwordStrength('correct horse')).rejects.toBeInstanceOf(ApiError);
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no strength path, and posts nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.passwordStrength('correct horse')).rejects.toThrow(
      'this server names no password strength path in its methods list: it offers no password method',
    );
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });

  it('keeps the strength path on a list that names one, and none on a list that does not', async () => {
    expect(await authOver(server({ methods: strengthList })).auth.methods()).toEqual(strengthList);
    const unnamed = await authOver(server({ methods: { ...consoleList, passwordStrength: '' } })).auth.methods();
    expect(unnamed).toEqual(consoleList);
    expect(Object.keys(unnamed)).not.toContain('passwordStrength');
  });
});

describe("choosing the person's first password", () => {
  const routes = { sendCode: '/console/api/user/first-password/code', set: '/console/api/user/first-password' };
  const choosing = (overrides: Partial<Server> = {}): Server =>
    server({ methods: { ...consoleList, firstPassword: routes }, ...overrides });

  it('keeps the first password paths the list names, and none on a list that names one alone', async () => {
    expect(await authOver(choosing()).auth.methods()).toEqual({ ...consoleList, firstPassword: routes });
    expect(
      await authOver(server({ methods: { ...consoleList, firstPassword: { set: routes.set } } })).auth.methods(),
    ).toEqual(consoleList);
  });

  it('reads the list again, then asks for the code at the path it names, sending no body', async () => {
    const { auth, transport, errors } = authOver(choosing());
    expect(await auth.sendFirstPasswordCode()).toEqual({
      outcome: 'sent',
      codeEmail: 'p•••@fleet.example',
      codeExpiresAt: '2026-11-17T12:10:00Z',
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${routes.sendCode}`]);
    expect(transport.requests[1].body).toBeUndefined();
    expect(errors).toEqual([]);
  });

  it('reads the list again, then posts the code and the password to the path it names', async () => {
    const { auth, transport, errors } = authOver(choosing());
    expect(await auth.setFirstPassword('483920', 'a harbor of my own')).toEqual({ outcome: 'set' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${routes.set}`]);
    expect(transport.requests[1].body).toEqual({ code: '483920', password: 'a harbor of my own' });
    expect(errors).toEqual([]);
  });

  for (const tt of [
    { name: 'an account that already has a password', status: 409, message: 'Your account already has a password.' },
    {
      name: 'an account with no confirmed address',
      status: 409,
      message: 'Your account has no confirmed email address',
    },
    { name: 'a session that acts for someone else', status: 403, message: 'not while impersonating' },
  ]) {
    it(`resolves the refusal of the code for ${tt.name} with the server's words, through no error hook`, async () => {
      const { auth, errors } = authOver(
        choosing({ firstPasswordCode: { status: tt.status, body: { message: tt.message } } }),
      );
      expect(await auth.sendFirstPasswordCode()).toEqual({
        outcome: 'refused',
        status: tt.status,
        message: tt.message,
      });
      expect(errors).toEqual([]);
    });
  }

  it('resolves the refusal of a mailbox sent too many codes with its code and the wait, through no error hook', async () => {
    const { auth, errors } = authOver(
      choosing({
        firstPasswordCode: {
          status: 429,
          body: { message: 'Too many emails to this address', code: 'too_many_emails', retryAfter: 900 },
        },
      }),
    );
    expect(await auth.sendFirstPasswordCode()).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_emails',
      message: 'Too many emails to this address',
      retryAfter: 900,
    });
    expect(errors).toEqual([]);
  });

  for (const tt of [
    { name: 'a code that is not the one sent', status: 400, message: 'That is not the code we sent.' },
    { name: 'a code that expired', status: 409, message: 'That code has expired or was already used.' },
    { name: 'a session that acts for someone else', status: 403, message: 'not while impersonating' },
  ]) {
    it(`resolves the refusal of the password for ${tt.name} with the server's words, through no error hook`, async () => {
      const { auth, errors } = authOver(
        choosing({ firstPasswordSet: { status: tt.status, body: { message: tt.message } } }),
      );
      expect(await auth.setFirstPassword('000000', 'a harbor of my own')).toEqual({
        outcome: 'refused',
        status: tt.status,
        message: tt.message,
      });
      expect(errors).toEqual([]);
    });
  }

  it('resolves the refusal after too many wrong codes with its code and the wait, through no error hook', async () => {
    const { auth, errors } = authOver(
      choosing({
        firstPasswordSet: {
          status: 429,
          body: { message: 'Too many wrong codes were entered', code: 'too_many_attempts', retryAfter: 600 },
        },
      }),
    );
    expect(await auth.setFirstPassword('000000', 'a harbor of my own')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_attempts',
      message: 'Too many wrong codes were entered',
      retryAfter: 600,
    });
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no first password paths, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.sendFirstPasswordCode()).rejects.toThrow(/names no first password paths/);
    await expect(auth.setFirstPassword('483920', 'a harbor of my own')).rejects.toThrow(
      /names no first password paths/,
    );
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe("removing the person's own password", () => {
  const removePath = '/console/api/user/password/remove';
  const removing = (overrides: Partial<Server> = {}): Server =>
    server({ methods: { ...consoleList, removePassword: removePath }, ...overrides });

  it('keeps the removal path the list names, and none on a list that names none', async () => {
    expect(await authOver(removing()).auth.methods()).toEqual({ ...consoleList, removePassword: removePath });
    expect(await authOver(server({ methods: { ...consoleList, removePassword: '' } })).auth.methods()).toEqual(
      consoleList,
    );
  });

  it('reads the list again, then posts the password as proof to the removal path it names', async () => {
    const { auth, transport, errors } = authOver(removing());
    expect(await auth.removePassword('old-secret')).toEqual({ outcome: 'removed' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${removePath}`]);
    expect(transport.requests[1].body).toEqual({ password: 'old-secret' });
    expect(errors).toEqual([]);
  });

  for (const tt of [
    { name: 'a password that does not match', status: 400, message: 'Password incorrect' },
    {
      name: 'a person no directory sign-in is linked to',
      status: 409,
      message: 'Your password is your only way to sign in',
    },
    { name: 'a session that acts for someone else', status: 403, message: 'not while impersonating' },
  ]) {
    it(`resolves the refusal of ${tt.name} with the server's words, through no error hook`, async () => {
      const { auth, errors } = authOver(removing({ remove: { status: tt.status, body: { message: tt.message } } }));
      expect(await auth.removePassword('secret')).toEqual({
        outcome: 'refused',
        status: tt.status,
        message: tt.message,
      });
      expect(errors).toEqual([]);
    });
  }

  it('resolves the refusal of a name at the limit of wrong passwords with its code and the wait, through no error hook', async () => {
    const { auth, errors } = authOver(
      removing({
        remove: {
          status: 429,
          body: { message: 'Too many sign-in attempts', code: 'too_many_attempts', retryAfter: 600 },
        },
      }),
    );
    expect(await auth.removePassword('secret')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_attempts',
      message: 'Too many sign-in attempts',
      retryAfter: 600,
    });
    expect(errors).toEqual([]);
  });

  it('throws on a server whose list names no removal path, sending nothing', async () => {
    const { auth, transport } = authOver(server());
    await expect(auth.removePassword('secret')).rejects.toThrow(/names no remove-your-password path/);
    expect(sent(transport).filter((request) => request.startsWith('POST'))).toEqual([]);
  });
});

describe('reading what the person signs in with', () => {
  const meansPath = '/console/api/user/sign-in-means';
  const reading = (overrides: Partial<Server> = {}): Server =>
    server({ methods: { ...consoleList, signInMeans: meansPath }, ...overrides });

  it('keeps the path the list names, and none on a list that names none', async () => {
    expect(await authOver(reading()).auth.methods()).toEqual({ ...consoleList, signInMeans: meansPath });
    expect(await authOver(server({ methods: { ...consoleList, signInMeans: '' } })).auth.methods()).toEqual(
      consoleList,
    );
  });

  it('reads the list, then the path it names, and answers the password and the directories by label', async () => {
    const { auth, transport, errors } = authOver(reading());
    expect(await auth.signInMeans()).toEqual({
      password: true,
      directories: [{ name: 'google', label: 'Fleet Google' }],
      directoryAddress: false,
      passwordDisabled: false,
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `GET ${meansPath}`]);
    expect(errors).toEqual([]);
  });

  it('answers a person with no password and no directory as the server says it', async () => {
    const { auth } = authOver(reading({ means: { status: 200, body: { password: false, directories: [] } } }));
    expect(await auth.signInMeans()).toEqual({
      password: false,
      directories: [],
      directoryAddress: false,
      passwordDisabled: false,
    });
  });

  it("answers whether the person's address is an organization's directory's, true only where the server says true", async () => {
    const directory = authOver(
      reading({ means: { status: 200, body: { password: false, directories: [], directoryAddress: true } } }),
    ).auth;
    expect((await directory.signInMeans())?.directoryAddress).toBe(true);
    const unsaid = authOver(
      reading({ means: { status: 200, body: { password: false, directories: [], directoryAddress: 'yes' } } }),
    ).auth;
    expect((await unsaid.signInMeans())?.directoryAddress).toBe(false);
  });

  it("answers whether the person's password is disabled, true only where the server says true", async () => {
    const disabled = authOver(
      reading({ means: { status: 200, body: { password: true, directories: [], passwordDisabled: true } } }),
    ).auth;
    expect((await disabled.signInMeans())?.passwordDisabled).toBe(true);
    const unsaid = authOver(reading({ means: { status: 200, body: { password: true, directories: [] } } })).auth;
    expect((await unsaid.signInMeans())?.passwordDisabled).toBe(false);
    const notBoolean = authOver(
      reading({ means: { status: 200, body: { password: true, directories: [], passwordDisabled: 'yes' } } }),
    ).auth;
    expect((await notBoolean.signInMeans())?.passwordDisabled).toBe(false);
  });

  it('answers nothing on a server whose list names no path, asking for nothing more', async () => {
    const { auth, transport } = authOver(server());
    expect(await auth.signInMeans()).toBeUndefined();
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`]);
  });

  for (const tt of [
    { name: 'nothing', body: undefined },
    { name: 'no word on the password', body: { directories: [] } },
    { name: 'no list of directories', body: { password: true } },
  ]) {
    it(`answers nothing where the server answers ${tt.name}, so a page takes no fact from it`, async () => {
      const { auth } = authOver(reading({ means: { status: 200, body: tt.body } }));
      expect(await auth.signInMeans()).toBeUndefined();
    });
  }

  it('leaves out a directory the server names without a label', async () => {
    const body = {
      password: true,
      directories: [{ name: 'google', label: '' }, { name: 'entra', label: 'Lakeside' }, null],
    };
    const { auth } = authOver(reading({ means: { status: 200, body } }));
    expect(await auth.signInMeans()).toEqual({
      password: true,
      directories: [{ name: 'entra', label: 'Lakeside' }],
      directoryAddress: false,
      passwordDisabled: false,
    });
  });
});

describe('the mailed links in the list', () => {
  it('are kept as the server names them: the username flag, the invitation, email and sign-up routes', async () => {
    const { auth } = authOver(server({ methods: mailingList }));
    expect(await auth.methods()).toEqual(mailingList);
  });

  it('keeps the route that confirms an address an administrator set, and the password reset routes', async () => {
    const { auth } = authOver(server({ methods: resettingList }));
    expect(await auth.methods()).toEqual(resettingList);
  });

  it('keeps the reset routes of a server whose people do not reset their own password, without the request route', async () => {
    const administratorsOnly = { open: resetRoutes.open, complete: resetRoutes.complete };
    const { auth } = authOver(server({ methods: { ...mailingList, passwordReset: administratorsOnly } }));
    expect((await auth.methods()).passwordReset).toEqual(administratorsOnly);
  });

  it('leaves off reset routes named in part: a link the page could open and not complete is no reset', async () => {
    const { auth } = authOver(
      server({ methods: { ...mailingList, passwordReset: { request: resetRoutes.request, open: resetRoutes.open } } }),
    );
    expect(await auth.methods()).toEqual(mailingList);
  });

  it('are left off a list that names none, or names a block in part', async () => {
    const { auth } = authOver(
      server({
        methods: {
          methods: [password],
          changePassword: changePath,
          usernameIsEmail: false,
          invitation: { open: invitationRoutes.open },
          email: { add: emailRoutes.add },
          signUp: { start: signUpRoutes.start, open: signUpRoutes.open },
        },
      }),
    );
    expect(await auth.methods()).toEqual({ methods: [password], changePassword: changePath });
  });

  it('keeps the invitation the browser carries, as the block names it under standing, and names none where the block has none', async () => {
    const standing = {
      email: 'ensign@fleet.example',
      name: 'Nova Ensign',
      tenantId: '5d1c8e2a-7b3f-4a9e-b6d0-2c4f8a1e9b37',
      tenantName: 'Anvil',
      expiresAt: '2026-10-30T12:00:00Z',
      usernameIsEmail: true,
      methods: ['google'],
      accountExists: true,
      hasPassword: false,
    };
    const carrying = authOver(
      server({ methods: { ...mailingList, invitation: { ...invitationRoutes, standing } } }),
    ).auth;
    expect(await carrying.methods()).toEqual({ ...mailingList, invitation: { ...invitationRoutes, standing } });
    const partial = authOver(
      server({
        methods: { ...mailingList, invitation: { ...invitationRoutes, standing: { email: 'ensign@fleet.example' } } },
      }),
    ).auth;
    expect((await partial.methods()).invitation?.standing).toEqual({
      email: 'ensign@fleet.example',
      name: '',
      tenantId: '',
      tenantName: '',
      expiresAt: '',
      usernameIsEmail: false,
      methods: [],
      accountExists: false,
      hasPassword: false,
    });
    const none = authOver(
      server({ methods: { ...mailingList, invitation: { ...invitationRoutes, standing: null } } }),
    ).auth;
    expect(await none.methods()).toEqual(mailingList);
  });

  it('keeps what the standing block says of the invited account, and reads a fact it does not say as false', async () => {
    const plain = {
      email: 'ensign@fleet.example',
      name: 'Nova Ensign',
      tenantId: '5d1c8e2a-7b3f-4a9e-b6d0-2c4f8a1e9b37',
      tenantName: 'Anvil',
      expiresAt: '2026-10-30T12:00:00Z',
      usernameIsEmail: true,
      methods: ['google'],
    };
    const standing = { ...plain, accountExists: true, hasPassword: true };
    const carrying = authOver(
      server({ methods: { ...mailingList, invitation: { ...invitationRoutes, standing } } }),
    ).auth;
    expect((await carrying.methods()).invitation?.standing).toEqual(standing);
    const unsaid = authOver(
      server({
        methods: {
          ...mailingList,
          invitation: { ...invitationRoutes, standing: { ...standing, accountExists: 'yes' } },
        },
      }),
    ).auth;
    // The password is read only beside an account.
    expect((await unsaid.methods()).invitation?.standing).toEqual({
      ...plain,
      accountExists: false,
      hasPassword: false,
    });
  });

  it('keeps the registration names the standing invitation says could admit the address, strings only, and none where it names none', async () => {
    const standing = {
      email: 'ensign@fleet.example',
      name: '',
      tenantId: '',
      tenantName: '',
      expiresAt: 'soon',
      usernameIsEmail: false,
      accountExists: false,
      hasPassword: false,
    };
    const named = authOver(
      server({
        methods: {
          ...mailingList,
          invitation: { ...invitationRoutes, standing: { ...standing, methods: ['google-one', 7] } },
        },
      }),
    ).auth;
    expect((await named.methods()).invitation?.standing).toEqual({ ...standing, methods: ['google-one'] });
    const unnamed = authOver(
      server({
        methods: {
          ...mailingList,
          invitation: { ...invitationRoutes, standing: { ...standing, methods: 'google-one' } },
        },
      }),
    ).auth;
    expect((await unnamed.methods()).invitation?.standing).toEqual({ ...standing, methods: [] });
  });

  it('keeps what the standing block says of the tenant the invitation makes, and nothing for any other invitation', async () => {
    const standing = {
      email: 'founder@dawn.example',
      name: 'Dawn Founder',
      tenantId: '',
      tenantName: 'Dawn',
      expiresAt: '2026-10-30T12:00:00Z',
      usernameIsEmail: true,
      methods: ['google'],
      accountExists: false,
      hasPassword: false,
    };
    const making = authOver(
      server({
        methods: {
          ...mailingList,
          invitation: { ...invitationRoutes, standing: { ...standing, newTenant: true, directoryOnly: true } },
        },
      }),
    ).auth;
    expect((await making.methods()).invitation?.standing).toEqual({
      ...standing,
      newTenant: true,
      directoryOnly: true,
    });
    const into = authOver(
      server({
        methods: {
          ...mailingList,
          invitation: { ...invitationRoutes, standing: { ...standing, newTenant: false, directoryOnly: false } },
        },
      }),
    ).auth;
    const kept = (await into.methods()).invitation?.standing;
    expect(kept).toEqual(standing);
    expect(Object.keys(kept ?? {})).not.toContain('newTenant');
  });

  it("keeps an invitation block without its sign-up route, a server without a password method's", async () => {
    const { auth } = authOver(
      server({
        methods: { methods: [google], invitation: { open: invitationRoutes.open, accept: invitationRoutes.accept } },
      }),
    );
    expect(await auth.methods()).toEqual({
      methods: [google],
      invitation: { open: invitationRoutes.open, accept: invitationRoutes.accept },
    });
  });
});

describe('an invitation', () => {
  it('carries what the server says of the invited account, and reads a fact it does not say as false', async () => {
    const known = authOver(
      server({
        methods: mailingList,
        invitationOpen: { status: 200, body: { ...opened, accountExists: true, hasPassword: false } },
      }),
    ).auth;
    expect(await known.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      ...opened,
      accountExists: true,
      hasPassword: false,
    });
    // hasPassword is read only beside accountExists, and is false unless the server said true.
    const partial = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...opened, accountExists: false } } }),
    ).auth;
    expect(await partial.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      ...opened,
      accountExists: false,
      hasPassword: false,
    });
    const unsaid = authOver(
      server({
        methods: mailingList,
        invitationOpen: {
          status: 200,
          body: { email: opened.email, expiresAt: opened.expiresAt, hasPassword: true },
        },
      }),
    ).auth;
    // The password is read only beside an account.
    expect(await unsaid.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      email: opened.email,
      name: '',
      tenantId: '',
      tenantName: '',
      expiresAt: opened.expiresAt,
      signUp: false,
      usernameIsEmail: false,
      methods: [],
      accountExists: false,
      hasPassword: false,
    });
  });

  it('carries the name the inviter gave, opened or carried by the browser, and an empty name where the answer has none', async () => {
    const named = await authOver(server({ methods: mailingList })).auth.openInvitation('tok-1');
    expect(named.outcome === 'opened' && named.name).toBe('Nova Ensign');
    const unnamed = { ...opened, name: undefined };
    const nameless = await authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: unnamed } }),
    ).auth.openInvitation('tok-1');
    expect(nameless.outcome === 'opened' && nameless.name).toBe('');
    const carrying = authOver(
      server({ methods: { ...mailingList, invitation: { ...invitationRoutes, standing: opened } } }),
    ).auth;
    expect((await carrying.methods()).invitation?.standing?.name).toBe('Nova Ensign');
    const carryingNameless = authOver(
      server({ methods: { ...mailingList, invitation: { ...invitationRoutes, standing: unnamed } } }),
    ).auth;
    expect((await carryingNameless.methods()).invitation?.standing?.name).toBe('');
  });

  it('is opened after the list is read again, posting the token, and described as the server answers', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    const result = await auth.openInvitation('tok-1');
    expect(result).toEqual({ outcome: 'opened', ...opened });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${invitationRoutes.open}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-1' });
  });

  it('reads an answer without a tenant as the global surface, and without the flags as neither', async () => {
    const { auth } = authOver(
      server({
        methods: mailingList,
        invitationOpen: { status: 200, body: { email: 'x@fleet.example', expiresAt: 'soon' } },
      }),
    );
    expect(await auth.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      email: 'x@fleet.example',
      name: '',
      tenantId: '',
      tenantName: '',
      expiresAt: 'soon',
      signUp: false,
      usernameIsEmail: false,
      methods: [],
      accountExists: false,
      hasPassword: false,
    });
  });

  it('carries that the invitation makes a tenant and whether that tenant is entered through its directory only, and neither for any other invitation', async () => {
    const making = { ...opened, tenantId: '', tenantName: 'Dawn', signUp: false };
    const closed = authOver(
      server({
        methods: mailingList,
        invitationOpen: { status: 200, body: { ...making, newTenant: true, directoryOnly: true } },
      }),
    ).auth;
    expect(await closed.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      ...making,
      newTenant: true,
      directoryOnly: true,
    });
    // The switch is false unless the server said true.
    const open = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...making, newTenant: true } } }),
    ).auth;
    expect(await open.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      ...making,
      newTenant: true,
      directoryOnly: false,
    });
    // An invitation into a tenant that exists says neither, whatever else the answer carries.
    const into = authOver(
      server({
        methods: mailingList,
        invitationOpen: { status: 200, body: { ...opened, newTenant: false, directoryOnly: true } },
      }),
    ).auth;
    const result = await into.openInvitation('tok-1');
    expect(result).toEqual({ outcome: 'opened', ...opened });
    expect(Object.keys(result)).not.toContain('newTenant');
    expect(Object.keys(result)).not.toContain('directoryOnly');
  });

  it('carries the registration names the server says could admit the address, strings only, and none where the answer names none', async () => {
    const named = authOver(
      server({
        methods: mailingList,
        invitationOpen: { status: 200, body: { ...opened, methods: ['google-one', 7, null] } },
      }),
    ).auth;
    expect(await named.openInvitation('tok-1')).toEqual({ outcome: 'opened', ...opened, methods: ['google-one'] });
    const empty = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...opened, methods: [] } } }),
    ).auth;
    expect(await empty.openInvitation('tok-1')).toEqual({ outcome: 'opened', ...opened, methods: [] });
    const unnamed = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...opened, methods: 'google-one' } } }),
    ).auth;
    expect(await unnamed.openInvitation('tok-1')).toEqual({ outcome: 'opened', ...opened, methods: [] });
  });

  it("resolves a dead link as the server's 404 refusal, through no error hook", async () => {
    const { auth, errors } = authOver(server({ methods: mailingList, invitationOpen: dead }));
    expect(await auth.openInvitation('tok-dead')).toEqual({
      outcome: 'refused',
      status: 404,
      message: dead.body.message,
    });
    expect(errors).toEqual([]);
  });

  it('is accepted by the signed-in person after the list is read again', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.acceptInvitation()).toEqual({ outcome: 'accepted' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${invitationRoutes.accept}`]);
  });

  it('resolves the refusal of an acceptance with nothing to accept', async () => {
    const { auth } = authOver(
      server({
        methods: mailingList,
        invitationAccept: { status: 404, body: { message: 'the browser carries no live invitation' } },
      }),
    );
    expect(await auth.acceptInvitation()).toEqual({
      outcome: 'refused',
      status: 404,
      message: 'the browser carries no live invitation',
    });
  });

  it('asks for an account with a password after the list is read again, the username sent only where given, and resolves the step that waits for the code', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.signUpFromInvitation({ password: 'orbit-and-anchor' })).toEqual({ outcome: 'pending' });
    expect(await auth.signUpFromInvitation({ password: 'orbit-and-anchor', username: 'ensign' })).toEqual({
      outcome: 'pending',
    });
    expect(sent(transport)).toEqual([
      `GET /console/api/${loginMethodsRoute}`,
      `POST ${invitationRoutes.signUp}`,
      `GET /console/api/${loginMethodsRoute}`,
      `POST ${invitationRoutes.signUp}`,
    ]);
    expect(transport.requests[1].body).toEqual({ password: 'orbit-and-anchor' });
    expect(transport.requests[3].body).toEqual({ password: 'orbit-and-anchor', username: 'ensign' });
  });

  const accountRefusals: { name: string; answer: TransportResponse }[] = [
    { name: 'a taken username or address (409)', answer: { status: 409, body: { message: 'the username is taken' } } },
    {
      name: 'a password too easy to guess (400)',
      answer: {
        status: 400,
        body: {
          message: 'This password is too easy to guess. Make it longer, or use several words that do not go together.',
        },
      },
    },
    {
      name: 'no live invitation (404)',
      answer: { status: 404, body: { message: 'the browser carries no live invitation' } },
    },
    {
      name: "the sign-in policy's refusal (403 with its code)",
      answer: { status: 403, body: { message: 'Sign-in denied', code: 'no_roles' } },
    },
  ];
  for (const tt of accountRefusals) {
    it(`resolves ${tt.name} as the refusal, through no error hook`, async () => {
      const { auth, errors } = authOver(server({ methods: mailingList, invitationSignUp: tt.answer }));
      const body = tt.answer.body as { message: string; code?: string };
      expect(await auth.signUpFromInvitation({ password: 'x' })).toEqual({
        outcome: 'refused',
        status: tt.answer.status,
        ...(body.code ? { code: body.code } : {}),
        message: body.message,
      });
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose list names no invitation routes, or no sign-up route for them', async () => {
    const { auth } = authOver(server());
    await expect(auth.openInvitation('tok-1')).rejects.toThrow(/names no invitation routes/);
    await expect(auth.acceptInvitation()).rejects.toThrow(/names no invitation routes/);
    const directory = authOver(
      server({
        methods: { methods: [google], invitation: { open: invitationRoutes.open, accept: invitationRoutes.accept } },
      }),
    );
    await expect(directory.auth.signUpFromInvitation({ password: 'x' })).rejects.toThrow(
      /no sign-up route for invitations/,
    );
  });
});

describe('the email address', () => {
  it('is added after the list is read again, posting the address, and answers sent whatever its state', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.addEmail('pax@fleet.example')).toEqual({ outcome: 'sent' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${emailRoutes.add}`]);
    expect(transport.requests[1].body).toEqual({ email: 'pax@fleet.example' });
  });

  it('resolves the refusals of an add: not an address, and a mailbox sent too many links, with the wait', async () => {
    const notOne = authOver(
      server({ methods: mailingList, emailAdd: { status: 400, body: { message: 'not an email address' } } }),
    );
    expect(await notOne.auth.addEmail('pax')).toEqual({
      outcome: 'refused',
      status: 400,
      message: 'not an email address',
    });
    const tooMany = authOver(
      server({
        methods: mailingList,
        emailAdd: { status: 429, body: { message: 'Too many links', code: 'too_many_emails', retryAfter: 600 } },
      }),
    );
    expect(await tooMany.auth.addEmail('pax@fleet.example')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_emails',
      message: 'Too many links',
      retryAfter: 600,
    });
    expect(tooMany.errors).toEqual([]);
  });

  it('is verified after the list is read again, posting the token, and answers the address', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.verifyEmail('tok-v')).toEqual({ outcome: 'verified', email: 'pax@fleet.example' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${emailRoutes.verify}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-v' });
  });

  it('resolves a dead link and a taken address as refusals', async () => {
    const gone = authOver(server({ methods: mailingList, emailVerify: dead }));
    expect(await gone.auth.verifyEmail('tok-v')).toEqual({
      outcome: 'refused',
      status: 404,
      message: dead.body.message,
    });
    const taken = authOver(
      server({ methods: mailingList, emailVerify: { status: 409, body: { message: 'the address is taken' } } }),
    );
    expect(await taken.auth.verifyEmail('tok-v')).toEqual({
      outcome: 'refused',
      status: 409,
      message: 'the address is taken',
    });
  });

  it('throws on a server whose list names no email routes', async () => {
    const { auth } = authOver(server());
    await expect(auth.addEmail('pax@fleet.example')).rejects.toThrow(/names no email address routes/);
    await expect(auth.verifyEmail('tok-v')).rejects.toThrow(/names no email address routes/);
  });

  it('is confirmed, where an administrator set it, after the list is read again, posting the token', async () => {
    const { auth, transport } = authOver(server({ methods: resettingList }));
    expect(await auth.confirmEmail('tok-c')).toEqual({ outcome: 'confirmed', email: 'pax@fleet.example' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${emailConfirmRoute}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-c' });
  });

  it('resolves a dead confirmation link and an address taken meanwhile as refusals', async () => {
    const gone = authOver(server({ methods: resettingList, emailConfirm: dead }));
    expect(await gone.auth.confirmEmail('tok-c')).toEqual({
      outcome: 'refused',
      status: 404,
      message: dead.body.message,
    });
    const taken = authOver(
      server({ methods: resettingList, emailConfirm: { status: 409, body: { message: 'the address is taken' } } }),
    );
    expect(await taken.auth.confirmEmail('tok-c')).toEqual({
      outcome: 'refused',
      status: 409,
      message: 'the address is taken',
    });
    expect(taken.errors).toEqual([]);
  });

  it('throws on a server whose list names no route that confirms an address', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    await expect(auth.confirmEmail('tok-c')).rejects.toThrow(/names no route that confirms an address/);
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`]);
  });
});

describe('a password reset', () => {
  it('is asked for after the list is read again, posting the address, and answers sent whatever its state', async () => {
    const { auth, transport } = authOver(server({ methods: resettingList }));
    expect(await auth.requestPasswordReset('pax@fleet.example')).toEqual({ outcome: 'sent' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${resetRoutes.request}`]);
    expect(transport.requests[1].body).toEqual({ email: 'pax@fleet.example' });
  });

  it('resolves the refusals of a request: turned off since the list was read, and a mailbox sent too many links, with the wait', async () => {
    const off = authOver(
      server({ methods: resettingList, resetRequest: { status: 404, body: { message: SELF_SERVICE_RESET_OFF } } }),
    );
    expect(await off.auth.requestPasswordReset('pax@fleet.example')).toEqual({
      outcome: 'refused',
      status: 404,
      message: SELF_SERVICE_RESET_OFF,
    });
    const tooMany = authOver(
      server({
        methods: resettingList,
        resetRequest: { status: 429, body: { message: 'Too many links', code: 'too_many_emails', retryAfter: 600 } },
      }),
    );
    expect(await tooMany.auth.requestPasswordReset('pax@fleet.example')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_emails',
      message: 'Too many links',
      retryAfter: 600,
    });
    expect(tooMany.errors).toEqual([]);
  });

  it('posts nothing where the list names no request route, and throws the sentence the server refuses such a request with', async () => {
    const { auth, transport } = authOver(
      server({
        methods: { ...mailingList, passwordReset: { open: resetRoutes.open, complete: resetRoutes.complete } },
      }),
    );
    await expect(auth.requestPasswordReset('pax@fleet.example')).rejects.toThrow(SELF_SERVICE_RESET_OFF);
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`]);
    expect(SELF_SERVICE_RESET_OFF).toBe(
      'Resetting your own password is turned off here. Ask an administrator to send you a reset link',
    );
  });

  it("is opened after the list is read again, posting the token, and answers the username and the account's name and address", async () => {
    const { auth, transport } = authOver(server({ methods: resettingList }));
    expect(await auth.openPasswordReset('tok-r')).toEqual({
      outcome: 'opened',
      username: 'pax',
      name: 'Pax Example',
      email: 'pax@fleet.example',
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${resetRoutes.open}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-r' });
  });

  it('reads a name or an address the opened link does not carry as empty', async () => {
    const { auth } = authOver(
      server({ methods: resettingList, resetOpen: { status: 200, body: { username: 'pax' } } }),
    );
    expect(await auth.openPasswordReset('tok-r')).toEqual({ outcome: 'opened', username: 'pax', name: '', email: '' });
  });

  it('resolves a dead link as a refusal when opened', async () => {
    const { auth, errors } = authOver(server({ methods: resettingList, resetOpen: dead }));
    expect(await auth.openPasswordReset('tok-r')).toEqual({
      outcome: 'refused',
      status: 404,
      message: dead.body.message,
    });
    expect(errors).toEqual([]);
  });

  it('is completed after the list is read again, posting the token and the new password', async () => {
    const { auth, transport } = authOver(server({ methods: resettingList }));
    expect(await auth.completePasswordReset('tok-r', 'a new password')).toEqual({ outcome: 'reset', username: 'pax' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${resetRoutes.complete}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-r', password: 'a new password' });
  });

  for (const tt of [
    {
      name: 'a password too easy to guess',
      answer: {
        status: 400,
        body: {
          message: 'This password is too easy to guess. Make it longer, or use several words that do not go together.',
        },
      },
    },
    { name: 'a dead link', answer: dead },
    {
      name: 'a password that changed while the reader was choosing',
      answer: { status: 409, body: { message: 'Your password was changed while you were choosing a new one' } },
    },
  ]) {
    it(`resolves ${tt.name} as a refusal of the completion, with the server's sentence, and reports no error`, async () => {
      const { auth, errors } = authOver(server({ methods: resettingList, resetComplete: tt.answer }));
      expect(await auth.completePasswordReset('tok-r', 'a new password')).toEqual({
        outcome: 'refused',
        status: tt.answer.status,
        message: tt.answer.body.message,
      });
      expect(errors).toEqual([]);
    });
  }

  it('throws on a server whose list names no password reset routes', async () => {
    const { auth } = authOver(server({ methods: mailingList }));
    await expect(auth.requestPasswordReset('pax@fleet.example')).rejects.toThrow(/names no password reset routes/);
    await expect(auth.openPasswordReset('tok-r')).rejects.toThrow(/names no password reset routes/);
    await expect(auth.completePasswordReset('tok-r', 'a new password')).rejects.toThrow(
      /names no password reset routes/,
    );
  });
});

describe('a sign-up', () => {
  it('is started after the list is read again, posting the address, and answers sent whatever its state', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.startSignUp('rookie@fleet.example')).toEqual({ outcome: 'sent' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${signUpRoutes.start}`]);
    expect(transport.requests[1].body).toEqual({ email: 'rookie@fleet.example' });
  });

  it('is opened after the list is read again, posting the token, and described as the server answers', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.openSignUp('tok-s')).toEqual({
      outcome: 'opened',
      email: 'rookie@fleet.example',
      usernameIsEmail: true,
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${signUpRoutes.open}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-s' });
    const gone = authOver(server({ methods: mailingList, signUpOpen: dead }));
    expect(await gone.auth.openSignUp('tok-s')).toEqual({
      outcome: 'refused',
      status: 404,
      message: dead.body.message,
    });
  });

  it('is completed after the list is read again, posting the token and the account, and resolves the sign-in', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.completeSignUp('tok-s', { password: 'orbit-and-anchor', username: 'rookie' })).toEqual({
      outcome: 'session',
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${signUpRoutes.complete}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-s', password: 'orbit-and-anchor', username: 'rookie' });
  });

  it('resolves a pending step the completion answers, and a taken address as the refusal', async () => {
    const pending = authOver(
      server({
        methods: mailingList,
        signUpComplete: { status: 200, body: { mfaIsRequired: true, redirectUrl: '/console/mfa' } },
      }),
    );
    expect(await pending.auth.completeSignUp('tok-s', { password: 'x' })).toEqual({
      outcome: 'pending',
      redirectUrl: '/console/mfa',
    });
    const taken = authOver(
      server({
        methods: mailingList,
        signUpComplete: { status: 409, body: { message: 'an account holds the address' } },
      }),
    );
    expect(await taken.auth.completeSignUp('tok-s', { password: 'x' })).toEqual({
      outcome: 'refused',
      status: 409,
      message: 'an account holds the address',
    });
  });

  it('throws on a server whose list names no sign-up routes', async () => {
    const { auth } = authOver(server());
    await expect(auth.startSignUp('rookie@fleet.example')).rejects.toThrow(/names no sign-up routes/);
    await expect(auth.openSignUp('tok-s')).rejects.toThrow(/names no sign-up routes/);
    await expect(auth.completeSignUp('tok-s', { password: 'x' })).rejects.toThrow(/names no sign-up routes/);
  });
});

describe('refusedForPasswordChange', () => {
  const cases: { name: string; status: number; body: unknown; want: boolean }[] = [
    {
      name: 'a 403 carrying the code is the forced change',
      status: 403,
      body: { message: 'Password change required', code: passwordChangeRequiredCode },
      want: true,
    },
    {
      name: 'a 403 with another code is an ordinary refusal',
      status: 403,
      body: { message: 'Not your mission', code: 'forbidden' },
      want: false,
    },
    { name: 'a 403 with no body is an ordinary refusal', status: 403, body: undefined, want: false },
    { name: 'a 403 whose body is a string is an ordinary refusal', status: 403, body: 'forbidden', want: false },
    {
      name: 'the code on another status is not the forced change',
      status: 401,
      body: { code: passwordChangeRequiredCode },
      want: false,
    },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(refusedForPasswordChange({ status: tt.status, body: tt.body })).toBe(tt.want);
    });
  }
});
