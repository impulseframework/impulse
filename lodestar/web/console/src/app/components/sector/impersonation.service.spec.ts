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
import { ImpersonationRecord, ImpersonationService } from './impersonation.service';

const cap = '2099-01-01T00:00:00Z';

/**
 * The server as the spec plays it: the session it answers, with the impersonation record
 * the mint route's session carries, an empty digest and no sector. Anything else is a 404.
 */
function sessionServer(session: { username: string; impersonation?: ImpersonationRecord }) {
  return (request: TransportRequest): TransportResponse => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, email: 'someone@lodestar.test', ...session } };
      case `${environment.apiUrl}/permission-digest`:
        return { status: 200, body: {} };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [] };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

/** Signs the spec in to the session the server answers and returns the service over it. */
async function signedIn(session: {
  username: string;
  impersonation?: ImpersonationRecord;
}): Promise<ImpersonationService> {
  await TestBed.configureTestingModule({
    providers: [
      provideResourceTesting({
        transport: scriptedTransport(sessionServer(session)),
        client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }),
      }),
      provideHttpClient(),
      provideHttpClientTesting(),
      // A mint ends at the dashboard: the route is there for the navigation to land on.
      provideRouter([{ path: 'dashboard', children: [] }]),
      provideFrame({ home: 'dashboard', menus: [] }),
    ],
  }).compileComponents();
  await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
  return TestBed.inject(ImpersonationService);
}

/**
 * The banner says who the session is and what it may do in the server's own words, and
 * each control on the roster asks the mint route for its own kind.
 *
 * Demonstrates: impersonation.view-as, impersonation.act-as-user, impersonation.act-as-role.
 */
describe('ImpersonationService', () => {
  it('draws no banner over a session of the person who signed in', async () => {
    const service = await signedIn({ username: 'marshal' });
    expect(service.banner()).toBeUndefined();
  });

  it('says a read-only session views, with the mask in the server words', async () => {
    const service = await signedIn({
      username: 'cadet',
      impersonation: {
        actor: 'marshal',
        principalKind: 'User',
        principal: 'f0000000-0000-4000-8000-000000000003',
        mask: ['List', 'Read'],
        maskWords: 'reading only',
        expiresAt: cap,
      },
    });
    expect(service.banner()?.text).toBe('Viewing as cadet: reading only. You are marshal.');
    // The service card strikes the kinds the mask does not keep.
    expect(service.masked('Update')).toBe(true);
    expect(service.masked('Read')).toBe(false);
  });

  it('says a session that keeps every kind acts, and what it is refused', async () => {
    const service = await signedIn({
      username: 'marshal',
      impersonation: {
        actor: 'governor',
        principalKind: 'User',
        principal: 'f0000000-0000-4000-8000-000000000002',
        refusedClasses: ['access', 'credential'],
        excepted: [
          { class: 'access', permission: 'Create', resource: 'HangarPasses' },
          { class: 'access', permission: 'Delete', resource: 'HangarPasses' },
          { class: 'access', permission: 'Update', resource: 'HangarPasses' },
        ],
        maskWords: 'everything except access actions (still allowed: writing HangarPasses) and credential actions',
        expiresAt: cap,
      },
    });
    expect(service.banner()?.text).toBe(
      'Acting as marshal: everything except access actions (still allowed: writing HangarPasses) and credential actions. You are governor.',
    );
    // Every kind is kept: no column of the service card is struck.
    expect(service.masked('Update')).toBe(false);
    expect(service.masked('Execute')).toBe(false);
  });

  it('says a session under a role acts as the role', async () => {
    const service = await signedIn({
      username: 'governor',
      impersonation: {
        actor: 'governor',
        principalKind: 'Role',
        principal: 'Dispatcher',
        maskWords: 'everything',
        expiresAt: cap,
      },
    });
    expect(service.banner()?.text).toBe('Acting as role Dispatcher. You are governor; subject still binds to you.');
  });

  it('names the role a session acts as in words', async () => {
    const service = await signedIn({
      username: 'governor',
      impersonation: {
        actor: 'governor',
        principalKind: 'Role',
        principal: 'SectorMarshal',
        maskWords: 'everything',
        expiresAt: cap,
      },
    });
    expect(service.banner()?.text).toBe('Acting as role Sector marshal. You are governor; subject still binds to you.');
  });

  it('asks the mint route for the kind of each control', async () => {
    const service = await signedIn({ username: 'governor' });
    const http = TestBed.inject(HttpTestingController);
    const asked = (): unknown => {
      // The mint route sits under the address the application's API is injected at.
      const request = http.expectOne(`${TestBed.inject(API_URL)}/impersonate`);
      const body: unknown = request.request.body;
      request.flush({ sessionId: 'a session', mask: 'as the server says it' });
      return body;
    };

    const viewing = service.viewAs('f0000000-0000-4000-8000-000000000003');
    expect(asked()).toEqual({
      kind: 'view',
      principal: 'f0000000-0000-4000-8000-000000000003',
      reason: 'crew roster: view as',
    });
    await viewing;

    const acting = service.actAs('f0000000-0000-4000-8000-000000000002');
    expect(asked()).toEqual({
      kind: 'act',
      principal: 'f0000000-0000-4000-8000-000000000002',
      reason: 'crew roster: act as',
    });
    await acting;

    const underRole = service.assumeRole('Dispatcher');
    expect(asked()).toEqual({ kind: 'role', principal: 'Dispatcher', reason: 'crew roster: assume role' });
    await underRole;
  });
});

// Taking a session swaps who the browser is while a page is open. The permission cache is
// never emptied under a page that is drawn: the sector's page that was open keeps the
// answers it was drawn from until the navigation leaves it, and the landing is decided
// for the new session. Once the new session is in, nothing the old one held in a sector
// the new one is not in is kept.

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
function peopleServer(request: TransportRequest): TransportResponse {
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

describe('ImpersonationService, taking a session while a sector page is open', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it('takes a session without emptying the cache under the open sector page, and lands where the new session lands', async () => {
    who = 'marshal';
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport: scriptedTransport(peopleServer),
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

    // The cadet is not in Anvil: what the marshal held there is forgotten, not kept.
    expect(auth.hasDigest(anvilID as Domain)).toBe(false);
    expect(
      auth.hasPermission({ resource: Resources.Missions, permission: Permissions.List, domain: anvilID as Domain }),
    ).toBe(false);
  });
});
