import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, Router, convertToParamMap } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { LoginMethodsComponent } from '@impulseframework/resource-angular/auth-login-methods';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { IdleService } from '@impulseframework/resource-angular/auth-idle-service';
import { LoginMethods, TransportRequest } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { of } from 'rxjs';
import { CREW_MANIFEST, DEMO_PASSWORD, LoginComponent } from './login.component';

/** What the console outlet's methods route answers: the password and the fleet's directory, in the generator program's order. */
const CONSOLE_METHODS: LoginMethods = {
  methods: [
    {
      name: 'password',
      method: 'password',
      kind: 'password',
      label: 'Password',
      path: '/console/api/user/password/login',
    },
    { name: 'google', method: 'google', kind: 'redirect', label: 'Google', path: '/console/api/user/google/login' },
  ],
  pending: {
    status: '/console/api/user/pending',
    confirm: '/console/api/user/pending/confirm',
    cancel: '/console/api/user/pending/cancel',
  },
};

// Demonstrates: client.login-component.
//
// The page composes the library's login component around the crew manifest: the component
// reads the methods route through the application's client and renders the form and the
// Google button from it; the page ends no session, prefills the component's form
// from a manifest card, and on the component's signedIn starts the idle watch and navigates.
// The sign-in itself (the XSRF re-read, the post, the pending step) is the library's and is
// specified there.

describe('LoginComponent', () => {
  let fixture: ComponentFixture<LoginComponent>;
  let transport: ScriptedTransport;

  beforeEach(async () => {
    transport = scriptedTransport((request: TransportRequest) =>
      request.url.endsWith('/user/methods')
        ? { status: 200, body: CONSOLE_METHODS, headers: { 'x-api-version': 'v0.3.1' } }
        : { status: 200, body: undefined },
    );
    await TestBed.configureTestingModule({
      imports: [LoginComponent],
      // Every request goes through the resource client on the scripted transport, which answers
      // the methods route with the console's list and everything else with an empty 200, and
      // keeps the log. provideResourceTesting also provides the empty router the component
      // reads its refusal code from.
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(LoginComponent);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  });

  it("shows the release the server named on the methods answer, in the card's footer", () => {
    const release: HTMLElement | null = fixture.nativeElement.querySelector('mat-card-footer.release impulse-release');
    expect(release?.textContent?.trim()).toBe('v0.3.1');
  });

  it('creates, ending no session, and the component asks the server what the outlet offers and whether a session stands', () => {
    expect(fixture.componentInstance).toBeTruthy();
    expect(transport.requests.map((request) => `${request.method} ${request.url}`)).toEqual([
      `GET ${environment.apiUrl}/user/methods`,
      `GET ${environment.apiUrl}/user/session`,
    ]);
  });

  it('renders the password form and the Google button from the list, with the manifest beside them', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('impulse-login-methods input[name="username"]')).not.toBeNull();
    expect(element.querySelector('impulse-login-methods input[name="password"]')).not.toBeNull();
    const redirects = Array.from(element.querySelectorAll('button.impulse-login-redirect')).map((button) =>
      button.textContent?.trim(),
    );
    expect(redirects).toEqual(['Sign in with Google']);
    expect(element.querySelectorAll('button.persona')).toHaveLength(CREW_MANIFEST.length);
    expect(element.querySelector('.impulse-login-message')).toBeNull();
  });

  // On a narrow screen the manifest gives way: it may be as narrow as the screen, so its
  // cards go down to one column, and a note's words are one text that wraps beside its
  // icon. A manifest that could not shrink was cut off at both edges of a phone's screen.
  it('lets the manifest give way to a narrow screen, each note an icon and one text that wraps', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(getComputedStyle(element.querySelector('.manifest') as HTMLElement).minWidth).toBe('0px');
    const notes = Array.from(element.querySelectorAll('.droid'));
    expect(notes).toHaveLength(2);
    for (const note of notes) {
      expect(Array.from(note.children).map((child) => child.tagName.toLowerCase())).toEqual(['mat-icon', 'span']);
      const loose = Array.from(note.childNodes).filter(
        (node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim(),
      );
      expect(loose).toEqual([]);
    }
  });

  // The card stays at the top of the screen only where the manifest is beside it, on a
  // screen wide enough for both, and there the two are never put one under the other. On a
  // narrower screen the manifest is under the card and the two scroll together: a card
  // that stayed at the top of a phone's screen lay over the persona buttons as they
  // scrolled up under it.
  it('keeps the card at the top of the screen only where the manifest is beside it', () => {
    const sheet = Array.from(document.querySelectorAll('style'))
      .map((style) => style.textContent ?? '')
      .filter((text) => text.includes('.login-card'))
      .join('\n');
    const sideBySide = /@media \(min-width:\s*1244px\)\s*\{((?:[^{}]*\{[^{}]*\})*)\s*\}/g;
    /** What the page's stylesheet declares for `selector`: on every screen, or only where the two are side by side. */
    const declares = (selector: string, where: 'everywhere' | 'side by side' = 'everywhere'): string => {
      const rules =
        where === 'everywhere'
          ? sheet.replace(/@media[^{]*\{(?:[^{}]*\{[^{}]*\})*\s*\}/g, '')
          : Array.from(sheet.matchAll(sideBySide))
              .map(([, held]) => held)
              .join('\n');
      return Array.from(rules.matchAll(/([^{}]+)\{([^{}]*)\}/g))
        .filter(([, named]) => (named ?? '').replace(/\[_ng[^\]]*\]/g, '').trim() === selector)
        .map(([, , declarations]) => declarations ?? '')
        .join('\n');
    };

    expect(declares('.login-card')).not.toContain('position');
    expect(declares('.login-page')).toMatch(/flex-wrap:\s*wrap/);
    expect(declares('.login-card', 'side by side')).toMatch(/position:\s*sticky/);
    expect(declares('.login-page', 'side by side')).toMatch(/flex-wrap:\s*nowrap/);
  });

  it("prefills the component's form from the crew manifest, signing in staying an explicit click", async () => {
    const component = fixture.componentInstance;
    expect(component.decks.length).toBeGreaterThan(1);
    expect(component.decks.flatMap((deck) => component.personasOn(deck))).toHaveLength(CREW_MANIFEST.length);

    component.fillPersona('overseer');
    fixture.detectChanges();
    await fixture.whenStable();
    const methods = fixture.debugElement.query((node) => node.componentInstance instanceof LoginMethodsComponent)
      .componentInstance as LoginMethodsComponent;
    expect(methods.username).toBe('overseer');
    expect(methods.password).toBe(DEMO_PASSWORD);
    // The boxes the person sees hold the persona too, not the component's fields alone.
    const box = (name: string): string =>
      (fixture.nativeElement as HTMLElement).querySelector<HTMLInputElement>(`input[name="${name}"]`)?.value ?? '';
    expect([box('username'), box('password')]).toEqual(['overseer', DEMO_PASSWORD]);
    expect(component.selected()).toBe('overseer');
    expect(fixture.nativeElement.querySelector('button.persona.selected .login')?.textContent?.trim()).toBe('overseer');
    expect(transport.requests.filter((request) => request.method === 'POST')).toHaveLength(0);
  });

  it("on the component's signedIn starts the idle watch and moves to the page it is handed", () => {
    const idle = TestBed.inject(IdleService);
    const started = vi.spyOn(idle, 'start');
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    fixture.componentInstance.go('/dashboard');
    expect(started).toHaveBeenCalledTimes(1);
    expect(navigated).toHaveBeenCalledWith('/dashboard');
    idle.stop();
  });
});

describe('LoginComponent, arriving with a mailed link', () => {
  // The page ends no session on arrival, so a mailed link followed signed in keeps the session:
  // an invitation is accepted by the person signed in, an address confirmation is posted for
  // the session the page finds, and a sign-up creates an account of its own. Signing out is
  // the sign-out route's.
  async function arriveWith(query: Record<string, string>): Promise<ScriptedTransport> {
    const params = convertToParamMap(query);
    const transport = scriptedTransport((request: TransportRequest) =>
      request.url.endsWith('/user/methods') ? { status: 200, body: CONSOLE_METHODS } : { status: 200, body: undefined },
    );
    TestBed.resetTestingModule();
    await TestBed.configureTestingModule({
      imports: [LoginComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: ActivatedRoute, useValue: { queryParamMap: of(params), snapshot: { queryParamMap: params } } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(LoginComponent);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();

    return transport;
  }

  it('keeps the session for every link: an invitation, an address confirmation, a sign-up', async () => {
    const links: Record<string, string>[] = [{ invitation: 'token' }, { verify: 'token' }, { signUp: 'token' }];
    for (const query of links) {
      const transport = await arriveWith(query);
      expect(transport.requests.filter((request) => request.method === 'DELETE')).toEqual([]);
    }
  });
});
