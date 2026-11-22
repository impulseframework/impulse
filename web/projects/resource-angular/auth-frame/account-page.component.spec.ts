import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { DEFAULT_LOGIN_MESSAGES } from '@impulseframework/resource-angular/types';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';
import { AccountPageComponent, passwordRemovalConsequence } from './account-page.component';
import { notNamedFor, passwordMethods, provideFrameTesting, SpecServer, specTransport } from './frame-testing';

// The person's account page shows their lines: the username where it is not the address,
// the name, the email address, a password where they hold one or may choose one, and the
// directories they sign in through. Each line the person may change carries an icon that
// opens it as a form in place, one line at a time; what the server serves and what the
// person signs in with decide which lines carry one. An impersonated session changes
// nothing.

/** Where the spec's server serves the email address routes. */
const emailRoutes = { add: '/api/user/email', verify: '/api/user/email/verify' };
/** Where the spec's server serves the choice of a first password. */
const firstPasswordRoutes = { sendCode: '/api/user/first-password/code', set: '/api/user/first-password' };
/** Everything the spec's server serves for a person's own account. */
const everything: Record<string, unknown> = {
  ...passwordMethods,
  email: emailRoutes,
  firstPassword: firstPasswordRoutes,
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
  }: Setup = {}): Promise<void> => {
    let meansNow = meansAnswer;
    strengthAnswer = strength;
    held = [];
    const server: SpecServer = {
      methods: { methods: [], ...methods },
      session: { ...session },
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
      await open({ means: means(true, ['Lakeside Google']) });
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
      expect(inLine('password', 'said')).toBe('Your password is changed.');
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

  // The part stays as it was until the list of where the person is signed in takes it.
  describe('This browser', () => {
    it('is not drawn where the browser is not remembered', async () => {
      await open({ session: { ...gale, browserRemembered: false } });
      expect(element().querySelector('.impulse-account-page-browser')).toBeNull();
    });

    it('forgets a remembered browser at the path the server names, and says what comes next', async () => {
      await open({ session: { ...gale, browserRemembered: true } });
      expect(text('.impulse-account-page-browser p')).toBe(
        'This browser is remembered for you, so signing in here skips the emailed code.',
      );
      const reads = sessionReads();
      await press(element().querySelector<HTMLButtonElement>('button.impulse-account-page-forget'));
      expect(postedTo(passwordMethods.forgetBrowser)).toHaveLength(1);
      expect(sessionReads()).toBe(reads + 1);
      expect(text('.impulse-account-page-browser .impulse-account-page-message')).toBe(
        'This browser will ask for the emailed code the next time you sign in here.',
      );
      expect(element().querySelector('button.impulse-account-page-forget')).toBeNull();
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
