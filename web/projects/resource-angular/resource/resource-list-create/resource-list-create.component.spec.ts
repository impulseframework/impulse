import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ActivatedRoute } from '@angular/router';
import { Resource } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  ListViewConfig,
  listViewConfig,
  ListViewConfigOptions,
  rootConfig,
} from '@impulseframework/resource-angular/types';
import { Subject } from 'rxjs';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceCreateDialogComponent } from '../resource-create-dialog/resource-create-dialog.component';
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

// Where a list's create opens: on the page above the list, or, for a list whose config
// says createInDialog, in a dialog titled with the create button's label. The dialog's
// answer is what the list acts on: a created row is read with the list's rows, and a
// canceled dialog asks nothing.
describe('ResourceListCreateComponent, where its create opens', () => {
  let component: ResourceListCreateComponent;
  let fixture: ComponentFixture<ResourceListCreateComponent>;
  let open: ReturnType<typeof vi.spyOn>;
  /** The answers of the dialogs opened so far, each given by the test. */
  let answers: Subject<boolean | undefined>[];

  const page = (): HTMLElement => fixture.nativeElement as HTMLElement;

  async function draw(config: ListViewConfig): Promise<void> {
    const root = rootConfig({ parentConfig: config, routeData: { route: 'squadrons', hasViewRoute: true } });
    await TestBed.configureTestingModule({
      imports: [ResourceListCreateComponent],
      providers: [
        provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) }),
        // A collapsed list's panel turns its animation off, which needs the animations there.
        provideNoopAnimations(),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { config: root }, params: {}, queryParams: {} } } },
      ],
    }).compileComponents();
    answers = [];
    open = vi.spyOn(TestBed.inject(MatDialog), 'open').mockImplementation(() => {
      const answer = new Subject<boolean | undefined>();
      answers.push(answer);
      return { afterClosed: () => answer.asObservable(), close: () => answer.next(false) } as unknown as MatDialogRef<
        ResourceCreateDialogComponent,
        boolean
      >;
    });

    fixture = TestBed.createComponent(ResourceListCreateComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput('resourceConfig', config);
    fixture.detectChanges();
  }

  const squadrons = (options: Partial<ListViewConfigOptions> = {}): ListViewConfig =>
    listViewConfig({
      title: 'Squadrons',
      createTitle: 'Squadron',
      createButtonLabel: 'Add a squadron',
      primaryResource: 'Squadrons' as Resource,
      elements: [],
      listColumns: [],
      ...options,
    });

  it('draws the form on the page, above the list, and opens no dialog', async () => {
    await draw(squadrons());

    component.listChild()?.createMode.emit(true);
    fixture.detectChanges();

    expect(open).not.toHaveBeenCalled();
    expect(component.create()).toBe(true);
    expect(page().querySelector('impulse-resource-create')).not.toBeNull();
  });

  it("opens the create in a dialog under the button's label where the config says createInDialog, and draws no form on the page", async () => {
    const config = squadrons({ createInDialog: true });
    await draw(config);

    component.listChild()?.createMode.emit(true);
    fixture.detectChanges();

    expect(open).toHaveBeenCalledTimes(1);
    expect(open).toHaveBeenCalledWith(ResourceCreateDialogComponent, {
      data: { label: 'Add a squadron', config, parentData: {}, loadCreatedResource: false },
      injector: component.injector,
      delayFocusTrap: false,
      disableClose: true,
    });
    expect(component.create()).toBe(false);
    expect(page().querySelector('impulse-resource-create')).toBeNull();

    // A second press while the dialog is open opens no second one.
    component.listChild()?.createMode.emit(true);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('reads its rows again once the dialog created a row, and asks nothing when it was canceled', async () => {
    await draw(squadrons({ createInDialog: true }));
    const list = component.listChild();
    if (!list) {
      throw new Error('the list is not drawn');
    }
    const reload = vi.spyOn(list, 'reloadListData');

    list.createMode.emit(true);
    answers[0].next(false);
    expect(reload).not.toHaveBeenCalled();

    // The dialog is closed, so the button opens another.
    list.createMode.emit(true);
    expect(open).toHaveBeenCalledTimes(2);
    answers[1].next(true);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("opens the dialog from a collapsed list's own add button too, under the row the list sits under", async () => {
    await draw(squadrons({ createInDialog: true, collapsible: true }));
    fixture.componentRef.setInput('parentData', { id: 'wing-1' });
    fixture.detectChanges();

    component.createResource(new MouseEvent('click'));

    expect(open).toHaveBeenCalledTimes(1);
    expect((open.mock.calls[0][1] as { data: { parentData: unknown } }).data.parentData).toEqual({ id: 'wing-1' });
    expect(component.create()).toBe(false);
  });

  it('closes its dialog when the list leaves the page', async () => {
    await draw(squadrons({ createInDialog: true }));
    component.listChild()?.createMode.emit(true);
    const closed: (boolean | undefined)[] = [];
    answers[0].subscribe((answer) => closed.push(answer));

    fixture.destroy();

    expect(closed).toEqual([false]);
  });
});
