import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { CrewSectorUsers, HangarPasses } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import {
  CompoundResourceComponent,
  hyphenConcat,
  ResourceListComponent,
} from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ConfigElement,
  FieldElement,
  ListViewConfig,
  RESOURCE_DOMAIN,
  RESOURCE_META,
} from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { hangarsConfig } from './hangars.config';

// A hangar's passes name a person as the sector's people list shows them: by the name,
// and by the login they sign in under for an account that carries no name, which the
// server reads beside the id with the page of passes. The form's picker shows the name
// first and the login beside it.

const anvilID = '10000000-0000-4000-8000-000000000001';
const sector = `${environment.apiUrl}/sectors/${anvilID}`;
const dock = { id: 'h-1', name: 'Anvil Dock One', zone: 'north' };

const passes = [
  {
    id: 'p-1',
    hangarId: dock.id,
    userId: 'u-armorer',
    userName: 'Armorer Arlo',
    squadronId: null,
    groupId: null,
    levelId: 'run',
  },
  // A machine account carries no name: the sector's people list shows it by its login.
  {
    id: 'p-2',
    hangarId: dock.id,
    userId: 'u-droid',
    userName: 'droid-r7',
    squadronId: null,
    groupId: null,
    levelId: 'visit',
  },
];

function server(request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  const listed = { List: 'granted', Read: 'granted' };
  switch (url.pathname) {
    case `${environment.apiUrl}/permission-digest`:
      return {
        status: 200,
        body:
          url.searchParams.get('domain') === anvilID
            ? { HangarPasses: listed, CrewSectorUsers: listed, Squadrons: listed, CrewSectorGroups: listed }
            : {},
      };
    case `${environment.apiUrl}/user-domains`:
      return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
    case `${sector}/hangar-passes`:
      return { status: 200, body: passes, headers: { 'total-count': '2' } };
    default:
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  }
}

/** The Passes list under a hangar's page. */
const passesConfig = hangarsConfig.relatedConfigs[0] as ListViewConfig;

describe("a hangar's passes", () => {
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
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(anvilID as Domain) },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { data: { config: hangarsConfig }, params: {}, queryParams: {} } },
        },
      ],
    }).compileComponents();
    const client = TestBed.inject<ClientBase>(RESOURCE_CLIENT);
    await client.permissions.loadDomains();
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(anvilID as Domain);
    fixture = TestBed.createComponent(ResourceListComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', passesConfig);
    fixture.componentRef.setInput('relatedData', dock);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    // The page's rows land with the names its Person column shows.
    const drawn = (): boolean => column('Person').some((cell) => cell.includes('Arlo'));
    for (let i = 0; i < 200 && !drawn(); i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      fixture.detectChanges();
    }
  });

  it('names a person by name, and by the login where the account has no name', () => {
    expect(column('Person')).toEqual(['Armorer Arlo', 'droid-r7']);
  });

  it('asks for the names beside the ids with the page of passes, and reads no other list', () => {
    const lists = transport.requests.filter((request) => request.url.startsWith(`${sector}/hangar-passes?`));
    expect(lists.length).toBe(1);
    const asked = new URL(lists[0]?.url ?? '', 'http://lodestar.test').searchParams;
    expect(asked.get('columns')?.split(',')).toEqual(
      expect.arrayContaining(['userId', 'userName', 'squadronId', 'squadronName', 'groupId', 'groupName']),
    );
    for (const other of ['crew-sector-users', 'squadrons', 'crew-sector-groups']) {
      expect(transport.requests.some((request) => request.url.startsWith(`${sector}/${other}`))).toBe(false);
    }
  });

  it('offers each person in the picker the same way', () => {
    const fields = (elements: ConfigElement[]): FieldElement[] =>
      elements.flatMap((each) =>
        each.type === 'section' ? fields(each.children) : each.type === 'field' ? [each] : [],
      );
    const person = fields(passesConfig.elements).find((each) => each.name === HangarPasses.fieldName.userId);
    expect(person, 'the pass form has no Person field').toBeDefined();
    const shown = person?.enumeratedConfig.listDisplay ?? [];

    expect(shown).toEqual([CrewSectorUsers.fieldName.name, CrewSectorUsers.fieldName.username]);
    expect(person?.enumeratedConfig.viewDisplay).toEqual(shown);
    // The picker joins what it shows with the library's hyphen, which leaves out a field
    // the row has no value for.
    expect(hyphenConcat({ name: 'Armorer Arlo', username: 'armorer' }, ...shown)).toBe('Armorer Arlo - armorer');
    expect(hyphenConcat({ username: 'droid-r7' }, ...shown)).toBe('droid-r7');
  });
});
