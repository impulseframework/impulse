import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  Domain,
  IdentityDescriptor,
  IdentityRecordSharing,
  Method,
  MethodMeta,
  PermissionDigest,
  Resource,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import {
  CompoundResourceComponent,
  MethodDialogData,
  MethodDialogResult,
  ResourceListComponent,
} from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ChildResourceConfig,
  FieldMeta,
  ListViewConfig,
  METHOD_META,
  MethodPresentation,
  RecordData,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  ResourceMeta,
  rootConfig,
} from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';
import { recordsOnly, sharingConfig } from './sharing';

// A record's Sharing, as the library's list draws it on the record's page: who holds a
// role on the record, a person or a group, with the buttons that share it and Remove on
// each row, all from the tenant's side of the Auth in the API's description.

/** A resource's metadata with the named fields; `id` is its key where it has one. */
const fields = (...names: string[]): ResourceMeta =>
  ({
    route: 'r',
    fields: names.map(
      (fieldName) =>
        ({
          fieldName,
          displayType: 'string',
          required: false,
          isIndex: false,
          ...(fieldName === 'id' ? { primaryKey: { ordinalPosition: 0 } } : {}),
        }) as FieldMeta,
    ),
  }) as ResourceMeta;

const metas: Record<string, ResourceMeta> = {
  Voyages: fields('id', 'shipId', 'name'),
  CrewVoyageHolders: fields('recordId', 'memberKind', 'memberId', 'role', 'domainId', 'name', 'startsAt', 'endsAt'),
  CrewVoyageRoles: fields('name', 'method'),
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const voyages: IdentityRecordSharing = {
  record: 'Voyages',
  one: 'voyage',
  many: 'voyages',
  holders: 'CrewVoyageHolders',
  roles: 'CrewVoyageRoles',
  give: 'GiveCrewRoleOnVoyage',
  take: 'TakeCrewRoleOnVoyage',
  giveGroup: 'GiveCrewGroupRoleOnVoyage',
  takeGroup: 'TakeCrewGroupRoleOnVoyage',
};
const crewSharing = (shared: IdentityRecordSharing[]): IdentityDescriptor => ({
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Ship',
  sides: [
    { scope: 'global', lists: { users: 'CrewSessionUsers' }, methods: {} },
    { scope: 'domain', lists: { users: 'CrewShipUsers' }, methods: {}, sharing: shared },
  ],
});
const descriptorOf = (...identity: IdentityDescriptor[]): ApiDescriptor =>
  ({
    resources: {},
    methods: {},
    permissionDigestRoute: 'permission-digest',
    userDomainsRoute: 'user-domains',
    domainRoute: { segment: 'ships', param: 'shipID' },
    identity,
  }) as ApiDescriptor;

const shared = (config: ChildResourceConfig | undefined): ListViewConfig => {
  if (!config) {
    throw new Error('no Sharing list');
  }
  return config as ListViewConfig;
};
const presentation = (presentations: false | MethodPresentation[] | undefined, method: string): MethodPresentation => {
  const found = (presentations || []).find((p) => p.method === method);
  if (!found) {
    throw new Error(`no presentation of ${method}`);
  }
  return found;
};

describe("sharingConfig, a record's Sharing", () => {
  const descriptor = descriptorOf(crewSharing([voyages]));
  const config = shared(sharingConfig(descriptor, resourceMeta, { auth: 'Crew', record: 'Voyages' }));
  const person: RecordData = { recordId: 'v1', memberKind: 'person', memberId: 'u1', role: 'Navigator', name: 'Dana' };
  const group: RecordData = {
    recordId: 'v1',
    memberKind: 'group',
    memberId: 'g1',
    role: 'Lookout',
    name: 'Night shift',
  };

  it("lists the roles held on the page's record, titled Sharing, under the record by its key", () => {
    expect(config.title).toBe('Sharing');
    expect(config.primaryResource).toBe('CrewVoyageHolders');
    expect(config.parentRelation).toEqual({ parentKey: 'id', childKey: 'recordId' });
    expect(config.emptyMessage).toBe('Nobody holds a role on this voyage alone.');
  });

  it('draws Name, Kind, Role and Ends, and reads the record and the holder without drawing them', () => {
    expect(config.listColumns.filter((column) => !column.hidden).map((column) => [column.id, column.header])).toEqual([
      ['name', 'Name'],
      ['memberKind', 'Kind'],
      ['role', 'Role'],
      ['endsAt', 'Ends'],
    ]);
    expect(config.listColumns.filter((column) => column.hidden).map((column) => column.id)).toEqual([
      'recordId',
      'memberId',
    ]);
    const kind = config.listColumns.find((column) => column.id === 'memberKind');
    expect([kind?.valueGetter?.(person, {} as never), kind?.valueGetter?.(group, {} as never)]).toEqual([
      'Person',
      'Group',
    ]);
    const ends = config.listColumns.find((column) => column.id === 'endsAt');
    expect(ends?.valueGetter?.({ ...person, endsAt: null }, {} as never)).toBe('');
    expect(ends?.valueGetter?.({ ...person, endsAt: '2026-12-02T00:00:00Z' }, {} as never)).toBe(
      '2026-12-01T23:59:59.999Z',
    );
  });

  it("offers Share with a person and Share with a group above the list, each sending the page's record", () => {
    expect((config.listMethods || []).map((p) => [p.method, p.label])).toEqual([
      ['GiveCrewRoleOnVoyage', 'Share with a person'],
      ['GiveCrewGroupRoleOnVoyage', 'Share with a group'],
    ]);
    for (const method of ['GiveCrewRoleOnVoyage', 'GiveCrewGroupRoleOnVoyage']) {
      const give = presentation(config.listMethods, method);
      expect(give.fill?.({ id: 'v1', name: 'Apollo' })).toEqual({ recordId: 'v1' });
      expect(give.labels).toEqual({
        userId: 'Person',
        groupId: 'Group',
        role: 'Role',
        startsAt: 'From',
        endsAt: 'Until',
      });
      expect(give.throughDay).toEqual(['endsAt']);
      // The role picks from the roles the caller could give with this method on the
      // page's record: the list is under its record, so a role held on that record alone
      // lists them.
      expect(give.narrows?.['role']?.({ recordId: 'v1' })).toBe(`method:eq:${method},recordId:eq:v1`);
      // A key the filter's grammar would read as more than a value travels in quotes.
      expect(give.narrows?.['role']?.({ recordId: 'a,b' })).toBe(`method:eq:${method},recordId:eq:"a,b"`);
    }
  });

  it("offers Remove on each row by the row's kind, sending the holder and the role, and asks first", () => {
    const take = presentation(config.rowMethods, 'TakeCrewRoleOnVoyage');
    const takeGroup = presentation(config.rowMethods, 'TakeCrewGroupRoleOnVoyage');
    expect([take.label, takeGroup.label]).toEqual(['Remove', 'Remove']);
    expect([take.when?.(person), take.when?.(group), takeGroup.when?.(person), takeGroup.when?.(group)]).toEqual([
      true,
      false,
      false,
      true,
    ]);
    expect(take.fill?.(person)).toEqual({ recordId: 'v1', userId: 'u1', role: 'Navigator' });
    expect(takeGroup.fill?.(group)).toEqual({ recordId: 'v1', groupId: 'g1', role: 'Lookout' });
    const ask = take.confirm as (row: RecordData) => string;
    expect(ask(person)).toBe('Remove the role Navigator on this voyage from Dana?');
    expect(ask({ ...person, name: 'Jess' })).toBe('Remove the role Navigator on this voyage from Jess?');
    expect((takeGroup.confirm as (row: RecordData) => string)({ ...group, role: 'NightLookout' })).toBe(
      'Remove the role Night lookout on this voyage from Night shift?',
    );
  });

  it('offers no group button and no group Remove where the Auth keeps no groups', () => {
    const people = { ...voyages, giveGroup: undefined, takeGroup: undefined };
    const alone = shared(
      sharingConfig(descriptorOf(crewSharing([people])), resourceMeta, { auth: 'Crew', record: 'Voyages' }),
    );
    expect((alone.listMethods || []).map((p) => p.label)).toEqual(['Share with a person']);
    expect((alone.rowMethods || []).map((p) => p.method)).toEqual(['TakeCrewRoleOnVoyage']);
  });

  it('answers nothing for a record the side does not share, and for a description that shares none', () => {
    expect(sharingConfig(descriptor, resourceMeta, { auth: 'Crew', record: 'Ships' })).toBeUndefined();
    expect(
      sharingConfig(descriptorOf(crewSharing([])), resourceMeta, { auth: 'Crew', record: 'Voyages' }),
    ).toBeUndefined();
  });

  it('refuses an Auth the description does not carry', () => {
    expect(() => sharingConfig(descriptor, resourceMeta, { auth: 'Guests', record: 'Voyages' })).toThrowError(
      /carries no generated management of the auth Guests \(it carries: Crew\)/,
    );
  });

  it('answers the same configuration for the same description, Auth and record', () => {
    expect(sharingConfig(descriptor, resourceMeta, { auth: 'Crew', record: 'Voyages' })).toBe(config);
  });
});

// The Sharing list as the library's list draws it on a voyage's page: Remove in each row's
// menu, where the voyage's own answer lists the take method for the row's kind, and run
// against the voyage, the holder and the role filled from the row.
describe("the Sharing list drawn on a record's page", () => {
  const holders = 'CrewVoyageHolders' as Resource;
  // The voyages' four methods; the Auth keeps groups, so the description names the group pair.
  const { give, take } = voyages;
  const giveGroup = voyages.giveGroup as string;
  const takeGroup = voyages.takeGroup as string;
  const drawnDescriptor: ApiDescriptor = {
    ...descriptorOf(crewSharing([voyages])),
    resources: {
      [holders]: {
        resource: holders,
        property: 'crewVoyageHolders',
        route: 'crew-voyage-holders',
        scope: 'domain',
        consolidated: false,
        keys: ['recordId', 'memberKind', 'memberId', 'role'],
        operations: ['list'],
        page: { default: 25, max: 200 },
      },
    },
    methods: Object.fromEntries(
      [give, take, giveGroup, takeGroup].map((name) => [
        name,
        {
          method: name as Method,
          property: name.charAt(0).toLowerCase() + name.slice(1),
          route: name.toLowerCase(),
          scope: 'domain',
        },
      ]),
    ),
  } as ApiDescriptor;
  // Each method names the voyage (its target row) by the record's key, and takes the holder and the role.
  const onVoyage = (holder: string): MethodMeta => ({
    route: 'r',
    target: { resource: 'Voyages' as Resource, key: { recordId: 'id' } },
    fields: [
      { fieldName: 'recordId', displayType: 'uuid' },
      { fieldName: holder, displayType: 'uuid' },
      { fieldName: 'role', displayType: 'string', required: true },
    ],
  });
  const methodMetas: Record<string, MethodMeta> = {
    [give]: onVoyage('userId'),
    [take]: onVoyage('userId'),
    [giveGroup]: onVoyage('groupId'),
    [takeGroup]: onVoyage('groupId'),
  };
  // The list reads the holders at the route their metadata names.
  const drawnMeta = (resource: Resource): ResourceMeta =>
    resource === holders ? { ...metas[resource], route: 'crew-voyage-holders' } : metas[resource];
  const held: RecordData[] = [
    { recordId: 'v1', memberKind: 'person', memberId: 'u1', role: 'Navigator', name: 'Dana', domainId: 'anvil' },
    { recordId: 'v1', memberKind: 'group', memberId: 'g1', role: 'Lookout', name: 'Night shift', domainId: 'anvil' },
  ];
  /** The caller lists the holders, every field of them. */
  const listsHolders = Object.fromEntries([
    [holders, { List: 'granted' }],
    ...['recordId', 'memberKind', 'memberId', 'role', 'domainId', 'name', 'startsAt', 'endsAt'].map((name) => [
      `${holders}.${name}`,
      { List: 'granted' },
    ]),
  ]) as PermissionDigest;
  /** The voyage the page is on, as its read answers: with the methods the caller may run on it, where it says. */
  const voyage = (execute?: string[]): RecordData =>
    ({
      id: 'v1',
      name: 'Apollo',
      ...(execute ? { zzCapabilities: { Execute: execute } } : {}),
    }) as unknown as RecordData;

  let fixture: ComponentFixture<ResourceListComponent>;
  let opened: MethodDialogData[];

  const draw = async (parent: RecordData): Promise<void> => {
    const config = shared(sharingConfig(drawnDescriptor, drawnMeta, { auth: 'Crew', record: 'Voyages' }));
    const transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: listsHolders };
      }
      if (url.pathname.endsWith('/crew-voyage-holders')) {
        return { status: 200, body: held, headers: { 'total-count': String(held.length) } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(drawnDescriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: drawnMeta },
        { provide: METHOD_META, useValue: (name: string): MethodMeta => methodMetas[name] },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>('anvil' as Domain) },
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { data: { config: rootConfig({ parentConfig: config }) }, params: {}, queryParams: {} },
          },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest('anvil' as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', config);
    fixture.componentRef.setInput('relatedData', parent);
    // The dialog is the method dialog's own spec; here it records what it is opened with.
    opened = [];
    const result: MethodDialogResult = { answer: undefined };
    fixture.componentInstance.dialog.open = ((_: unknown, dialogConfig: { data: MethodDialogData }) => {
      opened.push(dialogConfig.data);
      return { afterClosed: () => of(result) } as unknown as MatDialogRef<unknown>;
    }) as never;
    fixture.detectChanges();
    await settle();
  };

  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  /** How many menu buttons each row draws. */
  const menus = (): number[] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map(
      (row) => row.querySelectorAll('button.row-actions').length,
    );
  /** Opens the menu of the row at `index` and answers the entries it draws, by their words. */
  const openMenu = async (index: number): Promise<HTMLButtonElement[]> => {
    const row = element().querySelectorAll('tr.impulse-row')[index];
    row?.querySelector<HTMLButtonElement>('button.row-actions')?.click();
    fixture.detectChanges();
    await fixture.whenStable();
    const panels = document.querySelectorAll('.mat-mdc-menu-panel');
    return Array.from(panels[panels.length - 1]?.querySelectorAll<HTMLButtonElement>('button.row-action') ?? []);
  };
  const words = (entry: HTMLButtonElement): string =>
    entry.querySelector('.mat-mdc-menu-item-text')?.textContent?.trim() ?? '';

  it("draws Remove in each row's menu where the record's answer lists the take method of the row's kind, and removes the row's holder from the record", async () => {
    await draw(voyage([take, takeGroup]));

    expect(menus()).toEqual([1, 1]);
    const person = await openMenu(0);
    expect(person.map(words)).toEqual(['Remove']);
    person[0].click();
    await settle();
    const group = await openMenu(1);
    expect(group.map(words)).toEqual(['Remove']);
    group[0].click();
    await settle();

    // Each runs against the page's voyage, the holder and the role filled from its row, and asks first.
    expect(opened).toEqual([
      {
        method: take,
        label: 'Remove',
        preset: { recordId: 'v1', userId: 'u1', role: 'Navigator' },
        confirm: 'Remove the role Navigator on this voyage from Dana?',
      },
      {
        method: takeGroup,
        label: 'Remove',
        preset: { recordId: 'v1', groupId: 'g1', role: 'Lookout' },
        confirm: 'Remove the role Lookout on this voyage from Night shift?',
      },
    ]);
  });

  for (const tt of [
    { name: "the record's answer lists the person's take method alone", parent: voyage([give, take]), drawn: [1, 0] },
    { name: "the record's answer lists no take method", parent: voyage([give, giveGroup]), drawn: [0, 0] },
    { name: 'the record was read without its answer', parent: voyage(), drawn: [0, 0] },
  ]) {
    it(`draws Remove only on the rows whose take method the record's answer lists: ${tt.name}`, async () => {
      await draw(tt.parent);

      expect(menus()).toEqual(tt.drawn);
    });
  }
});

describe("recordsOnly, the people list's words for a person who holds only records", () => {
  const ports: IdentityRecordSharing = { ...voyages, record: 'Ports', one: 'port', many: 'ports' };
  const say = (row: RecordData): string | undefined => recordsOnly(row, [voyages, ports]);

  it('names one record with a name by its word and name', () => {
    expect(say({ recordsHeld: 1, recordsHeldKind: 'Voyages', recordHeldName: 'Apollo' })).toBe('Voyage Apollo only');
  });

  it('counts one record without a name', () => {
    expect(say({ recordsHeld: 1, recordsHeldKind: 'Voyages', recordHeldName: '' })).toBe('1 voyage only');
  });

  it('counts several of one kind by the kind', () => {
    expect(say({ recordsHeld: 3, recordsHeldKind: 'Voyages', recordHeldName: '' })).toBe('3 voyages only');
  });

  it('counts several of more than one kind as records', () => {
    expect(say({ recordsHeld: 3, recordsHeldKind: '', recordHeldName: '' })).toBe('3 records only');
  });

  it('says nothing of a person who holds a role on no record', () => {
    expect(say({ recordsHeld: 0, recordsHeldKind: '', recordHeldName: '' })).toBeUndefined();
    expect(say({})).toBeUndefined();
  });
});
