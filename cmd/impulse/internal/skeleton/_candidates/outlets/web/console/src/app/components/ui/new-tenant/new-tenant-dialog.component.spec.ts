import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { apiDescriptor, createApi, createTenantRoute } from '@app/service/zz_gen_api';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { NewTenantDialogComponent } from './new-tenant-dialog.component';

/** The id the server generates for the tenant the spec creates. */
const west = '7d3f2a10-5b6c-4d7e-8f90-a1b2c3d4e5f6';

/**
 * The server as the spec plays it through the client's transport: a session holding the
 * tenants listed, with an empty digest everywhere. The post to the create-your-tenant
 * route goes through HttpClient, which the testing controller answers below. Nothing is
 * scripted for a create on the Tenant record: the dialog never sends one.
 */
function server(domains: () => { domain: string; name: string }[]): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://skeleton.test');
    switch (`${request.method} ${url.pathname}`) {
      case `GET ${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'newcomer' } };
      case `GET ${environment.apiUrl}/permission-digest`:
        return { status: 200, body: {} };
      case `GET ${environment.apiUrl}/user-domains`:
        return { status: 200, body: domains() };
      case `GET ${environment.apiUrl}/features`:
        return { status: 200, body: { enabled: [] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('NewTenantDialogComponent', () => {
  let fixture: ComponentFixture<NewTenantDialogComponent>;
  let transport: ScriptedTransport;
  let http: HttpTestingController;
  let domains: { domain: string; name: string }[];
  let closed: (string | undefined)[];

  async function render(): Promise<void> {
    domains = [];
    closed = [];
    transport = scriptedTransport(server(() => domains));
    await TestBed.configureTestingModule({
      imports: [NewTenantDialogComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideFrame({ home: 'dashboard', menus: [] }),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: MatDialogRef, useValue: { close: (id?: string): void => void closed.push(id) } },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(NewTenantDialogComponent);
    fixture.detectChanges();
  }

  afterEach(() => http.verify());

  it('renders the form for a session holding no tenant, submittable once the name is filled', async () => {
    await render();
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('h2')?.textContent).toContain('Create your tenant');
    expect(element.textContent).toContain('you become its administrator');
    const submit = element.querySelector<HTMLButtonElement>('button[type="submit"]');
    expect(submit?.disabled).toBe(true);

    // The name is all a person gives: the form asks for no key.
    expect(element.querySelectorAll('input').length).toBe(1);

    fixture.componentInstance.form.setValue({ name: 'West' });
    fixture.detectChanges();
    expect(submit?.disabled).toBe(false);
  });

  it('posts the record to the generated route, reloads what the session holds and opens the dashboard under the new tenant', async () => {
    await render();
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentInstance.form.setValue({ name: 'West' });
    const pending = fixture.componentInstance.create();

    const request = http.expectOne(`${environment.apiUrl}/${createTenantRoute}`);
    expect(request.request.method).toBe('POST');
    expect(request.request.body).toEqual({ name: 'West' });
    // The server has the tenant now, under the id it generated: the reload lists it.
    domains = [{ domain: west, name: 'West' }];
    request.flush({ id: west });
    await pending;

    expect(TestBed.inject(AuthService).domains()).toEqual([west]);
    // Nobody creates the Tenant record directly: the dialog sent no create through the client.
    expect(transport.requests.some((sent) => sent.method === 'PATCH')).toBe(false);
    // The guard on the tenant segment is the tenant's one writer: the dialog only navigates there.
    expect(TestBed.inject(TenantService).current()).toBe('');
    expect(closed).toEqual([west]);
    expect(navigated).toHaveBeenCalledWith(['/', apiDescriptor.domainRoute.segment, west, 'dashboard']);
    expect(fixture.componentInstance.error()).toBe('');
  });

  it("reports the server's refusal in place and stays open", async () => {
    await render();
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentInstance.form.setValue({ name: 'West' });
    const pending = fixture.componentInstance.create();

    const refusal = {
      message: 'the session already holds a tenant; a person creates one tenant and becomes its administrator',
    };
    const request = http.expectOne(`${environment.apiUrl}/${createTenantRoute}`);
    request.flush(refusal, { status: 403, statusText: 'Forbidden' });
    await pending;
    fixture.detectChanges();

    expect(fixture.componentInstance.error()).toBe(refusal.message);
    expect((fixture.nativeElement as HTMLElement).querySelector('.refusal')?.textContent).toContain(
      'already holds a tenant',
    );
    expect(closed).toEqual([]);
    expect(navigated).not.toHaveBeenCalled();
  });

  it('says a name another tenant carries is taken, in place, and stays open', async () => {
    await render();
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentInstance.form.setValue({ name: 'West' });
    const pending = fixture.componentInstance.create();

    // The server answers 409 in the words of its tables; the dialog says what the person can act on.
    http
      .expectOne(`${environment.apiUrl}/${createTenantRoute}`)
      .flush(
        { message: 'Tenants, StaffUserRoles: a record with this key or a unique value already exists.' },
        { status: 409, statusText: 'Conflict' },
      );
    await pending;
    fixture.detectChanges();

    expect(fixture.componentInstance.error()).toBe('A tenant named West already exists. Choose another name.');
    expect((fixture.nativeElement as HTMLElement).querySelector('.refusal')?.textContent).toContain(
      'A tenant named West already exists',
    );
    expect(closed).toEqual([]);
    expect(navigated).not.toHaveBeenCalled();
  });
});
