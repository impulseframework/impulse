import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { apiDescriptor, createApi, createTenantRoute } from '@app/service/zz_gen_api';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { NewTenantData, NewTenantDialogComponent } from './new-tenant-dialog.component';

/**
 * The server as the spec plays it through the client's transport: a session holding the
 * tenants listed, with an empty digest everywhere, and the consolidated route the record's
 * create goes through, answering the new key. The self-service post goes through
 * HttpClient, which the testing controller answers below.
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
      case `PATCH ${environment.apiUrl}/resources`:
        return { status: 200, body: { tenants: ['west'] } };
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

  async function render(data: NewTenantData): Promise<void> {
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
        { provide: MAT_DIALOG_DATA, useValue: data },
        { provide: MatDialogRef, useValue: { close: (id?: string): void => void closed.push(id) } },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(NewTenantDialogComponent);
    fixture.detectChanges();
  }

  afterEach(() => http.verify());

  it('renders the form for a session holding no tenant, submittable once the key and the name are filled', async () => {
    await render({ selfService: true });
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('h2')?.textContent).toContain('Create your tenant');
    expect(element.textContent).toContain('you become its administrator');
    const submit = element.querySelector<HTMLButtonElement>('button[type="submit"]');
    expect(submit?.disabled).toBe(true);

    fixture.componentInstance.form.setValue({ id: 'West', name: 'West' });
    fixture.detectChanges();
    expect(fixture.componentInstance.form.controls.id.hasError('pattern')).toBe(true);
    expect(submit?.disabled).toBe(true);

    fixture.componentInstance.form.setValue({ id: 'west', name: 'West' });
    fixture.detectChanges();
    expect(submit?.disabled).toBe(false);
  });

  it('posts the record to the generated route, reloads what the session holds and opens the dashboard under the new tenant', async () => {
    await render({ selfService: true });
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentInstance.form.setValue({ id: 'west', name: 'West' });
    const pending = fixture.componentInstance.create();

    const request = http.expectOne(`${environment.apiUrl}/${createTenantRoute}`);
    expect(request.request.method).toBe('POST');
    expect(request.request.body).toEqual({ id: 'west', name: 'West' });
    // The server has the tenant now: the reload lists it.
    domains = [{ domain: 'west', name: 'West' }];
    request.flush({ id: 'west' });
    await pending;

    expect(TestBed.inject(AuthService).domains()).toEqual(['west']);
    // The guard on the tenant segment is the tenant's one writer: the dialog only navigates there.
    expect(TestBed.inject(TenantService).current()).toBe('');
    expect(closed).toEqual(['west']);
    expect(navigated).toHaveBeenCalledWith(['/', apiDescriptor.domainRoute.segment, 'west', 'dashboard']);
    expect(fixture.componentInstance.error()).toBe('');
  });

  it("reports the server's refusal in place and stays open", async () => {
    await render({ selfService: true });
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    fixture.componentInstance.form.setValue({ id: 'west', name: 'West' });
    const pending = fixture.componentInstance.create();

    http
      .expectOne(`${environment.apiUrl}/${createTenantRoute}`)
      .flush(
        { message: 'the session already holds a tenant; a person creates one tenant and becomes its administrator' },
        { status: 403, statusText: 'Forbidden' },
      );
    await pending;
    fixture.detectChanges();

    expect(fixture.componentInstance.error()).toBe(
      'the session already holds a tenant; a person creates one tenant and becomes its administrator',
    );
    expect((fixture.nativeElement as HTMLElement).querySelector('.refusal')?.textContent).toContain(
      'already holds a tenant',
    );
    expect(closed).toEqual([]);
    expect(navigated).not.toHaveBeenCalled();
  });

  it("creates the record through the client for an administrator and opens the record's page", async () => {
    await render({ selfService: false });
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    expect((fixture.nativeElement as HTMLElement).querySelector('h2')?.textContent).toContain('New tenant');
    fixture.componentInstance.form.setValue({ id: 'west', name: 'West' });
    await fixture.componentInstance.create();

    const created = transport.requests.find((request) => request.method === 'PATCH');
    expect(created?.url).toBe(`${environment.apiUrl}/resources`);
    // The key travels in the operation's path, the rest in its value.
    expect(created?.body).toEqual([{ op: 'add', path: '/tenants/west', value: { name: 'West' } }]);
    expect(closed).toEqual(['west']);
    expect(navigated).toHaveBeenCalledWith(['/', 'tenants', 'west']);
  });
});
