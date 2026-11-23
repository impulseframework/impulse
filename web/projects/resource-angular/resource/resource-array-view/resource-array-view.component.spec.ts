import { ComponentFixture, TestBed } from '@angular/core/testing';
import {
  ClientBase,
  PermissionDigest,
  Resource,
  ResourceMeta,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { arrayConfig, FieldName, RESOURCE_META, viewConfig } from '@impulseframework/resource-angular/types';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceArrayViewComponent } from './resource-array-view.component';

describe('ResourceArrayViewComponent', () => {
  let component: ResourceArrayViewComponent;
  let fixture: ComponentFixture<ResourceArrayViewComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceArrayViewComponent],
      providers: [provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) })],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceArrayViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput(
      'resourceConfig',
      arrayConfig({
        primaryResource: 'Squadrons' as Resource,
        iteratedConfig: viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [] }),
        listFilter: (row: { id: string }): string => `id:ne:${row.id}`,
      }),
    );
    // The children belong to a row the page hands over; the view asks nothing without it.
    fixture.componentRef.setInput('parentData', { id: 'sq-1' });
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

describe('ResourceArrayViewComponent, where the children could not be read', () => {
  const cases: { name: string; status: number; message: string; want: string }[] = [
    {
      name: 'says in its own words that the person may not see them, where the server refused the read',
      status: 403,
      message: 'scope (global), user (3f2a9c1e-7b64-4d1a-9e0c-5a8d2b7f6c13) does not have (List) on [Squadrons]',
      want: 'You do not have permission to see this list.',
    },
    {
      name: 'says in its own words that they could not be loaded, where the read failed',
      status: 500,
      message: 'spanner: code = "Unavailable", desc = "the session pool is closed"',
      want: 'This list could not be loaded.',
    },
  ];
  let consoleError: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
  });

  afterEach(() => {
    consoleError.mockRestore();
  });

  for (const tt of cases) {
    it(`${tt.name}, and writes what the server said to the console, never to the screen`, async () => {
      const transport = scriptedTransport({ status: tt.status, body: { message: tt.message } });
      const squadrons: ResourceMeta = {
        route: 'squadrons',
        fields: [
          { fieldName: 'id', required: true, primaryKey: { ordinalPosition: 1 }, displayType: 'string', isIndex: true },
        ],
      };
      await TestBed.configureTestingModule({
        imports: [ResourceArrayViewComponent],
        providers: [
          provideResourceTesting({ transport }),
          { provide: RESOURCE_META, useValue: (): ResourceMeta => squadrons },
        ],
      }).compileComponents();
      const fixture = TestBed.createComponent(ResourceArrayViewComponent);
      fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
      fixture.componentRef.setInput(
        'resourceConfig',
        arrayConfig({
          primaryResource: 'Squadrons' as Resource,
          iteratedConfig: viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [] }),
          listFilter: (row: { id: string }): string => `id:ne:${row.id}`,
        }),
      );
      fixture.componentRef.setInput('parentData', { id: 'sq-1' });
      fixture.detectChanges();
      const element = fixture.nativeElement as HTMLElement;
      // The read is asked and ends, refused or failed.
      for (let i = 0; i < 50 && !element.textContent?.includes(tt.want); i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
        fixture.detectChanges();
      }

      expect(transport.requests.map((request) => request.url.split('?')[0])).toContain('/api/squadrons');
      expect(element.textContent).toContain(tt.want);
      expect(element.textContent).not.toMatch(
        /scope \(|user \(|3f2a9c1e|does not have|\[Squadrons\]|spanner|session pool/,
      );
      // The line as a console prints it: the resource's name where the words keep its
      // place (%s), then the server's own message of the error written after them.
      const written = consoleError.mock.calls.map(([words, ...values]: unknown[]) => {
        const error = values.pop();
        const line = values.reduce<string>((text, value) => text.replace('%s', () => String(value)), String(words));
        return `${line} ${error instanceof Error ? error.message : ''}`;
      });
      expect(written).toEqual([`Squadrons: the list could not be loaded: ${tt.message}`]);
    });
  }
});

describe('ResourceArrayViewComponent, sorting its children by a field the digest lists as conditional', () => {
  // The config sorts the children by the name, which the digest lists as conditional. A
  // computed resource refuses that sort, so the children are asked by the key, the
  // squadrons declaring no order; a table sorts over what the caller may see.
  const digest: PermissionDigest = {
    Squadrons: { List: 'granted' },
    'Squadrons.id': { List: 'granted' },
    'Squadrons.name': { List: 'conditional' },
  };
  /** The sort of the view's first request for the children. */
  const firstSort = async (computed: boolean): Promise<string | null> => {
    const transport = scriptedTransport((request: TransportRequest): TransportResponse =>
      request.url.startsWith('/api/permission-digest') ? { status: 200, body: digest } : { status: 200, body: [] },
    );
    const squadrons: ResourceMeta = {
      route: 'squadrons',
      fields: [
        { fieldName: 'id', required: true, primaryKey: { ordinalPosition: 1 }, displayType: 'string', isIndex: true },
        { fieldName: 'name', required: true, displayType: 'string', isIndex: false },
      ],
    };
    await TestBed.configureTestingModule({
      imports: [ResourceArrayViewComponent],
      providers: [
        provideResourceTesting({ transport }),
        {
          provide: RESOURCE_META,
          useValue: (): ResourceMeta => (computed ? { ...squadrons, computed: true } : squadrons),
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    const fixture = TestBed.createComponent(ResourceArrayViewComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput(
      'resourceConfig',
      arrayConfig({
        primaryResource: 'Squadrons' as Resource,
        iteratedConfig: viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [] }),
        listFilter: (row: { id: string }): string => `id:ne:${row.id}`,
        sorts: [{ field: 'name' as FieldName, direction: 'asc' }],
      }),
    );
    fixture.componentRef.setInput('parentData', { id: 'sq-1' });
    fixture.detectChanges();
    const children = (): TransportRequest[] =>
      transport.requests.filter((request) => request.url.split('?')[0] === '/api/squadrons');
    for (let i = 0; i < 50 && children().length === 0; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      fixture.detectChanges();
    }
    const first = children()[0];
    return first ? new URL(first.url, 'http://view.test').searchParams.get('sort') : null;
  };

  it('over a computed resource with no declared order, the key ascending', async () => {
    expect(await firstSort(true)).toBe('id:asc');
  });

  it('over a table, the sort by the name', async () => {
    expect(await firstSort(false)).toBe('name:asc');
  });
});
