import { NgComponentOutlet } from '@angular/common';
import {
  Component,
  computed,
  effect,
  inject,
  Injector,
  input,
  OnInit,
  output,
  Signal,
  signal,
  Type,
  untracked,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatExpansionModule, MatExpansionPanel } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  ArrayConfig,
  ColumnConfig,
  RecordData,
  RESOURCE_META,
  ViewConfig,
} from '@impulseframework/resource-angular/types';
import { ListPermission, readMode } from '@impulseframework/resource';
import {
  ActionAccessControlWrapperComponent,
  ActionButtonContext,
} from '../actions/action-button-smart/action-access-control-wrapper.component';
import { ResourceCreateComponent } from '../resource-create/resource-create.component';
import { listEmptyMessage, listSorts, pageLabel, sameSorts } from '../resource-list/list-request';
import { ResourceStore } from '../resource-store.service';
import { listableFieldsOf } from '../search-box/search-fields';

/**
 * The children of one row, each drawn as a form. How the children are read is the
 * listed resource's descriptor's statement alone, its declared maximum page size
 * (readMode): with none the children are read whole in one request; with one the view
 * holds one server page at the descriptor's default size, Previous and Next by the
 * server's cursors, and never gathers every child.
 */
@Component({
  selector: 'impulse-resource-array-view',
  templateUrl: './resource-array-view.component.html',
  styleUrl: './resource-array-view.component.scss',
  imports: [
    MatButtonModule,
    MatExpansionModule,
    MatIconModule,
    MatTooltipModule,
    ResourceCreateComponent,
    ActionAccessControlWrapperComponent,
    NgComponentOutlet,
  ],
  providers: [ResourceStore],
})
export class ResourceArrayViewComponent implements OnInit {
  resourceMeta = inject(RESOURCE_META);
  store = inject(ResourceStore);
  /** The store of the row page the children sit under: the count of the methods that ran against the row or under it. */
  private pageStore = inject(ResourceStore, { skipSelf: true, optional: true });
  injector = inject(Injector);
  private auth = inject(AuthService);
  /** The digest's List field set for any resource (listableFieldsOf): the children's own. */
  private readonly listableOf = listableFieldsOf(this.auth);

  resourceConfig = input.required<ArrayConfig>();
  /**
   * The row the children belong to, whose values the config's listFilter is written
   * against. No default: until the compound page hands the row over, the view asks for
   * nothing, so no filter ever names a value the row does not have yet.
   */
  parentData = input<RecordData | undefined>();
  expPanel = viewChild<MatExpansionPanel, MatExpansionPanel>('expPanel', { read: MatExpansionPanel });

  emptyOneToOne = output<boolean>();

  createMode = signal(false);
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  compoundResourceComponent = input.required<Type<any>>();

  /** The listed resource's generated descriptor; a resource the API does not describe has none and is read whole. */
  private descriptor = computed(() => {
    const config = this.resourceConfig();
    return this.store.client.descriptor.resources[config.connectorResource || config.primaryResource];
  });

  /**
   * The sorts the children are asked in (listSorts): the config's sorts as the server
   * answers them for this caller, following the digest.
   */
  private askedSorts = computed(
    () => {
      const config = this.resourceConfig();
      const listed = config.connectorResource || config.primaryResource;
      return listSorts(
        config.sorts,
        this.resourceMeta(listed),
        this.auth.fieldPermissionStates({ resource: listed, permission: ListPermission }),
        this.descriptor(),
        this.listableOf(listed),
      );
    },
    { equal: sameSorts },
  );

  /** Whether the children are paged: the listed resource declares a maximum page size. */
  paged = computed(() => {
    const descriptor = this.descriptor();
    return descriptor !== undefined && readMode(descriptor) === 'paged';
  });

  /** The children shown: the server's page on a paged source, the whole list otherwise. */
  rows = computed<RecordData[]>(() => (this.paged() ? this.store.page().rows : this.store.listData()));

  /** Whether the children have arrived. */
  resolved = computed(() =>
    this.paged() ? this.store.pageStatus() === 'resolved' : this.store.listStatus() === 'resolved',
  );

  /** What the read of the children ended in when the server refused it or it failed, else undefined. */
  private unreadError = computed<unknown>(() => (this.paged() ? this.store.pageError() : this.store.listError()));

  /**
   * What an empty view says: in the page's own words, that the person may not see the
   * children or that they could not be loaded, when the server refused the read or it
   * failed; else the plain text. What the server said is written to the console, never to
   * the screen.
   */
  emptyMessage = computed(() => listEmptyMessage(this.unreadError(), false));

  /** The pager's label under a paged view: this page's range of the total. */
  pagerLabel = computed(() => {
    const page = this.store.page();
    return pageLabel(page.offset, page.rows.length, page.total);
  });

  showCreateButton = computed(() => {
    const list = this.rows();
    const resourceConfig = this.resourceConfig();
    const iteratedConfig = resourceConfig.iteratedConfig;
    if (list && resourceConfig?.viewType === 'OneToOne') {
      this.emptyOneToOne.emit(list.length === 0);
      return list.length === 0;
    }
    if ('createTitle' in iteratedConfig && iteratedConfig.createTitle !== '') {
      return true;
    }
    return false;
  });

  createActionConfig: Signal<ActionButtonContext | undefined> = computed(() => {
    const config = this.resourceConfig();
    const showCreate = this.showCreateButton();
    const meta = this.store.resourceMeta();

    if (config === undefined || showCreate === undefined || meta === undefined) {
      return undefined;
    }

    const parent = this.parentData() ?? ({} as RecordData);
    return {
      actionType: 'create',
      meta: this.store.resourceMeta(),
      shouldRender: () => (showCreate && config.shouldRenderActions?.create?.(parent)) ?? false,
      resourceData: parent,
    };
  });

  createConfig = computed(() => {
    const config = this.resourceConfig();
    if (config.createConfig && Object.keys(config.createConfig).length !== 0) {
      return config.createConfig as ViewConfig;
    }
    if (config.iteratedConfig && Object.keys(config.iteratedConfig).length !== 0) {
      return config.iteratedConfig as ViewConfig;
    }
    return config;
  });

  resourceListRoute = computed(() => {
    const config = this.resourceConfig();
    const meta = this.resourceMeta(config.connectorResource || config.primaryResource);
    return meta.route;
  });

  setCreateMode(value: boolean): void {
    this.createMode.set(value);
    if (value && this.expPanel && this.expPanel()?.closed) {
      this.expPanel()?.open();
    }
  }

  ngOnInit(): void {
    this.store.resourceName.set(this.resourceConfig().primaryResource);
    const meta = this.resourceMeta(this.resourceConfig().primaryResource);
    this.store.resourceMeta.set(meta);
    this.store.overrideRoute.set(this.resourceListRoute());
    const columnArray = [];
    if (this.resourceConfig().connectorField !== '') {
      columnArray.push({ id: this.resourceConfig().connectorField });
    } else {
      columnArray.push({ id: 'id' } as ColumnConfig);
    }
    this.store.listColumns.set(columnArray);
    this.store.sorts.set(this.askedSorts());
  }

  constructor() {
    // What the server said of children it refused, or how the read failed, is for whoever
    // looks into it: it is written to the console, once, with the resource it belongs to.
    // The line's words are a constant, and the console puts the resource's name where they
    // keep its place (%s): a name is never itself read as words to print.
    effect(() => {
      const error = this.unreadError();
      if (error) {
        const resource = untracked(() => this.resourceConfig().primaryResource);
        console.error('%s: the list could not be loaded:', resource, error);
      }
    });

    // A method that ran against the page's row, or from one of its lists, may have
    // changed these children: they are read again. The first run only notes the count
    // the view opened on.
    let seen = this.pageStore?.rowChanges() ?? 0;
    effect(() => {
      const count = this.pageStore?.rowChanges() ?? 0;
      if (count === seen) {
        return;
      }
      seen = count;
      untracked(() => {
        if (this.paged()) {
          this.store.reloadPage();
        } else {
          this.store.reloadListData();
        }
      });
    });

    // The store asks what the children are asked in now: the sorts follow the digest (askedSorts).
    effect(() => {
      this.store.sorts.set(this.askedSorts());
    });

    // The filter follows the parent row: nothing until the row is on hand, then the
    // configured listFilter over it, and the reader built once. A later change of the
    // row (a reload after a save) re-runs this, sets the same filter string, and issues
    // nothing; a changed filter asks again through the reader's own params.
    effect(() => {
      const parentData = this.parentData();
      const resourceConfig = this.resourceConfig();
      if (parentData === undefined || !resourceConfig || !('listFilter' in resourceConfig)) {
        return;
      }

      this.store.filter.set(resourceConfig.listFilter(parentData));
      this.store.disableCacheForFilterPii.set(resourceConfig.disableCacheForFilterPii);
      // A paged source: the store holds one server page and the pager turns it. A whole
      // source: one request, every child.
      if (this.paged()) {
        this.store.buildStorePage();
      } else {
        this.store.buildStoreListData();
      }
    });
  }

  createResource(event: MouseEvent): void {
    event.stopPropagation();
    const expPanel = this.expPanel();

    if (!expPanel) {
      this.setCreateMode(true);
      return;
    }
    this.setCreateMode(true);
    if (expPanel.closed) {
      expPanel.open();
    }
  }

  onResourceDeleted(): void {
    this.setCreateMode(false);
  }

  onCreateCompleted(): void {
    if (this.paged()) {
      this.store.reloadPage();
    } else {
      this.store.reloadListData();
    }
    this.setCreateMode(false);
  }
}
