import { Injector, runInInjectionContext } from '@angular/core';
import { Router } from '@angular/router';
import {
  ApiDescriptor,
  FieldMeta,
  IdentityDescriptor,
  Method,
  Resource,
  ResourceMeta,
} from '@impulseframework/resource';
import { ClosedPlace, pageDataKey, Place, PlaceService } from '@impulseframework/resource-angular/auth-frame';
import {
  AlertType,
  ColumnConfig,
  CreateNotificationMessage,
  FieldElement,
  ListViewConfig,
  RecordData,
  RootConfig,
  rpcConfig,
  SectionElement,
} from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { describe, expect, it, vi } from 'vitest';
import { identityPages } from './identity-pages';
import { inviteTenantAction, tenantsConfig, tenantsPage, tenantsRoute } from './tenant-pages';

// The tenants page is placed from the API's description alone: the whole application's
// side of the Auth whose people make the tenants names the server's list of tenants
// (`tenants`), the invitations still waiting (`invitedTenants`) and the method that sends
// one. The list shows each tenant with the record's own fields, its directory, its people
// and, where the record marks when a tenant was made (`tenantSinceField`), since when; a
// tenant's page shows the record's fields, and under them a summary that names its
// administrators. Where the description names no list of tenants the page lists the tenant
// record itself.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const users = 'CrewSessionUsers' as Resource;
const invited = 'CrewInvitedSectors' as Resource;
const listed = 'CrewListedSectors' as Resource;
const sectors = 'Sectors' as Resource;

const metas: Record<string, ResourceMeta> = {
  [users]: {
    route: 'crew-session-users',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('username')],
  } as ResourceMeta,
  [invited]: {
    route: 'crew-invited-sectors',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      field('email'),
      field('name'),
      field('sectorName'),
      field('sectorRegion'),
      field('sectorDirectoryOnly', { displayType: 'boolean' }),
      field('createdAt', { displayType: 'date' }),
      field('expiresAt', { displayType: 'date' }),
    ],
  } as ResourceMeta,
  // The server's list: the record's fields, then what the list says of each tenant.
  [listed]: {
    route: 'crew-listed-sectors',
    rowsOf: sectors,
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      field('name'),
      field('region'),
      field('established', { displayType: 'civildate' }),
      field('directoryOnly', { displayType: 'boolean' }),
      field('people', { displayType: 'number' }),
      field('since', { displayType: 'date' }),
      field('directory'),
      field('administrators', { displayType: 'string[]' }),
    ],
  } as ResourceMeta,
  // The tenant record says which of its fields a sector is called by, and keeps when each
  // was charted, which the database writes and the list carries as since.
  [sectors]: {
    route: 'sectors',
    nameField: 'name',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      field('name'),
      field('region'),
      field('established', { displayType: 'civildate' }),
      field('chartedAt', { displayType: 'date', readOnly: true }),
      field('directoryOnly', { displayType: 'boolean' }),
    ],
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

const crewOf = (lists: Record<string, string>, extra: Partial<IdentityDescriptor> = {}): IdentityDescriptor => ({
  auth: 'Crew',
  label: 'Crew',
  tenant: 'Sector',
  tenantResource: sectors,
  tenantDirectoryOnlyField: 'directoryOnly',
  tenantSinceField: 'chartedAt',
  sides: [
    {
      scope: 'global',
      lists: { users, ...lists },
      methods: { inviteTenant: invite, withdrawInvitedTenant: withdraw },
    },
  ],
  ...extra,
});
const descriptorOf = (...identity: IdentityDescriptor[]): ApiDescriptor => ({
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: { [invite]: method(invite), [withdraw]: method(withdraw) },
  identity,
});
/** A fresh description each time: the configuration is made once for each description. */
const fullDescriptor = (): ApiDescriptor => descriptorOf(crewOf({ invitedTenants: invited, tenants: listed }));

const list = (config: RootConfig): ListViewConfig => config.parentConfig as ListViewConfig;
const shown = (columns: ColumnConfig[]): [string, string | undefined][] =>
  columns.filter((column) => !column.hidden).map((column) => [column.id, column.header]);
const hiddenIds = (columns: ColumnConfig[]): string[] =>
  columns.filter((column) => column.hidden).map((column) => column.id);
const formOf = (config: RootConfig): SectionElement => list(config).elements[0] as SectionElement;

describe('tenantsPage and tenantsRoute', () => {
  const crew = fullDescriptor();
  const config = tenantsConfig(crew, resourceMeta, { auth: 'Crew' });

  it("label the page and title the list by what the application calls its tenants, at the record's own address", () => {
    const descriptor = fullDescriptor();
    expect(tenantsPage(descriptor, resourceMeta, { auth: 'Crew' })).toMatchObject({
      label: 'All sectors',
      icon: 'domain',
      route: ['sectors'],
      permission: { resource: listed, permission: 'List' },
    });
    expect(list(tenantsConfig(descriptor, resourceMeta, { auth: 'Crew' })).title).toBe('Sectors');
    expect(list(config).primaryResource).toBe(listed);
    expect(list(config).showRowCount).toBe(true);
    expect(list(config).emptyMessage).toBe('There are no sectors yet.');
  });

  it('say a record of two words as a person would', () => {
    const costCenters = 'CostCenters' as Resource;
    const listedCenters = 'StaffListedCostCenters' as Resource;
    const centerMetas: Record<string, ResourceMeta> = {
      [costCenters]: {
        route: 'cost-centers',
        nameField: 'name',
        fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name')],
      } as ResourceMeta,
      [listedCenters]: {
        route: 'staff-listed-cost-centers',
        rowsOf: costCenters,
        fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('name'), field('people'), field('since')],
      } as ResourceMeta,
    };
    const descriptor = descriptorOf({
      auth: 'Staff',
      label: 'Staff',
      tenant: 'CostCenter',
      tenantResource: costCenters,
      sides: [{ scope: 'global', lists: { users, tenants: listedCenters }, methods: {} }],
    });
    const meta = (resource: Resource): ResourceMeta => centerMetas[resource];
    const built = tenantsConfig(descriptor, meta, { auth: 'Staff' });
    expect(tenantsPage(descriptor, meta, { auth: 'Staff' })).toMatchObject({
      label: 'All cost centers',
      route: ['cost-centers'],
    });
    expect(list(built).title).toBe('Cost centers');
    expect(list(built).emptyMessage).toBe('There are no cost centers yet.');
    expect(list(built).rowButtons?.map((button) => button.label)).toEqual(['Open this cost center']);
    expect(formOf(built).label).toBe('Cost center');
  });

  it("list the name first, opening the row, then the record's own fields in its order, the directory, the switch, the people and since when, and the key unseen", () => {
    expect(shown(list(config).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['directory', 'Directory'],
      ['directoryOnly', 'Directory only'],
      ['people', 'People'],
      ['since', 'Since'],
    ]);
    expect(list(config).listColumns[0].opensRow).toBe(true);
    expect(list(config).listColumns.filter((column) => column.opensRow)).toHaveLength(1);
    expect(hiddenIds(list(config).listColumns)).toEqual(['id']);
    // The record's own field for when a sector was charted is the list's Since, and no
    // column of its own.
    expect(list(config).listColumns.map((column) => column.id)).not.toContain('chartedAt');
  });

  it('draw no Since where the description names no field for when a tenant was made', () => {
    const plainMetas: Record<string, ResourceMeta> = {
      ...metas,
      [sectors]: {
        ...metas[sectors],
        fields: metas[sectors].fields.filter((candidate) => candidate.fieldName !== 'chartedAt'),
      } as ResourceMeta,
      [listed]: {
        ...metas[listed],
        fields: metas[listed].fields.filter((candidate) => candidate.fieldName !== 'since'),
      } as ResourceMeta,
    };
    const built = tenantsConfig(
      descriptorOf(crewOf({ invitedTenants: invited, tenants: listed }, { tenantSinceField: undefined })),
      (resource) => plainMetas[resource],
      { auth: 'Crew' },
    );
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['directory', 'Directory'],
      ['directoryOnly', 'Directory only'],
      ['people', 'People'],
    ]);
    const summary = built.relatedConfigs[0] as ListViewConfig;
    expect(shown(summary.listColumns)).toEqual([
      ['administrators', 'Administrators'],
      ['directory', 'Directory'],
      ['people', 'People'],
    ]);
  });

  it('carry a field of the record named since as any field where the description names none', () => {
    const sinceMetas: Record<string, ResourceMeta> = {
      ...metas,
      [sectors]: {
        ...metas[sectors],
        fields: [
          ...metas[sectors].fields.filter((candidate) => candidate.fieldName !== 'chartedAt').slice(0, 3),
          field('since', { displayType: 'date' }),
          ...metas[sectors].fields.filter((candidate) => candidate.fieldName !== 'chartedAt').slice(3),
        ],
      } as ResourceMeta,
    };
    const built = tenantsConfig(
      descriptorOf(crewOf({ invitedTenants: invited, tenants: listed }, { tenantSinceField: undefined })),
      (resource) => sinceMetas[resource],
      { auth: 'Crew' },
    );
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['since', 'Since'],
      ['established', 'Established'],
      ['directory', 'Directory'],
      ['directoryOnly', 'Directory only'],
      ['people', 'People'],
    ]);
    expect(formOf(built).children.map((element) => (element as FieldElement).name)).toEqual([
      'name',
      'region',
      'since',
      'established',
      'directoryOnly',
      'id',
    ]);
    const summary = built.relatedConfigs[0] as ListViewConfig;
    expect(shown(summary.listColumns).map(([id]) => id)).toEqual(['administrators', 'directory', 'people']);
  });

  it("draw one Since column, after People, where the record's own field for it is named since", () => {
    const sinceMetas: Record<string, ResourceMeta> = {
      ...metas,
      [sectors]: {
        ...metas[sectors],
        fields: metas[sectors].fields.map((candidate) =>
          candidate.fieldName === 'chartedAt' ? field('since', { displayType: 'date', readOnly: true }) : candidate,
        ),
      } as ResourceMeta,
    };
    const built = tenantsConfig(
      descriptorOf(crewOf({ invitedTenants: invited, tenants: listed }, { tenantSinceField: 'since' })),
      (resource) => sinceMetas[resource],
      { auth: 'Crew' },
    );
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['directory', 'Directory'],
      ['directoryOnly', 'Directory only'],
      ['people', 'People'],
      ['since', 'Since'],
    ]);
    expect(formOf(built).children.map((element) => (element as FieldElement).name)).not.toContain('since');
  });

  it('leave out the switch where the description names none, and a field the list does not carry', () => {
    const plainMetas: Record<string, ResourceMeta> = {
      ...metas,
      [sectors]: {
        ...metas[sectors],
        fields: [...metas[sectors].fields, field('motto')],
      } as ResourceMeta,
      [listed]: {
        ...metas[listed],
        fields: metas[listed].fields.filter(
          (candidate) => !['directoryOnly', 'directory'].includes(candidate.fieldName),
        ),
      } as ResourceMeta,
    };
    const built = tenantsConfig(
      descriptorOf(crewOf({ invitedTenants: invited, tenants: listed }, { tenantDirectoryOnlyField: undefined })),
      (resource) => plainMetas[resource],
      { auth: 'Crew' },
    );
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['people', 'People'],
      ['since', 'Since'],
    ]);
    // The form is the record's: a field the list does not carry is still a field of the tenant.
    expect(formOf(built).children.map((element) => (element as FieldElement).label)).toEqual([
      'Name',
      'Region',
      'Established',
      'Directory only',
      'Motto',
      'Id',
    ]);
  });

  it('keep the View column where the record has no name field, and head a row by nothing of its own', () => {
    const namelessMetas: Record<string, ResourceMeta> = {
      ...metas,
      [sectors]: { ...metas[sectors], nameField: undefined } as ResourceMeta,
    };
    const built = tenantsConfig(fullDescriptor(), (resource) => namelessMetas[resource], { auth: 'Crew' });
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['directory', 'Directory'],
      ['directoryOnly', 'Directory only'],
      ['people', 'People'],
      ['since', 'Since'],
    ]);
    expect(list(built).listColumns.some((column) => column.opensRow)).toBe(false);
    expect(list(built).showViewButton).toBe(true);
    expect(list(built).rowTitle).toBeUndefined();
  });

  it("draw a tenant's form with the name, the record's fields, and the switch and the key read only", () => {
    const form = formOf(config);
    expect(form.label).toBe('Sector');
    expect(
      form.children.map((element) => {
        const drawn = element as FieldElement;
        return [drawn.name, drawn.label, drawn.cols, drawn.readOnly];
      }),
    ).toEqual([
      ['name', 'Name', 4, false],
      ['region', 'Region', 4, false],
      ['established', 'Established', 4, false],
      ['directoryOnly', 'Directory only', 4, true],
      ['id', 'Id', 4, true],
    ]);
  });

  it("head a tenant's page by the tenant's name", () => {
    expect(list(config).rowTitle?.({ id: 'a1', name: 'Driftmoor' })).toBe('Driftmoor');
  });

  it('send an invitation from above the list, with the waiting list above it, titled, keeping Withdraw and no method of its own', () => {
    // The Invited sectors page's own list, as the identity pages build it.
    const own = identityPages(crew, resourceMeta, { auth: 'Crew', scope: 'global' }).find(
      (candidate) => candidate.parentConfig.primaryResource === invited,
    )?.parentConfig as ListViewConfig;
    expect(list(config).listMethods).toEqual(own.listMethods);
    expect(list(config).listMethods?.map((presented) => [presented.method, presented.label, presented.icon])).toEqual([
      [invite, 'Invite a sector', 'send'],
    ]);
    expect(list(config).above).toHaveLength(1);
    const invitations = list(config).above?.[0] as ListViewConfig;
    expect(invitations.primaryResource).toBe(invited);
    expect(invitations.title).toBe('Invited sectors, waiting for a first sign-in');
    expect(invitations.emptyMessage).toBe('No invitation is waiting.');
    // Drawn only while an invitation waits, and not asked of a person who may not list them.
    expect(invitations.hiddenWhenEmpty).toBe(true);
    expect(invitations).not.toHaveProperty('listMethods');
    expect(invitations.listColumns).toBe(own.listColumns);
    expect(invitations.rowMethods).toBe(own.rowMethods);
    expect(
      (invitations.rowMethods === false ? [] : (invitations.rowMethods ?? [])).map((presented) => [
        presented.method,
        presented.label,
      ]),
    ).toEqual([[withdraw, 'Withdraw']]);
    // The Invited sectors page keeps its own title and its Invite.
    expect([own.title, own.listMethods?.length]).toEqual(['Invited sectors', 1]);
  });

  it('draw no waiting list and no Invite where the description names no invitations', () => {
    const built = tenantsConfig(descriptorOf(crewOf({ tenants: listed })), resourceMeta, { auth: 'Crew' });
    expect(list(built).above).toBeUndefined();
    expect(list(built).listMethods).toBeUndefined();
  });

  it("put a summary under a tenant's row: its administrators a line each, its directory, its people and since when, and nothing to change", () => {
    expect(config.relatedConfigs).toHaveLength(1);
    const summary = config.relatedConfigs[0] as ListViewConfig;
    expect(summary.title).toBe('Summary');
    expect(summary.primaryResource).toBe(listed);
    expect(summary.parentRelation).toEqual({ parentKey: 'id', childKey: 'id' });
    expect(
      summary.listColumns
        .filter((column) => !column.hidden)
        .map((column) => [column.id, column.header, column.lines ?? false, column.filterable, column.sortable]),
    ).toEqual([
      ['administrators', 'Administrators', true, false, false],
      ['directory', 'Directory', false, false, false],
      ['people', 'People', false, false, false],
      ['since', 'Since', false, false, false],
    ]);
    expect(hiddenIds(summary.listColumns)).toEqual(['id']);
    expect(summary.showViewButton).toBe(false);
    expect(summary.showRowCount).toBe(false);
    expect(summary.showSearchBox).toBe(false);
    expect(summary.rowMethods).toBe(false);
    expect(summary.listMethods).toBeUndefined();
    expect(summary.shouldRenderActions.create({})).toBe(false);
    expect(summary.shouldRenderActions.edit({})).toBe(false);
    expect(summary.shouldRenderActions.delete({})).toBe(false);
    expect(summary.rowButtons).toEqual(list(config).rowButtons);
  });

  describe('the button that opens a tenant', () => {
    const driftmoor: Place = { id: 'a1', label: 'Driftmoor' };
    // A sector that seats the person and admits only people who sign in through its own
    // directory: closed to this sign-in, with the server's reason.
    const bastion: ClosedPlace = {
      id: 'c3',
      label: 'Bastion Prime',
      reason: 'The bastion admits only people who sign in through its own directory. You signed in with a password.',
    };
    const run = (row: RecordData, held: Place[], closed: ClosedPlace[] = []) => {
      const places = { places: () => held, closed: () => closed, open: vi.fn(() => Promise.resolve(true)) };
      const notices: CreateNotificationMessage[] = [];
      const notifications = {
        addGlobalNotification: (notice: CreateNotificationMessage) => notices.push(notice),
      };
      const injector = Injector.create({
        providers: [
          { provide: PlaceService, useValue: places },
          { provide: NotificationService, useValue: notifications },
        ],
      });
      const button = list(config).rowButtons?.[0];
      button?.run(row, injector);
      return { button, open: places.open, notices };
    };

    it("opens the place of the row's id", () => {
      const { button, open, notices } = run({ id: 'a1', name: 'Driftmoor' }, [{ id: '', label: 'Outside' }, driftmoor]);
      expect([button?.label, button?.icon]).toEqual(['Open this sector', 'login']);
      expect(open).toHaveBeenCalledWith(driftmoor);
      expect(notices).toEqual([]);
    });

    it('opens nothing and says why where the tenant is none of the places of the person', () => {
      const { open, notices } = run({ id: 'b2', name: 'Quietfall' }, [driftmoor], [bastion]);
      expect(open).not.toHaveBeenCalled();
      expect(notices).toEqual([
        {
          message: 'You hold no role in Quietfall, so you cannot open it.',
          type: AlertType.INFO,
          duration: 5000,
          link: '',
        },
      ]);
    });

    it("opens nothing and says the server's reason, word for word, where the tenant seats the person and is closed to this sign-in", () => {
      const { open, notices } = run({ id: 'c3', name: 'Bastion' }, [driftmoor], [bastion]);
      expect(open).not.toHaveBeenCalled();
      expect(notices).toEqual([
        {
          message:
            'The bastion admits only people who sign in through its own directory. You signed in with a password.',
          type: AlertType.INFO,
          duration: 8000,
          link: '',
        },
      ]);
    });

    it('names the tenant by its word where the row carries no name', () => {
      const { notices } = run({ id: 'b2' }, [driftmoor]);
      expect(notices.map((notice) => notice.message)).toEqual([
        'You hold no role in this sector, so you cannot open it.',
      ]);
    });
  });

  it('list the tenant record itself where the description names no list of tenants, with nothing above, no Invite and no summary', () => {
    const built = tenantsConfig(descriptorOf(crewOf({ invitedTenants: invited })), resourceMeta, { auth: 'Crew' });
    expect(list(built).primaryResource).toBe(sectors);
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['directoryOnly', 'Directory only'],
      ['chartedAt', 'Since'],
    ]);
    expect(hiddenIds(list(built).listColumns)).toEqual(['id']);
    expect(list(built).above).toBeUndefined();
    expect(list(built).listMethods).toBeUndefined();
    expect(list(built).emptyMessage).toBe('There are no sectors yet.');
    expect(built.relatedConfigs).toEqual([]);
    expect(list(built).rowTitle?.({ id: 'a1', name: 'Driftmoor' })).toBe('Driftmoor');
    expect(list(built).rowButtons?.map((button) => button.label)).toEqual(['Open this sector']);
    // The record's own field for when a sector was charted is a field of the form, labeled
    // Since, read only where its metadata says the server never takes it.
    expect(
      formOf(built).children.map((element) => {
        const drawn = element as FieldElement;
        return [drawn.name, drawn.label, drawn.cols, drawn.readOnly];
      }),
    ).toEqual([
      ['name', 'Name', 4, false],
      ['region', 'Region', 4, false],
      ['established', 'Established', 4, false],
      ['directoryOnly', 'Directory only', 4, true],
      ['chartedAt', 'Since', 4, false],
      ['id', 'Id', 4, true],
    ]);
  });

  it('list the tenant record itself with no Since where the description names no field for it', () => {
    const plainMetas: Record<string, ResourceMeta> = {
      ...metas,
      [sectors]: {
        ...metas[sectors],
        fields: metas[sectors].fields.filter((candidate) => candidate.fieldName !== 'chartedAt'),
      } as ResourceMeta,
    };
    const built = tenantsConfig(
      descriptorOf(crewOf({ invitedTenants: invited }, { tenantSinceField: undefined })),
      (resource) => plainMetas[resource],
      { auth: 'Crew' },
    );
    expect(shown(list(built).listColumns)).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['established', 'Established'],
      ['directoryOnly', 'Directory only'],
    ]);
    expect(formOf(built).children).toHaveLength(5);
  });

  it("offer the application's own methods on a tenant's page", () => {
    const rpcConfigs = [rpcConfig({ label: 'Issue bulletin', method: 'IssueBulletin' as Method, conditions: [] })];
    const built = tenantsConfig(fullDescriptor(), resourceMeta, { auth: 'Crew', rpcConfigs });
    expect(built.rpcConfigs).toBe(rpcConfigs);
  });

  it('answer the same configuration to the same call, and refuse a second call with other options', () => {
    const descriptor = fullDescriptor();
    const rpcConfigs = [rpcConfig({ label: 'Issue bulletin', method: 'IssueBulletin' as Method, conditions: [] })];
    const first = tenantsConfig(descriptor, resourceMeta, { auth: 'Crew', rpcConfigs });
    expect(tenantsConfig(descriptor, resourceMeta, { auth: 'Crew', rpcConfigs })).toBe(first);
    expect(tenantsPage(descriptor, resourceMeta, { auth: 'Crew', rpcConfigs })).toBe(
      tenantsPage(descriptor, resourceMeta, { auth: 'Crew', rpcConfigs }),
    );
    expect(() => tenantsRoute(descriptor, resourceMeta, { auth: 'Crew' })).toThrow(
      'tenantsPage: the tenants page of Crew was already made with other options; pass tenantsPage and tenantsRoute the same options',
    );
    expect(() => tenantsConfig(descriptor, resourceMeta, { auth: 'Crew', rpcConfigs: [...rpcConfigs] })).toThrow(
      'tenantsPage: the tenants page of Crew was already made with other options; pass tenantsPage and tenantsRoute the same options',
    );
  });

  it('refuse, naming what is missing, an auth the description does not carry, one with no whole application side, and one with no tenant record', () => {
    const descriptor = descriptorOf(
      crewOf({ tenants: listed }),
      { auth: 'Guests', label: 'Guests', tenant: 'Sector', tenantResource: sectors, sides: [] },
      { auth: 'Members', label: 'Members', sides: [{ scope: 'global', lists: { users }, methods: {} }] },
    );
    expect(() => tenantsPage(descriptor, resourceMeta, { auth: 'Pilots' })).toThrow(
      'tenantsPage: the API description carries no generated management of the auth Pilots (it carries: Crew, Guests, Members); the auth is named as GenerateIdentity names it, and its management is served on the outlets that declare or manage it',
    );
    expect(() => tenantsPage(descriptorOf(), resourceMeta, { auth: 'Pilots' })).toThrow(
      'tenantsPage: the API description carries no generated management of the auth Pilots (it carries: none);',
    );
    expect(() => tenantsRoute(descriptor, resourceMeta, { auth: 'Guests' })).toThrow(
      "tenantsPage: the generated management of Guests has no whole application's side on this API",
    );
    expect(() => tenantsPage(descriptor, resourceMeta, { auth: 'Members' })).toThrow(
      'tenantsPage: the description names no tenant record for Members on this API: the application has no tenants, or this outlet does not serve the record, so there is no list of tenants to place',
    );
  });

  it('route the list guarded by List on what it lists, and a tenant at its own address guarded by Read on the record', () => {
    const descriptor = fullDescriptor();
    const route = tenantsRoute(descriptor, resourceMeta, { auth: 'Crew' });
    expect(route.path).toBe('sectors');
    expect(route.data?.[pageDataKey]).toBe(tenantsPage(descriptor, resourceMeta, { auth: 'Crew' }));
    const [listRoute, rowRoute] = route.children ?? [];
    expect([listRoute.path, listRoute.data?.['scope']]).toEqual(['', { resource: listed, permission: 'List' }]);
    expect(listRoute.canActivate).toHaveLength(1);
    expect([rowRoute.path, rowRoute.data?.['scope']]).toEqual([':uuid', { resource: sectors, permission: 'Read' }]);
    expect(rowRoute.canActivate).toHaveLength(1);
  });
});

// The menu's way to the invitation: an action, shown outside every tenant to a person who
// may send one, that opens the tenants page with the invitation's dialog.
describe('inviteTenantAction', () => {
  /** The action as a menu file makes it, where the menus are declared, with a router that records where it goes. */
  const made = (descriptor: ApiDescriptor, auth = 'Crew') => {
    const router = { navigate: vi.fn(() => Promise.resolve(true)) };
    const injector = Injector.create({ providers: [{ provide: Router, useValue: router }] });
    const action = runInInjectionContext(injector, () => inviteTenantAction(descriptor, resourceMeta, { auth }));
    return { action, navigate: router.navigate };
  };

  it('is labeled with the word the tenants go by, sends, lives outside every tenant, and needs Execute on the invitation', () => {
    const { action } = made(fullDescriptor());
    expect([action.label, action.icon, action.place]).toEqual(['Invite a sector…', 'send', 'outside']);
    expect(action.permission).toEqual({ resource: invite, permission: 'Execute' });
  });

  it("opens the tenants page from the root with the invitation's method named in the address", () => {
    const descriptor = fullDescriptor();
    const { action, navigate } = made(descriptor);
    expect(navigate).not.toHaveBeenCalled();
    action.run();
    expect(navigate.mock.calls).toEqual([[['/sectors'], { queryParams: { run: invite } }]]);
    expect(tenantsConfig(descriptor, resourceMeta, { auth: 'Crew' }).routeData.route).toBe('sectors');
  });

  it('picks the article by how the word is said: "a unit", "an office"', () => {
    const labelOf = (tenant: string): string =>
      made(descriptorOf(crewOf({ invitedTenants: invited, tenants: listed }, { tenant }))).action.label;
    expect(labelOf('Unit')).toBe('Invite a unit…');
    expect(labelOf('Office')).toBe('Invite an office…');
  });

  it("is refused where the description names no method that invites a tenant, and with the tenants page's own refusals", () => {
    const noInvitation = descriptorOf({
      ...crewOf({ tenants: listed }),
      sides: [{ scope: 'global', lists: { users, tenants: listed }, methods: {} }],
    });
    expect(() => made(noInvitation)).toThrow(
      'inviteTenantAction: the description names no method that invites a tenant for Crew: its tenants are not made by invitation, so there is no invitation to place in a menu',
    );
    expect(() => made(fullDescriptor(), 'Pilots')).toThrow(
      'tenantsPage: the API description carries no generated management of the auth Pilots (it carries: Crew);',
    );
  });
});
