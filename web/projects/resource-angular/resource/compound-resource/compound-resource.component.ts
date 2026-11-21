import { Location } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  inject,
  Injector,
  input,
  OnInit,
  output,
  signal,
  Type,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { ActivatedRoute, Router, RouterModule } from '@angular/router';
import {
  ChildResourceConfig,
  ParentResourceConfig,
  RecordData,
  Resource,
  RESOURCE_META,
  RootConfig,
  writeResource,
} from '@impulseframework/resource-angular/types';
import {
  ActionAccessControlWrapperComponent,
  ActionButtonContext,
} from '../actions/action-button-smart/action-access-control-wrapper.component';
import { RpcButtonComponent } from '../actions/rpc-button/rpc-button.component';
import { ResourceArrayViewComponent } from '../resource-array-view/resource-array-view.component';
import { ResourceListCreateComponent } from '../resource-list-create/resource-list-create.component';
import { ResourceResolverComponent } from '../resource-resolver/resource-resolver.component';
import { ResourceStore } from '../resource-store.service';
import { ResourceViewComponent, viewUsesExpansionPanel } from '../resource-view/resource-view.component';
import { backFromRowPage } from '../row-page-back';
import { RowStoreDirective } from '../row-store.directive';

@Component({
  selector: 'compound-resource',
  templateUrl: './compound-resource.component.html',
  styleUrl: './compound-resource.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: true,
  imports: [
    MatIconModule,
    RouterModule,
    MatButtonModule,
    RpcButtonComponent,
    ResourceViewComponent,
    ResourceListCreateComponent,
    ResourceArrayViewComponent,
    ResourceResolverComponent,
    ActionAccessControlWrapperComponent,
    RouterModule,
    RowStoreDirective,
  ],
  providers: [ResourceStore],
})
/**
 * A row page. The page owns the row: its store reads the row once, the primary view
 * draws and writes through that same store, and the related configs (array views,
 * child lists, resolvers, RPC buttons) exist only once the row is present and stay
 * through a reload of it, so no child asks for anything from a row that has not
 * arrived, and after a save every child reads the saved values.
 */
export class CompoundResourceComponent implements OnInit {
  location = inject(Location);
  router = inject(Router);
  route = inject(ActivatedRoute);
  store = inject(ResourceStore);
  injector = inject(Injector);
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  componentRef: Type<any> = CompoundResourceComponent;
  resourceMeta = inject(RESOURCE_META);

  resourceConfig = input<ParentResourceConfig | ChildResourceConfig>();
  isArrayChild = input<boolean>(false);
  uuid = input.required<string>();
  parentData = input<RecordData>();

  rootConfig = computed(() => this.route.snapshot.data['config'] as RootConfig);

  emptyOneToOne = signal(false);
  missingRoot = input(false);
  resourceCreate = output();
  deleted = output<boolean>();
  navAfterDelete = input<boolean>(true);
  navAfterDeleteConsideringRoot = computed(() => {
    return this.navAfterDelete();
  });

  hasElements = computed(() => {
    const config = this.primaryConfig();
    return config && (config.type === 'ListView' || config.type === 'View') && config.elements.length > 0;
  });

  /**
   * The key of the row this page reads: the parent's value in `parentRelation.parentKey`
   * when the primary config relates the row to a parent, else the page's own `uuid`.
   * Resolved against the inherited parent row alone, never against the page's own row,
   * so the key holds still once the row lands (the row rarely carries the parent's field,
   * and a key that flipped to 'undefined' would idle the reader and take the page down).
   */
  primaryConfigParentId = computed(() => {
    const config = this.primaryConfig();
    if (config.type === 'View' || config.type === 'ListView') {
      const parentKey = config.parentRelation?.parentKey;
      const parent = this.parentData();
      if (parentKey !== '' && parent !== undefined) {
        return String(parent[parentKey]);
      }
    }
    return this.uuid();
  });

  primaryConfig = computed(() => {
    const config = this.resourceConfig();
    if (config) {
      return config;
    }
    return this.rootConfig().parentConfig;
  });

  /**
   * How the page reads its heading off its row: the config's rowTitle on the route's own
   * page, and undefined on every other page, which is headed by the config's title.
   */
  rowTitle = computed(() => {
    const config = this.primaryConfig();
    if (this.isArrayChild() || !this.isRootConfig() || (config.type !== 'ListView' && config.type !== 'View')) {
      return undefined;
    }
    return config.rowTitle;
  });

  /**
   * What the page is headed by: the row's own name where the config says how to read it
   * off the row (rowTitle), and nothing until the row has been read, since a heading that
   * changed under the reader would be a guess; the config's title otherwise.
   */
  title = computed(() => {
    if (this.isArrayChild()) {
      return '';
    }
    const config = this.primaryConfig();
    if (config.type === 'Component') {
      return '';
    }
    const rowTitle = this.rowTitle();
    if (rowTitle) {
      return this.store.rowPresent() ? rowTitle(this.store.viewData()) : '';
    }
    return config.title;
  });

  isRootConfig = computed(() => {
    return this.resourceConfig() === undefined;
  });

  /**
   * Whether the page has a title at all, by its config alone: one the config names, or
   * the row's own name, which is not there until the row has been read. What the page
   * keeps room for is settled by this and never by the title's text, so that nothing is
   * given room, or denied it, that changes once the row arrives.
   */
  titled = computed(() => {
    return this.rowTitle() !== undefined || this.title() !== '';
  });

  /**
   * Whether the page's own row view draws the page's title, in the row it shares with
   * its buttons: wherever the page has a title or is headed by its row's name, draws such
   * a view, and the view is not folded into a panel, which has a title of its own. The
   * view is drawn for every page that holds anything, a field or a part under the row, so
   * the view draws the title of a page of lists alone, having no field to show, and of a
   * page whose first part is a component of the application's. The page draws the title
   * itself in the three cases left: the view of its row is folded into a panel; the page
   * holds nothing, no field and no part under the row, so no view is drawn; the page's
   * configuration is not a row view's (an array's), which draws no view. A page with no
   * title (a row of an array, a component of the application's) draws none.
   *
   * Who draws the title is settled by the config alone, never by whether the row has been
   * read. A page headed by its row's name has no title until the row is there, and the
   * view's row waits for it: were the page to take the title's place meanwhile, it would
   * draw a header, and leave room above the row for it, that both go away once the row
   * arrives, and a page draws nothing that it then takes away.
   */
  viewDrawsTitle = computed(() => {
    const config = this.primaryConfig();
    if (!this.titled() || !(this.hasElements() || this.configs().length > 0)) {
      return false;
    }
    return (config.type === 'View' || config.type === 'ListView') && !viewUsesExpansionPanel(config, true);
  });

  configs = computed(() => {
    if (this.hasElements() && this.emptyOneToOne()) {
      return [];
    }

    if (this.isRootConfig()) {
      return this.rootConfig().relatedConfigs;
    }

    const config = this.primaryConfig();
    if (config.type === 'ListView' || config.type === 'View') {
      return config.relatedConfigs;
    }
    return [];
  });

  rpcConfigs = computed(() => {
    const isRootConfig = this.isRootConfig();
    const rootConfig = this.rootConfig();

    if (isRootConfig === undefined || rootConfig === undefined) {
      return [];
    }

    if (!isRootConfig || rootConfig.rpcConfigs === undefined) {
      return [];
    }

    return rootConfig.rpcConfigs?.map((config) => ({
      config,
      context: {
        actionType: 'rpc',
        shouldRender: config.shouldRender,
        resourceData: this.resolvedData(),
      } satisfies ActionButtonContext,
    }));
  });
  hasRpcConfigs = computed(() => !!this.rpcConfigs() && this.rpcConfigs()!.length > 0);

  /** The row the page's children read: the page's row once present, else the inherited parent row, else empty. */
  resolvedData = computed<RecordData>(() => {
    if (this.store.rowPresent()) {
      return this.store.viewData();
    }
    return this.parentData() ?? ({} as RecordData);
  });

  ngOnInit(): void {
    // The row page reads and writes the resource whose row it opens: the listed
    // resource, or its table when the list is a view declaring one (rowsOf).
    const primary = this.primaryConfig().primaryResource as Resource;
    const resource = writeResource(primary, this.resourceMeta(primary));
    const meta = this.resourceMeta(resource);

    if (meta) {
      this.store.resourceName.set(resource);
      this.store.resourceMeta.set(meta);
    }
    // A page that offers none of its row's methods asks the server nothing about them;
    // said here, before the row is first read.
    const config = this.primaryConfig();
    if (config.type === 'View' || config.type === 'ListView') {
      this.store.viewMethods.set(config.rowMethods !== false);
    }
  }

  constructor() {
    effect(() => {
      this.store.uuid.set(this.primaryConfigParentId());

      const c = this.primaryConfig();
      if (this.missingRoot()) {
        return;
      }
      if (c.type === 'View' || c.type === 'ListView') {
        // The list component holds its own store and its own page; this store serves
        // the row the page is on, live by the config's word and the metadata's. The build is
        // idempotent: one reader, tracking the key.
        this.store.liveChoice.set(c.live);
        this.store.buildStoreViewData();
      }
    });
  }

  /**
   * Leaves the page as its Back button does, for a button of the application's own: back
   * by the history where the router came to the page from another page, and to the list
   * by the route where the page was opened by its address.
   */
  goBack(): void {
    backFromRowPage(this.router, this.location, this.route);
  }
}
