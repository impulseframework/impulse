import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';
import { AccountPageComponent, passwordsDiffer } from './account-page.component';
import { passwordMethods, provideFrameTesting, SpecServer, specTransport } from './frame-testing';

// The person's account page holds a section only where their sign-in offers it: their name
// where the server serves its change and the session is their own, the password change
// where the auth has the password method, forgetting this browser where it is remembered
// for them and the server serves forgetting it. A server that offers none of them leaves
// the page saying there is nothing to change.

interface Setup {
  methods?: Record<string, unknown>;
  remembered?: boolean;
  /** More of the session read's answer: the person's name, an impersonation record. */
  session?: Record<string, unknown>;
  /** What the server answers the change of the name with. */
  nameAnswer?: TransportResponse;
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

  const open = async ({ methods = {}, remembered = false, session = {}, nameAnswer }: Setup): Promise<void> => {
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
    expect(text('.name h2')).toBe('Your name');
    expect(text('.name p')).toBe('Shown for you in place of your username, gale, which you still sign in with.');
    expect(box('personName')?.value).toBe('Gale Reyes');
    expect(box('personName')?.getAttribute('maxlength')).toBe('200');
    expect(element().querySelector('.none')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(true);
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
    expect(text('.name .message')).toBe('Your name is saved.');
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
    expect(text('.name .message')).toBe('Your name is removed. Your username is shown instead.');
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
    expect(text('.name .message')).toBe('a name is at most 200 characters');
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
    expect(text('.name .message')).toBe('Your name is saved.');
    Object.assign(nameAnswer, { status: 500, body: { message: 'the server failed' } });
    type('personName', 'Gale Reyes');
    await expect(fixture.componentInstance.changeName()).rejects.toThrow();
    await settle();
    expect(element().querySelector('.name .message')).toBeNull();
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
    expect(element().querySelector('.name')).toBeNull();
    expect(TestBed.inject(Account).name()).toBe(false);
    expect(text('.none')).toBe('Your sign-in is managed by your organization, so there is nothing to change here.');
  });

  it('offers the password change to a person whose auth has the password method', async () => {
    await open({ methods: { changePassword: passwordMethods.changePassword } });
    expect(text('.password h2')).toBe('Change your password');
    expect(element().querySelector('.browser')).toBeNull();
    expect(element().querySelector('.none')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(true);
  });

  it('says there is nothing to change for a sign-in through the organization', async () => {
    await open({});
    expect(text('.none')).toBe('Your sign-in is managed by your organization, so there is nothing to change here.');
    expect(element().querySelector('.password')).toBeNull();
    expect(TestBed.inject(Account).any()).toBe(false);
  });

  it('refuses two new passwords that differ without asking the server', async () => {
    await open({ methods: { changePassword: passwordMethods.changePassword } });
    type('oldPassword', 'old');
    type('newPassword', 'new one');
    type('newPasswordAgain', 'new two');
    await fixture.componentInstance.changePassword();
    fixture.detectChanges();
    expect(text('.password .message')).toBe(passwordsDiffer);
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
    expect(text('.password .message')).toBe('Your password is changed.');
  });

  it('offers no forgetting where the browser is not remembered', async () => {
    await open({ methods: { forgetBrowser: passwordMethods.forgetBrowser }, remembered: false });
    expect(element().querySelector('.browser')).toBeNull();
  });

  it('forgets a remembered browser at the path the server names, and says what comes next', async () => {
    await open({ methods: { forgetBrowser: passwordMethods.forgetBrowser }, remembered: true });
    expect(text('.browser button.forget')).toBe('Forget this browser');
    await fixture.componentInstance.forgetBrowser();
    fixture.detectChanges();
    expect(posted()).toEqual([passwordMethods.forgetBrowser]);
    expect(text('.browser .message')).toBe(
      'This browser will ask for the emailed code the next time you sign in here.',
    );
    expect(element().querySelector('.browser button.forget')).toBeNull();
  });
});
