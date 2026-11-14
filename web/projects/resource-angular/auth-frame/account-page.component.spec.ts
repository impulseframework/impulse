import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';
import { AccountPageComponent, passwordsDiffer } from './account-page.component';
import { passwordMethods, provideFrameTesting, specTransport } from './frame-testing';

// The person's account page holds a section only where their sign-in offers it: the
// password change where the auth has the password method, forgetting this browser where it
// is remembered for them and the server serves forgetting it. A sign-in through the
// person's organization offers neither, and the page says there is nothing to change.

interface Setup {
  methods?: Record<string, unknown>;
  remembered?: boolean;
}

describe('AccountPageComponent', () => {
  let fixture: ComponentFixture<AccountPageComponent>;
  let transport: ScriptedTransport;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (selector: string): string | undefined =>
    element().querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim();
  const posted = (): string[] => transport.requests.filter((r) => r.method === 'POST').map((r) => r.url);
  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  };

  const open = async ({ methods = {}, remembered = false }: Setup): Promise<void> => {
    transport = specTransport({
      methods: { methods: [], ...methods },
      session: { authenticated: true, username: 'gale', browserRemembered: remembered },
      other: (url) => (url.pathname.startsWith('/api/user/') ? { status: 200, body: null } : undefined),
    });
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
