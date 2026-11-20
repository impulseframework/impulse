import { NgTemplateOutlet } from '@angular/common';
import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  Injector,
  input,
  Type,
  untracked,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { MatExpansionModule, MatExpansionPanel } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, Router } from '@angular/router';
import { ListViewConfig, RecordData, RootConfig } from '@impulseframework/resource-angular/types';
import {
  openCreateDialog,
  ResourceCreateDialogComponent,
} from '../resource-create-dialog/resource-create-dialog.component';
import { ResourceListComponent } from '../resource-list/resource-list.component';
import { ResourceStore } from '../resource-store.service';

@Component({
  standalone: true,
  selector: 'impulse-resource-list-create',
  templateUrl: './resource-list-create.component.html',
  styleUrl: './resource-list-create.component.scss',
  imports: [
    MatExpansionModule,
    MatIconModule,
    MatTooltipModule,
    MatButtonModule,
    ResourceListComponent,
    NgTemplateOutlet,
  ],
  providers: [ResourceStore],
})
export class ResourceListCreateComponent {
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  compoundResourceComponent = input.required<Type<any>>();

  injector = inject(Injector);
  store = inject(ResourceStore);
  /**
   * The store of the row page this list sits under: the page's row and the count of the
   * methods that ran under it. Absent for a list that is a page of its own.
   */
  private pageStore = inject(ResourceStore, { skipSelf: true, optional: true });
  /** Set while the count moves for a method this list ran itself: it has already asked for its rows again. */
  private ranHere = false;
  listChild = viewChild<ResourceListComponent, ResourceListComponent>('list', { read: ResourceListComponent });

  route = inject(ActivatedRoute);
  router = inject(Router);
  private dialog = inject(MatDialog);
  /** The open dialog of this list's create; none while the create is not being filled. */
  private createDialog?: MatDialogRef<ResourceCreateDialogComponent, boolean>;
  parentId = input<string>();
  /**
   * The parent row a child list is related to by `parentRelation`. No default: a child
   * list draws its table only once the parent's key value is on hand, so it never asks
   * for an unfiltered first page of the whole child resource; a root list, or one with
   * no parent relation, draws at once.
   */
  parentData = input<RecordData | undefined>();
  searchParams = input<Record<string, string>[]>([]);
  resourceConfig = input<ListViewConfig>();
  isRootList = input<boolean>(true);

  childKey = computed(() => {
    if (!this.config().parentRelation) {
      return '';
    }
    return this.config().parentRelation?.childKey as string;
  });

  filter = computed(() => {
    const filters = [];
    if (this.childKey() !== '' && this.parentKey() !== '') {
      filters.push(`${this.childKey()}:eq:${this.parentKey()}`);
    }
    return filters.join(',');
  });

  parentKey = computed(() => {
    if (this.parentData() === undefined || this.childKey() === undefined) {
      return '';
    }
    const parent = this.config().parentRelation?.parentKey;
    return this.parentData()?.[parent] || '';
  });

  /** Whether the list may ask: at once when nothing relates it to a parent, else once the parent's key value is present. */
  listReady = computed(() => this.childKey() === '' || this.parentKey() !== '');

  config = computed(() => {
    const inputConfig = this.resourceConfig();
    if (inputConfig) {
      return inputConfig as ListViewConfig;
    }
    return this.rootConfig().parentConfig as ListViewConfig;
  });

  expPanel = viewChild<MatExpansionPanel, MatExpansionPanel>('expPanel', { read: MatExpansionPanel });

  rootConfig = computed(() => {
    return this.route.snapshot.data['config'] as RootConfig;
  });

  createConfig = computed(() => {
    const config = this.config();
    if (Object.keys(config.createConfig).length !== 0) {
      return config.createConfig;
    }
    return config;
  });

  /**
   * A collapsed list's add button was pressed: the create opens as the button above an
   * open list opens it, and the panel stays as it is.
   */
  createResource(event: MouseEvent): void {
    event.stopPropagation();
    this.openCreate();
  }

  /**
   * Opens the list's create in a dialog, titled with the create button's label. The
   * list draws no form on its page: adding a row is a button that opens a dialog, on
   * every list. Once a row is created the list reads its rows again, and a collapsed
   * panel opens so the new row is on the screen. A press while the dialog is open opens
   * no second one.
   */
  openCreate(): void {
    if (this.createDialog) {
      return;
    }
    const config = this.config();
    const opened = openCreateDialog(
      this.dialog,
      {
        label: config.createButtonLabel || 'Create',
        config: this.createConfig(),
        parentData: this.parentData() ?? {},
        loadCreatedResource: config.loadCreatedResource,
      },
      this.injector,
    );
    this.createDialog = opened;
    opened.afterClosed().subscribe((created) => {
      this.createDialog = undefined;
      if (!created) {
        return;
      }
      this.listChild()?.reloadListData();
      this.expPanel()?.open();
    });
  }

  /**
   * A method ran from this list. Under a row page it may have changed the row or
   * another of the row's lists (a role taken away is one fewer role the person holds),
   * so the page reads the row and its other lists again; a list that is a page of its
   * own has nothing above it to tell.
   */
  methodRan(): void {
    if (this.isRootList() || !this.pageStore) {
      return;
    }
    this.ranHere = true;
    this.pageStore.rowChanged();
  }

  constructor() {
    // The dialog is made under this list's injector: it closes with the list.
    inject(DestroyRef).onDestroy(() => this.createDialog?.close(false));
    // A method that ran against the page's row, or from another of its lists, reads this
    // list again. The first run only notes the count the list opened on.
    let seen = this.pageStore?.rowChanges() ?? 0;
    effect(() => {
      const count = this.pageStore?.rowChanges() ?? 0;
      if (count === seen) {
        return;
      }
      seen = count;
      if (this.ranHere) {
        this.ranHere = false;
        return;
      }
      if (!this.isRootList()) {
        untracked(() => this.listChild()?.reloadListData());
      }
    });
  }
}
