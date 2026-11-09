import { TestBed } from '@angular/core/testing';
import { ActivatedRouteSnapshot, Router, RouterStateSnapshot, UrlTree } from '@angular/router';
import { ApiDescriptor, createClient } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { firstValueFrom, isObservable } from 'rxjs';

import { LoginAuthenticationGuard } from './authentication.guard';

// The login guard lets a signed-in session through, returns an unauthenticated one to the
// login page with the attempted URL kept, and returns a session whose operator-set
// password must change the same way: the server serves the page nothing until the person
// sets a password of their own, and the login page serves the change form.

const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

function serverWith(authenticated: boolean, passwordChangeRequired: boolean): ScriptedTransport {
  return scriptedTransport((request) => {
    switch (request.url) {
      case '/api/user/session':
        return { status: 200, body: { authenticated, username: 'overseer', passwordChangeRequired } };
      case '/api/permission-digest':
        return { status: 200, body: {} };
      case '/api/user-domains':
        return { status: 200, body: [] };
      case '/api/features':
        return { status: 200, body: { enabled: [] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
  });
}

/** Runs the guard for a navigation to `url` and answers what it decided. */
async function guardOver(transport: ScriptedTransport, url: string): Promise<boolean | UrlTree> {
  await TestBed.configureTestingModule({
    providers: [
      provideResourceTesting({ transport, client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }) }),
    ],
  }).compileComponents();
  const decision = TestBed.runInInjectionContext(() =>
    LoginAuthenticationGuard({} as ActivatedRouteSnapshot, { url } as RouterStateSnapshot),
  );
  const settled = isObservable(decision) ? await firstValueFrom(decision) : await decision;
  return settled as boolean | UrlTree;
}

describe('LoginAuthenticationGuard', () => {
  const cases: { name: string; authenticated: boolean; marked: boolean; wantThrough: boolean }[] = [
    { name: 'a signed-in session goes through', authenticated: true, marked: false, wantThrough: true },
    { name: 'no session is returned to the login page', authenticated: false, marked: false, wantThrough: false },
    {
      name: 'a session whose password must change is returned to the login page',
      authenticated: true,
      marked: true,
      wantThrough: false,
    },
  ];
  for (const tt of cases) {
    it(tt.name, async () => {
      const decision = await guardOver(serverWith(tt.authenticated, tt.marked), '/missions/m-7');
      const auth = TestBed.inject(AuthService);
      if (tt.wantThrough) {
        expect(decision).toBe(true);
        expect(auth.redirectUrl()).toBe('');
        return;
      }
      expect(decision).toEqual(TestBed.inject(Router).createUrlTree([auth.loginRoute()]));
      expect(auth.redirectUrl()).toBe('/missions/m-7');
    });
  }

  it('a session already checked and marked is sent to the login page without waiting on a new check', async () => {
    const transport = serverWith(true, true);
    await guardOver(transport, '/missions/m-7');
    const auth = TestBed.inject(AuthService);
    expect(auth.authenticated()).toBe(true);
    const decision = TestBed.runInInjectionContext(() =>
      LoginAuthenticationGuard({} as ActivatedRouteSnapshot, { url: '/deck' } as RouterStateSnapshot),
    );
    const settled = isObservable(decision) ? await firstValueFrom(decision) : await decision;
    expect(settled).toEqual(TestBed.inject(Router).createUrlTree([auth.loginRoute()]));
    expect(auth.redirectUrl()).toBe('/deck');
  });
});
