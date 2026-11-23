import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, UrlTree } from '@angular/router';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { DEFAULT_LOGIN_MESSAGES } from '@impulseframework/resource-angular/types';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';
import { AccountPageComponent, passwordRemovalConsequence } from './account-page.component';
import { notNamedFor, passwordMethods, provideFrameTesting, SpecServer, specTransport } from './frame-testing';
import { PlaceService } from './place.service';

// The person's account page shows their lines: the username where it is not the address,
// the name, the email address, a password where they hold one or may choose one, and the
// directories they sign in through. Each line the person may change carries an icon that
// opens it as a form in place, one line at a time; what the server serves and what the
// person signs in with decide which lines carry one. Under the lines, the person's tenants,
// each left behind typed words in a window of its own; then where the person is signed in,
// each browser signed out of or forgotten at once. An impersonated session changes nothing.

/** Where the spec's server serves the email address routes. */
const emailRoutes = { add: '/api/user/email', verify: '/api/user/email/verify' };
/** Where the spec's server serves the choice of a first password. */
const firstPasswordRoutes = { sendCode: '/api/user/first-password/code', set: '/api/user/first-password' };
/** Where the spec's server serves the person's own tenants. */
const tenantRoutes = { list: '/api/user/tenants', leave: '/api/user/tenants/leave' };
/** Everything the spec's server serves for a person's own account. */
const everything: Record<string, unknown> = {
  ...passwordMethods,
  email: emailRoutes,
  firstPassword: firstPasswordRoutes,
  ownTenants: tenantRoutes,
};
/** What the server serves a person who signs in through a directory alone: no password method. */
const directoryMethods: Record<string, unknown> = {
  changeUsername: passwordMethods.changeUsername,
  usernameFree: passwordMethods.usernameFree,
  changeName: passwordMethods.changeName,
  signInMeans: passwordMethods.signInMeans,
  email: emailRoutes,
};
/** A person who chose their username, holds a confirmed address and a password. */
const gale: Record<string, unknown> = {
  authenticated: true,
  username: 'gale',
  name: 'Gale Reyes',
  email: 'gale@lakeside.edu',
  emailVerified: true,
};
/** One of the person's sessions as the server answers it. */
interface SessionBody {
  readonly id: string;
  readonly browser?: string;
  readonly phone?: boolean;
  readonly signedInAt: string;
  readonly lastUsedAt: string;
  readonly current?: boolean;
}
/**
 * A moment of November 2026, the day, hour and minute in the zone the spec runs in, so the
 * page writes it as the spec reads it in any zone.
 */
const at = (day: number, hour: number, minute: number): string => new Date(2026, 10, day, hour, minute).toISOString();
/** The session of this browser. */
const here: SessionBody = {
  id: 's-here',
  browser: 'Firefox on Linux',
  signedInAt: at(3, 9, 5),
  lastUsedAt: at(22, 11, 40),
  current: true,
};
/** Another browser of the person's, a laptop's. */
const laptop: SessionBody = {
  id: 's-laptop',
  browser: 'Chrome on macOS',
  signedInAt: at(10, 14, 30),
  lastUsedAt: at(21, 8, 0),
};
/** Another, a phone's. */
const phone: SessionBody = {
  id: 's-phone',
  browser: 'Safari on iPhone',
  phone: true,
  signedInAt: at(1, 7, 15),
  lastUsedAt: at(20, 19, 45),
};
/** Another, of a browser the server did not recognize. */
const unnamed: SessionBody = { id: 's-unnamed', signedInAt: at(2, 12, 0), lastUsedAt: at(2, 12, 30) };
/** One of the person's tenants as the server lists it. */
interface TenantBody {
  readonly domain: string;
  readonly name: string;
  readonly roles: readonly string[];
  readonly refusal?: string;
  readonly deletesAccount: boolean;
}
/** A tenant the person holds two roles in, and others besides. */
const acme: TenantBody = { domain: 't-acme', name: 'Acme', roles: ['Reader', 'Warden'], deletesAccount: false };
/** A tenant the person holds no role of their own in: a place in one of its groups. */
const beta: TenantBody = { domain: 't-beta', name: 'Beta', roles: [], deletesAccount: false };
/** A tenant the person is the only administrator of. */
const gamma: TenantBody = {
  domain: 't-gamma',
  name: 'Gamma',
  roles: ['Administrator'],
  refusal: 'You are the only administrator of Gamma. Make someone else an administrator there first.',
  deletesAccount: false,
};
/** The one tenant of a person who holds nothing else: leaving it deletes the account. */
const only: TenantBody = { domain: 't-acme', name: 'Acme', roles: ['Reader'], deletesAccount: true };
/** The record of a session that acts for the person. */
const impersonation = { actor: 'ops', principalKind: 'User', principal: 'gale', expiresAt: '2026-11-15T12:00:00Z' };

/** What the server says the person signs in with: a password or none, the directories linked by their words, and whether the address is a directory's. */
function means(password: boolean, labels: string[] = [], directoryAddress = false): TransportResponse {
  return {
    status: 200,
    body: {
      password,
      directories: labels.map((label, i) => ({ name: `directory-${i}`, label })),
      directoryAddress,
    },
  };
}

/** The server's answer about a password it calls strong enough, as it answers by default. */
const strong: TransportResponse = { status: 200, body: { strongEnough: true, strength: 80, minimum: 50 } };
/** The server's answer about a password it calls too easy to guess. */
const weak: TransportResponse = {
  status: 200,
  body: { strongEnough: false, strength: 20, minimum: 50, reason: 'Add another word or two.' },
};
/** A refusal of mail to an address that was sent too many links, with the wait. */
const tooManyEmails: TransportResponse = {
  status: 429,
  body: { message: 'Too many emails to this address', code: 'too_many_emails', retryAfter: 120 },
};

const lineKeys = ['username', 'name', 'email', 'password', 'sign-in'] as const;
type LineKey = (typeof lineKeys)[number];

interface Setup {
  /** The methods list; unset, everything. */
  methods?: Record<string, unknown>;
  /** The session read's answer; unset, gale's. */
  session?: Record<string, unknown>;
  /** What the server answers the read of what the person signs in with; unset, a password and no directory. */
  means?: TransportResponse;
  /** What the server answers a post to a path with, by path; unset paths are served as a server serves them. */
  answers?: Record<string, TransportResponse>;
  /** The usernames someone else holds; unset, rey. */
  taken?: string[];
  /** The usernames whose question fails. */
  failing?: string[];
  /** The paths whose requests wait until the spec answers them (`held`). */
  hold?: string[];
  /** What the server answers a question about a new password's strength with; unset, strong enough. */
  strength?: TransportResponse;
  /** The person's live sessions, as the server lists them; unset, this browser's alone. */
  sessions?: SessionBody[];
  /** How many browsers the server remembers for the person; unset, none. */
  rememberedBrowsers?: number;
  /** The person's tenants, as the server lists them and as the person's list of tenants names them; unset, none. */
  tenants?: TenantBody[];
}

/** A request the spec's server holds until the spec answers it. */
interface Held {
  readonly path: string;
  readonly body: unknown;
  answer(response: TransportResponse): void;
}

describe('AccountPageComponent', () => {
  let fixture: ComponentFixture<AccountPageComponent>;
  let transport: ScriptedTransport;
  let held: Held[];
  let strengthAnswer: TransportResponse;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const page = (): AccountPageComponent => fixture.componentInstance;
  const text = (selector: string): string | undefined =>
    element().querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim();
  const posted = (): string[] => transport.requests.filter((r) => r.method === 'POST').map((r) => r.url);
  const postedTo = (path: string): unknown[] =>
    transport.requests.filter((r) => r.method === 'POST' && r.url === path).map((r) => r.body);
  const sessionReads = (): number => transport.requests.filter((r) => r.url === '/api/user/session').length;
  /** The bodies of the questions about a new password's strength, in order. */
  const strengthAsked = (): unknown[] =>
    transport.requests.filter((r) => r.url === passwordMethods.passwordStrength).map((r) => r.body);
  // A form box takes its model's value a moment after the view is drawn: settle waits it out.
  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
  };
  // While the server holds a request, the application is not stable: drain lets every
  // answer that is in arrive, and draws the page, without waiting for the held ones.
  const drain = async (): Promise<void> => {
    for (let i = 0; i < 5; i++) {
      await new Promise((resolve) => setTimeout(resolve));
      fixture.detectChanges();
    }
  };

  const open = async ({
    methods = everything,
    session = gale,
    means: meansAnswer = means(true),
    answers = {},
    taken = ['rey'],
    failing = [],
    hold = [],
    strength = strong,
    sessions = [here],
    rememberedBrowsers = 0,
    tenants = [],
  }: Setup = {}): Promise<void> => {
    let meansNow = meansAnswer;
    let sessionsNow = [...sessions];
    let rememberedNow = rememberedBrowsers;
    let tenantsNow = [...tenants];
    strengthAnswer = strength;
    held = [];
    const server: SpecServer = {
      methods: { methods: [], ...methods },
      session: { ...session },
      domains: tenants.map((tenant) => ({ domain: tenant.domain, name: tenant.name })),
      other: (url) => {
        const path = url.pathname;
        const given = answers[path];
        if (path === passwordMethods.signInMeans) {
          return meansNow;
        }
        if (path === passwordMethods.passwordStrength) {
          return strengthAnswer;
        }
        if (given) {
          return given;
        }
        if (path === firstPasswordRoutes.sendCode) {
          return { status: 200, body: { codeEmail: 'g•••@lakeside.edu' } };
        }
        if (path === firstPasswordRoutes.set) {
          meansNow = { status: 200, body: { ...(meansNow.body as object), password: true } };
          return { status: 200, body: {} };
        }
        if (path === passwordMethods.removePassword) {
          meansNow = { status: 200, body: { ...(meansNow.body as object), password: false } };
          return { status: 200, body: null };
        }
        return path.startsWith('/api/user/') ? { status: 200, body: null } : undefined;
      },
    };
    const inner = specTransport(server);
    // The answers that change what the server holds, and the questions whether a name is
    // free, which read the body.
    const respond = (request: TransportRequest): Promise<TransportResponse> => {
      const path = new URL(request.url, 'http://spec.test').pathname;
      const body = request.body as Record<string, string> | undefined;
      if (path === passwordMethods.usernameFree && !answers[path]) {
        void inner(request);
        const name = body?.['username'] ?? '';
        return Promise.resolve(
          failing.includes(name)
            ? { status: 500, body: { message: 'the question failed' } }
            : { status: 200, body: { free: !taken.includes(name) } },
        );
      }
      if (path === passwordMethods.changeUsername && !answers[path]) {
        void inner(request);
        const username = (body?.['username'] ?? '').trim();
        server.session = { ...server.session, username };
        return Promise.resolve({ status: 200, body: { username } });
      }
      const sessionRoutes = passwordMethods.ownSessions;
      // Where the person is signed in, and what each act on it changes on the server.
      if (!answers[path]) {
        if (path === sessionRoutes.list) {
          void inner(request);
          return Promise.resolve({
            status: 200,
            body: { sessions: [...sessionsNow], rememberedBrowsers: rememberedNow },
          });
        }
        if (path === sessionRoutes.end) {
          void inner(request);
          const ended = sessionsNow.some((session) => session.id === body?.['sessionId']);
          sessionsNow = sessionsNow.filter((session) => session.id !== body?.['sessionId']);
          return Promise.resolve(
            ended ? { status: 200, body: null } : { status: 404, body: { message: 'That session is gone.' } },
          );
        }
        if (path === sessionRoutes.endOthers) {
          void inner(request);
          const ended = sessionsNow.filter((session) => !session.current).length;
          sessionsNow = sessionsNow.filter((session) => session.current);
          return Promise.resolve({ status: 200, body: { ended } });
        }
        // The person's tenants, and a leave, which takes the tenant off the list and out of
        // the person's list of tenants.
        if (path === tenantRoutes.list) {
          void inner(request);
          return Promise.resolve({ status: 200, body: { tenants: [...tenantsNow] } });
        }
        if (path === tenantRoutes.leave) {
          void inner(request);
          const left = tenantsNow.find((tenant) => tenant.domain === body?.['domain']);
          tenantsNow = tenantsNow.filter((tenant) => tenant !== left);
          server.domains = server.domains?.filter((tenant) => tenant.domain !== left?.domain);
          return Promise.resolve(
            left
              ? { status: 200, body: { accountDeleted: left.deletesAccount } }
              : { status: 404, body: { message: 'You are not in that customer.' } },
          );
        }
        if (path === passwordMethods.changePassword) {
          // A change of the password signs the person out of every other browser.
          sessionsNow = sessionsNow.filter((session) => session.current);
        }
        if (path === passwordMethods.forgetBrowser) {
          rememberedNow = Math.max(0, rememberedNow - 1);
          server.session = { ...server.session, browserRemembered: false };
        }
        if (path === passwordMethods.forgetBrowsers) {
          rememberedNow = 0;
          server.session = { ...server.session, browserRemembered: false };
        }
      }
      const nameAnswer = answers[passwordMethods.changeName];
      if (path === passwordMethods.changeName && nameAnswer?.status === 200) {
        server.session = { ...server.session, name: (nameAnswer.body as { name?: string } | null)?.name ?? null };
      }
      return inner(request);
    };
    transport = Object.assign(
      (request: TransportRequest): Promise<TransportResponse> => {
        const path = new URL(request.url, 'http://spec.test').pathname;
        if (!hold.includes(path)) {
          return respond(request);
        }
        // The request is recorded, and what it changes on the server is done, at once; its answer waits for the spec.
        void respond(request);
        return new Promise<TransportResponse>((resolve) => held.push({ path, body: request.body, answer: resolve }));
      },
      { requests: inner.requests },
    );
    await TestBed.configureTestingModule({
      imports: [AccountPageComponent],
      providers: [provideFrameTesting(transport)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(AccountPageComponent);
    fixture.detectChanges();
    if (hold.length === 0) {
      await settle();
    } else {
      await drain();
    }
  };

  /** The keys of the lines drawn, in order. */
  const lines = (): string[] =>
    Array.from(element().querySelectorAll('.impulse-account-page-line')).map(
      (line) => lineKeys.find((key) => line.classList.contains(`impulse-account-page-${key}`)) ?? '(none)',
    );
  const line = (key: LineKey): HTMLElement | null =>
    element().querySelector<HTMLElement>(`.impulse-account-page-line.impulse-account-page-${key}`);
  const inLine = (key: LineKey, part: string): string | undefined =>
    line(key)?.querySelector(`.impulse-account-page-${part}`)?.textContent?.replace(/\s+/g, ' ').trim();
  const buttonIn = (key: LineKey, part: string): HTMLButtonElement | null =>
    line(key)?.querySelector<HTMLButtonElement>(`button.impulse-account-page-${part}`) ?? null;
  const icon = (key: LineKey): HTMLButtonElement | null => buttonIn(key, 'edit');
  const press = async (button: HTMLButtonElement | null): Promise<void> => {
    if (!button) throw new Error('no such button');
    button.click();
    await settle();
  };
  const openLine = async (key: LineKey): Promise<void> => press(icon(key));
  const type = (name: string, value: string): void => {
    const input = element().querySelector<HTMLInputElement>(`input[name="${name}"]`);
    if (!input) throw new Error(`no ${name} field`);
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };
  const box = (name: string): HTMLInputElement | null =>
    element().querySelector<HTMLInputElement>(`input[name="${name}"]`);
  const label = (key: LineKey): string | undefined => text(`.impulse-account-page-${key} mat-label`);
  /** The lines of the list of where the person is signed in, in order. */
  const sessionItems = (): HTMLElement[] =>
    Array.from(element().querySelectorAll<HTMLElement>('li.impulse-account-page-session'));
  const ofSession = (item: HTMLElement | undefined, part: string): string | undefined =>
    item?.querySelector(`.impulse-account-page-${part}`)?.textContent?.replace(/\s+/g, ' ').trim();
  /** The browsers' names in the list, the chip of this browser's with it. */
  const sessionNames = (): (string | undefined)[] => sessionItems().map((item) => ofSession(item, 'session-name'));
  const sectionButton = (part: string): HTMLButtonElement | null =>
    element().querySelector<HTMLButtonElement>(`.impulse-account-page-signed-in button.impulse-account-page-${part}`);
  const signOutOf = (label: string): HTMLButtonElement | null =>
    element().querySelector<HTMLButtonElement>(`button.impulse-account-page-sign-out[aria-label="${label}"]`);
  const sessionsSaid = (): string | undefined => text('.impulse-account-page-sessions-said');
  const listReads = (): number => transport.requests.filter((r) => r.url === passwordMethods.ownSessions.list).length;
  /** The rows of the list of the person's tenants, in order. */
  const tenantItems = (): HTMLElement[] =>
    Array.from(element().querySelectorAll<HTMLElement>('li.impulse-account-page-tenant'));
  const ofTenant = (item: HTMLElement | undefined, part: string): string | undefined =>
    item?.querySelector(`.impulse-account-page-${part}`)?.textContent?.replace(/\s+/g, ' ').trim();
  const leaveOf = (name: string): HTMLButtonElement | null =>
    element().querySelector<HTMLButtonElement>(`button.impulse-account-page-leave[aria-label="Leave ${name}"]`);
  const tenantsSaid = (): string | undefined => text('.impulse-account-page-tenants-said');
  const tenantReads = (): number => transport.requests.filter((r) => r.url === tenantRoutes.list).length;
  /** How many times the person's list of tenants was read: at the session check, and after a leave. */
  const domainReads = (): number =>
    transport.requests.filter((r) => new URL(r.url, 'http://spec.test').pathname === '/api/user-domains').length;
  /** The element of the leave window that carries a class, or null while no window is open. */
  const inWindow = <T extends HTMLElement = HTMLElement>(part: string): T | null =>
    document.querySelector<T>(`.impulse-leave-tenant-dialog-${part}`);
  /** Types into the leave window's box. */
  const typeInWindow = async (value: string): Promise<void> => {
    const input = inWindow<HTMLInputElement>('words');
    if (!input) throw new Error('no leave window is open');
    input.value = value;
    input.dispatchEvent(new Event('input'));
    await settle();
  };
  /** Presses Leave on a tenant's row, types the words in the window and presses its button, then lets the leave answer. */
  const leaveWith = async (name: string, words: string): Promise<void> => {
    await press(leaveOf(name));
    await typeInWindow(words);
    inWindow<HTMLButtonElement>('leave')?.click();
    await drain();
    await settle();
  };
  /** Stands in for the router's navigation, as the spec has no routes to go to, and answers where the page went. */
  const watchNavigations = (): (() => string[]) => {
    const router = TestBed.inject(Router);
    const navigate = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    return () =>
      navigate.mock.calls.map(([url]: [string | UrlTree, ...unknown[]]) =>
        typeof url === 'string' ? url : router.serializeUrl(url),
      );
  };

  describe('the lines', () => {
    it('shows a person who chose their username their four lines, each with its value, its hint and its icon', async () => {
      await open();
      expect(text('h1')).toBe('Your account');
      expect(lines()).toEqual(['username', 'name', 'email', 'password']);

      expect(inLine('username', 'key')).toBe('Username');
      expect(inLine('username', 'value')).toBe('gale');
      expect(inLine('username', 'hint')).toBe('You sign in with this.');
      expect(icon('username')?.getAttribute('aria-label')).toBe('Change your username');

      expect(inLine('name', 'key')).toBe('Name');
      expect(inLine('name', 'value')).toBe('Gale Reyes');
      expect(inLine('name', 'hint')).toBe('Shown to other people in place of your username.');
      expect(icon('name')?.getAttribute('aria-label')).toBe('Change your name');

      expect(inLine('email', 'key')).toBe('Email');
      expect(inLine('email', 'value')).toBe('gale@lakeside.edu Confirmed');
      expect(inLine('email', 'chip')).toBe('Confirmed');
      expect(inLine('email', 'hint')).toBe('Sign-in codes and notices go here.');
      expect(icon('email')?.getAttribute('aria-label')).toBe('Change your email address');

      expect(inLine('password', 'key')).toBe('Password');
      expect(inLine('password', 'value')).toBe('••••••••••');
      expect(line('password')?.querySelector('.impulse-account-page-value')?.getAttribute('aria-label')).toBe('Hidden');
      expect(inLine('password', 'hint')).toBeUndefined();
      expect(icon('password')?.getAttribute('aria-label')).toBe('Change your password');

      expect(element().querySelector('.impulse-account-page-acting')).toBeNull();
      expect(element().querySelectorAll('.impulse-account-page-said')).toHaveLength(0);
    });

    it('shows no Username line where the username is the address, and says the address is what they sign in with', async () => {
      await open({
        methods: { ...everything, usernameIsEmail: true },
        session: { ...gale, username: 'gale@lakeside.edu' },
      });
      expect(lines()).toEqual(['name', 'email', 'password']);
      expect(inLine('email', 'hint')).toBe('You sign in with this address.');
      expect(TestBed.inject(Account).usernameLine()).toBe(false);
    });

    it("shows a directory person their address as the directory's, unchanged here, and the directory they sign in through", async () => {
      await open({ methods: directoryMethods, means: means(false, ['Lakeside Google'], true) });
      expect(lines()).toEqual(['username', 'name', 'email', 'sign-in']);
      expect(inLine('username', 'hint')).toBeUndefined();
      expect(inLine('email', 'hint')).toBe("From your organization's directory, so it is not changed here.");
      expect(icon('email')).toBeNull();
      expect(inLine('sign-in', 'key')).toBe('Sign-in');
      expect(inLine('sign-in', 'value')).toBe('Through Lakeside Google');
      expect(inLine('sign-in', 'hint')).toBe('Your password is kept there, so there is none to change here.');
      expect(icon('sign-in')).toBeNull();
      expect(TestBed.inject(Account).directoryAddress()).toBe(true);
      expect(TestBed.inject(Account).email()).toBe(false);
    });

    it('shows a person with no password who may choose one "None", the icon to choose one, and their only way in', async () => {
      await open({ means: means(false, ['Lakeside Google']) });
      expect(lines()).toEqual(['username', 'name', 'email', 'password', 'sign-in']);
      expect(inLine('password', 'value')).toBe('None');
      expect(line('password')?.querySelector('.impulse-account-page-value.impulse-account-page-unset')).not.toBeNull();
      expect(inLine('password', 'hint')).toBe('You can choose one.');
      expect(icon('password')?.getAttribute('aria-label')).toBe('Choose a password');
      expect(inLine('username', 'hint')).toBeUndefined();
      expect(inLine('sign-in', 'hint')).toBe('It is your only way in. A password of your own would be a second.');
    });

    it('shows a person with a password and directories each directory, and that the password is another way in', async () => {
      await open({ means: means(true, ['Lakeside Google', 'Lakeside Entra']) });
      expect(lines()).toEqual(['username', 'name', 'email', 'password', 'sign-in']);
      expect(inLine('password', 'value')).toBe('••••••••••');
      expect(inLine('sign-in', 'value')).toBe('Through Lakeside Google or Lakeside Entra');
      expect(inLine('sign-in', 'hint')).toBe('You can also sign in with your password.');
      expect(inLine('username', 'hint')).toBe('You sign in with this.');
    });

    it('joins three directories as a list', async () => {
      await open({ means: means(true, ['Google', 'Entra', 'Okta']) });
      expect(inLine('sign-in', 'value')).toBe('Through Google, Entra or Okta');
    });

    it('says "Not set" for a name and an address the person does not hold, and offers to add an address', async () => {
      await open({ session: { authenticated: true, username: 'gale' } });
      expect(inLine('name', 'value')).toBe('Not set');
      expect(line('name')?.querySelector('.impulse-account-page-value.impulse-account-page-unset')).not.toBeNull();
      expect(inLine('email', 'value')).toBe('Not set');
      expect(line('email')?.querySelector('.impulse-account-page-value.impulse-account-page-unset')).not.toBeNull();
      expect(icon('email')?.getAttribute('aria-label')).toBe('Add an email address');
    });

    it('takes a name that is no address for no address, and says an unconfirmed one is not confirmed', async () => {
      await open({ session: { ...gale, email: 'gale', emailVerified: false } });
      expect(inLine('email', 'value')).toBe('Not set');
      TestBed.resetTestingModule();
      await open({ session: { ...gale, emailVerified: false } });
      expect(inLine('email', 'value')).toBe('gale@lakeside.edu Not confirmed');
      expect(icon('email')?.getAttribute('aria-label')).toBe('Change your email address');
    });

    it('says the person acts for someone else in an impersonated session, and offers no icon on any line', async () => {
      await open({ session: { ...gale, impersonation }, means: means(true, ['Lakeside Google']) });
      expect(text('.impulse-account-page-acting')).toBe(
        'You are acting as this person, so nothing here can be changed.',
      );
      expect(lines()).toEqual(['username', 'name', 'email', 'password', 'sign-in']);
      expect(element().querySelectorAll('.impulse-account-page-edit')).toHaveLength(0);
    });

    it('draws every line of a person whose sign-in offers nothing to change, with no icon and no sentence about acting', async () => {
      await open({ methods: {} });
      expect(lines()).toEqual(['username', 'name', 'email']);
      expect(element().querySelectorAll('.impulse-account-page-edit')).toHaveLength(0);
      expect(element().querySelector('.impulse-account-page-acting')).toBeNull();
    });

    it('draws no line until what the person signs in with has answered, so none is drawn and then taken away', async () => {
      await open({ methods: directoryMethods, hold: [passwordMethods.signInMeans] });
      expect(element().querySelector('.impulse-account-page-lines')).toBeNull();
      held[0]?.answer(means(false, ['Lakeside Google'], true));
      await settle();
      expect(lines()).toEqual(['username', 'name', 'email', 'sign-in']);
      expect(icon('email')).toBeNull();
    });

    it('draws the lines where the read of what the person signs in with fails', async () => {
      await open({ means: { status: 500, body: { message: 'the read failed' } } });
      expect(lines()).toEqual(['username', 'name', 'email', 'password']);
      expect(inLine('password', 'value')).toBe('••••••••••');
    });

    it("names every element for the account page, with each line open in turn: an application's rule for its own .name or .hint lands on none", async () => {
      await open({
        means: means(true, ['Lakeside Google']),
        session: { ...gale, browserRemembered: true },
        sessions: [here, laptop, unnamed],
        rememberedBrowsers: 2,
        tenants: [acme, gamma],
      });
      expect(sectionButton('forget')).not.toBeNull();
      expect(leaveOf('Acme')).not.toBeNull();
      expect(element().querySelector('.impulse-account-page-tenant-refusal')).not.toBeNull();
      expect(sectionButton('forget-all')).not.toBeNull();
      expect(element().querySelector('section.impulse-account-page-content')).not.toBeNull();
      expect(notNamedFor('impulse-account-page-', element())).toEqual([]);
      for (const key of ['username', 'name', 'email', 'password'] as const) {
        await openLine(key);
        expect(line(key)?.querySelector('.impulse-account-page-form')).not.toBeNull();
        expect(notNamedFor('impulse-account-page-', element())).toEqual([]);
      }
    });
  });

  describe('one line at a time', () => {
    it('opens a line in place of its value, closes any other and drops what was typed there, and changes no address', async () => {
      await open();
      const router = TestBed.inject(Router);
      const url = router.url;
      const href = window.location.href;
      await openLine('name');
      expect(line('name')?.querySelector('.impulse-account-page-form')).not.toBeNull();
      expect(line('name')?.querySelector('.impulse-account-page-value')).toBeNull();
      expect(icon('name')).toBeNull();
      type('personName', 'Somebody Else');
      await settle();
      await openLine('username');
      expect(line('name')?.querySelector('.impulse-account-page-form')).toBeNull();
      expect(inLine('name', 'value')).toBe('Gale Reyes');
      expect(line('username')?.querySelector('.impulse-account-page-form')).not.toBeNull();
      await openLine('name');
      expect(line('username')?.querySelector('.impulse-account-page-form')).toBeNull();
      expect(box('personName')?.value).toBe('Gale Reyes');
      expect(router.url).toBe(url);
      expect(window.location.href).toBe(href);
    });

    it('closes the line on Cancel and shows its value again', async () => {
      await open();
      await openLine('email');
      await press(buttonIn('email', 'cancel'));
      expect(line('email')?.querySelector('.impulse-account-page-form')).toBeNull();
      expect(inLine('email', 'value')).toBe('gale@lakeside.edu Confirmed');
      expect(posted()).toEqual([]);
    });

    it('disables the icons, Cancel and the buttons of the form while a request is out', async () => {
      await open({
        hold: [passwordMethods.changeName],
        answers: { [passwordMethods.changeName]: { status: 200, body: { name: 'Gale R.' } } },
      });
      await openLine('name');
      type('personName', 'Gale R.');
      fixture.detectChanges();
      const saving = page().saveName();
      await drain();
      expect(held).toHaveLength(1);
      expect(buttonIn('name', 'save')?.disabled).toBe(true);
      expect(buttonIn('name', 'cancel')?.disabled).toBe(true);
      for (const key of ['username', 'email', 'password'] as const) {
        expect(icon(key)?.disabled).toBe(true);
      }
      held[0]?.answer({ status: 200, body: { name: 'Gale R.' } });
      await saving;
      await settle();
      expect(icon('name')?.disabled).toBe(false);
    });

    it('keeps what a change said until a line is opened', async () => {
      await open({ answers: { [passwordMethods.changeName]: { status: 200, body: { name: 'Gale R.' } } } });
      await openLine('name');
      type('personName', 'Gale R.');
      await settle();
      await page().saveName();
      await settle();
      expect(inLine('name', 'said')).toBe('Your name is saved.');
      expect(line('name')?.querySelector('.impulse-account-page-said')?.getAttribute('role')).toBe('status');
      await openLine('email');
      expect(element().querySelector('.impulse-account-page-said')).toBeNull();
    });
  });

  describe('Username', () => {
    const say = (): string | undefined => inLine('username', 'say');
    const save = (): HTMLButtonElement | null => buttonIn('username', 'save');

    it('opens with the username in a box that takes a username, and says it is the username now', async () => {
      await open();
      await openLine('username');
      const input = box('username');
      expect(input?.value).toBe('gale');
      expect(label('username')).toBe('Username');
      expect(input?.getAttribute('autocomplete')).toBe('username');
      expect(input?.getAttribute('autocapitalize')).toBe('none');
      expect(input?.getAttribute('spellcheck')).toBe('false');
      expect(input?.getAttribute('maxlength')).toBe('200');
      expect(say()).toBe('This is your username now.');
      expect(line('username')?.querySelector('.impulse-account-page-say')?.getAttribute('role')).toBe('status');
      expect(save()?.disabled).toBe(true);
      expect(postedTo(passwordMethods.usernameFree)).toEqual([]);
    });

    it('says what the server answers of each name, for the name without the space around it, and unlocks Save only for a free one', async () => {
      await open({ failing: ['harbor'] });
      await openLine('username');

      type('username', '   ');
      await settle();
      expect(say()).toBe('');
      expect(save()?.disabled).toBe(true);

      type('username', ' gale ');
      await settle();
      expect(say()).toBe('This is your username now.');
      expect(save()?.disabled).toBe(true);

      type('username', '  rey ');
      await settle();
      expect(say()).toBe('Someone else has that username.');
      expect(save()?.disabled).toBe(true);

      type('username', 'gale.r');
      await settle();
      expect(say()).toBe('That username is free.');
      expect(save()?.disabled).toBe(false);

      type('username', 'harbor');
      await settle();
      expect(say()).toBe('We could not check that name. Change it to try again.');
      expect(save()?.disabled).toBe(true);

      expect(postedTo(passwordMethods.usernameFree)).toEqual([
        { username: 'rey' },
        { username: 'gale.r' },
        { username: 'harbor' },
      ]);
    });

    it("says the server's words for a name it does not keep, keeps Save locked, and asks about the next name", async () => {
      const words = 'A username is text on one line, with no tab or other control character.';
      await open({ hold: [passwordMethods.usernameFree] });
      await openLine('username');
      type('username', 'gale\tr');
      await drain();
      held[0]?.answer({ status: 400, body: { message: words } });
      await drain();
      expect(say()).toBe(words);
      expect(save()?.disabled).toBe(true);
      expect(page().refusal()).toBeUndefined();

      type('username', 'gale.r');
      await drain();
      held[1]?.answer({ status: 200, body: { free: true } });
      await drain();
      expect(say()).toBe('That username is free.');
      expect(save()?.disabled).toBe(false);
      expect(held.map((h) => h.body)).toEqual([{ username: 'gale\tr' }, { username: 'gale.r' }]);
    });

    it('asks no second question while one is out, draws no answer for an older text, and asks again for the text the box holds', async () => {
      await open({ hold: [passwordMethods.usernameFree] });
      await openLine('username');
      type('username', 'rey');
      await drain();
      expect(held.map((h) => h.body)).toEqual([{ username: 'rey' }]);
      expect(say()).toBe('Checking…');
      expect(save()?.disabled).toBe(true);

      type('username', 'gale.r');
      await drain();
      expect(held).toHaveLength(1);
      expect(say()).toBe('Checking…');

      held[0]?.answer({ status: 200, body: { free: false } });
      await drain();
      expect(held.map((h) => h.body)).toEqual([{ username: 'rey' }, { username: 'gale.r' }]);
      expect(say()).toBe('Checking…');
      expect(save()?.disabled).toBe(true);

      held[1]?.answer({ status: 200, body: { free: true } });
      await drain();
      expect(say()).toBe('That username is free.');
      expect(save()?.disabled).toBe(false);

      // The name last answered is not asked again, whatever space is around it; any other is.
      type('username', ' gale.r  ');
      await drain();
      expect(say()).toBe('That username is free.');
      expect(held).toHaveLength(2);
      type('username', 'rey');
      await drain();
      expect(held.map((h) => h.body)).toEqual([{ username: 'rey' }, { username: 'gale.r' }, { username: 'rey' }]);
      expect(say()).toBe('Checking…');
      held[2]?.answer({ status: 200, body: { free: false } });
      await drain();
      expect(say()).toBe('Someone else has that username.');
    });

    it('saves the name without the space around it, reads the session again, and says to sign in with it', async () => {
      await open();
      await openLine('username');
      type('username', ' gale.r ');
      await settle();
      const reads = sessionReads();
      await page().saveUsername();
      await settle();
      expect(postedTo(passwordMethods.changeUsername)).toEqual([{ username: 'gale.r' }]);
      expect(sessionReads()).toBe(reads + 1);
      expect(TestBed.inject(AuthService).sessionInfo().username).toBe('gale.r');
      expect(line('username')?.querySelector('.impulse-account-page-form')).toBeNull();
      expect(inLine('username', 'value')).toBe('gale.r');
      expect(inLine('username', 'said')).toBe('Your username is gale.r. Sign in with it from now on.');
    });

    it('says only the new username to a person who holds no password', async () => {
      await open({ means: means(false, ['Lakeside Google']) });
      await openLine('username');
      type('username', 'gale.r');
      await settle();
      await page().saveUsername();
      await settle();
      expect(inLine('username', 'said')).toBe('Your username is gale.r.');
    });

    it("shows the server's words when it refuses the name, and keeps the line open", async () => {
      await open({
        answers: {
          [passwordMethods.changeUsername]: { status: 409, body: { message: 'Someone else holds that username.' } },
        },
      });
      await openLine('username');
      type('username', 'gale.r');
      await settle();
      await page().saveUsername();
      await settle();
      expect(inLine('username', 'refusal')).toBe('Someone else holds that username.');
      expect(line('username')?.querySelector('.impulse-account-page-refusal')?.getAttribute('role')).toBe('alert');
      expect(box('username')?.value).toBe('gale.r');
      expect(inLine('username', 'said')).toBeUndefined();
    });

    it('offers no change of the username where the server serves no question whether a name is free', async () => {
      await open({ methods: { ...everything, usernameFree: undefined } });
      expect(lines()[0]).toBe('username');
      expect(icon('username')).toBeNull();
    });
  });

  describe('Name', () => {
    it('opens with the name in a box that takes a name, and saves it as typed, says so and reads the session again', async () => {
      await open({ answers: { [passwordMethods.changeName]: { status: 200, body: { name: 'Gale R.' } } } });
      await openLine('name');
      expect(label('name')).toBe('Name');
      expect(box('personName')?.value).toBe('Gale Reyes');
      expect(box('personName')?.getAttribute('autocomplete')).toBe('name');
      expect(box('personName')?.getAttribute('maxlength')).toBe('200');
      type('personName', '  Gale R. ');
      await settle();
      await page().saveName();
      await settle();
      expect(postedTo(passwordMethods.changeName)).toEqual([{ name: '  Gale R. ' }]);
      expect(inLine('name', 'said')).toBe('Your name is saved.');
      expect(TestBed.inject(AuthService).sessionInfo().name).toBe('Gale R.');
      expect(inLine('name', 'value')).toBe('Gale R.');
    });

    it('takes the name off with an empty box, and says what is shown instead', async () => {
      await open({ answers: { [passwordMethods.changeName]: { status: 200, body: {} } } });
      await openLine('name');
      type('personName', '');
      await settle();
      await page().saveName();
      await settle();
      expect(postedTo(passwordMethods.changeName)).toEqual([{ name: '' }]);
      expect(inLine('name', 'said')).toBe('Your name is removed. Your username is shown instead.');
      expect(inLine('name', 'value')).toBe('Not set');
    });

    it("shows the server's words when it refuses the name, and keeps what was typed", async () => {
      await open({
        answers: {
          [passwordMethods.changeName]: { status: 400, body: { message: 'A name is at most 200 characters.' } },
        },
      });
      await openLine('name');
      type('personName', 'Gale the Long');
      await settle();
      await page().saveName();
      await settle();
      expect(inLine('name', 'refusal')).toBe('A name is at most 200 characters.');
      expect(box('personName')?.value).toBe('Gale the Long');
    });
  });

  describe('Email', () => {
    it('asks for a new address where a confirmed one is held, mails the link, and says it waits for it to be opened', async () => {
      await open();
      await openLine('email');
      expect(label('email')).toBe('New email address');
      const input = box('email');
      expect(input?.value).toBe('');
      expect(input?.getAttribute('type')).toBe('email');
      expect(input?.getAttribute('autocomplete')).toBe('email');
      expect(input?.required).toBe(true);
      expect(inLine('email', 'hint')).toBe('We mail a link to the new address. Nothing changes until you open it.');
      expect(text('.impulse-account-page-email button.impulse-account-page-save')).toBe('Send the link');
      type('email', 'gale@harbor.example');
      await settle();
      await page().sendLink();
      await settle();
      expect(postedTo(emailRoutes.add)).toEqual([{ email: 'gale@harbor.example' }]);
      expect(line('email')?.querySelector('.impulse-account-page-form')).toBeNull();
      expect(inLine('email', 'said')).toBe('Waiting for you to open the link we mailed to gale@harbor.example.');
    });

    it('starts with the unconfirmed address, else with the address a directory asserted', async () => {
      await open({ session: { ...gale, emailVerified: false } });
      await openLine('email');
      expect(label('email')).toBe('Email address');
      expect(box('email')?.value).toBe('gale@lakeside.edu');
      TestBed.resetTestingModule();
      await open({ session: { authenticated: true, username: 'gale', emailHint: 'gale@lakeside.edu' } });
      await openLine('email');
      expect(label('email')).toBe('Email address');
      expect(box('email')?.value).toBe('gale@lakeside.edu');
    });

    it('says the new address is what the person signs in with where the username is the address', async () => {
      await open({
        methods: { ...everything, usernameIsEmail: true },
        session: { ...gale, username: 'gale@lakeside.edu' },
      });
      await openLine('email');
      expect(inLine('email', 'hint')).toBe(
        'We mail a link to the new address. When you open it, you sign in with the new address from then on.',
      );
    });

    it("says the application's sentence with the wait for a mailbox sent too many links, and holds the button for the wait", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        await open({ answers: { [emailRoutes.add]: tooManyEmails } });
        await openLine('email');
        type('email', 'gale@harbor.example');
        await settle();
        await page().sendLink();
        await settle();
        expect(inLine('email', 'refusal')).toBe(`${DEFAULT_LOGIN_MESSAGES['too_many_emails']} Try again in 2 minutes.`);
        expect(buttonIn('email', 'save')?.disabled).toBe(true);
        vi.advanceTimersByTime(120_000);
        await settle();
        expect(buttonIn('email', 'save')?.disabled).toBe(false);
      } finally {
        vi.useRealTimers();
      }
    });

    it("shows the server's words for any other refusal", async () => {
      await open({ answers: { [emailRoutes.add]: { status: 400, body: { message: 'not an email address' } } } });
      await openLine('email');
      type('email', 'gale');
      await settle();
      await page().sendLink();
      await settle();
      expect(inLine('email', 'refusal')).toBe('not an email address');
      expect(buttonIn('email', 'save')?.disabled).toBe(false);
    });
  });

  describe('Password', () => {
    const changeButton = (): HTMLButtonElement | null => buttonIn('password', 'save');

    it('asks for the current password beside one box for the new one, each with its show control, and says what a change does', async () => {
      await open();
      await openLine('password');
      expect(element().querySelectorAll('impulse-new-password')).toHaveLength(1);
      expect(element().querySelectorAll('input[name="newPassword"]')).toHaveLength(1);
      expect(label('password')).toContain('Current password');
      expect(box('oldPassword')?.getAttribute('autocomplete')).toBe('current-password');
      expect(box('oldPassword')?.type).toBe('password');
      const show = buttonIn('password', 'show');
      expect(show?.getAttribute('aria-label')).toBe('Show the password');
      expect(show?.getAttribute('aria-pressed')).toBe('false');
      await press(show);
      expect(box('oldPassword')?.type).toBe('text');
      expect(buttonIn('password', 'show')?.getAttribute('aria-label')).toBe('Hide the password');
      expect(buttonIn('password', 'show')?.getAttribute('aria-pressed')).toBe('true');
      expect(line('password')?.querySelector('.impulse-new-password-show')).not.toBeNull();
      expect(inLine('password', 'hint')).toBe('Changing it signs you out of every other browser.');
      expect(changeButton()?.textContent?.trim()).toBe('Change password');
    });

    it('keeps Change password locked until the current password is given and the server calls the new one strong enough', async () => {
      await open({ strength: weak });
      await openLine('password');
      type('newPassword', 'harbor');
      await settle();
      expect(changeButton()?.disabled).toBe(true);
      type('oldPassword', 'old harbor');
      await settle();
      expect(changeButton()?.disabled).toBe(true);
      strengthAnswer = strong;
      type('newPassword', 'a harbor of my own');
      await settle();
      expect(changeButton()?.disabled).toBe(false);
      type('oldPassword', '');
      await settle();
      expect(changeButton()?.disabled).toBe(true);
      await page().changePassword();
      expect(posted()).not.toContain(passwordMethods.changePassword);
    });

    it('changes the password, reads the session again and says so', async () => {
      await open();
      await openLine('password');
      type('oldPassword', 'old harbor');
      type('newPassword', 'a harbor of my own');
      await settle();
      const reads = sessionReads();
      await page().changePassword();
      await settle();
      expect(postedTo(passwordMethods.changePassword)).toEqual([
        { oldPassword: 'old harbor', newPassword: 'a harbor of my own' },
      ]);
      expect(sessionReads()).toBe(reads + 1);
      expect(line('password')?.querySelector('.impulse-account-page-form')).toBeNull();
      expect(inLine('password', 'said')).toBe('Your password is changed. Every other browser is signed out.');
    });

    it("shows the server's words when it refuses the change, and keeps the line open", async () => {
      await open({
        answers: { [passwordMethods.changePassword]: { status: 401, body: { message: 'That is not your password.' } } },
      });
      await openLine('password');
      type('oldPassword', 'wrong');
      type('newPassword', 'a harbor of my own');
      await settle();
      await page().changePassword();
      await settle();
      expect(inLine('password', 'refusal')).toBe('That is not your password.');
      expect(line('password')?.querySelector('.impulse-account-page-form')).not.toBeNull();
    });

    it('offers the removal under the buttons where the server serves it, locked until the current password is given', async () => {
      await open({ means: means(true, ['Lakeside Google']) });
      await openLine('password');
      expect(text('.impulse-account-page-password .impulse-account-page-form')).toContain(
        'If you also sign in another way, you can do without a password.',
      );
      const remove = (): HTMLButtonElement | null => buttonIn('password', 'remove');
      expect(remove()?.textContent?.trim()).toBe('Remove your password');
      expect(remove()?.disabled).toBe(true);
      type('oldPassword', 'old harbor');
      await settle();
      expect(remove()?.disabled).toBe(false);
    });

    it('offers no removal to a person no directory is linked to, nor where the server serves none', async () => {
      await open();
      await openLine('password');
      expect(buttonIn('password', 'remove')).toBeNull();
      TestBed.resetTestingModule();
      await open({ methods: { ...everything, removePassword: undefined }, means: means(true, ['Lakeside Google']) });
      await openLine('password');
      expect(buttonIn('password', 'remove')).toBeNull();
    });

    it('asks before it removes, in place of the buttons, and asks the server nothing when the person keeps the password', async () => {
      await open({ means: means(true, ['Lakeside Google']) });
      await openLine('password');
      type('oldPassword', 'old harbor');
      await settle();
      await press(buttonIn('password', 'remove'));
      expect(inLine('password', 'remove-ask')).toBe(
        'Remove your password? You will sign in through Lakeside Google only.',
      );
      expect(buttonIn('password', 'remove-confirm')?.textContent?.trim()).toBe('Remove password');
      expect(buttonIn('password', 'remove-keep')?.textContent?.trim()).toBe('Keep password');
      expect(changeButton()).toBeNull();
      expect(buttonIn('password', 'cancel')).toBeNull();
      expect(buttonIn('password', 'remove')).toBeNull();
      await press(buttonIn('password', 'remove-keep'));
      expect(inLine('password', 'remove-ask')).toBeUndefined();
      expect(changeButton()).not.toBeNull();
      expect(buttonIn('password', 'remove')).not.toBeNull();
      expect(postedTo(passwordMethods.removePassword)).toEqual([]);
    });

    it('removes the password with the current password as proof, says what is left, and reads what the person signs in with again', async () => {
      await open({ means: means(true, ['Lakeside Google']) });
      await openLine('password');
      type('oldPassword', 'old harbor');
      await settle();
      await press(buttonIn('password', 'remove'));
      await press(buttonIn('password', 'remove-confirm'));
      expect(postedTo(passwordMethods.removePassword)).toEqual([{ password: 'old harbor' }]);
      expect(inLine('password', 'said')).toBe(
        'Your password is removed. You will sign in through Lakeside Google only.',
      );
      // What the person signs in with was read again: no password is held now, and a first one may be chosen.
      expect(TestBed.inject(Account).holdsPassword()).toBe(false);
      expect(inLine('password', 'value')).toBe('None');
      expect(icon('password')?.getAttribute('aria-label')).toBe('Choose a password');
      expect(inLine('sign-in', 'hint')).toBe('It is your only way in. A password of your own would be a second.');
    });

    it('goes on saying the password is removed where no first password is offered in its place', async () => {
      await open({ methods: { ...everything, firstPassword: undefined }, means: means(true, ['Lakeside Google']) });
      await openLine('password');
      type('oldPassword', 'old harbor');
      await settle();
      await press(buttonIn('password', 'remove'));
      await press(buttonIn('password', 'remove-confirm'));
      expect(inLine('password', 'said')).toBe(
        'Your password is removed. You will sign in through Lakeside Google only.',
      );
      expect(icon('password')).toBeNull();
      await openLine('name');
      expect(line('password')).toBeNull();
    });

    it("takes the question away and shows the server's words when it refuses the removal", async () => {
      await open({
        means: means(true, ['Lakeside Google']),
        answers: {
          [passwordMethods.removePassword]: { status: 403, body: { message: 'Your password is your only way in.' } },
        },
      });
      await openLine('password');
      type('oldPassword', 'old harbor');
      await settle();
      await press(buttonIn('password', 'remove'));
      await press(buttonIn('password', 'remove-confirm'));
      expect(inLine('password', 'remove-ask')).toBeUndefined();
      expect(inLine('password', 'refusal')).toBe('Your password is your only way in.');
      expect(changeButton()).not.toBeNull();
      expect(inLine('password', 'value')).toBeUndefined();
    });
  });

  describe('a first password', () => {
    const openFirst = async (setup: Setup = {}): Promise<void> => {
      await open({ means: means(false, ['Lakeside Google']), ...setup });
      await openLine('password');
    };
    const setButton = (): HTMLButtonElement | null => buttonIn('password', 'save');

    it('says a code comes first, mails it, and asks for the code and one new password', async () => {
      await openFirst();
      expect(text('.impulse-account-page-password .impulse-account-page-form')).toContain(
        'To make sure it is you, we mail a code to the address on your account before you choose a password.',
      );
      expect(element().querySelector('impulse-new-password')).toBeNull();
      expect(buttonIn('password', 'cancel')).not.toBeNull();
      await press(buttonIn('password', 'first-code'));
      expect(buttonIn('password', 'first-code')).toBeNull();
      expect(postedTo(firstPasswordRoutes.sendCode)).toHaveLength(1);
      expect(inLine('password', 'say')).toBe('We mailed a code to g•••@lakeside.edu.');
      const code = box('firstPasswordCode');
      expect(label('password')).toContain('Code from the mail');
      expect(code?.getAttribute('inputmode')).toBe('numeric');
      expect(code?.getAttribute('autocomplete')).toBe('one-time-code');
      expect(code?.getAttribute('maxlength')).toBe('6');
      expect(element().querySelectorAll('input[name="newPassword"]')).toHaveLength(1);
      expect(inLine('password', 'hint')).toBe(
        'You keep signing in through Lakeside Google. The password is a second way in.',
      );
      expect(buttonIn('password', 'first-again')?.textContent?.trim()).toBe('Mail me a new code');
      expect(setButton()?.textContent?.trim()).toBe('Set the password');
    });

    it('names the organization where no directory is known', async () => {
      await openFirst({ means: means(false) });
      await press(buttonIn('password', 'first-code'));
      expect(inLine('password', 'hint')).toBe(
        'You keep signing in through your organization. The password is a second way in.',
      );
    });

    it('keeps Set the password locked until the code is six digits and the server calls the password strong enough', async () => {
      await openFirst({ strength: weak });
      await press(buttonIn('password', 'first-code'));
      type('firstPasswordCode', '123456');
      type('newPassword', 'harbor');
      await settle();
      expect(setButton()?.disabled).toBe(true);
      strengthAnswer = strong;
      type('newPassword', 'a harbor of my own');
      type('firstPasswordCode', '12345');
      await settle();
      expect(setButton()?.disabled).toBe(true);
      type('firstPasswordCode', '12345a');
      await settle();
      expect(setButton()?.disabled).toBe(true);
      type('firstPasswordCode', '123456');
      await settle();
      expect(setButton()?.disabled).toBe(false);
    });

    it('mails a new code on asking', async () => {
      await openFirst();
      await press(buttonIn('password', 'first-code'));
      await press(buttonIn('password', 'first-again'));
      expect(postedTo(firstPasswordRoutes.sendCode)).toHaveLength(2);
    });

    it('sets the password with the code, says so, and shows the password held', async () => {
      await openFirst();
      await press(buttonIn('password', 'first-code'));
      type('firstPasswordCode', '123456');
      type('newPassword', 'a harbor of my own');
      await settle();
      await page().setFirstPassword();
      await settle();
      expect(postedTo(firstPasswordRoutes.set)).toEqual([{ code: '123456', password: 'a harbor of my own' }]);
      expect(inLine('password', 'said')).toBe('Your password is set. You can sign in with it from now on.');
      expect(inLine('password', 'value')).toBe('••••••••••');
      expect(icon('password')?.getAttribute('aria-label')).toBe('Change your password');
      expect(inLine('sign-in', 'hint')).toBe('You can also sign in with your password.');
    });

    it("shows the server's words when it refuses the code or the password, and keeps the line open", async () => {
      await openFirst({
        answers: {
          [firstPasswordRoutes.sendCode]: { status: 409, body: { message: 'Your account has no confirmed address.' } },
        },
      });
      await press(buttonIn('password', 'first-code'));
      expect(inLine('password', 'refusal')).toBe('Your account has no confirmed address.');
      expect(box('firstPasswordCode')).toBeNull();
      TestBed.resetTestingModule();
      await openFirst({
        answers: { [firstPasswordRoutes.set]: { status: 400, body: { message: 'That is not the code we sent.' } } },
      });
      await press(buttonIn('password', 'first-code'));
      type('firstPasswordCode', '654321');
      type('newPassword', 'a harbor of my own');
      await settle();
      await page().setFirstPassword();
      await settle();
      expect(inLine('password', 'refusal')).toBe('That is not the code we sent.');
      expect(box('firstPasswordCode')?.value).toBe('654321');
    });
  });

  it("asks about the new password with the session's username, name and address, in either form", async () => {
    const asked = { password: 'a harbor of my own', username: 'gale', name: 'Gale Reyes', email: 'gale@lakeside.edu' };
    await open();
    await openLine('password');
    type('newPassword', 'a harbor of my own');
    await settle();
    expect(strengthAsked().at(-1)).toEqual(asked);
    TestBed.resetTestingModule();
    await open({ means: means(false, ['Lakeside Google']) });
    await openLine('password');
    await press(buttonIn('password', 'first-code'));
    type('newPassword', 'a harbor of my own');
    await settle();
    expect(strengthAsked().at(-1)).toEqual(asked);
  });

  describe('Where you are signed in', () => {
    it('lists where the person is signed in, this browser first with its chip, each with its icon, its name and its times', async () => {
      await open({ sessions: [here, laptop, phone, unnamed] });
      expect(text('.impulse-account-page-signed-in h2')).toBe('Where you are signed in');
      expect(sessionNames()).toEqual([
        'Firefox on Linux This browser',
        'Chrome on macOS',
        'Safari on iPhone',
        'Unknown browser',
      ]);
      expect(sessionItems().map((item) => item.classList.contains('impulse-account-page-session-current'))).toEqual([
        true,
        false,
        false,
        false,
      ]);
      expect(sessionItems().map((item) => item.querySelector('mat-icon')?.textContent?.trim())).toEqual([
        'computer',
        'computer',
        'smartphone',
        'computer',
      ]);
      expect(sessionItems().map((item) => item.querySelector('mat-icon')?.getAttribute('aria-hidden'))).toEqual([
        'true',
        'true',
        'true',
        'true',
      ]);
      expect(sessionItems().map((item) => ofSession(item, 'session-when'))).toEqual([
        'Signed in Nov 3, 2026, 9:05 AM.',
        'Signed in Nov 10, 2026, 2:30 PM. Last used Nov 21, 2026, 8:00 AM.',
        'Signed in Nov 1, 2026, 7:15 AM. Last used Nov 20, 2026, 7:45 PM.',
        'Signed in Nov 2, 2026, 12:00 PM. Last used Nov 2, 2026, 12:30 PM.',
      ]);
      // This browser's line carries no button where it is not remembered; another's signs out of it.
      expect(sessionItems()[0]?.querySelector('button')).toBeNull();
      expect(
        sessionItems()
          .slice(1)
          .map((item) => [
            item.querySelector('button.impulse-account-page-sign-out')?.textContent?.trim(),
            item.querySelector('button.impulse-account-page-sign-out')?.getAttribute('aria-label'),
          ]),
      ).toEqual([
        ['Sign out', 'Sign out of Chrome on macOS'],
        ['Sign out', 'Sign out of Safari on iPhone'],
        ['Sign out', 'Sign out of that browser'],
      ]);
      expect(sectionButton('sign-out-others')?.textContent?.trim()).toBe('Sign out everywhere else');
      expect(sectionButton('sign-out-others')?.disabled).toBe(false);
      expect(sectionButton('forget-all')).toBeNull();
      expect(sessionsSaid()).toBeUndefined();
    });

    it('draws the section once the list has answered', async () => {
      await open({ hold: [passwordMethods.ownSessions.list], sessions: [here, laptop] });
      expect(lines()).toEqual(['username', 'name', 'email', 'password']);
      expect(element().querySelector('.impulse-account-page-signed-in')).toBeNull();
      held[0]?.answer({ status: 200, body: { sessions: [here, laptop], rememberedBrowsers: 0 } });
      await drain();
      expect(sessionNames()).toEqual(['Firefox on Linux This browser', 'Chrome on macOS']);
    });

    it('says it could not read the list where the read fails', async () => {
      await open({
        sessions: [here, laptop],
        answers: { [passwordMethods.ownSessions.list]: { status: 500, body: { message: 'the read failed' } } },
      });
      expect(text('.impulse-account-page-signed-in h2')).toBe('Where you are signed in');
      expect(text('.impulse-account-page-sessions-failed')).toBe(
        'We could not read where you are signed in. Reload the page to try again.',
      );
      expect(element().querySelector('.impulse-account-page-sessions')).toBeNull();
      expect(sectionButton('sign-out-others')).toBeNull();
    });

    it('draws no section in an impersonated session, nor where the server names no list', async () => {
      await open({ session: { ...gale, impersonation }, sessions: [here, laptop] });
      expect(element().querySelector('.impulse-account-page-signed-in')).toBeNull();
      expect(listReads()).toBe(0);
      TestBed.resetTestingModule();
      await open({ methods: { ...everything, ownSessions: undefined }, sessions: [here, laptop] });
      expect(element().querySelector('.impulse-account-page-signed-in')).toBeNull();
      expect(listReads()).toBe(0);
    });

    it("signs out of another browser at once, posting that line's id alone, and reads the list again", async () => {
      await open({ sessions: [here, laptop, phone] });
      const reads = listReads();
      await press(signOutOf('Sign out of Chrome on macOS'));
      expect(postedTo(passwordMethods.ownSessions.end)).toEqual([{ sessionId: 's-laptop' }]);
      expect(listReads()).toBe(reads + 1);
      expect(sessionNames()).toEqual(['Firefox on Linux This browser', 'Safari on iPhone']);
      expect(sessionsSaid()).toBe('Signed out of Chrome on macOS.');
    });

    it('says a browser that was signed out already is signed out, and one without a name as that browser', async () => {
      await open({
        sessions: [here, unnamed],
        answers: { [passwordMethods.ownSessions.end]: { status: 404, body: { message: 'That session is gone.' } } },
      });
      await press(signOutOf('Sign out of that browser'));
      expect(postedTo(passwordMethods.ownSessions.end)).toEqual([{ sessionId: 's-unnamed' }]);
      expect(sessionsSaid()).toBe('Signed out of that browser.');
    });

    it('disables the buttons of the list while a request is out', async () => {
      await open({ sessions: [here, laptop, phone], hold: [passwordMethods.ownSessions.end] });
      signOutOf('Sign out of Chrome on macOS')?.click();
      await drain();
      const buttons = Array.from(
        element().querySelectorAll<HTMLButtonElement>('.impulse-account-page-signed-in button'),
      );
      expect(buttons.length).toBeGreaterThan(0);
      expect(buttons.every((button) => button.disabled)).toBe(true);
      held[0]?.answer({ status: 200, body: null });
      await settle();
      expect(signOutOf('Sign out of Safari on iPhone')?.disabled).toBe(false);
    });

    for (const tt of [
      {
        name: 'a sign-out of one browser',
        path: passwordMethods.ownSessions.end,
        act: (p: AccountPageComponent) => {
          const other = p.sessions().find((session) => session.id === laptop.id);
          return other ? p.signOut(other) : Promise.reject(new Error('the list holds no laptop'));
        },
      },
      {
        name: 'a sign-out everywhere else',
        path: passwordMethods.ownSessions.endOthers,
        act: (p: AccountPageComponent) => p.signOutOthers(),
      },
      {
        name: 'a forget of this browser',
        path: passwordMethods.forgetBrowser,
        act: (p: AccountPageComponent) => p.forgetBrowser(),
      },
      {
        name: 'a forget of every browser',
        path: passwordMethods.forgetBrowsers,
        act: (p: AccountPageComponent) => p.forgetAllBrowsers(),
      },
    ]) {
      it(`says under the list that an act the server fails did not work, reads the list again and enables the buttons: ${tt.name}`, async () => {
        await open({
          session: { ...gale, browserRemembered: true },
          sessions: [here, laptop, phone],
          rememberedBrowsers: 2,
          answers: { [tt.path]: { status: 500, body: { message: 'the server failed' } } },
        });
        const reads = listReads();
        await expect(tt.act(page())).resolves.toBeUndefined();
        await settle();
        expect(postedTo(tt.path)).toHaveLength(1);
        expect(sessionsSaid()).toBe('That did not work. Try again in a moment.');
        expect(listReads()).toBe(reads + 1);
        const buttons = Array.from(
          element().querySelectorAll<HTMLButtonElement>('.impulse-account-page-signed-in button'),
        );
        expect(buttons.length).toBeGreaterThan(0);
        expect(buttons.every((button) => !button.disabled)).toBe(true);
      });
    }

    for (const tt of [
      { name: 'two', sessions: [here, laptop, phone], ended: undefined, want: 'Signed out of 2 other browsers.' },
      { name: 'one', sessions: [here, laptop], ended: undefined, want: 'Signed out of 1 other browser.' },
      { name: 'none', sessions: [here, laptop], ended: 0, want: undefined },
    ]) {
      it(`signs out everywhere else and says how many browsers it signed out: ${tt.name}`, async () => {
        await open({
          sessions: tt.sessions,
          answers:
            tt.ended === undefined
              ? {}
              : { [passwordMethods.ownSessions.endOthers]: { status: 200, body: { ended: tt.ended } } },
        });
        const reads = listReads();
        await press(sectionButton('sign-out-others'));
        expect(postedTo(passwordMethods.ownSessions.endOthers)).toHaveLength(1);
        expect(listReads()).toBe(reads + 1);
        expect(sessionsSaid()).toBe(tt.want);
      });
    }

    it('reads "You are signed in nowhere else", disabled, where the list holds this browser alone', async () => {
      await open({ sessions: [here, laptop] });
      await press(sectionButton('sign-out-others'));
      expect(sessionNames()).toEqual(['Firefox on Linux This browser']);
      expect(sectionButton('sign-out-others')?.textContent?.trim()).toBe('You are signed in nowhere else');
      expect(sectionButton('sign-out-others')?.disabled).toBe(true);
    });

    it("offers Forget on this browser's line where it is remembered, forgets it at once and says what comes next", async () => {
      await open({ session: { ...gale, browserRemembered: true }, sessions: [here, laptop], rememberedBrowsers: 1 });
      expect(ofSession(sessionItems()[0], 'session-when')).toBe(
        'Signed in Nov 3, 2026, 9:05 AM. Remembered, so signing in here skips the emailed code.',
      );
      const forget = sessionItems()[0]?.querySelector<HTMLButtonElement>('button.impulse-account-page-forget') ?? null;
      expect(forget?.textContent?.trim()).toBe('Forget');
      expect(forget?.getAttribute('aria-label')).toBe('Forget this browser');
      expect(sessionItems()[1]?.querySelector('button.impulse-account-page-forget')).toBeNull();
      const reads = { session: sessionReads(), list: listReads() };
      await press(forget);
      expect(postedTo(passwordMethods.forgetBrowser)).toHaveLength(1);
      expect(sessionReads()).toBe(reads.session + 1);
      expect(listReads()).toBe(reads.list + 1);
      expect(sessionsSaid()).toBe('This browser will ask for the emailed code the next time you sign in here.');
      expect(sectionButton('forget')).toBeNull();
      expect(ofSession(sessionItems()[0], 'session-when')).toBe('Signed in Nov 3, 2026, 9:05 AM.');
    });

    it('offers no Forget where this browser is not remembered, nor where the server serves no forgetting', async () => {
      await open({ session: { ...gale, browserRemembered: false }, rememberedBrowsers: 1 });
      expect(sectionButton('forget')).toBeNull();
      TestBed.resetTestingModule();
      await open({
        methods: { ...everything, forgetBrowser: undefined },
        session: { ...gale, browserRemembered: true },
        rememberedBrowsers: 1,
      });
      expect(sectionButton('forget')).toBeNull();
      expect(ofSession(sessionItems()[0], 'session-when')).toBe('Signed in Nov 3, 2026, 9:05 AM.');
    });

    it('forgets every remembered browser at once where one is remembered, and says what comes next', async () => {
      await open({ session: { ...gale, browserRemembered: true }, sessions: [here, laptop], rememberedBrowsers: 2 });
      expect(sectionButton('forget-all')?.textContent?.trim()).toBe('Forget all remembered browsers');
      const reads = { session: sessionReads(), list: listReads() };
      await press(sectionButton('forget-all'));
      expect(postedTo(passwordMethods.forgetBrowsers)).toHaveLength(1);
      expect(sessionReads()).toBe(reads.session + 1);
      expect(listReads()).toBe(reads.list + 1);
      expect(sessionsSaid()).toBe('Every browser will ask for the emailed code the next time you sign in there.');
      expect(sectionButton('forget-all')).toBeNull();
      expect(sectionButton('forget')).toBeNull();
    });

    it('offers no Forget all where none is remembered, nor where the server names no path', async () => {
      await open({ rememberedBrowsers: 0 });
      expect(element().querySelector('.impulse-account-page-signed-in')).not.toBeNull();
      expect(sectionButton('forget-all')).toBeNull();
      TestBed.resetTestingModule();
      await open({ methods: { ...everything, forgetBrowsers: undefined }, rememberedBrowsers: 2 });
      expect(element().querySelector('.impulse-account-page-signed-in')).not.toBeNull();
      expect(sectionButton('forget-all')).toBeNull();
    });

    it('says a change of the password signed out every other browser, counting none, and reads the list again', async () => {
      for (const sessions of [[here, laptop, phone], [here]]) {
        TestBed.resetTestingModule();
        await open({ sessions });
        await openLine('password');
        type('oldPassword', 'old harbor');
        type('newPassword', 'a harbor of my own');
        await settle();
        const reads = listReads();
        await page().changePassword();
        await settle();
        expect(inLine('password', 'said')).toBe('Your password is changed. Every other browser is signed out.');
        expect(listReads()).toBe(reads + 1);
        expect(sessionNames()).toEqual(['Firefox on Linux This browser']);
      }
    });

    it('draws no part "This browser" of its own: the list carries it', async () => {
      await open({ session: { ...gale, browserRemembered: true }, rememberedBrowsers: 1 });
      expect(element().querySelector('.impulse-account-page-browser')).toBeNull();
      expect(element().textContent).not.toContain('This browser is remembered for you');
      expect(element().querySelectorAll('button.impulse-account-page-forget')).toHaveLength(1);
    });
  });

  describe('Your tenants', () => {
    /** The text of an element of the leave window, or undefined while no window is open. */
    const windowText = (part: string): string | undefined => inWindow(part)?.textContent?.replace(/\s+/g, ' ').trim();
    /** Presses Escape where the window listens for it. */
    const escape = (): void => {
      const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true });
      Object.defineProperty(event, 'keyCode', { get: () => 27 });
      document.body.dispatchEvent(event);
    };
    /** The parts of the page in order: the heading, then each part by its class. */
    const parts = (): string[] =>
      Array.from(element().querySelector('section.impulse-account-page-content')?.children ?? []).map(
        (child) => child.className || child.tagName.toLowerCase(),
      );
    const tenantNames = (): (string | undefined)[] => tenantItems().map((item) => ofTenant(item, 'tenant-name'));

    it('lists the tenants under "Your customers" between the lines and where the person is signed in, each with its roles or "No role" and Leave named for it', async () => {
      await open({ tenants: [acme, beta] });
      expect(parts()).toEqual([
        'h1',
        'impulse-account-page-lines',
        'impulse-account-page-tenants',
        'impulse-account-page-signed-in',
      ]);
      expect(text('.impulse-account-page-tenants h2')).toBe('Your customers');
      expect(tenantItems().map((item) => [ofTenant(item, 'tenant-name'), ofTenant(item, 'tenant-roles')])).toEqual([
        ['Acme', 'Reader, Warden'],
        ['Beta', 'No role'],
      ]);
      expect(
        tenantItems().map((item) => {
          const button = item.querySelector('button.impulse-account-page-leave');
          return [button?.textContent?.trim(), button?.getAttribute('aria-label')];
        }),
      ).toEqual([
        ['Leave', 'Leave Acme'],
        ['Leave', 'Leave Beta'],
      ]);
      expect(element().querySelector('.impulse-account-page-tenant-refusal')).toBeNull();
      expect(tenantsSaid()).toBeUndefined();
      expect(tenantReads()).toBe(1);
    });

    it('heads one tenant in the singular', async () => {
      await open({ tenants: [acme] });
      expect(text('.impulse-account-page-tenants h2')).toBe('Your customer');
    });

    it('says why under the roles of a tenant the person cannot leave, in place of Leave', async () => {
      await open({ tenants: [acme, gamma] });
      const row = tenantItems()[1];
      expect(ofTenant(row, 'tenant-name')).toBe('Gamma');
      expect(ofTenant(row, 'tenant-roles')).toBe('Administrator');
      expect(ofTenant(row, 'tenant-refusal')).toBe(gamma.refusal);
      expect(row?.querySelector('button')).toBeNull();
      expect(leaveOf('Acme')).not.toBeNull();
    });

    it('draws no section for an empty list, in an impersonated session, nor where the server names no list', async () => {
      await open();
      expect(element().querySelector('.impulse-account-page-tenants')).toBeNull();
      expect(tenantReads()).toBe(1);
      TestBed.resetTestingModule();
      await open({ session: { ...gale, impersonation }, tenants: [acme] });
      expect(element().querySelector('.impulse-account-page-tenants')).toBeNull();
      expect(tenantReads()).toBe(0);
      TestBed.resetTestingModule();
      await open({ methods: { ...everything, ownTenants: undefined }, tenants: [acme] });
      expect(element().querySelector('.impulse-account-page-tenants')).toBeNull();
      expect(tenantReads()).toBe(0);
    });

    it('says it could not read the list where the read fails', async () => {
      await open({
        tenants: [acme],
        answers: { [tenantRoutes.list]: { status: 500, body: { message: 'the read failed' } } },
      });
      expect(text('.impulse-account-page-tenants h2')).toBe('Your customers');
      expect(text('.impulse-account-page-tenants-failed')).toBe(
        'We could not read your customers. Reload the page to try again.',
      );
      expect(tenantItems()).toEqual([]);
    });

    it('asks in a window of its own named for the tenant, and asks the server nothing on Cancel, on Escape or on a press outside', async () => {
      await open({ tenants: [acme, beta] });
      const closings: [string, () => void][] = [
        ['Cancel', () => inWindow<HTMLButtonElement>('cancel')?.click()],
        ['Escape', escape],
        ['a press outside', () => document.querySelector<HTMLElement>('.cdk-overlay-backdrop')?.click()],
      ];
      for (const [name, close] of closings) {
        await press(leaveOf('Acme'));
        expect(windowText('title'), name).toBe('Leave Acme?');
        expect(windowText('what'), name).toBe(
          'You lose every role you hold in Acme (Reader, Warden), right away. Only an administrator there can bring you back.',
        );
        await typeInWindow('Delete me');
        close();
        await drain();
        await settle();
        expect(inWindow('title'), name).toBeNull();
      }
      expect(postedTo(tenantRoutes.leave)).toEqual([]);
      expect(tenantNames()).toEqual(['Acme', 'Beta']);
      expect(tenantsSaid()).toBeUndefined();
    });

    it('opens the window in the form that deletes the account where the list says leaving deletes it', async () => {
      await open({ tenants: [only] });
      await press(leaveOf('Acme'));
      expect(windowText('title')).toBe('Leave Acme and delete your account?');
      expect(windowText('what')).toBe(
        'Acme is your only customer. Leaving it deletes your account: your sign-in and every role you hold. You are signed out everywhere. This cannot be undone.',
      );
      await typeInWindow('Delete me');
      expect(inWindow<HTMLButtonElement>('leave')?.disabled).toBe(true);
      await typeInWindow('Delete my account');
      expect(inWindow<HTMLButtonElement>('leave')?.disabled).toBe(false);
    });

    it('leaves once the words are typed, posting the key and what the list said, reads the permissions and the list again, and says so', async () => {
      await open({ tenants: [acme, beta] });
      const went = watchNavigations();
      const domains = domainReads();
      const reads = tenantReads();
      await leaveWith('Acme', ' delete ME ');
      expect(postedTo(tenantRoutes.leave)).toEqual([{ domain: 't-acme', deletesAccount: false }]);
      expect(domainReads()).toBe(domains + 1);
      expect(tenantReads()).toBe(reads + 1);
      expect(tenantNames()).toEqual(['Beta']);
      expect(text('.impulse-account-page-tenants h2')).toBe('Your customer');
      expect(tenantsSaid()).toBe('You left Acme.');
      expect(went()).toEqual([]);
      expect(inWindow('title')).toBeNull();
    });

    it('goes on saying the person left a tenant where the list is then empty', async () => {
      await open({ tenants: [beta] });
      watchNavigations();
      await leaveWith('Beta', 'Delete me');
      expect(text('.impulse-account-page-tenants h2')).toBe('Your customers');
      expect(element().querySelector('.impulse-account-page-tenant-list')).toBeNull();
      expect(tenantsSaid()).toBe('You left Beta.');
    });

    it('takes a person who left the tenant the page is in to the first page of the place they land in', async () => {
      await open({ tenants: [acme, beta] });
      const went = watchNavigations();
      vi.spyOn(TestBed.inject(PlaceService), 'current').mockReturnValue({ id: acme.domain, label: acme.name });
      const reads = tenantReads();
      await leaveWith('Acme', 'Delete me');
      expect(postedTo(tenantRoutes.leave)).toEqual([{ domain: 't-acme', deletesAccount: false }]);
      expect(went()).toEqual(['/tenants/t-beta/dashboard']);
      expect(tenantReads()).toBe(reads);
      expect(tenantsSaid()).toBeUndefined();
    });

    it('goes where Sign out goes where the account went with the tenant', async () => {
      await open({ tenants: [only] });
      const went = watchNavigations();
      const domains = domainReads();
      await leaveWith('Acme', 'delete my account');
      expect(postedTo(tenantRoutes.leave)).toEqual([{ domain: 't-acme', deletesAccount: true }]);
      expect(went()).toEqual(['/logout']);
      expect(domainReads()).toBe(domains);
    });

    it("says the server's words where it refuses the leave, and reads the list again", async () => {
      const refusal = 'Leaving Acme would no longer do what this page said. Read the page again.';
      await open({
        tenants: [acme, beta],
        answers: { [tenantRoutes.leave]: { status: 409, body: { message: refusal } } },
      });
      const went = watchNavigations();
      const reads = tenantReads();
      await leaveWith('Acme', 'Delete me');
      expect(postedTo(tenantRoutes.leave)).toEqual([{ domain: 't-acme', deletesAccount: false }]);
      expect(tenantsSaid()).toBe(refusal);
      expect(tenantReads()).toBe(reads + 1);
      expect(tenantNames()).toEqual(['Acme', 'Beta']);
      expect(went()).toEqual([]);
    });

    it('says a leave the server fails did not work, reads the list again and enables the buttons', async () => {
      await open({
        tenants: [acme, beta],
        answers: { [tenantRoutes.leave]: { status: 500, body: { message: 'the leave failed' } } },
      });
      const went = watchNavigations();
      const reads = tenantReads();
      await leaveWith('Acme', 'Delete me');
      expect(tenantsSaid()).toBe('That did not work. Try again in a moment.');
      expect(tenantReads()).toBe(reads + 1);
      expect(leaveOf('Acme')?.disabled).toBe(false);
      expect(went()).toEqual([]);
    });

    it('disables the buttons of the page while the leave is out', async () => {
      await open({ tenants: [acme, beta], sessions: [here, laptop], hold: [tenantRoutes.leave] });
      watchNavigations();
      await press(leaveOf('Acme'));
      await typeInWindow('Delete me');
      inWindow<HTMLButtonElement>('leave')?.click();
      await drain();
      const buttons = Array.from(
        element().querySelectorAll<HTMLButtonElement>(
          '.impulse-account-page-tenants button, .impulse-account-page-signed-in button, button.impulse-account-page-edit',
        ),
      );
      expect(buttons.length).toBeGreaterThan(3);
      expect(buttons.every((button) => button.disabled)).toBe(true);
      held[0]?.answer({ status: 200, body: { accountDeleted: false } });
      await settle();
      expect(leaveOf('Beta')?.disabled).toBe(false);
      expect(tenantsSaid()).toBe('You left Acme.');
    });
  });

  for (const tt of [
    {
      name: 'the one other way in by its words',
      others: ['Lakeside'],
      want: 'You will sign in through Lakeside only.',
    },
    { name: 'two by both', others: ['Lakeside', 'Google'], want: 'You will sign in through Lakeside or Google only.' },
    {
      name: 'several as a list',
      others: ['Lakeside', 'Google', 'Entra'],
      want: 'You will sign in through Lakeside, Google or Entra only.',
    },
    {
      name: "the person's organization where the page knows of none",
      others: [],
      want: 'You will sign in through your organization only.',
    },
  ]) {
    it(`says what removing the password leaves: ${tt.name}`, () => {
      expect(passwordRemovalConsequence(tt.others)).toBe(tt.want);
    });
  }
});
