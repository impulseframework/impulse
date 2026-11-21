import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Resource, ResourceMeta } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { arrayConfig, RESOURCE_META, viewConfig } from '@impulseframework/resource-angular/types';

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
