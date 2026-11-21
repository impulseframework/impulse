import { Location } from '@angular/common';
import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, Routes } from '@angular/router';
import { Permission, PermissionDigest, Resource, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { AuthorizationGuard } from '@impulseframework/resource-angular/auth-authorization-guard';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameComponent } from './frame.component';
import { provideFrameTesting } from './frame-testing';
import { Menu, page } from './menu';
import { homeGuard, outsideGuard } from './place.guard';

// What a person sees where what they hold could not be read. A failed read is not a
// person who holds nothing: the frame draws "This page could not be loaded" with Try
// again where the page goes, and no page, whether the read at sign-in failed or a guard
// could not decide the page that was asked for. Try again reads again and draws the page.

@Component({ template: '<p class="spec-page">dashboard</p>' })
class DashboardComponent {}

@Component({ template: '<p class="spec-page">notes</p>' })
class NotesComponent {}

@Component({ template: '<p class="spec-page">crew</p>' })
class CrewComponent {}

const routes: Routes = [
  { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
  { path: 'dashboard', canActivate: [outsideGuard], component: DashboardComponent },
  // A page no guard decides.
  { path: 'notes', component: NotesComponent },
  // A page decided by what the person holds in one tenant.
  {
    path: 'crew',
    component: CrewComponent,
    canActivate: [AuthorizationGuard],
    data: { scope: { resource: 'Users', permission: 'List', domain: 'a-id' } },
  },
];

/** The application's menu file: one page of the place outside every tenant, under a grant there. */
const menus: Menu[] = [
  {
    label: 'System',
    groups: [
      {
        entries: [
          page({
            label: 'Feature flags',
            route: ['feature-flags'],
            permission: { resource: 'FeatureFlags' as Resource, permission: 'List' as Permission },
          }),
        ],
      },
    ],
  },
];
const digests: Record<string, PermissionDigest> = {
  '': { FeatureFlags: { List: 'granted' } },
  'a-id': { Users: { List: 'granted' } },
};

describe('a frame over permissions that could not be read', () => {
  /** The scopes whose digest the server fails to answer ('' is outside every tenant); a spec changes it between reads. */
  let down: Set<string>;
  /** The scope of every digest read, in order. */
  let asked: string[];
  let fixture: ComponentFixture<FrameComponent>;

  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  /** Signs the person in, with the given scopes failing, and draws the frame. */
  const signIn = async (failing: string[]): Promise<void> => {
    down = new Set(failing);
    asked = [];
    const transport = scriptedTransport((request): TransportResponse => {
      const url = new URL(request.url, 'http://spec.test');
      switch (url.pathname) {
        case '/api/user/session':
          return { status: 200, body: { authenticated: true, username: 'gale', email: 'gale@spec.example' } };
        case '/api/user/methods':
          return { status: 200, body: { methods: [] } };
        case '/api/permission-digest': {
          const domain = url.searchParams.get('domain') ?? '';
          asked.push(domain);
          return down.has(domain)
            ? { status: 500, body: { message: 'the digest could not be read' } }
            : { status: 200, body: digests[domain] ?? {} };
        }
        case '/api/user-domains':
          return { status: 200, body: [{ domain: 'a-id', name: 'Zenith' }] };
        case '/api/features':
          return { status: 200, body: { enabled: [] } };
        default:
          return { status: 404, body: { message: `the spec's server has no ${request.method} ${url.pathname}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [FrameComponent],
      providers: [provideFrameTesting(transport, { menus }, routes)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(FrameComponent);
    fixture.detectChanges();
    await fixture.whenStable();
  };
  /** Lets the reads and the navigations they start land, and draws what they left. */
  const settle = async (): Promise<void> => {
    for (let turn = 0; turn < 3; turn++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      fixture.detectChanges();
    }
  };
  const go = async (url: string): Promise<void> => {
    await TestBed.inject(Router).navigateByUrl(url);
    await settle();
  };
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const unread = (): string | undefined => element().querySelector('.impulse-frame-unread h1')?.textContent?.trim();
  const drawn = (): string | undefined => element().querySelector('.spec-page')?.textContent?.trim();
  const tryAgain = async (): Promise<void> => {
    (element().querySelector('.impulse-frame-unread .impulse-frame-try-again') as HTMLButtonElement).click();
    await settle();
  };

  it('says the page could not be loaded where the read at sign-in failed, and draws the page once Try again reads it', async () => {
    await signIn(['']);
    await go('/notes');

    // No guard decides this page; drawn now, it would sit under the menus of a person who holds nothing.
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();

    // A read that fails again leaves the failure where it is.
    await tryAgain();
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();

    down.clear();
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('notes');
    expect(asked).toEqual(['', '', '']);
  });

  it('names what it draws there for the frame, as every element of the frame is: a rule an application writes for its own content lands on none', async () => {
    await signIn(['']);
    await go('/notes');

    const block = element().querySelector('main > div') as HTMLElement;
    const button = block.querySelector('button') as HTMLElement;
    expect(block.querySelector('h1')?.textContent?.trim()).toBe('This page could not be loaded');
    for (const part of [block, button]) {
      // Material's own names on its button are not the frame's to give.
      const named = Array.from(part.classList).filter((name) => !/^_?(mat|mdc|cdk|ng)-/.test(name));
      expect(named.length).toBeGreaterThan(0);
      expect(named.filter((name) => !name.startsWith('impulse-frame-'))).toEqual([]);
    }
  });

  it('says the page could not be loaded where the landing cannot be decided, and lands once Try again reads it', async () => {
    await signIn(['']);
    await go('/');

    // Not the first tenant, where a person who holds nothing outside every tenant lands.
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    expect(TestBed.inject(Router).url).toBe('/');

    down.clear();
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('dashboard');
    expect(TestBed.inject(Router).url).toBe('/dashboard');
  });

  it('says the page could not be loaded where a page outside every tenant cannot be decided, and opens it once Try again reads it', async () => {
    await signIn(['']);
    await go('/dashboard');

    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    // The address bar keeps the page that was asked for, so a reload asks for it again.
    expect(TestBed.inject(Location).path()).toBe('');

    down.clear();
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('dashboard');
    expect(TestBed.inject(Location).path()).toBe('/dashboard');
  });

  it("says the page could not be loaded in the open page's place where a guard cannot read a tenant's digest", async () => {
    await signIn(['a-id']);
    await go('/notes');
    expect(drawn()).toBe('notes');

    await go('/crew');
    // Not the root address's page, where a person who may not open the page is sent.
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    expect(TestBed.inject(Location).path()).toBe('/notes');

    // A read that fails again leaves the failure where it is.
    await tryAgain();
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();

    down.clear();
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('crew');
    expect(TestBed.inject(Location).path()).toBe('/crew');
    expect(asked.filter((domain) => domain === 'a-id')).toHaveLength(3);
  });

  it('draws another page asked for while the failure is on the screen', async () => {
    await signIn(['a-id']);
    await go('/crew');
    expect(unread()).toBe('This page could not be loaded');

    await go('/notes');
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('notes');
  });
});
