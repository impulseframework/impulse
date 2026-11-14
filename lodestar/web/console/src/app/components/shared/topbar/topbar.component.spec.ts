import { ComponentFixture, TestBed } from '@angular/core/testing';
import { createApi } from '@app/service/zz_gen_api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { Domain, MenuItem } from '@impulseframework/resource-angular/types';
import { SectorService } from '@components/sector/sector.service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { TopbarComponent } from './topbar.component';

describe('TopbarComponent', () => {
  let fixture: ComponentFixture<TopbarComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TopbarComponent],
      // The sector service scopes each item's permission question through the generated client.
      providers: [provideResourceTesting({ client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) })],
    }).compileComponents();

    fixture = TestBed.createComponent(TopbarComponent);
    fixture.detectChanges();
  });

  it('creates the menu over the generated navigation items', () => {
    expect(fixture.componentInstance).toBeTruthy();
    expect(fixture.nativeElement.querySelector('nav.menu')).not.toBeNull();
  });

  // An item under the sector segment names the segment's route parameter: the open sector
  // fills it, and outside a sector the item has no link and is not drawn; a global item's
  // route is its own.
  const cases: { name: string; route: string[]; sector: string; want: string[] | undefined }[] = [
    { name: 'a global item keeps its route', route: ['pilots'], sector: 'anvil', want: ['pilots'] },
    { name: 'a global item keeps its route outside a sector', route: ['pilots'], sector: '', want: ['pilots'] },
    {
      name: 'a deck lands under the open sector',
      route: [DomainRoutePath, 'flight-deck'],
      sector: 'anvil',
      want: ['sectors/anvil', 'flight-deck'],
    },
    {
      name: 'a config-driven page registered at its route lands under the open sector',
      route: [`${DomainRoutePath}/missions`],
      sector: 'anvil',
      want: ['sectors/anvil/missions'],
    },
    {
      name: 'a sector item has no link outside a sector',
      route: [DomainRoutePath, 'flight-deck'],
      sector: '',
      want: undefined,
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      // Entered as the sector guard does; the top bar itself writes nothing.
      TestBed.inject(SectorService).enter(tt.sector);
      const item: MenuItem = { label: 'Deck', route: tt.route };

      expect(fixture.componentInstance.linkFor(item)).toEqual(tt.want);
    });
  }
});

// A group is drawn while one of its items is. The fleet-wide lists of accounts are granted
// to the Fleet Governor alone, so a persona whose digest holds none of them is shown no
// Crew accounts group, and one whose digest holds one is shown the group with that item. A
// group of sector pages is not drawn outside a sector: its items have no link there.
describe('TopbarComponent groups', () => {
  const logins: MenuItem = {
    label: 'Crew accounts',
    children: [
      { label: 'Crew accounts', route: ['crew-accounts'], permission: { resource: Resources.CrewSessionUsers, permission: Permissions.List } },
      { label: 'Sessions', route: ['crew-sessions'], permission: { resource: Resources.CrewSessions, permission: Permissions.List } },
    ],
  };
  const people: MenuItem = {
    label: 'Sector crew',
    children: [
      {
        label: 'People',
        route: [`${DomainRoutePath}/crew-people`],
        permission: { resource: Resources.CrewSectorUsers, permission: Permissions.List },
      },
    ],
  };
  const sectorDigest: PermissionDigest = { [Resources.CrewSectorUsers]: { List: 'granted' } };

  async function topbar(globalDigest: PermissionDigest, sector: string): Promise<TopbarComponent> {
    const transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lodestar.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/user/session`:
          return { status: 200, body: { authenticated: true, username: 'marshal' } };
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: url.searchParams.get('domain') ? sectorDigest : globalDigest };
        case `${environment.apiUrl}/user-domains`:
          return { status: 200, body: ['anvil'] };
        case `${environment.apiUrl}/features`:
          return { status: 200, body: { enabled: [] } };
        default:
          return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [TopbarComponent],
      providers: [provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) })],
    }).compileComponents();
    const auth = TestBed.inject(AuthService);
    await firstValueFrom(auth.checkUserSession());
    if (sector) {
      TestBed.inject(SectorService).enter(sector);
      await firstValueFrom(auth.loadDigest(sector as Domain));
    }
    const fixture = TestBed.createComponent(TopbarComponent);
    fixture.detectChanges();
    return fixture.componentInstance;
  }

  const cases: { name: string; digest: PermissionDigest; sector: string; item: MenuItem; want: boolean }[] = [
    { name: 'a group none of whose pages the persona may open is not drawn', digest: {}, sector: 'anvil', item: logins, want: false },
    {
      name: 'a group is drawn for one page the persona may open',
      digest: { [Resources.CrewSessions]: { List: 'granted' } },
      sector: 'anvil',
      item: logins,
      want: true,
    },
    { name: 'a group of sector pages is drawn inside a sector that grants one', digest: {}, sector: 'anvil', item: people, want: true },
    { name: 'a group of sector pages is not drawn outside a sector', digest: {}, sector: '', item: people, want: false },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const component = await topbar(tt.digest, tt.sector);

      expect(component.shown(tt.item)).toBe(tt.want);
    });
  }
});
