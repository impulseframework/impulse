import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { FRONTEND_LOGIN_PATH } from '@impulseframework/resource-angular/types';
import { IdleService } from '@impulseframework/resource-angular/ui-idle-service';

import { SignOutComponent } from './sign-out.component';

// The page ends the session on arrival and forwards the browser to the login page: the
// idle watch stops, the session's DELETE goes, and the browser moves on whatever the server
// answered, since a refusal there is a session it no longer held.

interface Arrival {
  fixture: ComponentFixture<SignOutComponent>;
  transport: ScriptedTransport;
  navigated: ReturnType<typeof vi.spyOn>;
  stopped: ReturnType<typeof vi.spyOn>;
}

async function arrive(sessionDelete: TransportResponse, loginPath?: string): Promise<Arrival> {
  const transport = scriptedTransport((request: TransportRequest) =>
    request.method === 'DELETE' ? sessionDelete : { status: 200, body: { authenticated: false } },
  );
  await TestBed.configureTestingModule({
    imports: [SignOutComponent],
    providers: [
      provideResourceTesting({ transport }),
      ...(loginPath === undefined ? [] : [{ provide: FRONTEND_LOGIN_PATH, useValue: loginPath }]),
    ],
  }).compileComponents();
  const navigated = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
  const stopped = vi.spyOn(TestBed.inject(IdleService), 'stop');
  const fixture = TestBed.createComponent(SignOutComponent);
  fixture.detectChanges();
  await TestBed.inject(ApplicationRef).whenStable();
  fixture.detectChanges();

  return { fixture, transport, navigated, stopped };
}

describe('SignOutComponent', () => {
  it('stops the idle watch, ends the session and forwards the browser to the login page', async () => {
    const { fixture, transport, navigated, stopped } = await arrive({ status: 204, body: undefined });
    const auth = TestBed.inject(AuthService);
    expect(transport.requests.map((request) => `${request.method} ${request.url}`)).toEqual([
      'DELETE /api/user/session',
    ]);
    expect(stopped).toHaveBeenCalledTimes(1);
    expect(navigated).toHaveBeenCalledWith('/login');
    expect(auth.authenticated()).toBe(false);
    expect((fixture.nativeElement as HTMLElement).textContent?.trim()).toBe('Signing out…');
  });

  it('forwards to the login path the application configured', async () => {
    const { navigated } = await arrive({ status: 204, body: undefined }, '/sign-in');
    expect(navigated).toHaveBeenCalledWith('/sign-in');
  });

  it('forwards all the same when the server no longer holds the session', async () => {
    const { transport, navigated } = await arrive({ status: 401, body: { message: 'no session' } });
    expect(transport.requests).toHaveLength(1);
    expect(navigated).toHaveBeenCalledWith('/login');
  });
});
