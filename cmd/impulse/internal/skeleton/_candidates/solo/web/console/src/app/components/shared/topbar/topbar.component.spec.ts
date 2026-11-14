import { ComponentFixture, TestBed } from '@angular/core/testing';
import { createApi } from '@app/service/zz_gen_api';
import { Permissions } from '@app/service/zz_gen_constants';
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
});

// A group is drawn while one of its items is, so nobody is shown a menu that opens on
// nothing: a group of pages the user may not open is not drawn. The resources are named
// for the test alone, so the cases stand whichever resources the application declares.
describe('TopbarComponent groups', () => {
  const ledgers = 'Ledgers' as Resource;
  const notices = 'Notices' as Resource;
  const group: MenuItem = {
    label: 'Records',
    children: [
      { label: 'Ledgers', route: ['ledgers'], permission: { resource: ledgers, permission: Permissions.List } },
      { label: 'Notices', route: ['notices'], permission: { resource: notices, permission: Permissions.List } },
    ],
  };

  async function topbar(digest: PermissionDigest): Promise<TopbarComponent> {
    const transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://skeleton.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/user/session`:
          return { status: 200, body: { authenticated: true, username: 'admin' } };
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: digest };
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
    const fixture = TestBed.createComponent(TopbarComponent);
    fixture.detectChanges();
    return fixture.componentInstance;
  }

  const cases: { name: string; digest: PermissionDigest; want: boolean }[] = [
    { name: 'a group none of whose pages the user may open is not drawn', digest: {}, want: false },
    { name: 'a group is drawn for one page the user may open', digest: { [notices]: { List: 'granted' } }, want: true },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const component = await topbar(tt.digest);

      expect(component.shown(group)).toBe(tt.want);
    });
  }
});
