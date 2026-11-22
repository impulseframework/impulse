import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { ActivatedRoute } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  Domain,
  FieldMeta,
  IdentityDescriptor,
  Method,
  MethodMeta,
  PermissionDigest,
  Resource,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import {
  CompoundResourceComponent,
  MethodDialogData,
  MethodDialogResult,
  ResourceListComponent,
} from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ListViewConfig,
  METHOD_META,
  RESOURCE_DOMAIN,
  RESOURCE_META,
  RootConfig,
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';
import { identityPages } from './identity-pages';

// The page of the invitations that make a tenant, as the library's list draws it. Staff
// bring an organization in by inviting one person, and that person's first sign-in makes
// the tenant: nobody creates a tenant directly. The page is built from the API's
// description (identityPages) and is an ordinary list: the server lists the invitations
// that still stand, each with the tenant record's fields as staff entered them, the
// method that sends one stands above the list, its form drawn by the method dialog from
// the method's own fields, and a row offers Withdraw. Each action is offered only to a
// caller whose roles grant its method.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const users = 'CrewSessionUsers' as Resource;
const invited = 'CrewInvitedSectors' as Resource;
const sectors = 'Sectors' as Resource;

const metas: Record<string, ResourceMeta> = {
  [users]: {
    route: 'crew-session-users',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('username')],
  } as ResourceMeta,
  // The generated list: the invitation's own fields, and the tenant record's under its name.
  [invited]: {
    route: 'crew-invited-sectors',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      field('email'),
      field('name'),
      field('sectorName'),
      field('sectorRegion'),
      field('sectorEstablished', { displayType: 'civildate' }),
      field('sectorDirectoryOnly', { displayType: 'boolean' }),
      field('defaultRole'),
      field('invitedByUserId', { displayType: 'uuid' }),
      field('createdAt', { displayType: 'date' }),
      field('expiresAt', { displayType: 'date' }),
    ],
  } as ResourceMeta,
  // The tenant record says which of its fields a sector is called by.
  [sectors]: {
    route: 'sectors',
    nameField: 'name',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name'), field('region')],
  } as ResourceMeta,
};
const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];

const invite = 'InviteCrewSector';
const withdraw = 'WithdrawCrewInvitedSector';
const method = (name: string): ApiDescriptor['methods'][string] => ({
  method: name as Method,
  property: name.charAt(0).toLowerCase() + name.slice(1),
  route: name.toLowerCase(),
  scope: 'global',
});

/** The crew's management with the invitations that make a sector, and the same without them. */
const crewOf = (lists: Record<string, string>, methods: Record<string, string>): IdentityDescriptor => ({
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Sector',
  tenantResource: sectors,
  sides: [{ scope: 'global', lists: { users, ...lists }, methods }],
});
const descriptorOf = (identity: IdentityDescriptor): ApiDescriptor => ({
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    [users]: {
      resource: users,
      property: 'crewSessionUsers',
      route: 'crew-session-users',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
    },
    [invited]: {
      resource: invited,
      property: 'crewInvitedSectors',
      route: 'crew-invited-sectors',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list'],
    },
    [sectors]: {
      resource: sectors,
      property: 'sectors',
      route: 'sectors',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
    },
  },
  methods: { [invite]: method(invite), [withdraw]: method(withdraw) },
  identity: [identity],
});
const descriptor = descriptorOf(
  crewOf({ invitedTenants: invited }, { inviteTenant: invite, withdrawInvitedTenant: withdraw }),
);

// Neither method names a target row: the one that sends an invitation takes the invited
// address and the tenant record's fields, and the one that withdraws takes the
// invitation's id, which the page fills from the row.
const methodMetas: Record<string, MethodMeta> = {
  [invite]: {
    route: 'invite-crew-sector',
    answers: true,
    fields: [
      { fieldName: 'email', displayType: 'string', required: true },
      { fieldName: 'name', displayType: 'string', optional: true },
      { fieldName: 'sectorName', displayType: 'string', required: true },
      { fieldName: 'sectorRegion', displayType: 'string', required: true },
      { fieldName: 'sectorEstablished', displayType: 'civildate', required: true },
      { fieldName: 'sectorDirectoryOnly', displayType: 'boolean' },
      { fieldName: 'defaultRole', displayType: 'string', optional: true, attribute: 'role' },
    ],
  } as MethodMeta,
  [withdraw]: {
    route: 'withdraw-crew-invited-sector',
    statuses: [204],
    fields: [{ fieldName: 'invitationId', displayType: 'uuid' }],
  } as MethodMeta,
};

const dawn = {
  id: 'a1111111-1111-4111-8111-111111111111',
  email: 'founder@dawn.example',
  name: 'Dawn Founder',
  sectorName: 'Dawn',
  sectorRegion: 'Outer frontier',
  sectorEstablished: '2226-10-02',
  sectorDirectoryOnly: false,
  defaultRole: 'SectorCrew',
  createdAt: '2026-11-17T10:00:00Z',
  expiresAt: '2026-11-24T10:00:00Z',
};
/** A row as a caller who may not read the record's fields gets it: the server leaves them out. */
const masked = {
  id: 'b2222222-2222-4222-8222-222222222222',
  email: 'founder@ember.example',
  createdAt: '2026-11-17T11:00:00Z',
  expiresAt: '2026-11-24T11:00:00Z',
};

const list = (config: RootConfig): ListViewConfig => config.parentConfig as ListViewConfig;

describe('identityPages, the invitations that make a tenant', () => {
  const pages = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' });
  const page = pages.find((config) => list(config).primaryResource === invited);

  it("builds the page among the whole application's, named for what the application calls its tenants, at a route named for the auth", () => {
    expect(
      pages.map((config) => [
        config.routeData.route,
        config.page?.label,
        config.page?.icon,
        config.routeData.hasViewRoute,
      ]),
    ).toEqual([
      ['crew-accounts', 'Accounts', 'manage_accounts', true],
      ['crew-invited-sectors', 'Invited sectors', 'domain_add', false],
    ]);
    expect(list(page as RootConfig).title).toBe('Invited sectors');
  });

  it('is no page where the description names no such list: an auth that makes no tenant, or an application without tenants', () => {
    const without = descriptorOf(crewOf({}, { inviteTenant: invite, withdrawInvitedTenant: withdraw }));
    expect(
      identityPages(without, resourceMeta, { auth: 'Crew', scope: 'global' }).map((config) => config.routeData.route),
    ).toEqual(['crew-accounts']);
  });

  it("lists each invitation by the tenant record's own fields, each under the record's name, then who was invited and when", () => {
    const columns = list(page as RootConfig).listColumns;
    expect(columns.filter((column) => !column.hidden).map((column) => [column.id, column.header])).toEqual([
      ['sectorName', 'Sector name'],
      ['sectorRegion', 'Sector region'],
      ['sectorEstablished', 'Sector established'],
      ['sectorDirectoryOnly', 'Directory only'],
      ['defaultRole', 'Starting role'],
      ['email', 'Invited'],
      ['name', 'Name'],
      ['createdAt', 'Sent'],
      ['expiresAt', 'Expires'],
    ]);
    // Read with each row and not drawn: what Withdraw is filled from. Who sent it is an id nobody reads.
    expect(columns.filter((column) => column.hidden).map((column) => column.id)).toEqual(['id']);
  });

  it('follows the record: a field the application adds is a column with no change here, and a record that keeps no switch or no starting role has neither column', () => {
    const wider: Record<string, ResourceMeta> = {
      ...metas,
      [invited]: {
        route: 'crew-invited-sectors',
        fields: [
          field('id', { primaryKey: { ordinalPosition: 0 } }),
          field('email'),
          field('sectorName'),
          field('sectorCallSign'),
          field('createdAt'),
        ],
      } as ResourceMeta,
    };
    const built = identityPages(descriptorOf(crewOf({ invitedTenants: invited }, {})), (resource) => wider[resource], {
      auth: 'Crew',
      scope: 'global',
    });
    const config = list(built.find((candidate) => list(candidate).primaryResource === invited) as RootConfig);
    expect(config.listColumns.filter((column) => !column.hidden).map((column) => [column.id, column.header])).toEqual([
      ['sectorName', 'Sector name'],
      ['sectorCallSign', 'Sector call sign'],
      ['email', 'Invited'],
      ['createdAt', 'Sent'],
    ]);
    // Neither method is named: the page lists and offers nothing.
    expect(config.rowMethods).toEqual([]);
    expect(config.listMethods).toEqual([]);
  });

  it('names the tenants by the list itself and the one invited by the record, whatever the application calls them', () => {
    const offices: IdentityDescriptor = {
      auth: 'Staff',
      label: 'Staff',
      tenant: 'Office',
      sides: [
        {
          scope: 'global',
          lists: { users, invitedTenants: 'StaffInvitedOffices' },
          methods: { inviteTenant: 'InviteStaffOffice' },
        },
      ],
    };
    const officeMetas: Record<string, ResourceMeta> = {
      ...metas,
      StaffInvitedOffices: {
        route: 'staff-invited-offices',
        fields: [field('id'), field('email'), field('officeName')],
      } as ResourceMeta,
    };
    const built = identityPages(descriptorOf(offices), (resource) => officeMetas[resource], {
      auth: 'Staff',
      scope: 'global',
    });
    const office = built.find((candidate) => list(candidate).primaryResource === 'StaffInvitedOffices') as RootConfig;
    expect([office.routeData.route, office.page?.label, list(office).title]).toEqual([
      'staff-invited-offices',
      'Invited offices',
      'Invited offices',
    ]);
    expect((list(office).listMethods || []).map((presentation) => presentation.label)).toEqual(['Invite an office']);
  });

  it('keeps a plural acronym whole where the list names the tenants: the invitations that make a partner API', () => {
    const partners: IdentityDescriptor = {
      auth: 'Staff',
      label: 'Staff',
      tenant: 'PartnerAPI',
      sides: [
        {
          scope: 'global',
          lists: { users, invitedTenants: 'StaffInvitedPartnerAPIs' },
          methods: { inviteTenant: 'InviteStaffPartnerAPI' },
        },
      ],
    };
    const partnerMetas: Record<string, ResourceMeta> = {
      ...metas,
      StaffInvitedPartnerAPIs: {
        route: 'staff-invited-partner-apis',
        fields: [field('id'), field('email'), field('partnerAPIName')],
      } as ResourceMeta,
    };
    const built = identityPages(descriptorOf(partners), (resource) => partnerMetas[resource], {
      auth: 'Staff',
      scope: 'global',
    });
    const partner = built.find(
      (candidate) => list(candidate).primaryResource === 'StaffInvitedPartnerAPIs',
    ) as RootConfig;
    expect([partner.page?.label, list(partner).title]).toEqual(['Invited partner APIs', 'Invited partner APIs']);
  });

  it('takes the route the application gives', () => {
    const built = identityPages(descriptor, resourceMeta, {
      auth: 'Crew',
      scope: 'global',
      routes: { invitedTenants: 'new-sectors' },
    });
    expect(built.map((config) => config.routeData.route)).toEqual(['crew-accounts', 'new-sectors']);
  });

  it("words the method that sends one and its form: the person's address and name, the record's fields by their names, the switch and the starting role", () => {
    expect(list(page as RootConfig).listMethods).toEqual([
      {
        method: invite,
        label: 'Invite a sector',
        icon: 'send',
        labels: {
          email: "Person's email",
          name: "Person's name",
          sectorDirectoryOnly: 'Entered through its directory only',
          defaultRole: "Role its directory's people start with",
        },
        answered: expect.any(Function),
      },
    ]);
  });

  it('says once the invitation is sent whether its message went, and what to do where it did not', () => {
    // The server ends an invitation whose message could not be sent, and answers so.
    const answered = (list(page as RootConfig).listMethods ?? [])[0]?.answered;
    expect(answered?.({ id: 'i-1', sent: true })).toEqual({ message: 'The invitation is on its way.' });
    expect(answered?.({ id: 'i-1', sent: false })).toEqual({
      message: 'The invitation was not mailed, so it is ended. Invite the person again.',
      failed: true,
    });
  });

  it('asks before a withdrawal, naming the address and the tenant that will not be set up, and says how details are changed', () => {
    const presentation = (list(page as RootConfig).rowMethods || [])[0];
    expect([presentation.method, presentation.label, presentation.successMessage]).toEqual([
      withdraw,
      'Withdraw',
      'The invitation is withdrawn.',
    ]);
    expect(presentation.fill?.(dawn)).toEqual({ invitationId: dawn.id });
    const ask = presentation.confirm as (row: Record<string, unknown>) => string;
    expect(ask(dawn)).toBe(
      "Withdraw the invitation sent to founder@dawn.example? Its link stops working, and Dawn is not set up. To change an invitation's details, withdraw it and send another.",
    );
    // A caller who may not read the record's name is told no name.
    expect(ask(masked)).toBe(
      "Withdraw the invitation sent to founder@ember.example? Its link stops working, and the sector is not set up. To change an invitation's details, withdraw it and send another.",
    );
  });
});

describe('the page of the invitations that make a tenant, drawn by the list', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  let opened: MethodDialogData[];

  /** The caller lists the invitations; a test adds what else they hold. */
  const lists: PermissionDigest = Object.fromEntries([
    [invited, { List: 'granted' }],
    ...[
      'email',
      'name',
      'sectorName',
      'sectorRegion',
      'sectorEstablished',
      'sectorDirectoryOnly',
      'defaultRole',
      'createdAt',
      'expiresAt',
    ].map((name) => [`${invited}.${name}`, { List: 'granted' }]),
  ]) as PermissionDigest;
  const maySend: PermissionDigest = { [invite]: { Execute: 'granted' } };
  const mayWithdraw: PermissionDigest = { [withdraw]: { Execute: 'granted' } };

  const create = async (rows: Record<string, unknown>[], granted: PermissionDigest = {}): Promise<void> => {
    const digest: PermissionDigest = { ...lists, ...granted };
    const page = identityPages(descriptor, resourceMeta, { auth: 'Crew', scope: 'global' }).find(
      (config) => config.page?.label === 'Invited sectors',
    );
    if (!page) {
      throw new Error('identityPages built no page of the invited sectors');
    }
    const config = page.parentConfig as ListViewConfig;
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/crew-invited-sectors') {
        return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
      }
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: METHOD_META, useValue: (name: string): MethodMeta => methodMetas[name] },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(undefined) },
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', config);
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

  const actionsOf = (index: number): RowAction[] =>
    ((fixture.componentInstance.processedRowData()[index] as Record<string, unknown>)[rowActionsKey] as
      RowAction[] | undefined) ?? [];
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const above = (): string[] =>
    Array.from(element().querySelectorAll('button.impulse-resource-list-method')).map((button) =>
      (button.textContent ?? '').replace(/\s+/g, ' ').trim(),
    );
  const cells = (): string[][] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td'))
        .map((cell) => (cell.textContent ?? '').trim())
        .filter((text) => text !== ''),
    );

  it('lists one row for each invitation that stands, with the details staff entered, and leaves empty what the server left out', async () => {
    await create([dawn, masked]);

    expect(cells()[0].slice(0, 2)).toEqual(['Dawn', 'Outer frontier']);
    expect(cells()[0]).toContain('founder@dawn.example');
    expect(cells()[0]).toContain('SectorCrew');
    // The caller may not read the record's fields of the second: its row shows who was invited and nothing of the sector.
    expect(cells()[1][0]).toBe('founder@ember.example');
    expect(cells()[1].join(' ')).not.toContain('Dawn');
  });

  it('offers a caller who holds the grants the method that sends one above the list and Withdraw on each row', async () => {
    await create([dawn, masked], { ...maySend, ...mayWithdraw });

    expect(above()).toEqual(['send Invite a sector']);
    expect(actionsOf(0).map((action) => [action.id, action.label])).toEqual([[withdraw, 'Withdraw']]);
    expect(actionsOf(1).map((action) => [action.id, action.label])).toEqual([[withdraw, 'Withdraw']]);
  });

  it('offers a caller without the grants neither: the list alone', async () => {
    await create([dawn]);

    expect(above()).toEqual([]);
    expect(actionsOf(0)).toEqual([]);
    expect(element().querySelectorAll('button.row-actions').length).toBe(0);
  });

  it('offers a caller who holds one of the two grants that action alone', async () => {
    await create([dawn], maySend);
    expect(above()).toEqual(['send Invite a sector']);
    expect(actionsOf(0)).toEqual([]);
    TestBed.resetTestingModule();

    await create([dawn], mayWithdraw);
    expect(above()).toEqual([]);
    expect(actionsOf(0).map((action) => action.label)).toEqual(['Withdraw']);
  });

  it("opens the method that sends one in the method dialog, which draws the form from the method's fields under the page's words, nothing filled", async () => {
    await create([dawn], { ...maySend, ...mayWithdraw });

    fixture.componentInstance.runMethod(fixture.componentInstance.listMethods()[0]);

    expect(opened).toEqual([
      {
        method: invite,
        label: 'Invite a sector',
        preset: {},
        confirm: undefined,
        labels: {
          email: "Person's email",
          name: "Person's name",
          sectorDirectoryOnly: 'Entered through its directory only',
          defaultRole: "Role its directory's people start with",
        },
      },
    ]);
  });

  it("opens Withdraw with the invitation's id filled from the row and the question that names it", async () => {
    await create([dawn, masked], mayWithdraw);

    actionsOf(1)[0].run();
    actionsOf(0)[0].run();

    expect(opened).toEqual([
      {
        method: withdraw,
        label: 'Withdraw',
        preset: { invitationId: masked.id },
        confirm:
          "Withdraw the invitation sent to founder@ember.example? Its link stops working, and the sector is not set up. To change an invitation's details, withdraw it and send another.",
      },
      {
        method: withdraw,
        label: 'Withdraw',
        preset: { invitationId: dawn.id },
        confirm:
          "Withdraw the invitation sent to founder@dawn.example? Its link stops working, and Dawn is not set up. To change an invitation's details, withdraw it and send another.",
      },
    ]);
  });
});
