import { ApplicationRef, Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Router, Routes } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import { PermissionDigest } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import { provideFrameTesting, specTransport } from './frame-testing';
import { homeGuard, outsideGuard } from './place.guard';
import { PlaceService } from './place.service';
import { tenantGuard } from './tenant.guard';

// Where a person works and where they land. The places are the place outside every tenant,
// for a person who holds something there, then the tenants by name. Signing in lands where
// the person last worked if they still can, else in the first place; nobody lands on an
// empty page outside every tenant. The tenant every library page reads is the URL's.

@Component({ template: 'the page' })
class PageComponent {}

const routes: Routes = [
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
];

/** A grant outside every tenant. */
const holdsOutside: PermissionDigest = { FeatureFlags: { List: 'granted' } };
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

  const signIn = async (digest: PermissionDigest, config: Partial<FrameConfig> = {}): Promise<PlaceService> => {
    const transport = specTransport({ digests: { '': digest }, domains });
    TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, config, routes)] });
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
