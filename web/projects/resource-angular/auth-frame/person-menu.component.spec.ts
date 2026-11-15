import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ApiDescriptor } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import {
  issuingDescriptor,
  passwordMethods,
  provideFrameTesting,
  specDescriptor,
  specTransport,
} from './frame-testing';
import { PersonMenuComponent } from './person-menu.component';

// The person's menu holds what is theirs: their account where the page holds something for
// them, their API keys where the application issues tokens, the application's own entries,
// Appearance, and Sign out last. Both pages are every place's: they open where the person is.

interface Setup {
  methods?: Record<string, unknown>;
  descriptor?: ApiDescriptor;
  config?: Partial<FrameConfig>;
  /** The session read's answer, where the spec's own is not the one wanted. */
  session?: Record<string, unknown>;
}

describe('PersonMenuComponent', () => {
  let fixture: ComponentFixture<PersonMenuComponent>;

  const open = async ({ methods = {}, descriptor = specDescriptor, config = {}, session }: Setup): Promise<void> => {
    const transport = specTransport({ methods: { methods: [], ...methods }, ...(session ? { session } : {}) });
    await TestBed.configureTestingModule({
      imports: [PersonMenuComponent],
      providers: [provideFrameTesting(transport, config, [], descriptor)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(PersonMenuComponent);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  };

  const labels = (): string[][] =>
    fixture.componentInstance.groups().map((group) => group.entries.map((entry) => entry.label));

  it('draws the initial and the name', async () => {
    await open({});
    const button = (fixture.nativeElement as HTMLElement).querySelector('button.person');
    expect(button?.querySelector('.initial')?.textContent).toBe('G');
    expect(button?.querySelector('.name')?.textContent).toBe('gale');
    expect(button?.getAttribute('aria-label')).toBe('Signed in as gale. Your menu');
  });

  it("draws the person's name where they hold one, its initial, and says who is signed in by it", async () => {
    await open({ session: { authenticated: true, username: 'gale', name: 'Rey Gale', email: 'gale@spec.example' } });
    const button = (fixture.nativeElement as HTMLElement).querySelector('button.person');
    expect(button?.querySelector('.initial')?.textContent).toBe('R');
    expect(button?.querySelector('.name')?.textContent).toBe('Rey Gale');
    expect(button?.getAttribute('aria-label')).toBe('Signed in as Rey Gale. Your menu');
    expect(fixture.componentInstance.username()).toBe('gale');
  });

  it('draws a whole first character, whatever the name starts with', async () => {
    for (const [name, initial] of [
      ['\u{20BB7}田 Gale', '\u{20BB7}'],
      ['e\u0301lan', 'E\u0301'],
      ['\u{1F1E8}\u{1F1E6} Gale', '\u{1F1E8}\u{1F1E6}'],
    ]) {
      TestBed.resetTestingModule();
      await open({ session: { authenticated: true, username: 'gale', name } });
      expect(fixture.componentInstance.initial()).toBe(initial);
    }
  });

  it('heads the menu with the name and puts the username under it, and with the username alone for a person without a name', async () => {
    const who = (selector: string): string | undefined =>
      document.querySelector(`.cdk-overlay-container .who ${selector}`)?.textContent ?? undefined;
    const openMenu = async (): Promise<void> => {
      (fixture.nativeElement as HTMLElement).querySelector<HTMLButtonElement>('button.person')?.click();
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
    };
    await open({ session: { authenticated: true, username: 'gale', name: 'Rey Gale', email: 'gale@spec.example' } });
    await openMenu();
    expect(who('.who-name')).toBe('Rey Gale');
    expect(who('.who-login')).toBe('gale');
    expect(who('.who-email')).toBe('gale@spec.example');

    TestBed.resetTestingModule();
    await open({ session: { authenticated: true, username: 'gale' } });
    await openMenu();
    expect(who('.who-name')).toBe('gale');
    expect(who('.who-login')).toBeUndefined();
  });

  it('falls back to the username for a name that is empty', async () => {
    await open({ session: { authenticated: true, username: 'gale', name: '' } });
    expect(fixture.componentInstance.name()).toBe('gale');
    expect(fixture.componentInstance.initial()).toBe('G');
  });

  it('offers the account page for the name alone, where the server serves its change', async () => {
    await open({ methods: { changeName: passwordMethods.changeName } });
    expect(labels()).toEqual([['Your account'], ['Appearance'], ['Sign out']]);
  });

  it('holds only Appearance and Sign out for a person with no account section and no tokens', async () => {
    await open({});
    expect(labels()).toEqual([['Appearance'], ['Sign out']]);
  });

  it('offers the account page where it holds something, opened where the person is', async () => {
    await open({ methods: { changePassword: passwordMethods.changePassword } });
    expect(labels()).toEqual([['Your account'], ['Appearance'], ['Sign out']]);
    expect(fixture.componentInstance.groups()[0]?.entries[0]?.link).toEqual(['account']);
  });

  it('offers Your API keys where the application issues tokens', async () => {
    await open({ descriptor: issuingDescriptor });
    expect(labels()).toEqual([['Your API keys'], ['Appearance'], ['Sign out']]);
    expect(fixture.componentInstance.groups()[0]?.entries[0]?.link).toEqual(['api-keys']);
  });

  it("puts the application's own entries above Appearance", async () => {
    await open({
      methods: { changePassword: passwordMethods.changePassword },
      descriptor: issuingDescriptor,
      config: {
        person: [{ entries: [{ label: 'Notifications', icon: 'notifications', run: (): void => undefined }] }],
      },
    });
    expect(labels()).toEqual([['Your account', 'Your API keys'], ['Notifications'], ['Appearance'], ['Sign out']]);
  });
});
