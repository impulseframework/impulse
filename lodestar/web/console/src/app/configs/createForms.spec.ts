import { TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { Clients, ShipClasses } from '@app/service/zz_gen_constants';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { ResourceCreateComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ListViewConfig, RESOURCE_META, RootConfig } from '@impulseframework/resource-angular/types';
import { environment } from '@env';
import { clientsConfig } from './clients.config';
import { shipClassesConfig } from './shipClasses.config';

// A create form starts a yes or no field its resource requires unchecked, with nothing
// written in the page's config, and writes nothing about it to the console. The two forms
// of the console that hold such a field say nothing of where it starts: a new client is
// not trusted and a new ship class is not hardened until the form says so, which is what
// an empty box reads as.

function server(request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  switch (url.pathname) {
    case `${environment.apiUrl}/permission-digest`:
      return {
        status: 200,
        body: { Clients: { Create: 'granted', List: 'granted' }, ShipClasses: { Create: 'granted', List: 'granted' } },
      };
    case `${environment.apiUrl}/user-domains`:
      return { status: 200, body: [] };
    default:
      return { status: 200, body: [], headers: { 'total-count': '0' } };
  }
}

describe("the console's create forms", () => {
  const forms: [string, RootConfig, string][] = [
    ['Clients', clientsConfig, Clients.fieldName.trusted],
    ['Ship classes', shipClassesConfig, ShipClasses.fieldName.hardened],
  ];

  afterEach(() => {
    vi.restoreAllMocks();
  });

  for (const [page, config, yesOrNo] of forms) {
    it(`${page}: the yes or no field starts unchecked, and the form writes nothing to the console`, async () => {
      const written: string[] = [];
      vi.spyOn(console, 'error').mockImplementation((...said: unknown[]) => {
        written.push(said.map(String).join(' '));
      });
      const transport = scriptedTransport(server);
      await TestBed.configureTestingModule({
        imports: [ResourceCreateComponent],
        providers: [
          provideResourceTesting({
            transport,
            client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }),
          }),
          { provide: RESOURCE_META, useValue: resourceMeta },
          { provide: ActivatedRoute, useValue: { snapshot: { data: { config }, params: {}, queryParams: {} } } },
        ],
      }).compileComponents();
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest();
      const fixture = TestBed.createComponent(ResourceCreateComponent);
      fixture.componentRef.setInput('resourceConfig', config.parentConfig as ListViewConfig);
      fixture.detectChanges();

      // Both at once, so that a form that fails says what it started the field at and what it wrote.
      expect({
        startsAt: fixture.componentInstance.form().get(yesOrNo)?.value as unknown,
        written,
      }).toEqual({ startsAt: false, written: [] });
    });
  }
});
