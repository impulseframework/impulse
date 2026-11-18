import { describe, expect, it } from 'bun:test';
import {
  AuthClient,
  LoginMethod,
  LoginMethods,
  loginMethodsRoute,
  passwordChangeRequiredCode,
  refusedForPasswordChange,
  returnPath,
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
const invitationRoutes = {
  open: '/console/api/user/invitation',
  accept: '/console/api/user/invitation/accept',
  signUp: '/console/api/user/invitation/sign-up',
};
const emailRoutes = { add: '/console/api/user/email', verify: '/console/api/user/email/verify' };
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
const opened = { email: 'ensign@fleet.example', tenantId: '5d1c8e2a-7b3f-4a9e-b6d0-2c4f8a1e9b37', tenantName: 'Anvil', expiresAt: '2026-10-30T12:00:00Z', signUp: true, usernameIsEmail: true };
const dead = { status: 404, body: { message: 'the link has expired or was already used' } };

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
  remove: TransportResponse;
  firstPasswordCode: TransportResponse;
  firstPasswordSet: TransportResponse;
  means: TransportResponse;
  name: TransportResponse;
  look: TransportResponse;
  invitationOpen: TransportResponse;
  invitationAccept: TransportResponse;
  invitationSignUp: TransportResponse;
  emailAdd: TransportResponse;
  emailVerify: TransportResponse;
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
    remove: { status: 200, body: undefined },
    firstPasswordCode: {
      status: 200,
      body: { codeEmail: 'p•••@fleet.example', codeExpiresAt: '2026-11-17T12:10:00Z' },
    },
    firstPasswordSet: { status: 200, body: {} },
    means: { status: 200, body: { password: true, directories: [{ name: 'google', label: 'Fleet Google' }] } },
    name: { status: 200, body: { name: 'Nova Example' } },
    look: { status: 200, body: { scheme: 'dark', palette: 'workspace' } },
    invitationOpen: { status: 200, body: opened },
    invitationAccept: { status: 200, body: { accepted: true } },
    invitationSignUp: { status: 200, body: { mfaIsRequired: false } },
    emailAdd: { status: 200, body: {} },
    emailVerify: { status: 200, body: { email: 'pax@fleet.example' } },
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
      answer: { status: 401, body: { message: 'Invalid Credentials' } },
      want: { outcome: 'refused', status: 401, message: 'Invalid Credentials' },
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
      answer: { status: 401, body: { message: 'Invalid Credentials' } },
      want: { outcome: 'refused', status: 401, message: 'Invalid Credentials' },
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
      mailing({ status: { status: 200, body: { ...codeStep, emailCode: { codeEmail: 'n•••@fleet.example', codeSent: false } } } }),
    );
    expect(await auth.pending()).toEqual({
      outcome: 'waiting',
      ...codeStep,
      emailCode: { codeEmail: 'n•••@fleet.example', enrollEmail: false, codeSent: false },
    });
  });

  it('is reported standing, with its expiry, and as waiting for an address', async () => {
    const standing = authOver(mailing({ status: { status: 200, body: { ...codeStep, emailCode: codeMailed } } }));
    expect(await standing.auth.pending()).toEqual({
      outcome: 'waiting',
      ...codeStep,
      emailCode: { codeEmail: 'n•••@fleet.example', enrollEmail: false, codeSent: true, codeExpiresAt: '2026-10-17T12:10:00Z' },
    });
    const enrolling = authOver(
      mailing({ status: { status: 200, body: { ...codeStep, emailCode: { enrollEmail: true, codeSent: false } } } }),
    );
    expect(await enrolling.auth.pending()).toEqual({
      outcome: 'waiting',
      ...codeStep,
      emailCode: { codeEmail: '', enrollEmail: true, codeSent: false },
    });
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
      answer: { status: 409, body: { message: 'the pending sign-in waits for a password confirmation, not a second factor' } },
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
    const { auth, errors } = authOver(mailing({ enrollEmail: { status: 400, body: { message: 'not an email address' } } }));
    expect(await auth.enrollEmail('nova')).toEqual({ outcome: 'refused', status: 400, message: 'not an email address' });
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
      want: { outcome: 'refused', status: 401, code: 'code_expired', message: 'no sign-in code stands: ask for a new one' },
    },
    {
      name: 'a sign-in that does not wait for the code resolves as refused',
      answer: { status: 409, body: { message: 'the pending sign-in waits for a password confirmation, not a second factor' } },
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
      name: 'a name too long to keep resolves with the server\'s words',
      answer: { status: 400, body: { message: 'a name is at most 200 characters' } },
      want: { outcome: 'refused', status: 400, message: 'a name is at most 200 characters' },
    },
    {
      name: 'an impersonated session resolves as refused',
      answer: { status: 403, body: { message: 'ChangeName is not permitted in an impersonated session' } },
      want: { outcome: 'refused', status: 403, message: 'ChangeName is not permitted in an impersonated session' },
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

  const answers: { name: string; body: unknown; want: unknown }[] = [
    { name: 'an empty answer is a look of nothing', body: {}, want: {} },
    { name: 'no body is a look of nothing', body: undefined, want: {} },
    { name: 'a scheme that is neither is not kept', body: { scheme: 'sepia', palette: 'blue' }, want: { palette: 'blue' } },
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
      name: "a scheme that is neither light nor dark resolves with the server's words",
      answer: { status: 400, body: { message: 'a scheme is light or dark' } },
      want: { outcome: 'refused', status: 400, message: 'a scheme is light or dark' },
    },
    {
      name: 'an impersonated session resolves as refused',
      answer: { status: 403, body: { message: 'ChangeLook is not permitted in an impersonated session' } },
      want: { outcome: 'refused', status: 403, message: 'ChangeLook is not permitted in an impersonated session' },
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

  it('resolves the refusal of a wrong or an unchanged password with the server\'s words, through no error hook', async () => {
    const { auth, errors } = authOver(server({ change: { status: 400, body: { message: 'the new password is the old one' } } }));
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
    });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `GET ${meansPath}`]);
    expect(errors).toEqual([]);
  });

  it('answers a person with no password and no directory as the server says it', async () => {
    const { auth } = authOver(reading({ means: { status: 200, body: { password: false, directories: [] } } }));
    expect(await auth.signInMeans()).toEqual({ password: false, directories: [] });
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
    expect(await auth.signInMeans()).toEqual({ password: true, directories: [{ name: 'entra', label: 'Lakeside' }] });
  });
});

describe('the mailed links in the list', () => {
  it('are kept as the server names them: the username flag, the invitation, email and sign-up routes', async () => {
    const { auth } = authOver(server({ methods: mailingList }));
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
      tenantId: '5d1c8e2a-7b3f-4a9e-b6d0-2c4f8a1e9b37',
      tenantName: 'Anvil',
      expiresAt: '2026-10-30T12:00:00Z',
      usernameIsEmail: true,
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
      tenantId: '',
      tenantName: '',
      expiresAt: '',
      usernameIsEmail: false,
    });
    const none = authOver(
      server({ methods: { ...mailingList, invitation: { ...invitationRoutes, standing: null } } }),
    ).auth;
    expect(await none.methods()).toEqual(mailingList);
  });

  it('keeps what the standing block says of the invited account, and nothing where the server did not say', async () => {
    const plain = {
      email: 'ensign@fleet.example',
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
        methods: { ...mailingList, invitation: { ...invitationRoutes, standing: { ...standing, accountExists: 'yes' } } },
      }),
    ).auth;
    expect((await unsaid.methods()).invitation?.standing).toEqual(plain);
  });

  it('keeps the registration names the standing invitation says could admit the address, strings only, and none where it names none', async () => {
    const standing = { email: 'ensign@fleet.example', tenantId: '', tenantName: '', expiresAt: 'soon', usernameIsEmail: false };
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
    const kept = (await unnamed.methods()).invitation?.standing;
    expect(kept).toEqual(standing);
    expect(Object.keys(kept ?? {})).not.toContain('methods');
  });

  it('keeps what the standing block says of the tenant the invitation makes, and nothing for any other invitation', async () => {
    const standing = {
      email: 'founder@dawn.example',
      tenantId: '',
      tenantName: 'Dawn',
      expiresAt: '2026-10-30T12:00:00Z',
      usernameIsEmail: true,
    };
    const making = authOver(
      server({
        methods: {
          ...mailingList,
          invitation: { ...invitationRoutes, standing: { ...standing, newTenant: true, directoryOnly: true } },
        },
      }),
    ).auth;
    expect((await making.methods()).invitation?.standing).toEqual({ ...standing, newTenant: true, directoryOnly: true });
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
      server({ methods: { methods: [google], invitation: { open: invitationRoutes.open, accept: invitationRoutes.accept } } }),
    );
    expect(await auth.methods()).toEqual({
      methods: [google],
      invitation: { open: invitationRoutes.open, accept: invitationRoutes.accept },
    });
  });
});

describe('an invitation', () => {
  it('carries what the server says of the invited account, and nothing where an older server says nothing', async () => {
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
    const older = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...opened, hasPassword: true } } }),
    ).auth;
    expect(await older.openInvitation('tok-1')).toEqual({ outcome: 'opened', ...opened });
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
      server({ methods: mailingList, invitationOpen: { status: 200, body: { email: 'x@fleet.example', expiresAt: 'soon' } } }),
    );
    expect(await auth.openInvitation('tok-1')).toEqual({
      outcome: 'opened',
      email: 'x@fleet.example',
      tenantId: '',
      tenantName: '',
      expiresAt: 'soon',
      signUp: false,
      usernameIsEmail: false,
    });
  });

  it('carries that the invitation makes a tenant and whether that tenant is entered through its directory only, and neither for any other invitation', async () => {
    const making = { ...opened, tenantId: '', tenantName: 'Dawn', signUp: false };
    const closed = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...making, newTenant: true, directoryOnly: true } } }),
    ).auth;
    expect(await closed.openInvitation('tok-1')).toEqual({ outcome: 'opened', ...making, newTenant: true, directoryOnly: true });
    // The switch is false unless the server said true.
    const open = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...making, newTenant: true } } }),
    ).auth;
    expect(await open.openInvitation('tok-1')).toEqual({ outcome: 'opened', ...making, newTenant: true, directoryOnly: false });
    // An invitation into a tenant that exists says neither, whatever else the answer carries.
    const into = authOver(
      server({ methods: mailingList, invitationOpen: { status: 200, body: { ...opened, newTenant: false, directoryOnly: true } } }),
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
    const result = await unnamed.openInvitation('tok-1');
    expect(result).toEqual({ outcome: 'opened', ...opened });
    expect(Object.keys(result)).not.toContain('methods');
  });

  it("resolves a dead link as the server's 404 refusal, through no error hook", async () => {
    const { auth, errors } = authOver(server({ methods: mailingList, invitationOpen: dead }));
    expect(await auth.openInvitation('tok-dead')).toEqual({ outcome: 'refused', status: 404, message: dead.body.message });
    expect(errors).toEqual([]);
  });

  it('is accepted by the signed-in person after the list is read again', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.acceptInvitation()).toEqual({ outcome: 'accepted' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${invitationRoutes.accept}`]);
  });

  it('resolves the refusal of an acceptance with nothing to accept', async () => {
    const { auth } = authOver(
      server({ methods: mailingList, invitationAccept: { status: 404, body: { message: 'the browser carries no live invitation' } } }),
    );
    expect(await auth.acceptInvitation()).toEqual({ outcome: 'refused', status: 404, message: 'the browser carries no live invitation' });
  });

  it('creates an account with a password after the list is read again, the username sent only where given, and resolves the sign-in', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.signUpFromInvitation({ password: 'orbit-and-anchor' })).toEqual({ outcome: 'session' });
    expect(await auth.signUpFromInvitation({ password: 'orbit-and-anchor', username: 'ensign' })).toEqual({ outcome: 'session' });
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
    { name: 'a short password (400)', answer: { status: 400, body: { message: 'a password is at least eight characters' } } },
    { name: 'no live invitation (404)', answer: { status: 404, body: { message: 'the browser carries no live invitation' } } },
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
      server({ methods: { methods: [google], invitation: { open: invitationRoutes.open, accept: invitationRoutes.accept } } }),
    );
    await expect(directory.auth.signUpFromInvitation({ password: 'x' })).rejects.toThrow(/no sign-up route for invitations/);
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
    const notOne = authOver(server({ methods: mailingList, emailAdd: { status: 400, body: { message: 'not an email address' } } }));
    expect(await notOne.auth.addEmail('pax')).toEqual({ outcome: 'refused', status: 400, message: 'not an email address' });
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
    expect(await gone.auth.verifyEmail('tok-v')).toEqual({ outcome: 'refused', status: 404, message: dead.body.message });
    const taken = authOver(server({ methods: mailingList, emailVerify: { status: 409, body: { message: 'the address is taken' } } }));
    expect(await taken.auth.verifyEmail('tok-v')).toEqual({ outcome: 'refused', status: 409, message: 'the address is taken' });
  });

  it('throws on a server whose list names no email routes', async () => {
    const { auth } = authOver(server());
    await expect(auth.addEmail('pax@fleet.example')).rejects.toThrow(/names no email address routes/);
    await expect(auth.verifyEmail('tok-v')).rejects.toThrow(/names no email address routes/);
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
    expect(await auth.openSignUp('tok-s')).toEqual({ outcome: 'opened', email: 'rookie@fleet.example', usernameIsEmail: true });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${signUpRoutes.open}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-s' });
    const gone = authOver(server({ methods: mailingList, signUpOpen: dead }));
    expect(await gone.auth.openSignUp('tok-s')).toEqual({ outcome: 'refused', status: 404, message: dead.body.message });
  });

  it('is completed after the list is read again, posting the token and the account, and resolves the sign-in', async () => {
    const { auth, transport } = authOver(server({ methods: mailingList }));
    expect(await auth.completeSignUp('tok-s', { password: 'orbit-and-anchor', username: 'rookie' })).toEqual({ outcome: 'session' });
    expect(sent(transport)).toEqual([`GET /console/api/${loginMethodsRoute}`, `POST ${signUpRoutes.complete}`]);
    expect(transport.requests[1].body).toEqual({ token: 'tok-s', password: 'orbit-and-anchor', username: 'rookie' });
  });

  it('resolves a pending step the completion answers, and a taken address as the refusal', async () => {
    const pending = authOver(
      server({ methods: mailingList, signUpComplete: { status: 200, body: { mfaIsRequired: true, redirectUrl: '/console/mfa' } } }),
    );
    expect(await pending.auth.completeSignUp('tok-s', { password: 'x' })).toEqual({ outcome: 'pending', redirectUrl: '/console/mfa' });
    const taken = authOver(
      server({ methods: mailingList, signUpComplete: { status: 409, body: { message: 'an account holds the address' } } }),
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
    { name: 'a 403 carrying the code is the forced change', status: 403, body: { message: 'Password change required', code: passwordChangeRequiredCode }, want: true },
    { name: 'a 403 with another code is an ordinary refusal', status: 403, body: { message: 'Not your mission', code: 'forbidden' }, want: false },
    { name: 'a 403 with no body is an ordinary refusal', status: 403, body: undefined, want: false },
    { name: 'a 403 whose body is a string is an ordinary refusal', status: 403, body: 'forbidden', want: false },
    { name: 'the code on another status is not the forced change', status: 401, body: { code: passwordChangeRequiredCode }, want: false },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(refusedForPasswordChange({ status: tt.status, body: tt.body })).toBe(tt.want);
    });
  }
});
