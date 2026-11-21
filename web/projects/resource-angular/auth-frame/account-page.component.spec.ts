import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { SignInMeans, TransportResponse } from '@impulseframework/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';
import {
  AccountPageComponent,
  firstPasswordsDiffer,
  passwordRemovalConsequence,
  passwordsDiffer,
} from './account-page.component';
import { notNamedFor, passwordMethods, provideFrameTesting, SpecServer, specTransport } from './frame-testing';

// The person's account page holds a section only where their sign-in offers it: their name
// where the server serves its change and the session is their own, the password change
// where the auth has the password method, the removal of the password where the server
// serves it and the session is their own, forgetting this browser where it is remembered
// for them and the server serves forgetting it. A person who holds no password chooses a
// first one where the server serves it and the session is their own. A server that offers
// none of them leaves the page saying there is nothing to change.

/** Where the spec's server serves the choice of a first password. */
const firstPasswordRoutes = { sendCode: '/api/user/first-password/code', set: '/api/user/first-password' };

interface Setup {
  methods?: Record<string, unknown>;
  remembered?: boolean;
  /** More of the session read's answer: the person's name, an impersonation record. */
  session?: Record<string, unknown>;
  /** What the server answers the change of the name with. */
  nameAnswer?: TransportResponse;
  /** What the server answers the removal of the password with; unset, it removes it. */
  removeAnswer?: TransportResponse;
  /**
   * What the server answers the read of what the person signs in with; unset, it answers
   * nothing the page can read, as a server that serves no such read. A removal the server
   * makes is answered from then on: the person holds no password.
   */
  means?: TransportResponse;
  /** What the server answers a request for the code of a first password with; unset, it mails one. */
  firstPasswordCode?: TransportResponse;
  /**
   * What the server answers the first password with; unset, it sets it, and the read of
   * what the person signs in with answers a password from then on.
   */
  firstPasswordSet?: TransportResponse;
}

describe('AccountPageComponent', () => {
  let fixture: ComponentFixture<AccountPageComponent>;
  let transport: ScriptedTransport;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (selector: string): string | undefined =>
    element().querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim();
  const posted = (): string[] => transport.requests.filter((r) => r.method === 'POST').map((r) => r.url);
  // A form box takes its model's value a moment after the view is drawn: settle waits it out.
  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
  };

  const open = async ({
    methods = {},
    remembered = false,
    session = {},
    nameAnswer,
    removeAnswer,
    means,
    firstPasswordCode,
    firstPasswordSet,
  }: Setup): Promise<void> => {
    let meansNow = means;
    // The session the spec's server answers: a saved name is answered from then on, in an
    // answer of its own, as a server's every read is.
    const server: SpecServer = {
      methods: { methods: [], ...methods },
      session: { authenticated: true, username: 'gale', browserRemembered: remembered, ...session },
      other: (url) => {
        if (url.pathname === passwordMethods.changeName && nameAnswer) {
          if (nameAnswer.status === 200) {
            server.session = { ...server.session, name: (nameAnswer.body as { name?: string } | null)?.name ?? null };
          }
          return nameAnswer;
        }
        if (url.pathname === passwordMethods.removePassword) {
          if (removeAnswer) {
            return removeAnswer;
          }
          if (meansNow?.status === 200) {
            meansNow = { status: 200, body: { ...(meansNow.body as object), password: false } };
          }
          return { status: 200, body: null };
        }
        if (url.pathname === firstPasswordRoutes.sendCode) {
          return firstPasswordCode ?? { status: 200, body: { codeEmail: 'g•••@lakeside.edu' } };
        }
        if (url.pathname === firstPasswordRoutes.set) {
          if (firstPasswordSet) {
            return firstPasswordSet;
          }
          if (meansNow?.status === 200) {
            meansNow = { status: 200, body: { ...(meansNow.body as object), password: true } };
          }
          return { status: 200, body: {} };
        }
        if (url.pathname === passwordMethods.signInMeans && meansNow) {
          return meansNow;
        }
        return url.pathname.startsWith('/api/user/') ? { status: 200, body: null } : undefined;
      },
    };
    transport = specTransport(server);
    await TestBed.configureTestingModule({
      imports: [AccountPageComponent],
      providers: [provideFrameTesting(transport)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(AccountPageComponent);
    fixture.detectChanges();
    await settle();
  };

  const type = (name: string, value: string): void => {
    const input = element().querySelector<HTMLInputElement>(`input[name="${name}"]`);
    if (!input) throw new Error(`no ${name} field`);
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };

  const box = (name: string): HTMLInputElement | null =>
    element().querySelector<HTMLInputElement>(`input[name="${name}"]`);

  it("offers the person's name where the server serves its change, the box holding the name they have", async () => {
    await open({ methods: { changeName: passwordMethods.changeName }, session: { name: 'Gale Reyes' } });
    expect(text('.impulse-account-page-name h2')).toBe('Your name');
    expect(text('.impulse-account-page-name p')).toBe(
      'Shown for you in place of your username, gale, which you still sign in with.',
    );
    expect(box('personName')?.value).toBe('Gale Reyes');
    expect(box('personName')?.getAttribute('maxlength')).toBe('200');
    expect(element().querySelector('.impulse-account-page-none')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(true);
  });

  it("names every element for the account page: an application's rule for its own .name, .none or .message lands on none", async () => {
    await open({
      methods: {
        changeName: passwordMethods.changeName,
        changePassword: passwordMethods.changePassword,
        forgetBrowser: passwordMethods.forgetBrowser,
      },
      remembered: true,
      session: { name: 'Gale Reyes' },
    });

    expect(notNamedFor('impulse-account-page-', element())).toEqual([]);
    expect(element().querySelector('section.impulse-account-page-content')).not.toBeNull();
    expect(text('.impulse-account-page-browser button.impulse-account-page-forget')).toBe('Forget this browser');
  });

  it('starts the box empty for a person without a name', async () => {
    await open({ methods: { changeName: passwordMethods.changeName } });
    expect(box('personName')?.value).toBe('');
  });

  it('saves the name at the path the server names, says so, and shows the name the server kept', async () => {
    await open({
      methods: { changeName: passwordMethods.changeName },
      nameAnswer: { status: 200, body: { name: 'Gale R.' } },
    });
    type('personName', '  Gale R. ');
    await fixture.componentInstance.changeName();
    await settle();
    expect(posted()).toEqual([passwordMethods.changeName]);
    expect(transport.requests.find((r) => r.method === 'POST')?.body).toEqual({ name: '  Gale R. ' });
    expect(text('.impulse-account-page-name .impulse-account-page-message')).toBe('Your name is saved.');
    // The session was read again: it carries the name, and the box follows it.
    expect(TestBed.inject(AuthService).sessionInfo().name).toBe('Gale R.');
    expect(box('personName')?.value).toBe('Gale R.');
  });

  it('takes the name off with an empty box, and says what is shown instead', async () => {
    await open({
      methods: { changeName: passwordMethods.changeName },
      session: { name: 'Gale Reyes' },
      nameAnswer: { status: 200, body: {} },
    });
    type('personName', '');
    await fixture.componentInstance.changeName();
    await settle();
    expect(transport.requests.find((r) => r.method === 'POST')?.body).toEqual({ name: '' });
    expect(text('.impulse-account-page-name .impulse-account-page-message')).toBe(
      'Your name is removed. Your username is shown instead.',
    );
    expect(TestBed.inject(AuthService).sessionInfo().name ?? undefined).toBeUndefined();
  });

  it("shows the server's words when it refuses the name, and keeps what was typed", async () => {
    await open({
      methods: { changeName: passwordMethods.changeName },
      session: { name: 'Gale Reyes' },
      nameAnswer: { status: 400, body: { message: 'a name is at most 200 characters' } },
    });
    type('personName', 'Gale Reyes the Second');
    await fixture.componentInstance.changeName();
    await settle();
    expect(text('.impulse-account-page-name .impulse-account-page-message')).toBe('a name is at most 200 characters');
    expect(box('personName')?.value).toBe('Gale Reyes the Second');
    expect(TestBed.inject(AuthService).sessionInfo().name).toBe('Gale Reyes');
  });

  it('leaves what the person is typing when the session is read again and answers the same name', async () => {
    await open({ methods: { changeName: passwordMethods.changeName }, session: { name: 'Gale Reyes' } });
    type('personName', 'Gale Reyes the Second');
    // The keepalive reads the session while the person is still typing.
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    await settle();
    expect(box('personName')?.value).toBe('Gale Reyes the Second');
  });

  it('says nothing of an earlier change once a later one fails without an answer', async () => {
    const nameAnswer: TransportResponse = { status: 200, body: { name: 'Gale R.' } };
    await open({ methods: { changeName: passwordMethods.changeName }, nameAnswer });
    type('personName', 'Gale R.');
    await fixture.componentInstance.changeName();
    await settle();
    expect(text('.impulse-account-page-name .impulse-account-page-message')).toBe('Your name is saved.');
    Object.assign(nameAnswer, { status: 500, body: { message: 'the server failed' } });
    type('personName', 'Gale Reyes');
    await expect(fixture.componentInstance.changeName()).rejects.toThrow();
    await settle();
    expect(element().querySelector('.impulse-account-page-name .impulse-account-page-message')).toBeNull();
    expect(fixture.componentInstance.busy()).toBe(false);
  });

  it('offers no change of the name in a session that acts for someone else', async () => {
    await open({
      methods: { changeName: passwordMethods.changeName },
      session: {
        name: 'Gale Reyes',
        impersonation: { actor: 'ops', principalKind: 'User', principal: 'gale', expiresAt: '2026-11-15T12:00:00Z' },
      },
    });
    expect(element().querySelector('.impulse-account-page-name')).toBeNull();
    expect(TestBed.inject(Account).name()).toBe(false);
    expect(text('.impulse-account-page-none')).toBe(
      'Your sign-in is managed by your organization, so there is nothing to change here.',
    );
  });

  it('offers the password change to a person whose auth has the password method', async () => {
    await open({ methods: { changePassword: passwordMethods.changePassword } });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(element().querySelector('.impulse-account-page-browser')).toBeNull();
    expect(element().querySelector('.impulse-account-page-none')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(true);
  });

  it('says there is nothing to change for a sign-in through the organization', async () => {
    await open({});
    expect(text('.impulse-account-page-none')).toBe(
      'Your sign-in is managed by your organization, so there is nothing to change here.',
    );
    expect(element().querySelector('.impulse-account-page-password')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(false);
  });

  it('refuses two new passwords that differ without asking the server', async () => {
    await open({ methods: { changePassword: passwordMethods.changePassword } });
    type('oldPassword', 'old');
    type('newPassword', 'new one');
    type('newPasswordAgain', 'new two');
    await fixture.componentInstance.changePassword();
    fixture.detectChanges();
    expect(text('.impulse-account-page-password .impulse-account-page-message')).toBe(passwordsDiffer);
    expect(posted()).toEqual([]);
  });

  it('changes the password at the path the server names, and says so', async () => {
    await open({ methods: { changePassword: passwordMethods.changePassword } });
    type('oldPassword', 'old');
    type('newPassword', 'new one');
    type('newPasswordAgain', 'new one');
    await fixture.componentInstance.changePassword();
    fixture.detectChanges();
    expect(posted()).toEqual([passwordMethods.changePassword]);
    expect(text('.impulse-account-page-password .impulse-account-page-message')).toBe('Your password is changed.');
  });

  // A sign-in that offers the password and one directory, serves the password's removal
  // and says what the person signs in with: here a password, and Lakeside linked to them.
  const lakeside = {
    name: 'lakeside',
    method: 'workos',
    kind: 'redirect',
    label: 'Lakeside',
    path: '/api/user/lakeside/login',
  };
  const removal = {
    methods: [lakeside],
    changePassword: passwordMethods.changePassword,
    removePassword: passwordMethods.removePassword,
    signInMeans: passwordMethods.signInMeans,
  };
  const linkedToLakeside: TransportResponse = {
    status: 200,
    body: { password: true, directories: [{ name: 'lakeside', label: 'Lakeside' }] },
  };

  it('offers the removal of the password where the server serves it, under the change', async () => {
    await open({ methods: removal, means: linkedToLakeside });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(text('.impulse-account-page-remove-password h2')).toBe('Remove your password');
    expect(text('.impulse-account-page-remove-password p')).toBe(
      'If you also sign in another way, you can do without a password.',
    );
    expect(text('.impulse-account-page-remove-password button.impulse-account-page-remove-submit')).toBe(
      'Remove password',
    );
    expect(TestBed.inject(Account).removePassword()).toBe(true);
  });

  it('offers no removal where the server serves none', async () => {
    await open({
      methods: {
        methods: [lakeside],
        changePassword: passwordMethods.changePassword,
        signInMeans: passwordMethods.signInMeans,
      },
      means: linkedToLakeside,
    });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(TestBed.inject(Account).removePassword()).toBe(false);
  });

  it('offers no removal in a session that acts for someone else', async () => {
    await open({
      methods: removal,
      means: linkedToLakeside,
      session: {
        impersonation: { actor: 'ops', principalKind: 'User', principal: 'gale', expiresAt: '2026-11-15T12:00:00Z' },
      },
    });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(TestBed.inject(Account).removePassword()).toBe(false);
  });

  it('asks before it removes, saying what is left to sign in through, and asks the server nothing when the person keeps the password', async () => {
    await open({ methods: removal, means: linkedToLakeside });
    type('removalPassword', 'old');
    (element().querySelector('button.impulse-account-page-remove-submit') as HTMLButtonElement).click();
    await settle();
    expect(text('.impulse-account-page-remove-password .impulse-account-page-question')).toBe(
      'Remove your password? You will sign in through Lakeside only.',
    );
    expect(text('.impulse-account-page-remove-password button.impulse-account-page-remove-confirm')).toBe(
      'Remove password',
    );
    expect(posted()).toEqual([]);
    (element().querySelector('button.impulse-account-page-remove-keep') as HTMLButtonElement).click();
    await settle();
    expect(element().querySelector('.impulse-account-page-remove-password .impulse-account-page-question')).toBeNull();
    expect(box('removalPassword')).not.toBeNull();
    expect(posted()).toEqual([]);
  });

  it('removes the password at the path the server names, with the password as proof, and says what is left', async () => {
    await open({ methods: removal, means: linkedToLakeside });
    type('removalPassword', 'old');
    fixture.componentInstance.askToRemovePassword();
    await fixture.componentInstance.removePassword();
    await settle();
    expect(posted()).toEqual([passwordMethods.removePassword]);
    expect(transport.requests.find((r) => r.method === 'POST')?.body).toEqual({ password: 'old' });
    expect(text('.impulse-account-page-password h2')).toBe('Your password');
    expect(text('.impulse-account-page-password .impulse-account-page-message')).toBe(
      'Your password is removed. You will sign in through Lakeside only.',
    );
    // Nothing is left to change or to remove.
    expect(box('oldPassword')).toBeNull();
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
  });

  it("shows the server's words when it refuses the removal, and leaves the password where it was", async () => {
    const onlyWay = 'Your password is your only way to sign in: sign in through your directory first';
    await open({
      methods: removal,
      means: linkedToLakeside,
      removeAnswer: { status: 409, body: { message: onlyWay } },
    });
    type('removalPassword', 'old');
    fixture.componentInstance.askToRemovePassword();
    await fixture.componentInstance.removePassword();
    await settle();
    expect(posted()).toEqual([passwordMethods.removePassword]);
    expect(text('.impulse-account-page-remove-password .impulse-account-page-message')).toBe(onlyWay);
    expect(element().querySelector('.impulse-account-page-remove-password .impulse-account-page-question')).toBeNull();
    expect(box('removalPassword')).not.toBeNull();
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(fixture.componentInstance.passwordRemoved()).toBe(false);
  });

  // The server says what the person signs in with: the login page offers Lakeside and
  // Fleet Google, and the person is linked to Fleet Google alone.
  const fleetGoogle = {
    name: 'google',
    method: 'google',
    kind: 'redirect',
    label: 'Fleet Google',
    path: '/api/user/google/login',
  };
  const told = { ...removal, methods: [lakeside, fleetGoogle], signInMeans: passwordMethods.signInMeans };
  const signsInWith = (password: boolean, linked: boolean): TransportResponse => ({
    status: 200,
    body: { password, directories: linked ? [{ name: 'google', label: 'Fleet Google' }] : [] },
  });

  it('offers the change and the removal to a person who holds a password and a linked directory, and names that directory alone', async () => {
    await open({ methods: told, means: signsInWith(true, true) });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(text('.impulse-account-page-remove-password h2')).toBe('Remove your password');
    type('removalPassword', 'old');
    (element().querySelector('button.impulse-account-page-remove-submit') as HTMLButtonElement).click();
    await settle();
    expect(text('.impulse-account-page-remove-password .impulse-account-page-question')).toBe(
      'Remove your password? You will sign in through Fleet Google only.',
    );
  });

  it('offers the change and no removal to a person who holds a password and no linked directory', async () => {
    await open({ methods: told, means: signsInWith(true, false) });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(TestBed.inject(Account).removePassword()).toBe(false);
  });

  it('offers neither the change nor the removal to a person who holds no password', async () => {
    await open({ methods: told, means: signsInWith(false, true) });
    expect(element().querySelector('.impulse-account-page-password')).toBeNull();
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(TestBed.inject(Account).password()).toBe(false);
    expect(TestBed.inject(Account).removePassword()).toBe(false);
    expect(text('.impulse-account-page-none')).toBe(
      'Your sign-in is managed by your organization, so there is nothing to change here.',
    );
  });

  it('holds no password section for a person whose auth registers no password method, beside the sections it does offer', async () => {
    // What the methods route of such an auth answers: its directory methods, the change of
    // the person's name and the read of what they sign in with, and none of the password
    // method's routes, which the server names only where the method is registered.
    await open({
      methods: {
        methods: [lakeside, fleetGoogle],
        changeName: passwordMethods.changeName,
        signInMeans: passwordMethods.signInMeans,
      },
      means: signsInWith(false, true),
    });
    expect(text('.impulse-account-page-name h2')).toBe('Your name');
    expect(element().querySelector('.impulse-account-page-password')).toBeNull();
    expect(element().querySelector('.impulse-account-page-first-password')).toBeNull();
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(element().querySelector('input[type="password"]')).toBeNull();
    expect(TestBed.inject(Account).password()).toBe(false);
    expect(TestBed.inject(Account).firstPassword()).toBe(false);
    expect(TestBed.inject(Account).removePassword()).toBe(false);
    expect(element().querySelector('.impulse-account-page-none')).toBeNull();
  });

  // The server serves the choice of a first password, as one whose auth has the password
  // method does.
  const choosing = { ...told, firstPassword: firstPasswordRoutes };
  const click = (selector: string): void => {
    const button = element().querySelector<HTMLButtonElement>(selector);
    if (!button) throw new Error(`no ${selector} button`);
    button.click();
  };

  it('offers a person who holds no password the choice of one where the server serves it, asking for the code first', async () => {
    await open({ methods: choosing, means: signsInWith(false, true) });
    expect(text('.impulse-account-page-first-password h2')).toBe('Choose a password');
    expect(text('.impulse-account-page-first-password p')).toBe(
      'You have no password here. To choose one, ask for a code first: we email it to the address on your account.',
    );
    expect(text('button.impulse-account-page-first-code')).toBe('Email me a code');
    // Nothing is typed until a code is on its way.
    expect(box('firstPasswordCode')).toBeNull();
    expect(box('firstPassword')).toBeNull();
    expect(element().querySelector('.impulse-account-page-password')).toBeNull();
    expect(element().querySelector('.impulse-account-page-none')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(true);
    expect(posted()).toEqual([]);
  });

  for (const tt of [
    {
      name: 'a person who holds a password, who changes it',
      setup: { methods: choosing, means: signsInWith(true, true) },
    },
    {
      name: 'a session that acts for someone else',
      setup: {
        methods: choosing,
        means: signsInWith(false, true),
        session: {
          impersonation: { actor: 'ops', principalKind: 'User', principal: 'gale', expiresAt: '2026-11-15T12:00:00Z' },
        },
      },
    },
    {
      name: 'a read of what the person signs in with that fails: no answer is no offer',
      setup: { methods: choosing, means: { status: 500, body: { message: 'the database is away' } } },
    },
    { name: 'a server that serves no first password', setup: { methods: told, means: signsInWith(false, true) } },
  ]) {
    it(`offers no choice of a first password to ${tt.name}`, async () => {
      await open(tt.setup);
      expect(element().querySelector('.impulse-account-page-first-password')).toBeNull();
      expect(TestBed.inject(Account).firstPassword()).toBe(false);
    });
  }

  it('mails the code at the path the server names and says where it went, then sets the password with the code and says so', async () => {
    await open({ methods: choosing, means: signsInWith(false, true) });
    click('button.impulse-account-page-first-code');
    await settle();
    expect(posted()).toEqual([firstPasswordRoutes.sendCode]);
    expect(text('.impulse-account-page-first-password .impulse-account-page-sent')).toBe(
      'We sent a code to g•••@lakeside.edu. Enter it with your password.',
    );
    expect(text('button.impulse-account-page-first-code')).toBe('Email me a new code');
    expect(text('button.impulse-account-page-first-submit')).toBe('Set the password');

    type('firstPasswordCode', '483920');
    type('firstPassword', 'a harbor of my own');
    type('firstPasswordAgain', 'a harbor of my own');
    await fixture.componentInstance.setFirstPassword();
    await settle();
    expect(posted()).toEqual([firstPasswordRoutes.sendCode, firstPasswordRoutes.set]);
    expect(transport.requests.filter((r) => r.method === 'POST')[1].body).toEqual({
      code: '483920',
      password: 'a harbor of my own',
    });
    expect(text('.impulse-account-page-password h2')).toBe('Your password');
    expect(text('.impulse-account-page-password .impulse-account-page-message')).toBe(
      'Your password is set. You can sign in with it from now on.',
    );
    // The choice is gone, and nothing says there is nothing to change.
    expect(element().querySelector('.impulse-account-page-first-password')).toBeNull();
    expect(element().querySelector('.impulse-account-page-none')).toBeNull();
    // The person holds a password now, wherever else the page is opened.
    expect(TestBed.inject(Account).firstPassword()).toBe(false);
    expect(TestBed.inject(Account).password()).toBe(true);
  });

  it('refuses two passwords that differ without asking the server', async () => {
    await open({ methods: choosing, means: signsInWith(false, true) });
    click('button.impulse-account-page-first-code');
    await settle();
    type('firstPasswordCode', '483920');
    type('firstPassword', 'a harbor of my own');
    type('firstPasswordAgain', 'a harbor of my owm');
    await fixture.componentInstance.setFirstPassword();
    await settle();
    expect(text('.impulse-account-page-first-password .impulse-account-page-refusal')).toBe(firstPasswordsDiffer);
    expect(posted()).toEqual([firstPasswordRoutes.sendCode]);
  });

  it("shows the server's words when it refuses the code, and asks for nothing more", async () => {
    const noAddress =
      'Your account has no confirmed email address, and the code to choose a password is sent to one. Add your email address, confirm it from the link we send you, then choose your password';
    await open({
      methods: choosing,
      means: signsInWith(false, true),
      firstPasswordCode: { status: 409, body: { message: noAddress } },
    });
    click('button.impulse-account-page-first-code');
    await settle();
    expect(text('.impulse-account-page-first-password .impulse-account-page-refusal')).toBe(noAddress);
    expect(element().querySelector('.impulse-account-page-first-password .impulse-account-page-sent')).toBeNull();
    expect(box('firstPasswordCode')).toBeNull();
    expect(text('button.impulse-account-page-first-code')).toBe('Email me a code');
  });

  it("shows the server's words when it refuses the password, and keeps the choice open with what was typed", async () => {
    const wrongCode = 'That is not the code we sent. Check it and try again';
    await open({
      methods: choosing,
      means: signsInWith(false, true),
      firstPasswordSet: { status: 400, body: { message: wrongCode } },
    });
    click('button.impulse-account-page-first-code');
    await settle();
    type('firstPasswordCode', '000000');
    type('firstPassword', 'a harbor of my own');
    type('firstPasswordAgain', 'a harbor of my own');
    await fixture.componentInstance.setFirstPassword();
    await settle();
    expect(text('.impulse-account-page-first-password .impulse-account-page-refusal')).toBe(wrongCode);
    expect(box('firstPasswordCode')?.value).toBe('000000');
    expect(text('.impulse-account-page-first-password h2')).toBe('Choose a password');
    expect(TestBed.inject(Account).firstPassword()).toBe(true);
  });

  it('offers the change and no removal where the read of what the person signs in with fails: no answer is no offer', async () => {
    await open({ methods: told, means: { status: 500, body: { message: 'the database is away' } } });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(TestBed.inject(Account).removePassword()).toBe(false);
    // The page names no way in the server did not say is the person's.
    expect(TestBed.inject(Account).otherSignIns()).toEqual([]);
  });

  it('offers the change and no removal where the server names no read of what the person signs in with', async () => {
    await open({
      methods: {
        methods: [lakeside, fleetGoogle],
        changePassword: passwordMethods.changePassword,
        removePassword: passwordMethods.removePassword,
      },
    });
    expect(text('.impulse-account-page-password h2')).toBe('Change your password');
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(TestBed.inject(Account).removePassword()).toBe(false);
    expect(TestBed.inject(Account).otherSignIns()).toEqual([]);
  });

  it('offers no password section while what the person signs in with is still being read, so none is drawn and then taken away', async () => {
    transport = specTransport({
      methods: told,
      session: { authenticated: true, username: 'gale' },
      other: (url) => (url.pathname.startsWith('/api/user/') ? { status: 200, body: null } : undefined),
    });
    await TestBed.configureTestingModule({ providers: [provideFrameTesting(transport)] }).compileComponents();
    // The read is held open: the page knows what the sign-in offers and not yet what the person holds.
    let answer: (means: SignInMeans) => void = () => undefined;
    const client = TestBed.inject(RESOURCE_CLIENT);
    client.auth.signInMeans = () => new Promise<SignInMeans>((resolve) => (answer = resolve));
    const account = TestBed.inject(Account);
    const turn = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

    account.password();
    await turn();
    await turn();
    expect([account.password(), account.removePassword()]).toEqual([false, false]);

    answer({ password: true, directories: [{ name: 'google', label: 'Fleet Google' }] });
    await turn();
    expect([account.password(), account.removePassword()]).toEqual([true, true]);
  });

  it('goes on saying what is left once the password is removed, and offers nothing more to change', async () => {
    await open({ methods: told, means: signsInWith(true, true) });
    type('removalPassword', 'old');
    fixture.componentInstance.askToRemovePassword();
    await fixture.componentInstance.removePassword();
    await settle();
    expect(text('.impulse-account-page-password .impulse-account-page-message')).toBe(
      'Your password is removed. You will sign in through Fleet Google only.',
    );
    expect(box('oldPassword')).toBeNull();
    expect(element().querySelector('.impulse-account-page-remove-password')).toBeNull();
    expect(element().querySelector('.impulse-account-page-none')).toBeNull();
    // The page read again what the person signs in with: no password is held now.
    expect(TestBed.inject(Account).password()).toBe(false);
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

  it('offers no forgetting where the browser is not remembered', async () => {
    await open({ methods: { forgetBrowser: passwordMethods.forgetBrowser }, remembered: false });
    expect(element().querySelector('.impulse-account-page-browser')).toBeNull();
  });

  it('forgets a remembered browser at the path the server names, and says what comes next', async () => {
    await open({ methods: { forgetBrowser: passwordMethods.forgetBrowser }, remembered: true });
    expect(text('.impulse-account-page-browser button.impulse-account-page-forget')).toBe('Forget this browser');
    await fixture.componentInstance.forgetBrowser();
    fixture.detectChanges();
    expect(posted()).toEqual([passwordMethods.forgetBrowser]);
    expect(text('.impulse-account-page-browser .impulse-account-page-message')).toBe(
      'This browser will ask for the emailed code the next time you sign in here.',
    );
    expect(element().querySelector('.impulse-account-page-browser button.impulse-account-page-forget')).toBeNull();
  });
});
