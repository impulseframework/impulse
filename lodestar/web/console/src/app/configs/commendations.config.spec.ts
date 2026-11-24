import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { CompoundResourceComponent, ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { RESOURCE_META } from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { commendationsConfig } from './commendations.config';

// The commendations list names each cited pilot as the registry does. A citation holds
// the pilot's id, which nobody reads: the column shows the pilot's name, which the server
// reads beside the id with the page of citations, sorts and filters by it, and never shows
// the id. Nothing reads the registry.

const pax = '30000000-0000-4000-8000-000000000004';
const vela = '30000000-0000-4000-8000-000000000005';

const citations = [
  {
    id: 'k-1',
    pilotId: pax,
    pilotName: 'Pilot Pax',
    citation: 'Brought the Kestrel home on one engine.',
    awardedByName: 'adjutant',
  },
  {
    id: 'k-2',
    pilotId: vela,
    pilotName: 'Veteran Vela',
    citation: 'Held the line at the Cinder Verge.',
    awardedByName: 'adjutant',
  },
];

function server(request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  switch (url.pathname) {
    case `${environment.apiUrl}/permission-digest`:
      return {
        status: 200,
        body: { Commendations: { List: 'granted', Read: 'granted' }, Pilots: { List: 'granted', Read: 'granted' } },
      };
    case `${environment.apiUrl}/user-domains`:
      return { status: 200, body: [] };
    case `${environment.apiUrl}/features`:
      return { status: 200, body: { commendations: true } };
    case `${environment.apiUrl}/commendations`:
      return { status: 200, body: citations, headers: { 'total-count': '2' } };
    default:
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  }
}

describe('the commendations list', () => {
  let fixture: ComponentFixture<ResourceListComponent>;
  let transport: ScriptedTransport;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null | undefined): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

  /** The cells under a header, a row after another; none while the list has no such column drawn. */
  const column = (header: string): string[] => {
    const headers = Array.from(element().querySelectorAll('thead th'));
    const index = headers.findIndex((th) => text(th.querySelector('.col-header') ?? th) === header);
    if (index < 0) {
      return [];
    }

    return Array.from(element().querySelectorAll('tr.impulse-row')).map((row) =>
      text(row.querySelectorAll('td')[index]),
    );
  };

  beforeEach(async () => {
    transport = scriptedTransport(server);
    await TestBed.configureTestingModule({
      imports: [ResourceListComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        { provide: RESOURCE_META, useValue: resourceMeta },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: commendationsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', commendationsConfig.parentConfig);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    // The page's rows land with the names its Pilot column shows.
    const drawn = (): boolean => column('Pilot').some((cell) => cell.includes('Pax'));
    for (let i = 0; i < 200 && !drawn(); i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      fixture.detectChanges();
    }
  });

  it('names each pilot as the registry does, never by an id', () => {
    expect(column('Pilot')).toEqual(['Pilot Pax', 'Veteran Vela']);
    expect(text(element())).not.toContain('30000000-');
  });

  it("asks for the pilot's name beside the id with the page of citations, and reads no registry", () => {
    const lists = transport.requests.filter((request) =>
      request.url.startsWith(`${environment.apiUrl}/commendations?`),
    );
    expect(lists.length).toBe(1);
    const asked = new URL(lists[0]?.url ?? '', 'http://lodestar.test').searchParams;
    expect(asked.get('columns')?.split(',')).toEqual(expect.arrayContaining(['pilotId', 'pilotName']));
    expect(transport.requests.some((request) => request.url.startsWith(`${environment.apiUrl}/pilots`))).toBe(false);
  });

  it("sorts the citations by the pilot's name from the Pilot heading", async () => {
    const heading = Array.from(element().querySelectorAll<HTMLElement>('th button.header-label')).find(
      (button) => text(button) === 'Pilot',
    );
    expect(heading).toBeDefined();
    heading?.click();
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    const last = transport.requests
      .filter((request) => request.url.startsWith(`${environment.apiUrl}/commendations?`))
      .at(-1);
    expect(new URL(last?.url ?? '', 'http://lodestar.test').searchParams.get('sort')).toBe('pilotName:asc');
  });
});
