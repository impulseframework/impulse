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
import { ActivatedRoute, RouterModule } from '@angular/router';
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
import { ResourceViewComponent } from '../resource-view/resource-view.component';
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

  title = computed(() => {
    if (this.isArrayChild()) {
      return '';
    }
    const config = this.primaryConfig();
    if (config.type !== 'Component') {
      return config.title;
    }
    return '';
  });

  isRootConfig = computed(() => {
    return this.resourceConfig() === undefined;
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

  goBack(): void {
    this.location.back();
  }
}
