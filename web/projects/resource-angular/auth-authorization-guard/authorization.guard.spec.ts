import { Location } from '@angular/common';
import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { NavigationEnd, provideRouter, Router, Routes } from '@angular/router';
import { ApiDescriptor, createClient, PermissionDigest } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { UnreadPage } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { filter } from 'rxjs';

import { AuthorizationGuard } from './authorization.guard';

// The guard opens a page for a person the digest lets in and sends anyone else to the
// root address. A digest that could not be read is neither: the navigation lands on the
// root address carrying the address that was asked for, which a shell (the library's
// frame) draws as "This page could not be loaded" with Try again.

@Component({ template: 'the page' })
class PageComponent {}

const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
};

const scope = { resource: 'Missions', permission: 'List' };
const holds: PermissionDigest = { Missions: { List: 'granted' } };

describe('AuthorizationGuard', () => {
  /** What the digest answers, read by read: a status, with the digest for a 200. */
  let answers: (read: number) => { status: number; body: unknown };
  let reads: number;
  /** What each navigation that ended carried as its info. */
  let landed: unknown[];

  const appOver = (routes: Routes): Router => {
    reads = 0;
    landed = [];
    const transport = scriptedTransport((request) =>
      request.url === '/api/permission-digest'
        ? answers(++reads)
        : { status: 404, body: { message: `unscripted ${request.url}` } },
    );
    TestBed.configureTestingModule({
      providers: [
        { provide: RESOURCE_CLIENT, useValue: createClient(descriptor, { baseUrl: '/api', transport }) },
        provideRouter(routes),
      ],
    });
    const router = TestBed.inject(Router);
    router.events
      .pipe(filter((event) => event instanceof NavigationEnd))
      .subscribe(() => landed.push(router.currentNavigation()?.extras.info));
    return router;
  };
  const guarded: Routes = [
    { path: '', component: PageComponent },
    { path: 'missions', component: PageComponent, canActivate: [AuthorizationGuard], data: { scope } },
  ];
  const down = { status: 500, body: { message: 'the digest could not be read' } };

  it('opens the page for a person the digest lets in, and sends anyone else to the root address', async () => {
    answers = () => ({ status: 200, body: holds });
    const router = appOver([
      ...guarded,
      {
        path: 'refits',
        component: PageComponent,
        canActivate: [AuthorizationGuard],
        data: { scope: { resource: 'Refits', permission: 'List' } },
      },
    ]);

    await router.navigateByUrl('/missions');
    expect(router.url).toBe('/missions');
    await router.navigateByUrl('/refits');
    expect(router.url).toBe('/');
    expect(landed).toEqual([undefined, undefined]);
  });

  it('lands on the root address carrying the address asked for where the digest cannot be read, and opens the page once it can', async () => {
    answers = (read) => (read === 1 ? down : { status: 200, body: holds });
    const router = appOver(guarded);
    const location = TestBed.inject(Location);

    await router.navigateByUrl('/missions');
    // Not the page, and not a person turned away as one who may not open it: the
    // navigation says which page could not be decided, and the address bar is left alone.
    expect(router.url).toBe('/');
    expect(landed).toHaveLength(1);
    expect(landed[0]).toBeInstanceOf(UnreadPage);
    expect((landed[0] as UnreadPage).url).toBe('/missions');
    expect(location.path()).toBe('');

    await router.navigateByUrl((landed[0] as UnreadPage).url);
    expect(router.url).toBe('/missions');
    expect(location.path()).toBe('/missions');
    expect(landed[1]).toBeUndefined();
    expect(reads).toBe(2);
  });

  it('lets the navigation land where the root address leads back to the page, and does not send it round again', async () => {
    // The digest answers at its sixth read: a guard that sent the navigation round until
    // it could read would read six times and open the page.
    answers = (read) => (read < 6 ? down : { status: 200, body: holds });
    const router = appOver([
      { path: '', pathMatch: 'full', redirectTo: 'missions' },
      { path: 'missions', component: PageComponent, canActivate: [AuthorizationGuard], data: { scope } },
    ]);

    await router.navigateByUrl('/missions');
    expect(reads).toBe(2);
    expect(landed).toHaveLength(1);
    expect(landed[0]).toBeInstanceOf(UnreadPage);
    expect((landed[0] as UnreadPage).url).toBe('/missions');
  });
});
