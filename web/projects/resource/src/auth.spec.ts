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
const opened = { email: 'ensign@fleet.example', tenant: 'anvil', expiresAt: '2026-10-30T12:00:00Z', signUp: true, usernameIsEmail: true };
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
      answer: { status: 429, body: { message: 'too many codes', code: 'too_many_attempts', retryAfter: 600 } },
      want: { outcome: 'refused', status: 429, code: 'too_many_attempts', message: 'too many codes', retryAfter: 600 },
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

  it('throws on a server whose list names no change path', async () => {
    const { auth } = authOver(server({ methods: { methods: [google], pending: pendingRoutes } }));
    await expect(auth.changePassword('old', 'new')).rejects.toThrow(/names no password change path/);
  });

  it('keeps the change path off a list that does not name one', async () => {
    const { auth } = authOver(server({ methods: { methods: [google] } }));
    expect(await auth.methods()).toEqual({ methods: [google] });
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
      tenant: 'anvil',
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
      tenant: '',
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
      tenant: 'anvil',
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
    const standing = { email: 'ensign@fleet.example', tenant: '', expiresAt: 'soon', usernameIsEmail: false };
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
      tenant: '',
      expiresAt: 'soon',
      signUp: false,
      usernameIsEmail: false,
    });
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
        emailAdd: { status: 429, body: { message: 'Too many links', code: 'too_many_attempts', retryAfter: 600 } },
      }),
    );
    expect(await tooMany.auth.addEmail('pax@fleet.example')).toEqual({
      outcome: 'refused',
      status: 429,
      code: 'too_many_attempts',
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
