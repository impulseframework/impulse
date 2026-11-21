import { Location } from '@angular/common';
import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Route, Router, Routes } from '@angular/router';
import { Permission, PermissionDigest, Resource, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameComponent } from './frame.component';
import { provideFrameTesting } from './frame-testing';
import { Menu, page } from './menu';
import { homeGuard, outsideGuard } from './place.guard';
import { TenantRefusedComponent } from './tenant-refused.component';
import { tenantGuard } from './tenant.guard';
import { refusedSegment, TenantService } from './tenant.service';

// What a person sees where the list of tenants they are in could not be read. A failed
// read is not a person in no tenant: the frame draws "This page could not be loaded" with
// Try again where the page goes, no page, and no place switcher, and a tenant the person
// is in never says 404 Not Found. Try again reads the list again and draws the page.

@Component({ template: '<p class="spec-page">dashboard</p>' })
class DashboardComponent {}

@Component({ template: '<p class="spec-page">notes</p>' })
class NotesComponent {}

@Component({ template: '<p class="spec-page">tenant dashboard</p>' })
class TenantDashboardComponent {}

/** The tenant segment as the spec's descriptor names it, the generated DomainRoutePath. */
const tenantPath = 'tenants/:tenantID';

/** The application's routes, with the tenant's dashboard as given. */
const routesWith = (tenantDashboard: Route): Routes => [
  { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
  { path: 'dashboard', canActivate: [outsideGuard], component: DashboardComponent },
  // A page no guard decides.
  { path: 'notes', component: NotesComponent },
  { path: `${tenantPath}/${refusedSegment}`, component: TenantRefusedComponent },
  { path: tenantPath, canActivate: [tenantGuard], children: [tenantDashboard] },
];

const routes = routesWith({ path: 'dashboard', component: TenantDashboardComponent });

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

/** Where the frame keeps the place the person last worked in: per application and per login. */
const rememberKey = 'impulse-place:/:gale';

/** What the person holds outside every tenant in each spec: something, or nothing. */
const something: PermissionDigest = { FeatureFlags: { List: 'granted' } };
const nothing: PermissionDigest = {};

describe('a frame over a tenant list that could not be read', () => {
  /** Whether the server fails to answer the list; a spec changes it between reads. */
  let down: boolean;
  /** How many times the list was asked for. */
  let listReads: number;
  let fixture: ComponentFixture<FrameComponent>;

  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  /** Signs the person in, the list failing, and draws the frame. The person is in one tenant, Zenith. */
  const signIn = async (outside: PermissionDigest, pages: Routes = routes): Promise<void> => {
    down = true;
    listReads = 0;
    const transport = scriptedTransport((request): TransportResponse => {
      const url = new URL(request.url, 'http://spec.test');
      switch (url.pathname) {
        case '/api/user/session':
          return { status: 200, body: { authenticated: true, username: 'gale', email: 'gale@spec.example' } };
        case '/api/user/methods':
          return { status: 200, body: { methods: [] } };
        case '/api/permission-digest':
          return { status: 200, body: url.searchParams.get('domain') ? { Users: { List: 'granted' } } : outside };
        case '/api/user-domains':
          listReads++;
          return down
            ? { status: 500, body: { message: 'the list could not be read' } }
            : { status: 200, body: [{ domain: 'a-id', name: 'Zenith' }] };
        case '/api/features':
          return { status: 200, body: { enabled: [] } };
        default:
          return { status: 404, body: { message: `the spec's server has no ${request.method} ${url.pathname}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [FrameComponent],
      providers: [provideFrameTesting(transport, { menus }, pages)],
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
  /** What the place switcher draws: nothing, the name of a lone place, or the open place on its button. */
  const switcher = (): string | undefined =>
    element()
      .querySelector('impulse-place-switcher .impulse-place-switcher-place .impulse-place-switcher-name')
      ?.textContent?.trim();
  const tryAgain = async (): Promise<void> => {
    (element().querySelector('.impulse-frame-unread .impulse-frame-try-again') as HTMLButtonElement).click();
    await settle();
  };

  it('says the page could not be loaded, with no switcher, where the list could not be read at sign-in, and draws both once Try again reads it', async () => {
    await signIn(something);
    await go('/notes');

    // No guard decides this page; drawn now, it would sit under the bar of a person in no tenant.
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    // Not the lone place of a person who has only the place outside every tenant.
    expect(switcher()).toBeUndefined();
    expect(element().textContent).not.toContain('404 Not Found');

    // A read that fails again leaves the failure where it is.
    await tryAgain();
    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    expect(switcher()).toBeUndefined();

    down = false;
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('notes');
    expect(switcher()).toBe('Configuration');
    expect(element().querySelector('impulse-place-switcher button.impulse-place-switcher-place')).not.toBeNull();
  });

  it('lands nobody as a person in no tenant: the root address says the page could not be loaded, and lands in their tenant once Try again reads it', async () => {
    await signIn(nothing);
    await go('/');

    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    expect(switcher()).toBeUndefined();
    expect(TestBed.inject(Router).url).toBe('/');

    down = false;
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('tenant dashboard');
    expect(TestBed.inject(Router).url).toBe('/tenants/a-id/dashboard');
    expect(switcher()).toBe('Zenith');
  });

  it('says the page could not be loaded where a page outside every tenant cannot be decided, and opens it once Try again reads it', async () => {
    await signIn(something);
    const reads = listReads;
    await go('/dashboard');

    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    // The guard asked for the list again, and could not decide: the address bar keeps
    // what was asked for, so a reload asks for it again.
    expect(listReads).toBe(reads + 1);
    expect(TestBed.inject(Location).path()).toBe('');

    down = false;
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('dashboard');
    expect(TestBed.inject(Location).path()).toBe('/dashboard');
  });

  it("says the page could not be loaded for a tenant's page, never 404 Not Found for a tenant the person is in, and one Try again opens it", async () => {
    await signIn(nothing);
    await go('/tenants/a-id/dashboard');

    expect(unread()).toBe('This page could not be loaded');
    expect(drawn()).toBeUndefined();
    expect(element().textContent).not.toContain('404 Not Found');
    expect(switcher()).toBeUndefined();

    down = false;
    await tryAgain();
    expect(unread()).toBeUndefined();
    expect(element().textContent).not.toContain('could not be loaded');
    expect(drawn()).toBe('tenant dashboard');
    expect(TestBed.inject(Router).url).toBe('/tenants/a-id/dashboard');
    expect(switcher()).toBe('Zenith');
  });

  it("keeps the failure on the screen, with no switcher and never 404 Not Found, until the tenant's page Try again asked for is drawn", async () => {
    // The tenant's dashboard is a page the router loads when it is first opened, and the
    // spec holds it back.
    let arrive = (): void => undefined;
    const loaded = new Promise<void>((resolve) => (arrive = resolve));
    await signIn(
      something,
      routesWith({ path: 'dashboard', loadComponent: () => loaded.then(() => TenantDashboardComponent) }),
    );
    await go('/tenants/a-id/dashboard');
    expect(unread()).toBe('This page could not be loaded');

    // The list answers and the tenant opens while its page is still on its way: the list
    // that was just read is not drawn as a person outside every tenant, and the refusal
    // page under the frame is not drawn as 404 Not Found.
    down = false;
    await tryAgain();
    expect(TestBed.inject(TenantService).current()).toBe('a-id');
    expect(unread()).toBe('This page could not be loaded');
    expect(switcher()).toBeUndefined();
    expect(element().textContent).not.toContain('404 Not Found');
    expect(drawn()).toBeUndefined();

    arrive();
    await settle();
    expect(unread()).toBeUndefined();
    expect(drawn()).toBe('tenant dashboard');
    expect(switcher()).toBe('Zenith');
  });

  it('keeps where the person last worked through the failure: the root address lands in the tenant they worked in once Try again reads the list', async () => {
    localStorage.setItem(rememberKey, 'a-id');
    await signIn(something);
    await go('/');
    expect(unread()).toBe('This page could not be loaded');

    // A person who holds something outside every tenant lands there the first time; this
    // one last worked in Zenith, and the failure has not moved that.
    down = false;
    await tryAgain();
    expect(drawn()).toBe('tenant dashboard');
    expect(TestBed.inject(Router).url).toBe('/tenants/a-id/dashboard');
    expect(localStorage.getItem(rememberKey)).toBe('a-id');
  });
});
