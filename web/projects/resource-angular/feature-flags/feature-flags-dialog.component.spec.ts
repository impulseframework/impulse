import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { ApiDescriptor, createClient, FeatureFlag, featureFlagsResource, setFeatureMethod } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';

import { FeatureFlagsDialogComponent, onForSentence } from './feature-flags-dialog.component';
import { featureFlagsDialogDefaults, openFeatureFlagsDialog } from './open-feature-flags-dialog';

// The dialog lists every flag with its description and how long it has been on, flips
// one through the generated method when the digest grants it (the client then refreshing
// the set and the digest, the dialog reading the flags again), shows a read-only list
// without the grant, and shows a refusal inline in the server's words; on an API that
// serves no feature flags it says so in place of the list.

const descriptor: ApiDescriptor = {
  resources: {
    [featureFlagsResource]: {
      resource: featureFlagsResource,
      property: 'featureFlags',
      route: 'feature-flags',
      scope: 'global',
      consolidated: false,
      keys: ['name'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
  },
  methods: {
    [setFeatureMethod]: { method: setFeatureMethod, property: 'setFeature', route: 'set-feature', scope: 'global', answers: true },
  },
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

const day = 86_400_000;
const now = Date.now();

const flags = (): FeatureFlag[] => [
  {
    name: 'debriefs',
    description: 'Crews file a debrief after a sortie.',
    enabled: true,
    updatedAt: new Date(now - 3 * day - 60_000).toISOString(),
    updatedBy: 'hollis',
  },
  { name: 'manifests', description: 'Cargo manifests on the consignment page.', enabled: false, updatedAt: new Date(now - 30 * day).toISOString(), updatedBy: 'migrate' },
];

interface Server {
  granted: boolean;
  rows: FeatureFlag[];
  flip: (name: string, enabled: boolean) => { status: number; body: unknown };
}

function serverWith(server: Server): ScriptedTransport {
  return scriptedTransport((request) => {
    const path = request.url.replace(/\?.*$/, '');
    switch (path) {
      case '/api/feature-flags':
        return { status: 200, body: server.rows };
      case '/api/features':
        return { status: 200, body: { enabled: server.rows.filter((flag) => flag.enabled).map((flag) => flag.name) } };
      case '/api/permission-digest':
        return { status: 200, body: server.granted ? { [setFeatureMethod]: { Execute: 'granted' } } : {} };
      case '/api/user-domains':
        return { status: 200, body: [] };
      case '/api/set-feature': {
        const body = request.body as { name: string; enabled: boolean };
        return server.flip(body.name, body.enabled);
      }
      default:
        return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
  });
}

/** A server whose flip writes the row and answers it. */
function writingServer(granted: boolean): Server {
  const server: Server = {
    granted,
    rows: flags(),
    flip: (name, enabled) => {
      const row = server.rows.find((flag) => flag.name === name);
      if (!row) {
        return { status: 404, body: { message: `no feature named ${name}` } };
      }
      row.enabled = enabled;
      row.updatedAt = new Date(now).toISOString();
      return { status: 200, body: { name, enabled, updatedAt: row.updatedAt } };
    },
  };
  return server;
}

async function dialogOver(
  transport: ScriptedTransport,
  api: ApiDescriptor = descriptor,
): Promise<ComponentFixture<FeatureFlagsDialogComponent>> {
  await TestBed.configureTestingModule({
    imports: [FeatureFlagsDialogComponent],
    providers: [
      provideResourceTesting({ transport, client: (t) => createClient(api, { baseUrl: '/api', transport: t }) }),
      { provide: MatDialogRef, useValue: { close: () => undefined } },
    ],
  }).compileComponents();
  // The digest decides what the dialog may do; the application loads it at sign-in.
  await TestBed.inject(RESOURCE_CLIENT).permissions.loadDigest();
  const fixture = TestBed.createComponent(FeatureFlagsDialogComponent);
  fixture.detectChanges();
  await TestBed.inject(ApplicationRef).whenStable();
  fixture.detectChanges();
  return fixture;
}

const rowsOf = (fixture: ComponentFixture<FeatureFlagsDialogComponent>): HTMLElement[] =>
  Array.from((fixture.nativeElement as HTMLElement).querySelectorAll<HTMLElement>('li.feature-flag'));
const togglesOf = (fixture: ComponentFixture<FeatureFlagsDialogComponent>): HTMLButtonElement[] =>
  Array.from((fixture.nativeElement as HTMLElement).querySelectorAll<HTMLButtonElement>('button[role="switch"]'));
const textOf = (fixture: ComponentFixture<FeatureFlagsDialogComponent>): string =>
  (fixture.nativeElement as HTMLElement).textContent ?? '';

describe('onForSentence', () => {
  const at = new Date('2026-10-02T12:00:00Z');
  const cases: { name: string; updatedAt: string; want: string }[] = [
    { name: 'flipped minutes ago', updatedAt: '2026-10-02T11:30:00Z', want: 'on for less than a day in this environment' },
    { name: 'flipped a day ago', updatedAt: '2026-10-01T11:00:00Z', want: 'on for 1 day in this environment' },
    { name: 'flipped three days ago', updatedAt: '2026-09-29T08:00:00Z', want: 'on for 3 days in this environment' },
    { name: 'a timestamp in the future, by clock skew', updatedAt: '2026-10-03T12:00:00Z', want: 'on for less than a day in this environment' },
    { name: 'a timestamp that cannot be read', updatedAt: 'yesterday', want: 'on in this environment' },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(onForSentence(tt.updatedAt, at)).toBe(tt.want);
    });
  }
});

describe('FeatureFlagsDialogComponent', () => {
  it('lists every flag by name with its description, and how long an enabled one has been on', async () => {
    const transport = serverWith(writingServer(true));
    const fixture = await dialogOver(transport);
    const rows = rowsOf(fixture);
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('debriefs');
    expect(rows[0].textContent).toContain('Crews file a debrief after a sortie.');
    expect(rows[0].textContent).toContain('on for 3 days in this environment');
    expect(rows[1].textContent).toContain('manifests');
    expect(rows[1].textContent).not.toContain('in this environment');
    expect(transport.requests.filter((r) => r.url.startsWith('/api/feature-flags')).map((r) => r.url)).toEqual([
      '/api/feature-flags?sort=name&limit=all',
    ]);
    expect(togglesOf(fixture).map((toggle) => toggle.getAttribute('aria-checked'))).toEqual(['true', 'false']);
  });

  it('without Execute on the flip method the list is read-only: the toggles are disabled and the dialog says so', async () => {
    const fixture = await dialogOver(serverWith(writingServer(false)));
    expect(fixture.componentInstance.canSet()).toBe(false);
    expect(togglesOf(fixture).map((toggle) => toggle.disabled)).toEqual([true, true]);
    expect(textOf(fixture)).toContain('You can see the flags but not change them.');
  });

  it('with the grant a toggle flips the flag through the generated method, the client refreshes the set and the digest, and the flags are read again', async () => {
    const server = writingServer(true);
    const transport = serverWith(server);
    const fixture = await dialogOver(transport);
    expect(togglesOf(fixture).map((toggle) => toggle.disabled)).toEqual([false, false]);
    expect(textOf(fixture)).not.toContain('You can see the flags but not change them.');
    transport.requests.length = 0;

    await fixture.componentInstance.toggle(fixture.componentInstance.flags()[1], true);
    fixture.detectChanges();

    expect(transport.requests[0].method).toBe('POST');
    expect(transport.requests[0].url).toBe('/api/set-feature');
    expect(transport.requests[0].body).toEqual({ name: 'manifests', enabled: true });
    const after = transport.requests.slice(1).map((r) => r.url.replace(/\?.*$/, ''));
    expect(after).toContain('/api/features');
    expect(after).toContain('/api/permission-digest');
    expect(after).toContain('/api/user-domains');
    expect(after[after.length - 1]).toBe('/api/feature-flags');
    expect(TestBed.inject(RESOURCE_CLIENT).features.enabled('manifests')).toBe(true);
    expect(rowsOf(fixture)[1].textContent).toContain('on for less than a day in this environment');
    expect(fixture.componentInstance.error()).toBeUndefined();
    expect(fixture.componentInstance.pending()).toBeUndefined();
  });

  it('a refused flip shows the server\'s message inline, refreshes nothing, and reads the flags again so the toggle stays where the server says', async () => {
    const server = writingServer(true);
    server.flip = () => ({ status: 403, body: { message: 'FeatureAdministrator required' } });
    const transport = serverWith(server);
    const fixture = await dialogOver(transport);
    transport.requests.length = 0;

    await fixture.componentInstance.toggle(fixture.componentInstance.flags()[1], true);
    fixture.detectChanges();

    expect(transport.requests.map((r) => r.url.replace(/\?.*$/, ''))).toEqual(['/api/set-feature', '/api/feature-flags']);
    expect(fixture.componentInstance.error()).toBe('FeatureAdministrator required');
    expect((fixture.nativeElement as HTMLElement).querySelector('[role="alert"]')?.textContent).toContain(
      'FeatureAdministrator required',
    );
    expect(togglesOf(fixture)[1].getAttribute('aria-checked')).toBe('false');
    expect(TestBed.inject(RESOURCE_CLIENT).features.enabled('manifests')).toBe(false);
  });

  it('an API that declares no flags resource says so inline instead of a list', async () => {
    const transport = serverWith(writingServer(true));
    const fixture = await dialogOver(transport, { ...descriptor, resources: {} });
    expect(rowsOf(fixture)).toHaveLength(0);
    expect(fixture.componentInstance.error()).toMatch(/declares no FeatureFlags resource/);
    expect(textOf(fixture)).toContain('declares no FeatureFlags resource');
    expect(transport.requests.filter((r) => r.url.startsWith('/api/feature-flags'))).toHaveLength(0);
  });

  it('an API that serves no feature flags says so inline instead of a list, asking for nothing', async () => {
    const transport = serverWith(writingServer(true));
    const fixture = await dialogOver(transport, { ...descriptor, features: undefined });
    expect(rowsOf(fixture)).toHaveLength(0);
    expect(fixture.componentInstance.error()).toMatch(/serves no feature flags/);
    expect(textOf(fixture)).toContain('this API serves no feature flags');
    expect(transport.requests.filter((r) => r.url.startsWith('/api/feature-flags'))).toHaveLength(0);
  });

  it('a server that refuses the list shows the refusal inline', async () => {
    const transport = scriptedTransport((request) =>
      request.url.startsWith('/api/feature-flags')
        ? { status: 403, body: { message: 'List on FeatureFlags required' } }
        : { status: 200, body: request.url === '/api/permission-digest' ? {} : [] },
    );
    const fixture = await dialogOver(transport);
    expect(fixture.componentInstance.error()).toBe('List on FeatureFlags required');
    expect(textOf(fixture)).toContain('List on FeatureFlags required');
    expect(textOf(fixture)).not.toContain('declares no feature flags');
  });
});

describe('openFeatureFlagsDialog', () => {
  it('opens the dialog component through the given MatDialog with the defaults, a passed config overriding them', async () => {
    await TestBed.configureTestingModule({
      providers: [provideResourceTesting({ transport: serverWith(writingServer(true)) })],
    }).compileComponents();
    const dialog = TestBed.inject(MatDialog);
    const opened: { component: unknown; config: unknown }[] = [];
    vi.spyOn(dialog, 'open').mockImplementation(((component: unknown, config: unknown) => {
      opened.push({ component, config });
      return { close: () => undefined } as unknown as MatDialogRef<FeatureFlagsDialogComponent>;
    }) as typeof dialog.open);

    openFeatureFlagsDialog(dialog);
    openFeatureFlagsDialog(dialog, { width: '400px' });

    expect(opened).toEqual([
      { component: FeatureFlagsDialogComponent, config: featureFlagsDialogDefaults },
      { component: FeatureFlagsDialogComponent, config: { ...featureFlagsDialogDefaults, width: '400px' } },
    ]);
  });
});
