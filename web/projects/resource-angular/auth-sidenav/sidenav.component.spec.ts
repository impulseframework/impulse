import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ApiDescriptor, createClient } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { firstValueFrom } from 'rxjs';

import { NavGroups, SidenavComponent } from './sidenav.component';

describe('SidenavComponent', () => {
  let component: SidenavComponent;
  let fixture: ComponentFixture<SidenavComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SidenavComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(SidenavComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// A link behind a feature flag is drawn only while the flag is on; a link naming no flag
// is drawn as before. The session must be authenticated for any link to be drawn at all.
describe('SidenavComponent feature flags', () => {
  const descriptor: ApiDescriptor = {
    resources: {},
    methods: {},
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    features: { route: 'features' },
  };
  let enabled: string[] = [];
  const navGroups: NavGroups = {
    main: [
      { type: 'link', label: 'Missions', routerLink: ['/missions'] },
      { type: 'link', label: 'Debriefs', routerLink: ['/debriefs'], feature: 'debriefs' },
    ],
  };

  async function sidenav(): Promise<ComponentFixture<SidenavComponent>> {
    const transport = scriptedTransport((request) => {
      switch (request.url) {
        case '/api/user/session':
          return { status: 200, body: { authenticated: true } };
        case '/api/features':
          return { status: 200, body: { enabled } };
        case '/api/permission-digest':
          return { status: 200, body: {} };
        case '/api/user-domains':
          return { status: 200, body: [] };
        default:
          return { status: 404, body: { message: `unscripted ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [SidenavComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const fixture = TestBed.createComponent(SidenavComponent);
    fixture.componentInstance.navGroups = navGroups;
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    return fixture;
  }

  const labels = (fixture: ComponentFixture<SidenavComponent>): string[] =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('a .text')).map(
      (el) => el.textContent?.trim() ?? '',
    );

  it('hides a link whose flag is off and draws one naming no flag', async () => {
    enabled = [];
    const fixture = await sidenav();
    expect(labels(fixture)).toEqual(['Missions']);
  });

  it('draws a link whose flag is on, and hides it again when a refresh turns the flag off', async () => {
    enabled = ['debriefs'];
    const fixture = await sidenav();
    expect(labels(fixture)).toEqual(['Missions', 'Debriefs']);

    enabled = [];
    await TestBed.inject(RESOURCE_CLIENT).features.refresh();
    TestBed.tick();
    expect(labels(fixture)).toEqual(['Missions']);
  });
});
