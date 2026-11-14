import { ComponentFixture, TestBed } from '@angular/core/testing';
import { createApi } from '@app/service/zz_gen_api';
import { Permissions } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { TenantService } from '@app/tenant/tenant.service';
import { PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { MenuItem, Resource } from '@impulseframework/resource-angular/types';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { TopbarComponent } from './topbar.component';

describe('TopbarComponent', () => {
  let fixture: ComponentFixture<TopbarComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TopbarComponent],
      providers: [provideResourceTesting({ client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) })],
    }).compileComponents();

    fixture = TestBed.createComponent(TopbarComponent);
    fixture.detectChanges();
  });

  it('creates the menu over the generated navigation items', () => {
    expect(fixture.componentInstance).toBeTruthy();
    expect(fixture.nativeElement.querySelector('nav.menu')).not.toBeNull();
  });

  // An item under the tenant segment names the segment's route parameter: the open tenant
  // fills it, and outside a tenant the item has no link and is not drawn; a global item's
  // route is its own.
  const cases: { name: string; route: string[]; tenant: string; want: string[] | undefined }[] = [
    { name: 'a global item keeps its route', route: ['users'], tenant: 'acme', want: ['users'] },
    { name: 'a global item keeps its route outside a tenant', route: ['users'], tenant: '', want: ['users'] },
    {
      name: 'a tenant item lands under the open tenant',
      route: [DomainRoutePath, 'users'],
      tenant: 'acme',
      want: ['tenants/acme', 'users'],
    },
    {
      name: 'a tenant item has no link outside a tenant',
      route: [DomainRoutePath, 'users'],
      tenant: '',
      want: undefined,
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      // Entered as the tenant guard does; the top bar itself writes nothing.
      TestBed.inject(TenantService).enter(tt.tenant);
      const item: MenuItem = { label: 'Users', route: tt.route };

      expect(fixture.componentInstance.linkFor(item)).toEqual(tt.want);
    });
  }
});

// A group is drawn while one of its items is, so nobody is shown a menu that opens on
// nothing: a group of pages the user may not open is not drawn, and neither is a group of
// a tenant's pages outside a tenant, where its items have no link, whatever the user
// holds. The resources are named for the test alone, so the cases stand whichever
// resources the application declares.
describe('TopbarComponent groups', () => {
  const ledgers = 'Ledgers' as Resource;
  const notices = 'Notices' as Resource;
  const settings: MenuItem = {
    label: 'Settings',
    children: [{ label: 'Ledgers', route: ['ledgers'], permission: { resource: ledgers, permission: Permissions.List } }],
  };
  const board: MenuItem = {
    label: 'Board',
    children: [
      {
        label: 'Notices',
        route: [`${DomainRoutePath}/notices`],
        permission: { resource: notices, permission: Permissions.List },
      },
    ],
  };

  async function topbar(digest: PermissionDigest, tenant: string): Promise<TopbarComponent> {
    const transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://skeleton.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/user/session`:
          return { status: 200, body: { authenticated: true, username: 'admin' } };
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: digest };
        case `${environment.apiUrl}/user-domains`:
          return { status: 200, body: ['acme'] };
        case `${environment.apiUrl}/features`:
          return { status: 200, body: { enabled: [] } };
        default:
          return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [TopbarComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    TestBed.inject(TenantService).enter(tenant);
    const fixture = TestBed.createComponent(TopbarComponent);
    fixture.detectChanges();
    return fixture.componentInstance;
  }

  const granted: PermissionDigest = { [ledgers]: { List: 'granted' }, [notices]: { List: 'granted' } };
  const cases: { name: string; digest: PermissionDigest; tenant: string; item: MenuItem; want: boolean }[] = [
    { name: 'a group none of whose pages the user may open is not drawn', digest: {}, tenant: 'acme', item: settings, want: false },
    { name: 'a group is drawn for one page the user may open', digest: granted, tenant: 'acme', item: settings, want: true },
    { name: "a group of a tenant's pages is drawn inside a tenant", digest: granted, tenant: 'acme', item: board, want: true },
    { name: "a group of a tenant's pages is not drawn outside a tenant", digest: granted, tenant: '', item: board, want: false },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const component = await topbar(tt.digest, tt.tenant);

      expect(component.shown(tt.item)).toBe(tt.want);
    });
  }
});
