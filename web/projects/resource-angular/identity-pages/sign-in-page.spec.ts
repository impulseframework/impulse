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
  RowAction,
  rowActionsKey,
} from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';
import { identityPages } from './identity-pages';

// The Sign-in page as the library's list draws it. The page is built from the API's
// description (identityPages) and is an ordinary list: the server lists the Auth's
// sign-in methods, the page offers on each row the one action that changes what was
// chosen for it, and only to a caller whose roles grant the method. While a method is
// open to sign-up the page says so above the list.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const methodsList = 'StaffSignInMethods' as Resource;
const rolesList = 'StaffSignUpRoles' as Resource;
const users = 'StaffSessionUsers' as Resource;

const metas: Record<string, ResourceMeta> = {
  [methodsList]: {
    route: 'staff-sign-in-methods',
    fields: [
      field('name', { primaryKey: { ordinalPosition: 0 } }),
      field('label'),
      field('admits'),
      field('openSignUp', { displayType: 'boolean' }),
      field('defaultRole'),
      field('directoryGivesRoles', { displayType: 'boolean' }),
      field('ownRule', { displayType: 'boolean' }),
      field('says'),
    ],
  } as ResourceMeta,
  [rolesList]: {
    route: 'staff-sign-up-roles',
    nameField: 'name',
    fields: [field('name', { primaryKey: { ordinalPosition: 0 } }), field('description')],
  } as ResourceMeta,
  [users]: {
    route: 'staff-session-users',
    fields: [field('id', { primaryKey: { ordinalPosition: 0 } }), field('username')],
  } as ResourceMeta,
};

const open = 'OpenStaffSignUp';
const close = 'MakeStaffInviteOnly';
const method = (name: string): ApiDescriptor['methods'][string] => ({
  method: name as Method,
  property: name.charAt(0).toLowerCase() + name.slice(1),
  route: name.toLowerCase(),
  scope: 'global',
});

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {
    [methodsList]: {
      resource: methodsList,
      property: 'staffSignInMethods',
      route: 'staff-sign-in-methods',
      scope: 'global',
      consolidated: false,
      keys: ['name'],
      operations: ['list'],
    },
    [rolesList]: {
      resource: rolesList,
      property: 'staffSignUpRoles',
      route: 'staff-sign-up-roles',
      scope: 'global',
      consolidated: false,
      keys: ['name'],
      operations: ['list'],
    },
    [users]: {
      resource: users,
      property: 'staffSessionUsers',
      route: 'staff-session-users',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
    },
  },
  methods: { [open]: method(open), [close]: method(close) },
  identity: [
    {
      auth: 'Staff',
      label: 'Staff',
      sides: [
        {
          scope: 'global',
          lists: { users, signInMethods: methodsList, signUpRoles: rolesList },
          methods: { openSignUp: open, makeInviteOnly: close },
        },
      ],
    },
  ],
};

// Neither method names a target row: each takes the name of a sign-in method. Opening
// one declares a warning and the word a person types.
const methodMetas: Record<string, MethodMeta> = {
  [open]: {
    route: 'openstaffsignup',
    warning: { typed: 'open' },
    fields: [
      { fieldName: 'name', displayType: 'string', required: true },
      { fieldName: 'defaultRole', displayType: 'enumerated', enumeratedResource: rolesList },
    ],
  } as MethodMeta,
  [close]: {
    route: 'makestaffinviteonly',
    fields: [{ fieldName: 'name', displayType: 'string', required: true }],
  } as MethodMeta,
};

const password = {
  name: 'password',
  label: 'Password',
  admits: 'Anyone with an email address',
  openSignUp: true,
  defaultRole: 'Member',
  directoryGivesRoles: false,
  says: 'Open: Anyone with an email address will be able to sign up without an invitation. Each of them gets the role Member.',
};
const google = {
  name: 'google',
  label: 'Google',
  admits: 'Anyone with a Google account',
  openSignUp: false,
  defaultRole: '',
  directoryGivesRoles: false,
  says: 'Invite only: a person signs in once they are invited.',
};
const entra = {
  name: 'entra',
  label: 'Microsoft',
  admits: 'Anyone with an account at acme.example',
  openSignUp: false,
  defaultRole: '',
  directoryGivesRoles: true,
  says: 'The directory gives the roles, so it decides who may sign in: a person it gives a role signs in, and nobody else.',
};
/** A registration that carries an account rule of its own: the application's code decides who signs in. */
const partners = {
  name: 'partners',
  label: 'Partners',
  admits: 'Anyone with an account at Harbor',
  openSignUp: false,
  defaultRole: '',
  directoryGivesRoles: false,
  ownRule: true,
  says: "The application's own rule decides who may sign in through this method: it is neither opened to sign-up nor made invite only here.",
};
/** A method opened when its registration admitted fewer people than it does now. */
const widened = {
  name: 'workspace',
  label: 'Acme',
  admits: 'Anyone with an account at acme.example or lakeside.edu',
  openSignUp: false,
  defaultRole: '',
  directoryGivesRoles: false,
  ownRule: false,
  says: 'Invite only: the people this method admits have changed since it was opened, and it admits anyone with an account at acme.example or lakeside.edu now. Open it again to admit them.',
};

describe('the Sign-in page, drawn by the list', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;
  let opened: MethodDialogData[];

  /** The caller lists the sign-in methods; a test adds what else they hold. */
  const lists: PermissionDigest = Object.fromEntries([
    [methodsList, { List: 'granted' }],
    ...['name', 'label', 'admits', 'openSignUp', 'defaultRole', 'directoryGivesRoles', 'ownRule', 'says'].map(
      (name) => [`${methodsList}.${name}`, { List: 'granted' }],
    ),
  ]) as PermissionDigest;
  /** The grant on the setting: Execute on the two methods that change it. */
  const mayChoose: PermissionDigest = { [open]: { Execute: 'granted' }, [close]: { Execute: 'granted' } };

  const create = async (rows: Record<string, unknown>[], granted: PermissionDigest = {}): Promise<void> => {
    const digest: PermissionDigest = { ...lists, ...granted };
    const resourceMeta = (resource: Resource): ResourceMeta => metas[resource];
    const page = identityPages(descriptor, resourceMeta, { auth: 'Staff', scope: 'global' }).find(
      (config) => config.page?.label === 'Sign-in',
    );
    if (!page) {
      throw new Error('identityPages built no Sign-in page');
    }
    const config = page.parentConfig as ListViewConfig;
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lists.test');
      if (url.pathname === '/api/permission-digest') {
        return { status: 200, body: digest };
      }
      if (url.pathname === '/api/staff-sign-in-methods') {
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
  const cells = (): string[][] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td'))
        .map((cell) => (cell.textContent ?? '').trim())
        .filter((text) => text !== ''),
    );

  it('lists one row for each method: who can sign in with it, what was chosen, and what a person with no invitation meets', async () => {
    await create([password, google, entra], mayChoose);

    expect(cells().map((row) => row.slice(0, 3))).toEqual([
      ['Password', 'Anyone with an email address', 'Open'],
      ['Google', 'Anyone with a Google account', 'Invite only'],
      ['Microsoft', 'Anyone with an account at acme.example', 'Set by the directory'],
    ]);
    // The method whose directory gives the roles says why it has no action.
    expect(cells()[2].join(' ')).toContain('The directory gives the roles, so it decides who may sign in');
  });

  it('offers a caller who holds the grant the one action a row has, and none on a method whose directory gives the roles', async () => {
    await create([password, google, entra], mayChoose);

    expect(actionsOf(0).map((action) => [action.id, action.label])).toEqual([[close, 'Make invite only']]);
    expect(actionsOf(1).map((action) => [action.id, action.label])).toEqual([[open, 'Open sign-up']]);
    expect(actionsOf(2)).toEqual([]);
    const buttons = Array.from(element().querySelectorAll('tr.impulse-row')).map(
      (row) => row.querySelectorAll('button.row-actions').length,
    );
    expect(buttons).toEqual([1, 1, 0]);
  });

  it("offers no action on a method whose registration carries a rule of its own, and says the application's rule decides", async () => {
    await create([google, partners], mayChoose);

    expect(cells().map((row) => row.slice(0, 3))).toEqual([
      ['Google', 'Anyone with a Google account', 'Invite only'],
      ['Partners', 'Anyone with an account at Harbor', 'Set by the application'],
    ]);
    expect(cells()[1].join(' ')).toContain("The application's own rule decides who may sign in through this method");
    expect(actionsOf(0).map((action) => action.label)).toEqual(['Open sign-up']);
    expect(actionsOf(1)).toEqual([]);
    const buttons = Array.from(element().querySelectorAll('tr.impulse-row')).map(
      (row) => row.querySelectorAll('button.row-actions').length,
    );
    expect(buttons).toEqual([1, 0]);
  });

  it('says the people a method admits have changed since it was opened, and offers to open it again', async () => {
    await create([widened], mayChoose);

    expect(cells()[0].slice(0, 3)).toEqual([
      'Acme',
      'Anyone with an account at acme.example or lakeside.edu',
      'Invite only',
    ]);
    expect(cells()[0].join(' ')).toContain(
      'the people this method admits have changed since it was opened, and it admits anyone with an account at acme.example or lakeside.edu now',
    );
    expect(actionsOf(0).map((action) => [action.id, action.label])).toEqual([[open, 'Open sign-up']]);
    // Nothing is open: the page keeps no notice above the list.
    expect(element().querySelector('.impulse-resource-list-notice')).toBeNull();
  });

  it('offers a caller without the grant no action on any row', async () => {
    await create([password, google, entra]);

    expect([actionsOf(0), actionsOf(1), actionsOf(2)]).toEqual([[], [], []]);
    expect(element().querySelectorAll('button.row-actions').length).toBe(0);
    expect(fixture.componentInstance.columns().map((column) => column.id)).not.toContain(rowActionsKey);
  });

  it('offers a caller who holds one of the two grants that action alone', async () => {
    await create([password, google, entra], { [close]: { Execute: 'granted' } });

    expect(actionsOf(0).map((action) => action.label)).toEqual(['Make invite only']);
    expect(actionsOf(1)).toEqual([]);
  });

  it('opens the method of a row in its dialog, the name of the method filled from the row', async () => {
    await create([password, google, entra], mayChoose);

    actionsOf(1)[0].run();
    actionsOf(0)[0].run();

    expect(opened).toEqual([
      // Opening a method: the dialog asks the server for its warning and for the role,
      // listed with its description; the page asks no question of its own.
      {
        method: open,
        label: 'Open sign-up',
        preset: { name: 'google' },
        confirm: undefined,
        labels: { defaultRole: 'Role each new person gets' },
        shows: { defaultRole: ['name', 'description'] },
      },
      {
        method: close,
        label: 'Make invite only',
        preset: { name: 'password' },
        confirm: 'People who already have accounts keep them. New people will need an invitation.',
      },
    ]);
  });

  it('keeps a notice above the list, in the warning style, while a method is open to sign-up', async () => {
    await create([password, google, entra], mayChoose);

    const notice = element().querySelector('.impulse-resource-list-notice');
    expect(notice?.getAttribute('role')).toBe('status');
    expect(notice?.querySelector('mat-icon.impulse-resource-list-notice-icon')?.textContent?.trim()).toBe('warning');
    expect(notice?.querySelector('.impulse-resource-list-notice-words')?.textContent?.trim()).toBe(
      'Password is open to sign-up. Anyone with an email address will be able to sign up without an invitation. Each of them gets the role Member.',
    );
  });

  it('draws no notice while every method is invite only', async () => {
    await create([google, entra], mayChoose);

    expect(element().querySelector('.impulse-resource-list-notice')).toBeNull();
  });
});
