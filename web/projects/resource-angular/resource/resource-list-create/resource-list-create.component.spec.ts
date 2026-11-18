import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { Resource } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { listViewConfig, rootConfig } from '@impulseframework/resource-angular/types';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceListCreateComponent } from './resource-list-create.component';

describe('ResourceListCreateComponent', () => {
  let component: ResourceListCreateComponent;
  let fixture: ComponentFixture<ResourceListCreateComponent>;

  beforeEach(async () => {
    const squadrons = listViewConfig({ primaryResource: 'Squadrons' as Resource, elements: [], listColumns: [] });
    // The page's configuration comes from the route the router generator built for it.
    const page = rootConfig({ parentConfig: squadrons, routeData: { route: 'squadrons', hasViewRoute: true } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceListCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', squadrons);
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// A page that is more than one list: the list a page is built on draws the lists it
// names under it (`below`), each a list of its own that asks for its own rows.
describe("ResourceListCreateComponent, the lists below a page's list", () => {
  const ended = listViewConfig({
    title: 'Ended',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:true',
    showViewButton: false,
    elements: [],
    listColumns: [],
  });
  const heldNow = listViewConfig({
    title: 'Held right now',
    primaryResource: 'Squadrons' as Resource,
    filter: () => 'ended:eq:false',
    showViewButton: false,
    below: [ended],
    elements: [],
    listColumns: [],
  });

  async function draw(
    root: boolean,
  ): Promise<{ fixture: ComponentFixture<ResourceListCreateComponent>; urls: () => string[] }> {
    const transport = scriptedTransport({ status: 200, body: [] });
    const page = rootConfig({ parentConfig: heldNow, routeData: { route: 'squadrons', hasViewRoute: false } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: page }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(ResourceListCreateComponent);
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('isRootList', root);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    return { fixture, urls: () => transport.requests.map((request) => decodeURIComponent(request.url)) };
  }
  const titles = (fixture: ComponentFixture<unknown>): (string | undefined)[] =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('impulse-resource-list h1.title')).map((title) =>
      title.textContent?.trim(),
    );

  it('draws the list the page is built on, then each list named below it, in order', async () => {
    const { fixture } = await draw(true);
    expect(titles(fixture)).toEqual(['Held right now', 'Ended']);
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('impulse-resource-list-create.below')).toHaveLength(
      1,
    );
    expect(fixture.componentInstance.below()).toEqual([ended]);
  });

  it('draws none under a list that is not the one its page is built on', async () => {
    const { fixture } = await draw(false);
    expect(titles(fixture)).toEqual(['Held right now']);
    expect(fixture.componentInstance.below()).toEqual([]);
  });

  it("tells the lists below to read again once a method ran from the page's own list", async () => {
    const { fixture } = await draw(true);
    const before = fixture.componentInstance.store.rowChanges();
    fixture.componentInstance.methodRan();
    expect(fixture.componentInstance.store.rowChanges()).toBe(before + 1);
  });
});
