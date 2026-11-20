// Demonstrates: pii, field.object.
import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { methodMeta } from '@app/service/zz_gen_methods';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { METHOD_META, RESOURCE_DOMAIN, RESOURCE_META } from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { distressCallsConfig } from './distressCalls.config';

// The call log is the library's list over DistressCalls, drawn for each person by the
// fields their grant covers. The server as the spec plays it answers two people in
// Anvil. The Dispatcher's grant covers every field a call is read by, the caller's
// contact among them: their log draws it. The Cadet's covers the summary, the severity
// and the case number of the calls they filed: their log draws those three columns and
// asks the server for no other, so the caller's contact neither shows nor travels; and
// the Cadet, who may file a call, is offered Create where the Dispatcher is not.

const anvil = 'a1111111-1111-4111-8111-111111111111';

/** A digest that grants `permissions` on the calls and on each of `fields`. */
function grants(permissions: Record<string, string>, fields: string[]): PermissionDigest {
  return Object.fromEntries(
    ['DistressCalls', ...fields.map((f) => `DistressCalls.${f}`)].map((name) => [name, permissions]),
  ) as PermissionDigest;
}

const dispatcher = grants({ List: 'granted', Read: 'granted' }, [
  'sectorId',
  'summary',
  'severity',
  'callerContact',
  'caseNumber',
  'filedByUserId',
  'filedByName',
  'position',
]);

// The Cadet reads the calls they filed (a condition on the rows) and files a call by two inputs.
const cadet: PermissionDigest = {
  ...grants({ List: 'conditional', Read: 'conditional' }, ['caseNumber']),
  ...grants({ List: 'conditional', Read: 'conditional', Create: 'granted' }, ['summary', 'severity']),
};

describe('the call log', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const logPath = `${environment.apiUrl}/sectors/${anvil}/distress-calls`;

  /** Builds the list the page's route loads, for the person whose digest and rows are given. */
  const create = async (digest: PermissionDigest, rows: unknown[]): Promise<void> => {
    transport = scriptedTransport((request: TransportRequest): TransportResponse => {
      const url = new URL(request.url, 'http://lodestar.test');
      switch (url.pathname) {
        case `${environment.apiUrl}/permission-digest`:
          return { status: 200, body: url.searchParams.has('domain') ? digest : {} };
        case logPath:
          return { status: 200, body: rows, headers: { 'total-count': String(rows.length) } };
        default:
          return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
      }
    });
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        { provide: METHOD_META, useValue: methodMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(anvil as Domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: distressCallsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(anvil as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', distressCallsConfig.parentConfig);
    // The page mounts the list with its create button on, for whoever may create.
    fixture.componentRef.setInput('hideCreateButton', false);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    for (let i = 0; i < 50 && fixture.componentInstance.store.pageStatus() !== 'resolved'; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const headers = (): (string | undefined)[] =>
    Array.from(element().querySelectorAll('th .col-header')).map((h) => h.textContent?.trim());
  const cells = (): string[][] =>
    Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim() ?? ''),
    );
  const columnsAsked = (): string[] => {
    const asked = transport.requests.find((request) => request.url.startsWith(`${logPath}?`));
    return (new URL(asked?.url ?? '', 'http://lodestar.test').searchParams.get('columns') ?? '').split(',').sort();
  };

  it("draws the Dispatcher every field a call is read by, the caller's contact and the position among them", async () => {
    await create(dispatcher, [
      {
        id: 'd-1',
        caseNumber: 'DC-0042',
        summary: 'Hull breach off the Anvil relay',
        severity: 4,
        callerContact: 'relay desk, channel 7',
        filedByName: 'Cadet Orrin',
        position: { type: 'Point', coordinates: [12.5, -4] },
      },
      { id: 'd-2', caseNumber: 'DC-0043', summary: 'Lost beacon', severity: 1, filedByName: 'Marshal Greer' },
    ]);

    expect(element().querySelector('h1.title')?.textContent?.trim()).toBe('Call log (2)');
    expect(headers()).toEqual(['Case', 'Summary', 'Severity', 'Caller contact', 'Filed by', 'Position']);
    expect(cells()).toEqual([
      [
        'DC-0042',
        'Hull breach off the Anvil relay',
        '4',
        'relay desk, channel 7',
        'Cadet Orrin',
        // The position in words, latitude first, and never the GeoJSON the row carries.
        '4° S, 12.5° E',
      ],
      ['DC-0043', 'Lost beacon', '1', '', 'Marshal Greer', ''],
    ]);
    expect(columnsAsked()).toEqual([
      'callerContact',
      'caseNumber',
      'filedByName',
      'id',
      'position',
      'severity',
      'summary',
    ]);
    // The Dispatcher reads the log and files nothing.
    expect(element().querySelector('.create-button')).toBeNull();
  });

  it('draws the Cadet the three columns their grant covers, asks for no other, and offers them Create', async () => {
    await create(cadet, [
      { id: 'd-1', caseNumber: 'DC-0042', summary: 'Hull breach off the Anvil relay', severity: 4 },
    ]);

    expect(headers()).toEqual(['Case', 'Summary', 'Severity']);
    expect(cells()).toEqual([['DC-0042', 'Hull breach off the Anvil relay', '4']]);
    // The caller's contact is no column, and the request names it nowhere.
    expect(columnsAsked()).toEqual(['caseNumber', 'id', 'severity', 'summary']);
    expect(transport.requests.some((request) => request.url.includes('callerContact'))).toBe(false);
    expect(element().querySelector('.create-button')?.textContent?.trim()).toContain('Create');
  });

  it('says so when no call has been filed in the sector', async () => {
    await create(dispatcher, []);

    expect(element().querySelector('tr.no-records-row')?.textContent?.trim()).toBe(
      'No distress call has been filed in this sector.',
    );
  });
});
