import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame } from '@impulseframework/resource-angular/auth-frame';
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
function server(session: { username: string; impersonation?: ImpersonationRecord }) {
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
        transport: scriptedTransport(server(session)),
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
        maskWords:
          'everything except access actions (still allowed: writing HangarPasses) and credential actions',
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
