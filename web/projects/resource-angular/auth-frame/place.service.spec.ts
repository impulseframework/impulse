import { ApplicationRef, Component, DestroyRef, inject } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Router, RouterOutlet, Routes } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import { Permission, PermissionDigest, Resource } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import { provideFrameTesting, SpecServer, specTransport } from './frame-testing';
import { Menu, page } from './menu';
import { homeGuard, outsideGuard } from './place.guard';
import { PlaceService } from './place.service';
import { tenantGuard } from './tenant.guard';

// Where a person works and where they land. The places are the place outside every tenant,
// for a person the application's menus show something there, then the tenants by name. Signing in lands where
// the person last worked if they still can, else in the first place; nobody lands on an
// empty page outside every tenant. The tenant every library page reads is the URL's.
// Where a person works is where a page under the top bar was open: the sign-in and
// sign-out pages, routed outside the frame, are nobody's place.

@Component({ template: 'the page' })
class PageComponent {}

/** Stands for the frame: says a page under the top bar is on the screen, as FrameComponent does. */
@Component({ imports: [RouterOutlet], template: '<router-outlet />' })
class FramedComponent {
  constructor() {
    inject(PlaceService).drawn(inject(DestroyRef));
  }
}

/** A page that loads lazily, as an application's sign-in and sign-out pages do: the page being left stays up meanwhile. */
const lazyPage = (): Promise<typeof PageComponent> =>
  new Promise((resolve) => setTimeout(() => resolve(PageComponent), 30));

const routes: Routes = [
  { path: 'login', loadComponent: lazyPage },
  { path: 'logout', loadComponent: lazyPage },
  {
    path: '',
    component: FramedComponent,
    children: [
      { path: '', pathMatch: 'full', canActivate: [homeGuard], children: [] },
      { path: 'dashboard', canActivate: [outsideGuard], component: PageComponent },
      {
        path: 'tenants/:tenantID',
        canActivate: [tenantGuard],
        children: [
          { path: 'dashboard', component: PageComponent },
          { path: 'users', component: PageComponent },
        ],
      },
    ],
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
/** A grant outside every tenant that the menus show a page for. */
const holdsOutside: PermissionDigest = { FeatureFlags: { List: 'granted' } };
/** A grant outside every tenant that no menu entry answers to: a record read behind a tenant's page. */
const holdsUnplaced: PermissionDigest = { Contacts: { Read: 'granted' } };
/** No grant outside every tenant. */
const holdsNothing: PermissionDigest = {};
/** Two tenants, whose names sort the other way from their ids. */
const domains = [
  { domain: 'a-id', name: 'Zenith' },
  { domain: 'b-id', name: 'Acme' },
];
const rememberKey = 'impulse-place:/:gale';

describe('PlaceService', () => {
  let harness: RouterTestingHarness;

  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  const signIn = async (
    digest: PermissionDigest,
    config: Partial<FrameConfig> = {},
    server: SpecServer = {},
  ): Promise<PlaceService> => {
    server.digests = { '': digest };
    server.domains ??= domains;
    const transport = specTransport(server);
    TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, { menus, ...config }, routes)] });
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    harness = await RouterTestingHarness.create();
    return TestBed.inject(PlaceService);
  };
  const go = async (url: string): Promise<string> => {
    await harness.navigateByUrl(url);
    await TestBed.inject(ApplicationRef).whenStable();
    return TestBed.inject(Router).url;
  };

  it('lists the place outside every tenant first for a person who holds something there, then the tenants by name', async () => {
    const places = await signIn(holdsOutside);
    expect(places.places().map((place) => place.label)).toEqual(['Configuration', 'Acme', 'Zenith']);
  });

  it('lists only the tenants for a person who holds nothing outside them', async () => {
    const places = await signIn(holdsNothing);
    expect(places.places().map((place) => place.label)).toEqual(['Acme', 'Zenith']);
  });

  it('keeps a tenant that is closed to this sign-in out of the places and where the person lands, and lists it apart with the reason', async () => {
    const reason = 'Aardvark admits only people who sign in through its own directory. You signed in with a password.';
    const transport = specTransport({
      digests: { '': holdsNothing },
      domains: [...domains, { domain: 'c-id', name: 'Aardvark', unavailable: reason }],
    });
    TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, { menus }, routes)] });
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const places = TestBed.inject(PlaceService);
    expect(places.places().map((place) => place.label)).toEqual(['Acme', 'Zenith']);
    expect(places.unavailable()).toEqual([{ id: 'c-id', label: 'Aardvark', reason }]);
  });

  it('lists only the tenants for a person whose grant outside them no menu entry answers to', async () => {
    const places = await signIn(holdsUnplaced);
    expect(places.holdsOutside()).toBe(false);
    expect(places.places().map((place) => place.label)).toEqual(['Acme', 'Zenith']);
  });

  it('lists only the tenants in an application whose menus hold nothing outside them', async () => {
    const places = await signIn(holdsOutside, { menus: [] });
    expect(places.places().map((place) => place.label)).toEqual(['Acme', 'Zenith']);
  });

  it('names the place outside every tenant as the application does', async () => {
    const places = await signIn(holdsOutside, { outside: { label: 'Headquarters', icon: 'apartment' } });
    expect(places.current()).toEqual({ id: '', label: 'Headquarters' });
  });

  it("names the open tenant by the tenant record's name", async () => {
    const places = await signIn(holdsNothing);
    await go('/tenants/a-id/users');
    expect(places.current()).toEqual({ id: 'a-id', label: 'Zenith' });
  });

  const landings: { name: string; digest: PermissionDigest; remembered?: string; want: string }[] = [
    { name: 'a person who holds something outside every tenant lands there', digest: holdsOutside, want: '/dashboard' },
    {
      name: 'anyone else lands in their first tenant by name',
      digest: holdsNothing,
      want: '/tenants/b-id/dashboard',
    },
    {
      name: 'a person whose grant outside the tenants no menu entry answers to lands in their first tenant',
      digest: holdsUnplaced,
      want: '/tenants/b-id/dashboard',
    },
    {
      name: 'a person lands where they last worked',
      digest: holdsOutside,
      remembered: 'a-id',
      want: '/tenants/a-id/dashboard',
    },
    {
      name: 'a remembered place they can no longer work in is passed over',
      digest: holdsNothing,
      remembered: '@outside',
      want: '/tenants/b-id/dashboard',
    },
  ];
  for (const tt of landings) {
    it(tt.name, async () => {
      if (tt.remembered) localStorage.setItem(rememberKey, tt.remembered);
      await signIn(tt.digest);
      expect(await go('/')).toBe(tt.want);
    });
  }

  it('sends a person who holds nothing outside every tenant from a page there to where they land', async () => {
    await signIn(holdsNothing);
    expect(await go('/dashboard')).toBe('/tenants/b-id/dashboard');
  });

  it('remembers where the person works', async () => {
    await signIn(holdsOutside);
    await go('/tenants/a-id/users');
    expect(localStorage.getItem(rememberKey)).toBe('a-id');
    await go('/dashboard');
    expect(localStorage.getItem(rememberKey)).toBe('@outside');
  });

  it('keeps the tenant a person who holds something outside every tenant worked in through the sign-out and sign-in pages', async () => {
    await signIn(holdsOutside);
    await go('/tenants/a-id/users');
    expect(localStorage.getItem(rememberKey)).toBe('a-id');
    await go('/logout');
    expect(localStorage.getItem(rememberKey)).toBe('a-id');
    await go('/login');
    expect(localStorage.getItem(rememberKey)).toBe('a-id');
    expect(await go('/')).toBe('/tenants/a-id/dashboard');
  });

  // An accepted invitation's landing. The server names the tenant an invitation seated the
  // person in on the first session read after the acceptance, and on no later one.
  const gale = { authenticated: true, username: 'gale', email: 'gale@spec.example' };

  it('lands a person who has just accepted an invitation into a tenant in that tenant, whatever else they hold and wherever they last worked', async () => {
    localStorage.setItem(rememberKey, 'b-id');
    const server: SpecServer = { session: { ...gale, invitedTenant: 'a-id' } };
    await signIn(holdsOutside, {}, server);
    // The login page reads the session again before it hands the person on, and the
    // server names the tenant once.
    server.session = gale;
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    expect(await go('/')).toBe('/tenants/a-id/dashboard');
    expect(localStorage.getItem(rememberKey)).toBe('a-id');
  });

  it("lands where the person last worked once they have worked somewhere after the invitation's landing", async () => {
    const server: SpecServer = { session: { ...gale, invitedTenant: 'a-id' } };
    await signIn(holdsNothing, {}, server);
    server.session = gale;
    expect(await go('/')).toBe('/tenants/a-id/dashboard');
    await go('/tenants/b-id/users');
    expect(await go('/')).toBe('/tenants/b-id/dashboard');
  });

  it('reads the tenant list again for a signed-in person an invitation seats in a tenant the list did not hold, and lands them there', async () => {
    const server: SpecServer = { domains: [domains[1]] };
    await signIn(holdsNothing, {}, server);
    expect(await go('/')).toBe('/tenants/b-id/dashboard');
    await go('/login');
    // The person accepts the invitation as they are, on the sign-in page: the seat is made,
    // and the next session read names its tenant.
    server.domains = domains;
    server.session = { ...gale, invitedTenant: 'a-id' };
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    expect(await go('/')).toBe('/tenants/a-id/dashboard');
  });

  it('lands a person as ever when the tenant the invitation names is not a place of theirs', async () => {
    localStorage.setItem(rememberKey, 'a-id');
    const server: SpecServer = { session: { ...gale, invitedTenant: 'c-id' } };
    await signIn(holdsNothing, {}, server);
    expect(await go('/')).toBe('/tenants/a-id/dashboard');
  });

  it('remembers nothing for a person who has opened no page under the top bar', async () => {
    await signIn(holdsOutside);
    await go('/login');
    expect(localStorage.getItem(rememberKey)).toBeNull();
  });

  it('opens a tenant from outside on its first page, and another tenant on the same page', async () => {
    const places = await signIn(holdsOutside);
    await go('/dashboard');
    await places.open({ id: 'a-id', label: 'Zenith' });
    expect(TestBed.inject(Router).url).toBe('/tenants/a-id/dashboard');
    await go('/tenants/a-id/users');
    await places.open({ id: 'b-id', label: 'Acme' });
    expect(TestBed.inject(Router).url).toBe('/tenants/b-id/users');
    await places.open({ id: '', label: 'Configuration' });
    expect(TestBed.inject(Router).url).toBe('/dashboard');
  });

  it('hands every library page the tenant the URL is open in', async () => {
    await signIn(holdsOutside);
    const domain = TestBed.inject(RESOURCE_DOMAIN);
    await go('/tenants/a-id/users');
    expect(domain()).toBe('a-id');
    await go('/dashboard');
    expect(domain()).toBeUndefined();
  });
});
