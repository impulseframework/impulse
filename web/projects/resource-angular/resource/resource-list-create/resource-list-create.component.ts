import { NgTemplateOutlet } from '@angular/common';
import { Component, computed, effect, inject, Injector, input, signal, Type, untracked, viewChild } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatExpansionModule, MatExpansionPanel } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, Router } from '@angular/router';
import { ListViewConfig, RecordData, RootConfig } from '@impulseframework/resource-angular/types';
import { ResourceCreateComponent } from '../resource-create/resource-create.component';
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
    ResourceCreateComponent,
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
  create = signal<boolean>(false);
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

  createLinkType = computed(() => {
    return this.config().loadCreatedResource && this.create();
  });

  createConfig = computed(() => {
    const config = this.config();
    if (Object.keys(config.createConfig).length !== 0) {
      return config.createConfig;
    }
    return config;
  });

  createResource(event: MouseEvent): void {
    event.stopPropagation();
    this.create.set(true);

    if (this.expPanel() && this.expPanel()?.closed) {
      this.expPanel()?.open();
    }
  }

  makeCreatePatches(): void {
    if (this.create()) {
      this.listChild()?.reloadListData();
      this.create.set(false);
    }
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
