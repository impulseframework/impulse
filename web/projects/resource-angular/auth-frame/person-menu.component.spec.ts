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
}

describe('PersonMenuComponent', () => {
  let fixture: ComponentFixture<PersonMenuComponent>;

  const open = async ({ methods = {}, descriptor = specDescriptor, config = {} }: Setup): Promise<void> => {
    const transport = specTransport({ methods: { methods: [], ...methods } });
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
