import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { ApiDescriptor, FieldMeta, Resource, ResourceMeta, TenantCreationDescriptor } from '@impulseframework/resource';
import { Place, PlaceService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { API_URL, RESOURCE_META } from '@impulseframework/resource-angular/types';
import { of } from 'rxjs';
import { describe, expect, it, vi } from 'vitest';
import { CreateTenantDialogComponent, openCreateTenantDialog } from './create-tenant-dialog.component';

// The dialog a person who signed up and holds no tenant creates their own through. It is
// drawn from the description alone: the create-your-tenant route (`tenantCreation`) names
// the fields it takes, and the tenant record's metadata says how each is drawn. It posts
// them with the application's HttpClient, reads the permissions again, closes with the
// new tenant's id and opens the tenant's first page.

const field = (fieldName: string, extra: Partial<FieldMeta> = {}): FieldMeta =>
  ({ fieldName, displayType: 'string', required: false, isIndex: false, ...extra }) as FieldMeta;

const offices = 'Offices' as Resource;
const metas: Record<string, ResourceMeta> = {
  [offices]: {
    route: 'offices',
    nameField: 'name',
    fields: [
      field('id', { primaryKey: { ordinalPosition: 0 }, displayType: 'uuid' }),
      field('name', { required: true }),
      field('region', { required: true }),
      field('motto'),
    ],
  } as ResourceMeta,
};

const descriptorOf = (
  fields: TenantCreationDescriptor['fields'] | undefined,
  word: { one: string; many: string } | null = { one: 'Office', many: 'Offices' },
): ApiDescriptor => ({
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {},
  domainRoute: { segment: 'offices', param: 'officeID', ...(word ? { word } : {}) },
  ...(fields ? { tenantCreation: { route: 'create-office', resource: offices, key: 'id', fields } } : {}),
});

interface Opened {
  fixture: ComponentFixture<CreateTenantDialogComponent>;
  component: CreateTenantDialogComponent;
  http: HttpTestingController;
  /** What happened, in order: the permissions read, the dialog closed, the page opened. */
  events: unknown[][];
}

async function open(descriptor: ApiDescriptor): Promise<Opened> {
  const events: unknown[][] = [];
  await TestBed.configureTestingModule({
    imports: [CreateTenantDialogComponent],
    providers: [
      provideResourceTesting(),
      provideNativeDateAdapter(),
      provideHttpClient(),
      provideHttpClientTesting(),
      { provide: API_URL, useValue: '/console/api' },
      { provide: RESOURCE_META, useValue: (resource: Resource): ResourceMeta => metas[resource] },
      { provide: MAT_DIALOG_DATA, useValue: descriptor },
      { provide: MatDialogRef, useValue: { close: (id?: string) => events.push(['close', id]) } },
      {
        provide: AuthService,
        useValue: {
          refreshPermissions: () => {
            events.push(['permissions read']);
            return of(undefined);
          },
        },
      },
      {
        provide: PlaceService,
        useValue: {
          places: (): Place[] => [{ id: 'o-1', label: 'West' }],
          landing: (place: Place) => `/offices/${place.id}/home`,
        },
      },
    ],
  }).compileComponents();
  const router = TestBed.inject(Router);
  vi.spyOn(router, 'navigateByUrl').mockImplementation((url) => {
    events.push(['opened', String(url)]);
    return Promise.resolve(true);
  });
  const fixture = TestBed.createComponent(CreateTenantDialogComponent);
  fixture.detectChanges();
  return { fixture, component: fixture.componentInstance, http: TestBed.inject(HttpTestingController), events };
}

const element = (o: Opened): HTMLElement => o.fixture.nativeElement as HTMLElement;
const text = (o: Opened, selector: string): string => element(o).querySelector(selector)?.textContent?.trim() ?? '';
const texts = (o: Opened, selector: string): string[] =>
  [...element(o).querySelectorAll(selector)].map((found) => found.textContent?.trim() ?? '');
const createButton = (o: Opened): HTMLButtonElement =>
  element(o).querySelector('button[type="submit"]') as HTMLButtonElement;
const settled = async (o: Opened): Promise<void> => {
  o.fixture.detectChanges();
  await o.fixture.whenStable();
  o.fixture.detectChanges();
};

describe('CreateTenantDialogComponent', () => {
  it('asks for the name alone, in the words of the description, posts it, reads the permissions again, closes with the id and opens the tenant', async () => {
    const o = await open(descriptorOf([{ name: 'name', required: true }]));
    expect(text(o, 'h2')).toBe('Create your office');
    expect(text(o, 'mat-dialog-content > p').replace(/\s+/g, ' ')).toBe(
      'You are signed in and hold no office. Name yours: you become its administrator the moment it exists, and it is yours to invite people into.',
    );
    expect(element(o).querySelectorAll('input')).toHaveLength(1);
    expect(o.component.elements.map((drawn) => drawn.label)).toEqual(['Name']);
    expect(text(o, '.hint')).toBe('What people call the office, wherever it is listed. It can be changed later.');
    expect(texts(o, 'mat-dialog-actions button')).toEqual(['Cancel', 'Create office']);
    expect(createButton(o).disabled).toBe(true);

    o.component.form.controls['name'].setValue('West');
    await settled(o);
    expect(createButton(o).disabled).toBe(false);

    const created = o.component.create();
    await settled(o);
    // Held while the request is open.
    expect(createButton(o).disabled).toBe(true);
    const request = o.http.expectOne('/console/api/create-office');
    expect([request.request.method, request.request.body]).toEqual(['POST', { name: 'West' }]);
    request.flush({ id: 'o-1' });
    await created;

    expect(o.events).toEqual([['permissions read'], ['close', 'o-1'], ['opened', '/offices/o-1/home']]);
    o.http.verify();
  });

  it('asks for every field the route takes, in its order, holds Create to the required ones, and leaves out one left empty', async () => {
    const o = await open(
      descriptorOf([
        { name: 'name', required: true },
        { name: 'region', required: true },
        { name: 'motto', required: false },
      ]),
    );
    expect(element(o).querySelectorAll('input')).toHaveLength(3);
    expect(o.component.elements.map((drawn) => [drawn.name, drawn.label])).toEqual([
      ['name', 'Name'],
      ['region', 'Region'],
      ['motto', 'Motto'],
    ]);
    expect([...element(o).querySelectorAll('[data-field]')].map((drawn) => drawn.getAttribute('data-field'))).toEqual([
      'name',
      'region',
      'motto',
    ]);

    o.component.form.controls['name'].setValue('West');
    await settled(o);
    expect(createButton(o).disabled).toBe(true);
    o.component.form.controls['region'].setValue('Coast');
    await settled(o);
    expect(createButton(o).disabled).toBe(false);

    const created = o.component.create();
    const request = o.http.expectOne('/console/api/create-office');
    expect(request.request.body).toEqual({ name: 'West', region: 'Coast' });
    request.flush({ id: 'o-1' });
    await created;
    expect(o.events.map(([what]) => what)).toEqual(['permissions read', 'close', 'opened']);
  });

  it('says a name another tenant carries in its own words and stays open, and any other refusal in the server words', async () => {
    const o = await open(descriptorOf([{ name: 'name', required: true }]));
    o.component.form.controls['name'].setValue('West');

    const taken = o.component.create();
    o.http
      .expectOne('/console/api/create-office')
      .flush({ message: 'conflict' }, { status: 409, statusText: 'Conflict' });
    await taken;
    await settled(o);
    expect(text(o, '[role="alert"]')).toBe('An office named West already exists. Choose another name.');
    expect(o.events).toEqual([]);
    expect(createButton(o).disabled).toBe(false);

    const refused = o.component.create();
    o.http
      .expectOne('/console/api/create-office')
      .flush(
        { message: 'No sign-in method is open to sign-up, so nobody creates an office now.' },
        { status: 403, statusText: 'Forbidden' },
      );
    await refused;
    await settled(o);
    expect(text(o, '[role="alert"]')).toBe('No sign-in method is open to sign-up, so nobody creates an office now.');
    expect(o.events).toEqual([]);
  });

  it('closes with nothing on Cancel', async () => {
    const o = await open(descriptorOf([{ name: 'name', required: true }]));
    (element(o).querySelector('mat-dialog-actions button[type="button"]') as HTMLButtonElement).click();
    expect(o.events).toEqual([['close', undefined]]);
    o.http.verify();
  });

  it('says the tenant as a tenant where the description gives no word', async () => {
    const o = await open(descriptorOf([{ name: 'name', required: true }], null));
    expect(text(o, 'h2')).toBe('Create your tenant');
    expect(texts(o, 'mat-dialog-actions button')).toEqual(['Cancel', 'Create tenant']);
    o.component.form.controls['name'].setValue('West');
    const taken = o.component.create();
    o.http.expectOne('/console/api/create-office').flush(null, { status: 409, statusText: 'Conflict' });
    await taken;
    expect(o.component.refusal()).toBe('A tenant named West already exists. Choose another name.');
  });
});

describe('openCreateTenantDialog', () => {
  it('opens the dialog with the description, as wide as the dialog it replaces', () => {
    const opened = {};
    const dialog = { open: vi.fn(() => opened) };
    const descriptor = descriptorOf([{ name: 'name', required: true }]);
    expect(openCreateTenantDialog(dialog as unknown as MatDialog, descriptor)).toBe(opened);
    expect(dialog.open).toHaveBeenCalledWith(CreateTenantDialogComponent, {
      width: '480px',
      maxWidth: 'calc(100vw - 32px)',
      data: descriptor,
    });
  });

  it('is refused where the description carries no create-your-tenant route', () => {
    const dialog = { open: vi.fn() };
    expect(() => openCreateTenantDialog(dialog as unknown as MatDialog, descriptorOf(undefined))).toThrow(
      "openCreateTenantDialog: the API description carries no create-your-tenant route (tenantCreation): this outlet does not serve it, or the application's tenants are not made by the people who sign up",
    );
    expect(dialog.open).not.toHaveBeenCalled();
  });
});
