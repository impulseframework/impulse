import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router, Routes } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { outsideGuard, provideFrame, tenantGuard } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { API_URL } from '@impulseframework/resource-angular/types';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { ImpersonationService } from './impersonation.service';

// Taking a session swaps who the browser is while a page is open. The permission cache is
// never emptied under a page that is drawn: the sector's page that was open keeps the
// answers it was drawn from until the navigation leaves it, and the landing is decided
// for the new session.

@Component({ template: 'the page' })
class PageComponent {}

const routes: Routes = [
  { path: 'dashboard', canActivate: [outsideGuard], component: PageComponent },
  {
    path: 'sectors/:sectorID',
    canActivate: [tenantGuard],
    children: [
      { path: 'dashboard', component: PageComponent },
      { path: 'missions', component: PageComponent },
    ],
  },
];

const anvilID = 'a1111111-1111-4111-8111-111111111111';
const bastionID = 'b2222222-2222-4222-8222-222222222222';
const missions: PermissionDigest = { Missions: { List: 'granted', Read: 'granted' } };

/** What each of the two people holds: outside every sector, in each sector, and their sectors by name. */
const holds = {
  marshal: {
    global: { PilotCards: { List: 'granted' } } as PermissionDigest,
    sectors: { [anvilID]: missions, [bastionID]: missions } as Record<string, PermissionDigest>,
    list: [
      { domain: anvilID, name: 'Anvil' },
      { domain: bastionID, name: 'Bastion' },
    ],
  },
  cadet: {
    global: {} as PermissionDigest,
    sectors: { [bastionID]: missions } as Record<string, PermissionDigest>,
    list: [{ domain: bastionID, name: 'Bastion' }],
  },
};

/** Whose session the cookie names; the spec changes it when the server mints the session. */
let who: keyof typeof holds;

/** The server as the spec plays it: the session, the digests and the sectors of the person the cookie names. */
function server(request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  switch (url.pathname) {
    case `${environment.apiUrl}/user/session`:
      return { status: 200, body: { authenticated: true, username: who } };
    case `${environment.apiUrl}/permission-digest`: {
      const sector = url.searchParams.get('domain');
      return { status: 200, body: sector ? (holds[who].sectors[sector] ?? {}) : holds[who].global };
    }
    case `${environment.apiUrl}/user-domains`:
      return { status: 200, body: holds[who].list };
    case `${environment.apiUrl}/features`:
      return { status: 200, body: { enabled: [] } };
    default:
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  }
}

describe('ImpersonationService', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it('takes a session without emptying the cache under the open sector page, and lands where the new session lands', async () => {
    who = 'marshal';
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport: scriptedTransport(server),
          client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }),
        }),
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter(routes),
        provideFrame({ home: 'dashboard', menus: [] }),
        { provide: API_URL, useValue: environment.apiUrl },
      ],
    });
    const auth = TestBed.inject(AuthService);
    const router = TestBed.inject(Router);
    await firstValueFrom(auth.checkUserSession());
    await router.navigateByUrl(`/sectors/${anvilID}/missions`);
    expect(router.url).toBe(`/sectors/${anvilID}/missions`);

    // What the cache held each time it changed, with the page that was open then.
    const seen: { url: string; outside: boolean; anvil: boolean }[] = [];
    auth.permissions.subscribe((snapshot) =>
      seen.push({ url: router.url, outside: snapshot.digests.has(''), anvil: snapshot.digests.has(anvilID) }),
    );

    const taken = TestBed.inject(ImpersonationService).viewAs('cadet-id');
    who = 'cadet';
    TestBed.inject(HttpTestingController).expectOne(`${environment.apiUrl}/impersonate`).flush({});
    await taken;

    expect(seen.length).toBeGreaterThan(0);
    // Anvil's page stays over Anvil's answers until the navigation leaves it, and no page
    // is ever over a cache that holds nothing.
    expect(seen.filter((at) => at.url.startsWith(`/sectors/${anvilID}`) && !at.anvil)).toEqual([]);
    expect(seen.filter((at) => !at.outside)).toEqual([]);

    // The landing is the new session's: the cadet's one sector, not the marshal's first.
    expect(router.url).toBe(`/sectors/${bastionID}/dashboard`);
    expect(auth.sessionInfo().username).toBe('cadet');
    expect(auth.domains()).toEqual([bastionID]);
    expect(auth.hasPermission({ resource: Resources.PilotCards, permission: Permissions.List })).toBe(false);
    expect(
      auth.hasPermission({ resource: Resources.Missions, permission: Permissions.List, domain: bastionID as Domain }),
    ).toBe(true);
  });
});
