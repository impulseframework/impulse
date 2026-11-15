import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { NewTenantDialogComponent } from './new-tenant-dialog.component';

/** The id the server generates for the tenant the spec creates. */
const west = '7d3f2a10-5b6c-4d7e-8f90-a1b2c3d4e5f6';

/**
 * The server as the spec plays it: an administrator's session with an empty digest, and
 * the consolidated route the record's create goes through, answering the id it generated
 * or, when another tenant carries the name, the server's refusal.
 */
function server(taken: () => boolean): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://skeleton.test');
    switch (`${request.method} ${url.pathname}`) {
      case `GET ${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'admin' } };
      case `GET ${environment.apiUrl}/permission-digest`:
        return { status: 200, body: {} };
      case `GET ${environment.apiUrl}/user-domains`:
        return { status: 200, body: [] };
      case `GET ${environment.apiUrl}/features`:
        return { status: 200, body: { enabled: [] } };
      case `PATCH ${environment.apiUrl}/resources`:
        return taken()
          ? { status: 409, body: { message: 'Tenants: a record with this key or a unique value already exists.' } }
          : { status: 200, body: { tenants: [west] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('NewTenantDialogComponent', () => {
  let fixture: ComponentFixture<NewTenantDialogComponent>;
  let transport: ScriptedTransport;
  let taken: boolean;
  let closed: (string | undefined)[];

  beforeEach(async () => {
    taken = false;
    closed = [];
    transport = scriptedTransport(server(() => taken));
    await TestBed.configureTestingModule({
      imports: [NewTenantDialogComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: MatDialogRef, useValue: { close: (id?: string): void => void closed.push(id) } },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(NewTenantDialogComponent);
    fixture.detectChanges();
  });

  it('is submittable once the name is filled', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('h2')?.textContent).toContain('New tenant');
    const submit = element.querySelector<HTMLButtonElement>('button[type="submit"]');
    expect(submit?.disabled).toBe(true);

    // The name is all a person gives: the form asks for no key.
    expect(element.querySelectorAll('input').length).toBe(1);

    fixture.componentInstance.form.setValue({ name: 'West' });
    fixture.detectChanges();
    expect(submit?.disabled).toBe(false);
  });

  it("creates the record through the client and opens the record's page", async () => {
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentInstance.form.setValue({ name: 'West' });
    await fixture.componentInstance.create();

    const created = transport.requests.find((request) => request.method === 'PATCH');
    expect(created?.url).toBe(`${environment.apiUrl}/resources`);
    // The create names no key: the server generates the id and answers it.
    expect(created?.body).toEqual([{ op: 'add', path: '/tenants', value: { name: 'West' } }]);
    expect(closed).toEqual([west]);
    expect(navigated).toHaveBeenCalledWith(['/', 'tenants', west]);
  });

  it('says a name another tenant carries is taken, in place, and stays open', async () => {
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    taken = true;
    fixture.componentInstance.form.setValue({ name: 'West' });
    await fixture.componentInstance.create();
    fixture.detectChanges();

    // The server answers 409 in the words of its tables; the dialog says what the person can act on.
    expect(fixture.componentInstance.error()).toBe('A tenant named West already exists. Choose another name.');
    expect((fixture.nativeElement as HTMLElement).querySelector('.refusal')?.textContent).toContain(
      'A tenant named West already exists',
    );
    expect(closed).toEqual([]);
    expect(navigated).not.toHaveBeenCalled();
  });
});
