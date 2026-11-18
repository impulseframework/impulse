import { ApplicationRef, Injector } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  FeatureFlag,
  featureFlagsResource,
  FieldMeta,
  MethodMeta,
  PermissionDigest,
  Resource,
  ResourceMeta,
  setFeatureMethod,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { pageDataKey } from '@impulseframework/resource-angular/auth-frame';
import {
  CompoundResourceComponent,
  MethodDialogData,
  ResourceListComponent,
} from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  dayAndMinuteFormat,
  ListPermission,
  ListViewConfig,
  METHOD_META,
  RESOURCE_META,
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { format } from 'date-fns';
import { of } from 'rxjs';

import {
  featureFlagsConfig,
  featureFlagsPage,
  featureFlagsPath,
  featureFlagsRoute,
  flagInWords,
  flipQuestion,
  noFeatureFlags,
} from './feature-flags-page';

// The Feature flags page is the library's list over the feature flags resource: each flag
// by its name in words with its description, whether it is on, and who last turned it on
// or off and when. A row's menu flips the flag through the generated method, asking first,
// for a person the digest grants the method; a person who may list the flags and not flip
// them sees the list and no menu. Every row carries a History button. An application that
// declares no flag reads that it has none.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: true, isIndex: false, ...extra }) as FieldMeta;

// The feature flags resource and the flip method as the generator describes them.
const flagsMeta = {
  route: 'feature-flags',
  createDisabled: true,
  updateDisabled: true,
  deleteDisabled: true,
  live: false,
  fields: [
    field('name', { primaryKey: { ordinalPosition: 1 }, isIndex: true }),
    field('description'),
    field('enabled', { displayType: 'boolean' }),
    field('updatedAt', { displayType: 'date' }),
    field('updatedBy'),
  ],
} as ResourceMeta;
const resourceMeta = (resource: Resource): ResourceMeta =>
  resource === featureFlagsResource ? flagsMeta : (undefined as unknown as ResourceMeta);
const flipMeta: MethodMeta = {
  route: 'set-feature',
  fields: [
    { fieldName: 'name', displayType: 'string' },
    { fieldName: 'enabled', displayType: 'boolean' },
  ],
} as MethodMeta;

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
      page: { default: 50 },
      order: [{ field: 'name', direction: 'asc' }],
    },
  },
  methods: {
    [setFeatureMethod]: {
      method: setFeatureMethod,
      property: 'setFeature',
      route: 'set-feature',
      scope: 'global',
      answers: true,
    },
  },
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

const flags: FeatureFlag[] = [
  {
    name: 'cargo_manifest',
    description: "Cargo manifest shows a ship's cargo bays.",
    enabled: true,
    updatedAt: '2026-11-17T15:42:10Z',
    updatedBy: 'morgan',
  },
  // Nobody has flipped it: the server answers no time and nobody.
  {
    name: 'persona_entry',
    description: 'Persona entry opens the persona sign-in.',
    enabled: false,
    updatedAt: null,
    updatedBy: '',
  },
];

/** Every column the page reads, listed by the caller. */
const listsFlags: PermissionDigest = {
  [featureFlagsResource]: { List: 'granted' },
  ...Object.fromEntries(
    ['name', 'description', 'enabled', 'updatedAt', 'updatedBy'].map((name) => [
      `${featureFlagsResource}.${name}`,
      { List: 'granted' },
    ]),
  ),
};
const flipsToo: PermissionDigest = { ...listsFlags, [setFeatureMethod]: { Execute: 'granted' } };

describe('the Feature flags page', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  let opened: MethodDialogData[];

  const create = async (digest: PermissionDigest, served: FeatureFlag[] = flags): Promise<void> => {
    transport = scriptedTransport((request) => {
      const url = new URL(request.url, 'http://flags.test');
      switch (url.pathname) {
        case '/api/permission-digest':
          return { status: 200, body: digest };
        case '/api/feature-flags':
          return { status: 200, body: served, headers: { 'total-count': String(served.length) } };
        default:
          return { status: 404, body: { message: `unscripted ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: METHOD_META, useValue: (): MethodMeta => flipMeta },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: featureFlagsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', featureFlagsConfig.parentConfig);
    // The method dialog has its own spec; here it records what it is opened with.
    opened = [];
    fixture.componentInstance.dialog.open = ((_: unknown, config: { data: MethodDialogData }) => {
      opened.push(config.data);
      return { afterClosed: () => of(undefined) } as unknown as MatDialogRef<unknown>;
    }) as never;
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const headers = (): string[] =>
    Array.from(element().querySelectorAll('th .col-header')).map((header) => header.textContent?.trim() ?? '');
  const cells = (): unknown[][] =>
    (fixture.componentInstance.processedRowData() as Record<string, unknown>[]).map((row) => [
      row['name'],
      row['description'],
      row['enabled'],
      row['updatedAt'],
      row['updatedBy'],
    ]);
  const actionsOf = (index: number): RowAction[] =>
    ((fixture.componentInstance.processedRowData()[index] as Record<string, unknown>)[rowActionsKey] as
      RowAction[] | undefined) ?? [];
  const historyButtons = (): HTMLButtonElement[] =>
    Array.from(element().querySelectorAll<HTMLButtonElement>('tr.impulse-row impulse-table-button button'));

  it('lists each flag in words with its description, whether it is on, and who last flipped it and when', async () => {
    await create(listsFlags);

    expect(headers()).toEqual(['Flag', 'Description', 'On', 'Last changed', 'Changed by']);
    expect(cells()).toEqual([
      [
        'Cargo manifest',
        "Cargo manifest shows a ship's cargo bays.",
        true,
        format(new Date('2026-11-17T15:42:10Z'), dayAndMinuteFormat),
        'morgan',
      ],
      // A flag nobody has flipped has no last change, and the cell of who changed it is
      // left empty.
      ['Persona entry', 'Persona entry opens the persona sign-in.', false, 'Never', undefined],
    ]);
    // The page draws no search box and no count, and the flags' names as stored appear nowhere.
    expect(element().querySelector('impulse-search-box')).toBeNull();
    expect(element().textContent).not.toContain('cargo_manifest');
    // The last change and who made it are worked out of a flag's history, which the server
    // orders by neither: their headers are words, and the other three offer a sort.
    const pressed = Array.from(element().querySelectorAll('th button.header-label .col-header'));
    expect(pressed.map((header) => header.textContent?.trim())).toEqual(['Flag', 'Description', 'On']);
    const read = Array.from(element().querySelectorAll('th span.header-words .col-header'));
    expect(read.map((header) => header.textContent?.trim())).toEqual(['Last changed', 'Changed by']);
  });

  it('offers a person who may flip the one flip a row can take, and asks first', async () => {
    await create(flipsToo);

    expect(actionsOf(0).map((action) => [action.label, action.icon])).toEqual([['Turn off', 'toggle_off']]);
    expect(actionsOf(1).map((action) => [action.label, action.icon])).toEqual([['Turn on', 'toggle_on']]);

    actionsOf(0)[0].run();
    actionsOf(1)[0].run();

    // Each is the generated flip method, filled from the row by the flag's name as stored,
    // and nothing is left to ask but the question.
    expect(opened).toEqual([
      {
        method: setFeatureMethod,
        label: 'Turn off',
        preset: { name: 'cargo_manifest', enabled: false },
        confirm: 'Turn Cargo manifest off for everyone in this environment?',
      },
      {
        method: setFeatureMethod,
        label: 'Turn on',
        preset: { name: 'persona_entry', enabled: true },
        confirm: 'Turn Persona entry on for everyone in this environment?',
      },
    ]);
    // Both questions were declined (the dialog closed with no answer): the page stays as it
    // was, and the flags are not read again. The flip itself is the method dialog's to
    // send, and its own spec holds that a declined question sends none.
    expect(transport.requests.filter((request) => request.url.startsWith('/api/feature-flags'))).toHaveLength(1);
  });

  it('says when a flag was last changed to a person whose role does not show who changed it', async () => {
    // The role lists the flags without who last flipped them: the server serves no such
    // field, and when a flag was last changed is still the server's answer, not a guess
    // from who changed it.
    const withoutWho: PermissionDigest = { ...listsFlags };
    delete (withoutWho as Record<string, unknown>)[`${featureFlagsResource}.updatedBy`];
    const served = flags.map((flag) => ({ ...flag, updatedBy: undefined }) as unknown as FeatureFlag);
    await create(withoutWho, served);

    expect(headers()).toEqual(['Flag', 'Description', 'On', 'Last changed']);
    expect(cells().map((row) => [row[0], row[3]])).toEqual([
      ['Cargo manifest', format(new Date('2026-11-17T15:42:10Z'), dayAndMinuteFormat)],
      ['Persona entry', 'Never'],
    ]);
  });

  it('shows a person who may list the flags and not flip them the list and the history, and no way to flip', async () => {
    await create(listsFlags);

    expect(cells()).toHaveLength(2);
    expect(actionsOf(0)).toEqual([]);
    expect(actionsOf(1)).toEqual([]);
    expect(fixture.componentInstance.columns().map((col) => col.id)).not.toContain(rowActionsKey);
    expect(element().querySelectorAll('button.row-actions')).toHaveLength(0);
    expect(historyButtons().map((button) => button.getAttribute('aria-label'))).toEqual(['History', 'History']);
  });

  it('carries a History button on every row for a person who may flip, beside the menu', async () => {
    await create(flipsToo);

    expect(historyButtons().map((button) => button.getAttribute('aria-label'))).toEqual(['History', 'History']);
    expect(element().querySelectorAll('button.row-actions')).toHaveLength(2);
  });

  it('says an application that declares no flag has none', async () => {
    await create(flipsToo, []);

    expect(cells()).toEqual([]);
    expect(element().querySelector('tr.no-records-row')?.textContent?.trim()).toBe(noFeatureFlags);
    expect(noFeatureFlags).toBe('This application declares no feature flags.');
  });
});

describe('the Feature flags page in the menus and the routes', () => {
  it('is one page: Feature flags, with a flag, outside every tenant, for whoever may list the flags', () => {
    const page = featureFlagsPage(resourceMeta);

    expect(page).toEqual({
      label: 'Feature flags',
      icon: 'flag',
      route: [featureFlagsPath],
      permission: { resource: featureFlagsResource, permission: ListPermission },
      feature: undefined,
    });
    expect(featureFlagsPath).toBe('feature-flags');
    // The menu file's page is the page on the route, so the two cannot drift apart.
    expect(featureFlagsPage(resourceMeta)).toBe(page);
    const route = featureFlagsRoute(resourceMeta);
    expect(route.path).toBe(featureFlagsPath);
    expect((route.data as Record<string, unknown>)[pageDataKey]).toBe(page);
  });

  it('has no row page: a flag opens nowhere, and its history is a pop-up', () => {
    const route = featureFlagsRoute(resourceMeta);
    const paths = (route.children ?? []).map((child) => child.path);

    expect(paths).not.toContain(':uuid');
    expect((featureFlagsConfig.parentConfig as ListViewConfig).showViewButton).toBe(false);
  });
});

describe('the words of the Feature flags page', () => {
  const cases: { name: string; want: string }[] = [
    { name: 'cargo_manifest', want: 'Cargo manifest' },
    { name: 'persona_entry', want: 'Persona entry' },
    { name: 'debriefs', want: 'Debriefs' },
  ];
  for (const tt of cases) {
    it(`${tt.name} is listed as "${tt.want}"`, () => {
      expect(flagInWords({ name: tt.name })).toBe(tt.want);
    });
  }

  it('asks before a flip in the words of the flag, naming everyone in the environment', () => {
    expect(flipQuestion({ name: 'cargo_manifest' }, true)).toBe(
      'Turn Cargo manifest on for everyone in this environment?',
    );
    expect(flipQuestion({ name: 'cargo_manifest' }, false)).toBe(
      'Turn Cargo manifest off for everyone in this environment?',
    );
  });
});

describe("a row's History button", () => {
  it("opens the flag's history in a pop-up, named as the server names the flag and titled in its words", async () => {
    const opened: { data: unknown }[] = [];
    const dialog = {
      open: (_: unknown, config: { data: unknown }) => {
        opened.push({ data: config.data });
        return { close: () => undefined } as unknown as MatDialogRef<unknown>;
      },
    } as unknown as MatDialog;
    const injector = Injector.create({ providers: [{ provide: MatDialog, useValue: dialog }] });
    const button = (featureFlagsConfig.parentConfig as ListViewConfig).rowButtons?.[0];

    expect([button?.label, button?.icon]).toEqual(['History', 'history']);
    button?.run({ name: 'cargo_manifest', enabled: true }, injector);
    // The pop-up's package is loaded when a person first asks for a history.
    for (let i = 0; i < 50 && opened.length === 0; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }

    expect(opened).toEqual([{ data: { name: 'cargo_manifest', words: 'Cargo manifest' } }]);
  });
});
