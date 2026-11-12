import { Directive, input, Type } from '@angular/core';
import { TooltipPosition } from '@angular/material/tooltip';
import { FieldName, Method, PermissionScope, Resource } from './permissions';
import { NullBoolean } from './resource-meta';
import { ConcatFn, defaultEmptyFieldValue } from './resource-types';
import { ResourceValidatorFn } from './validators';

export interface FieldPointer {
  field: FieldName;
}

export interface MenuItem {
  label: string;
  route?: string[];
  children?: MenuItem[];
  /**
   * The permission the item's destination requires; a navigation that gates its items
   * with `impulseHasPermission` hides the item when the session user lacks it. Resource
   * routes fill it with the resource's List permission unless the config set one.
   */
  permission?: PermissionScope;
  /**
   * The feature flag the item's destination sits behind, by name (a member of the
   * generated `Feature` union); a navigation that gates its items with `impulseFeature`
   * hides the item while the flag is off. Resource routes fill it from the metadata's
   * `feature` when the resource is gated.
   */
  feature?: string;
}

export interface RouteResourceData {
  config: RootConfig;
  /** The permission the route requires — read by the AuthorizationGuard. */
  scope?: PermissionScope;
}

export type DataType = string | number | number[] | string[] | boolean | undefined | Date;
export type RecordData = Record<string, DataType | null>;

export type RPCDataType = string | number | string[] | number[] | boolean | Date;
export type RPCRecordData = Record<string, RPCDataType>;
export type RpcMethod = Record<string, DataType>;

export type ParentResourceConfig = ListViewConfig | ViewConfig | ArrayConfig;
export type ChildResourceConfig = ListViewConfig | ViewConfig | ComponentConfig | ArrayConfig;

export type ActionType = 'function' | 'link';

export type ListConcatFn =
  | 'space-concat'
  | 'hyphen-concat'
  | 'space-hyphen-concat'
  | 'hyphen-space-concat'
  | 'no-space-concat';
export type ConfigType = ChildResourceConfig;
export type FormatType = 'simpleSlashDateFormat';
export type RPCPlacement = 'inline' | 'end';

export type ColumnConfig = SingleColumnConfig | MultiColumnConfig;

/**
 * The display-row key an icon column's resolved icon rides under, beside the column's
 * own key, which holds the words. The list writes both while it builds the display row
 * and the grid reads both; a generated field name never carries a colon, so the key
 * collides with none. (The server's capability envelope rides a row the same way, under
 * its reserved property.)
 */
export function iconCellKey(id: string): string {
  return `${id}:icon`;
}

export interface ActionButtonConfig {
  label: string;
  icon: string;
  action?: (resource: { id: string }) => void;
  viewRoute?: string;
  /** The row field whose value follows viewRoute as the opened row's key; `id` when unset. */
  keyField?: FieldName;
  actionType?: ActionType;
  color?: string;
  disabledLabel?: string;
}
export interface ActionButtonConfigOptions {
  label: string;
  icon: string;
  action?: (resource: { id: string }) => void;
  viewRoute?: string;
  keyField?: FieldName;
  actionType?: ActionType;
  color?: string;
  disabledLabel?: string;
}

export function actionButtonConfig(config: ActionButtonConfigOptions): ActionButtonConfig {
  return {
    ...actionButtonConfigDefaults,
    ...config,
  } satisfies ActionButtonConfig;
}

export const actionButtonConfigDefaults = {
  label: '',
  icon: '',
  actionType: 'function' as ActionType,
  color: '',
  disabledLabel: '',
} satisfies ActionButtonConfig;

/**
 * Renders the column's field as a Material icon chosen by the field's value, with the
 * words for the value as the tooltip and the accessible name. The author declares the
 * map; the list resolves it while it builds the display row, as it applies every other
 * column's formatter, and the grid renders the result. No callback runs at render time,
 * and the column stays a field column: its header, sort and filter work on the field.
 */
export interface ColumnIconConfig {
  /**
   * The icon per value, keyed by the value's text: `true` and `false` for a boolean, the
   * key for an enumerated field, the number's text for a number. A value not listed shows
   * the cell's text instead of an icon.
   */
  icons: Record<string, string>;
  /** The words per value, the tooltip and the accessible name; a value not listed uses the cell's formatted text. */
  tooltips?: Record<string, string>;
  /** The icon, and its words, for a null or absent value; a null shows the column's empty text by default. */
  empty?: { icon: string; tooltip?: string };
}

/** What every column shares, whatever it renders. */
interface ColumnConfigBase {
  id: FieldName;
  header?: string;
  width?: number;
  resizable?: boolean;
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  valueGetter?: (data: any) => string;
  valueFormatter?: (data: any) => string;
  tooltipPosition?: TooltipPosition;
  viewRoute?: Resource;
  actionType?: ActionType;
  formatType?: FormatType | string;
  hidden?: boolean;
  emptyDataValue?: DataType;
  /**
   * Opt the column out of its filter control. A control is drawn only where the
   * generated metadata says the server filters the field (`filterable`); `false` removes
   * it there too. It never adds one where the server would refuse the filter.
   */
  filterable?: boolean;
  hideHeader?: boolean;
}

/** A column rendering its field's value as text. */
export interface FieldColumnConfig extends ColumnConfigBase {
  buttonConfig?: undefined;
  iconConfig?: undefined;
}

/** A column rendering an action button per row. */
export interface ButtonColumnConfig extends ColumnConfigBase {
  buttonConfig: ActionButtonConfig;
  iconConfig?: undefined;
}

/** A column rendering its field's value as an icon, by the declared map. */
export interface IconColumnConfig extends ColumnConfigBase {
  iconConfig: ColumnIconConfig;
  buttonConfig?: undefined;
}

/**
 * One column of a list: a field column, a button column or an icon column. The three are
 * exclusive by type, so a config naming both a button and an icon does not compile.
 */
export type SingleColumnConfig = FieldColumnConfig | ButtonColumnConfig | IconColumnConfig;
export type SingleColumnConfigOptions = SingleColumnConfig;

export function singleColumnConfig(config: SingleColumnConfigOptions): SingleColumnConfig {
  return {
    ...singleColumnConfigDefaults,
    ...config,
  } satisfies SingleColumnConfig;
}

export const singleColumnConfigDefaults = {
  id: '' as FieldName,
  header: '',
  resizable: true,
  actionType: 'function' as ActionType,
  hidden: false,
  filterable: true,
  hideHeader: false,
} satisfies SingleColumnConfig;

/** A column concatenating its field with fields of related resources: a field column with the related ids. */
export type MultiColumnConfig = FieldColumnConfig & {
  // todo: additionalIds name is not clear. the id provided is fetched from a different resource, such as a typeId
  additionalIds: AdditionalResourceConfig[];
  concatFn: ListConcatFn;
};
export type MultiColumnConfigOptions = MultiColumnConfig;

export function multiColumnConfig(config: MultiColumnConfigOptions): MultiColumnConfig {
  return {
    ...multiColumnConfigDefaults,
    ...config,
  } satisfies MultiColumnConfig;
}

export const multiColumnConfigDefaults = {
  ...singleColumnConfigDefaults,
  additionalIds: [] as AdditionalResourceConfig[],
  concatFn: 'hyphen-concat' as ListConcatFn,
} satisfies MultiColumnConfig;

export interface AdditionalResourceConfig {
  resource?: Resource;
  id: FieldName;
  field?: FieldName;
  hidden?: boolean;
}
export interface AdditionalResourceConfigOptions {
  id: FieldName;
  resource?: Resource;
  field?: FieldName;
  hidden?: boolean;
}

export function additionalResourceConfig(config: AdditionalResourceConfigOptions): AdditionalResourceConfig {
  return {
    ...additionalResourceConfigDefaults,
    ...config,
  } satisfies AdditionalResourceConfig;
}

export const additionalResourceConfigDefaults = {
  id: '' as FieldName,
  resource: '' as Resource,
  field: '' as FieldName,
  hidden: false,
} satisfies AdditionalResourceConfig;

export interface FieldSort {
  field: FieldName;
  direction: 'asc' | 'desc';
}
export interface FieldSortOptions {
  field: FieldName;
  direction?: 'asc' | 'desc';
}

export function fieldSort(config: FieldSortOptions): FieldSort {
  return {
    ...fieldSortDefaults,
    ...config,
  } satisfies FieldSort;
}

export const fieldSortDefaults = {
  field: '' as FieldName,
  direction: 'asc' as 'asc' | 'desc',
} satisfies FieldSort;

/**
 * How an enumerated field presents the rows of the resource its metadata names. Which
 * resource a picker lists is the generator's statement alone (a field-scope @enumerate,
 * or the schema's foreign key); the configuration narrows those rows with `filter`,
 * orders them with `sorts`, and chooses the columns and concatenation shown.
 */
export interface EnumeratedConfigOptions {
  /* A function to filter the list of enumerated options.
   * The `parentResource` argument contains the data of the resource that this enumerated field is part of.
   * It will be an object with the current form values, which can be empty during resource creation.
   */
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  filter?: (resource: any) => string;
  /**
   * Force the picker's list request into a POST, which the browser never caches. The
   * picker posts on its own when the configured filter, the search box or the prefix match
   * reaches a field the metadata marks `pii`; set this for a filter the metadata cannot
   * read, a hand-registered resource, say.
   */
  disableCacheForFilterPii?: boolean;
  /**
   * Specifies which resource data to pass to the filter function:
   * - 'rootResource': Pass in the root resource data
   * - 'parentResource': Pass in the parent resource data
   * @default 'parentResource'
   */
  filterType?: FilterType;
  /**
   * The order the options are listed in. On a source with a maximum page size (paged) it
   * is the sort the picker pages by; none, and the source's declared `@order` applies,
   * else the first display column ascending, since every paged request carries an order.
   */
  sorts?: FieldSort[];
  listDisplay?: FieldName[];
  viewDisplay: FieldName[];
  viewConcatFn?: ConcatFn;
  listConcatFn?: ConcatFn;
  viewDetails?: boolean;
  /**
   * Narrow the options by typing. A source read whole (no maximum page size) is narrowed
   * in the browser: every typed word must be found in the option's display, its id, or one
   * of the source's search fields. A paged source is narrowed on the server, one page at a
   * time: by the search box when the source has search fields the caller may list, else by
   * a prefix match on the display field when it answers `startswith`; a paged source with
   * neither is a configuration error at startup, naming the resource and the DDL way out.
   * Which fields the box searches is the source resource's, never the picker's.
   */
  searchable?: boolean;
}

type FilterType = 'parentResource' | 'rootResource';
export interface EnumeratedConfig {
  filter: (resource: any) => string;
  disableCacheForFilterPii: boolean;
  filterType: FilterType;
  sorts: FieldSort[];
  listDisplay: FieldName[];
  viewDisplay: FieldName[];
  viewConcatFn: ConcatFn;
  listConcatFn: ConcatFn;
  viewDetails: boolean;
  searchable: boolean;
}
export function enumeratedConfig(config: EnumeratedConfigOptions): EnumeratedConfig {
  return {
    ...enumeratedConfigDefaults,
    ...config,
  } satisfies EnumeratedConfig;
}
export const enumeratedConfigDefaults = {
  filter: (): string => '',
  disableCacheForFilterPii: false,
  filterType: 'parentResource' as FilterType,
  sorts: [] as FieldSort[],
  listDisplay: [] as FieldName[],
  viewDisplay: [] as FieldName[],
  viewConcatFn: 'hyphen-concat' as ConcatFn,
  listConcatFn: 'hyphen-concat' as ConcatFn,
  viewDetails: false,
  searchable: false,
};

export type FieldDefault = ForeignKeyDefault | StaticDefault | null;

export interface NullBooleanConfigOptions {
  displayValues?: Record<'null' | 'true' | 'false', { label: string; value: NullBoolean }>;
}
export interface NullBooleanConfig {
  /** This property maps the three possible values for this
   * nullboolean field with the display values users will see
   * on the screen for each
   */
  displayValues: Record<'null' | 'true' | 'false', { label: string; value: NullBoolean }>;
}
export function nullBooleanConfig(config: NullBooleanConfigOptions): NullBooleanConfig {
  return {
    ...nullBooleanConfigDefaults,
    ...config,
  } satisfies NullBooleanConfig;
}
const defaultBooleanDisplay = Object.freeze({
  null: {
    label: defaultEmptyFieldValue,
    value: null,
  },
  true: {
    label: 'Yes',
    value: true,
  },
  false: {
    label: 'No',
    value: false,
  },
});

export const nullBooleanConfigDefaults = {
  displayValues: defaultBooleanDisplay,
};

export interface ForeignKeyDefaultOptions {
  parentId: FieldName;
}
export interface ForeignKeyDefault {
  type: 'foreignKey';
  parentId: FieldName;
}
export function foreignKeyDefault(config: ForeignKeyDefaultOptions): ForeignKeyDefault {
  return {
    ...foreignKeyDefaultDefaults,
    ...config,
  } satisfies ForeignKeyDefault;
}
export const foreignKeyDefaultDefaults = {
  type: 'foreignKey',
  parentId: '' as FieldName,
} satisfies ForeignKeyDefault;

export interface StaticDefault {
  type: 'static';
  value: string | boolean;
}
export interface staticDefaultOptions {
  value: string | boolean;
}
export function staticDefault(config: staticDefaultOptions): StaticDefault {
  return {
    ...staticDefaultDefaults,
    ...config,
  } satisfies StaticDefault;
}
export const staticDefaultDefaults = {
  type: 'static',
  value: '',
} satisfies StaticDefault;

export type Affix = string | remoteData | parentData;

interface remoteData {
  resource: Resource;
  id: FieldName;
  field: FieldName;
}

interface parentData {
  field: FieldName;
}

export type ConfigElement = FieldElement | SectionElement | ComputedDisplayFieldElement | PaddingElement;

export type ColSize = 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12;

export interface ComputedDisplayFieldElementOptions {
  label?: string;
  class?: string;
  cols?: ColSize;
  calculatedValue?: (data: any) => string;
  shouldRender?: ((data: any) => boolean) | boolean;
}
export interface ComputedDisplayFieldElement {
  type: 'computedDisplayField';
  label: string;
  class: string;
  cols: ColSize;
  calculatedValue: (data: any) => string;
  shouldRender: ((data: any) => boolean) | boolean;
}
export function computedDisplayField(config: ComputedDisplayFieldElementOptions): ComputedDisplayFieldElement {
  return {
    ...computedDisplayFieldElementDefaults,
    ...config,
  } satisfies ComputedDisplayFieldElement;
}
export const computedDisplayFieldElementDefaults = {
  type: 'computedDisplayField',
  label: '',
  class: '',
  cols: 6,
  calculatedValue: (): string => '',
  shouldRender: true,
} satisfies ComputedDisplayFieldElement;

export interface PaddingElementOptions {
  cols?: ColSize;
  shouldRender?: ((data: any) => boolean) | boolean;
}
export interface PaddingElement {
  type: 'padding';
  cols: ColSize;
  /** Determines whether the padding is shown
   */
  shouldRender: ((data: any) => boolean) | boolean;
}
export function padding(config?: PaddingElementOptions): PaddingElement {
  return {
    ...paddingElementDefaults,
    ...config,
  } satisfies PaddingElement;
}
export const paddingElementDefaults = {
  type: 'padding',
  cols: 12,
  shouldRender: true,
} satisfies PaddingElement;

export interface SectionElementOptions {
  label?: string;
  class?: string;
  cols?: ColSize;
  shouldRender?: ((data: any) => boolean) | boolean;
  nullAllChildrenIfConditionallyHidden?: boolean;
  children?: ConfigElement[];
}
export interface SectionElement {
  type: 'section';
  label: string;
  class: string;
  cols: ColSize;
  /**
   * shouldRender is used to determine if the section should be
   * displayed to users.
   * When assigning shouldRender a function, the first argument
   * is the current state of the resource being displayed
   * Hides the layout AND all of its children
   */
  shouldRender: ((data: any) => boolean) | boolean;
  /** If shouldRender is NOT a function, this property does nothing
   * If shouldRender is a function, this property
   * sets children's values to null if shouldRender hides it (returns false)
   * within the resource-layout.component
   */
  nullAllChildrenIfConditionallyHidden: boolean;
  children: ConfigElement[];
}
export function section(config: SectionElementOptions): SectionElement {
  return {
    ...sectionElementDefaults,
    ...config,
  } satisfies SectionElement;
}
export const sectionElementDefaults = {
  type: 'section',
  label: '',
  class: '',
  cols: 12,
  shouldRender: true,
  nullAllChildrenIfConditionallyHidden: false,
  children: [],
} satisfies SectionElement;

export interface FieldElementOptions {
  name: FieldName;
  label?: string;
  cols?: ColSize;
  class?: string;
  enumeratedConfig?: EnumeratedConfig;
  default?: FieldDefault;
  nullBooleanConfig?: NullBooleanConfig;
  validators?: ((data: any) => ResourceValidatorFn[]) | ResourceValidatorFn[];
  readOnly?: boolean;
  prefixes?: Affix[];
  suffixes?: Affix[];
  shouldRender?: ((data: any) => boolean) | boolean;
  nullIfConditionallyHidden?: boolean;
}
export interface FieldElement {
  type: 'field';
  name: string;
  label: string;
  cols: ColSize;
  class: string;
  enumeratedConfig: EnumeratedConfig;
  default: FieldDefault;
  nullBooleanConfig: NullBooleanConfig;
  /**
   * A list of validators that must pass to allow a field to be submitted.
   * Utilize existing validators in the resourceValidators object, and
   * add any new or custom validators to it
   */
  validators: ((data: any) => ResourceValidatorFn[]) | ResourceValidatorFn[];
  readOnly: boolean;
  prefixes: Affix[];
  suffixes: Affix[];
  /**
   * shouldRender is used to determine if the element should be
   * displayed to users.
   * When assigning shouldRender a function, the first argument
   * is the current state of the resource being displayed
   */
  shouldRender: ((data: any) => boolean) | boolean;
  /** If shouldRender is NOT a function, this property does nothing
   * If shouldRender is a function, this property
   * determines whether the form control associated
   * with this config element should have its value
   * set to null if shouldRender hides it (returns false)
   */
  nullIfConditionallyHidden: boolean;
}

export function field(config: FieldElementOptions): FieldElement {
  return {
    ...fieldElementDefaults,
    ...config,
  } satisfies FieldElement;
}
export const fieldElementDefaults = {
  type: 'field',
  name: '',
  label: '',
  cols: 6,
  class: '',
  enumeratedConfig: { ...enumeratedConfigDefaults },
  default: null,
  validators: [],
  nullBooleanConfig: { ...nullBooleanConfigDefaults },
  readOnly: false,
  prefixes: [],
  suffixes: [],
  shouldRender: true,
  nullIfConditionallyHidden: false,
} satisfies FieldElement;

/**
 * RouteData is used to define the route for a config
 * route: the route for the config
 * hasViewRoute: when true, a route will be created for route/:uuid
 */
export interface RootRouteData {
  route?: string;
  hasViewRoute?: boolean;
}

export interface RPCConfigOptions {
  label: string;
  afterMethodRedirect?: string;
  conditions: {
    field: FieldName;
    matchValues: string[];
  }[];
  methodBodyTemplate?: any;
  successMessage?: string;
  elements?: ConfigElement[];
  placement?: RPCPlacement;
  method: Method;
  refreshResources?: Resource[];
  defaultModalWidth?: string;
  shouldRender?: () => boolean;
}
export interface RPCConfig {
  label: string;
  afterMethodRedirect: string | string[];
  conditions: {
    field: FieldName;
    matchValues: string[];
  }[];
  methodBodyTemplate: any;
  method: Method;
  successMessage: string;
  elements: ConfigElement[];
  placement: RPCPlacement;
  refreshResources: Resource[];
  defaultModalWidth: string;
  shouldRender: () => boolean;
}
export function rpcConfig(config: RPCConfigOptions): RPCConfig {
  return {
    ...rpcConfigDefaults,
    ...config,
  } satisfies RPCConfig;
}
export const rpcConfigDefaults = {
  label: '',
  afterMethodRedirect: '',
  conditions: [] as {
    field: FieldName;
    matchValues: string[];
  }[],
  methodBodyTemplate: {} as any,
  successMessage: '',
  elements: [] as ConfigElement[],
  placement: 'end' as RPCPlacement,
  method: '' as Method,
  refreshResources: [] as Resource[],
  defaultModalWidth: '',
  shouldRender: (): boolean => true,
} satisfies RPCConfig;

export interface RPCBaseFormData {
  elements: FieldElement[];
  label: string;
  method: Method;
}

// Config is the base type that all configurations should start with
export interface RootConfigOptions {
  routeData?: RootRouteData;
  nav?: {
    navItem: MenuItem;
    group?: string;
  };
  parentConfig: ParentResourceConfig;
  relatedConfigs?: ChildResourceConfig[];
  rpcConfigs?: RPCConfig[];
}
export interface RootConfig {
  routeData: RootRouteData;
  nav: {
    navItem: MenuItem;
    group?: string;
  };
  parentConfig: ParentResourceConfig;
  relatedConfigs: ChildResourceConfig[];
  rpcConfigs?: RPCConfig[];
}
export const rootConfigDefaults = {
  routeData: {} as RootRouteData,
  nav: {
    navItem: {} as MenuItem,
  },
  parentConfig: {} as ParentResourceConfig,
  relatedConfigs: [] as ChildResourceConfig[],
  rpcConfigs: [] as RPCConfig[],
} satisfies RootConfig;

export function rootConfig(config: RootConfigOptions): RootConfig {
  return {
    ...rootConfigDefaults,
    ...config,
  } satisfies RootConfig;
}

// Base type config is the base type for all resource configurations
export interface BaseConfigOptions {
  title?: string;
  createTitle?: string;
  createButtonLabel?: string;
  primaryResource: Resource;
  parentClass?: string;
  fieldClass?: string;
  elements: ConfigElement[];
  createConfig?: ConfigType;
  parentRelation?: {
    parentKey: FieldName;
    childKey: FieldName;
  };
  showBackButton?: boolean;
  createNavigation?: string[];
}
export interface BaseConfig {
  title: string;
  createTitle: string;
  createButtonLabel: string;
  primaryResource: Resource;
  parentClass: string;
  fieldClass: string;
  elements: ConfigElement[];
  createConfig: ConfigType;
  parentRelation: {
    parentKey: FieldName;
    childKey: FieldName;
  };
  showBackButton: boolean;
  createNavigation: string[];
}

@Directive()
export abstract class CustomConfigComponent {
  uuid = input<string>();
  parentData = input<RecordData>();
  config = input<ComponentConfig>();
}

// Available param types for a component config
export type ConfigParam = SwitchConfigParam;

// Custom component config renders a custom component and supplies params to it
export interface ComponentConfigOptions {
  // todo: it's weird that we have a primary resource here. Would be nice to remove but this adds a null check to all other configs
  primaryResource: Resource;
  component: Type<CustomConfigComponent> | 'SwitchResolver';
  params?: ConfigParam;
  relatedConfig?: ParentResourceConfig[];
  shouldRenderActions?: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
}
export interface ComponentConfig {
  type: 'Component';
  primaryResource: Resource;
  component: Type<CustomConfigComponent> | 'SwitchResolver';
  params: ConfigParam;
  relatedConfig: ParentResourceConfig[];
  shouldRenderActions: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
}
export function componentConfig(config: ComponentConfigOptions): ComponentConfig {
  return {
    ...componentConfigDefaults,
    ...config,
  } satisfies ComponentConfig;
}
export const componentConfigDefaults = {
  type: 'Component',
  primaryResource: '' as Resource,
  component: 'SwitchResolver',
  params: {
    cases: [],
  } satisfies ConfigParam,
  relatedConfig: [] as ParentResourceConfig[],
  shouldRenderActions: {
    create: (): boolean => true,
    edit: (): boolean => true,
    delete: (): boolean => true,
  },
} satisfies ComponentConfig;

// A switch is a component type that allows for conditional rendering of child components based on the value of a parent field
// When parentData[c.parentField] equals c.caseId render the caseConfig with parentData[c.childId] as the uuid
export interface switchCase {
  caseId: string;
  parentField: string;
  childId: string;
  config: ChildResourceConfig;
}
export interface SwitchConfigParamOptions {
  cases: switchCase[];
}
export interface SwitchConfigParam {
  cases: switchCase[];
}
export function switchParams(config: SwitchConfigParamOptions): SwitchConfigParam {
  return {
    ...switchParamsDefaults,
    ...config,
  } satisfies SwitchConfigParam;
}
export const switchParamsDefaults = {
  cases: [] as switchCase[],
} satisfies SwitchConfigParam;

export interface ListViewConfigOptions extends BaseConfigOptions {
  showViewButton?: boolean;
  loadCreatedResource?: boolean;
  collapsible?: boolean;
  /**
   * Whether a row opens in place, by the listed resource's single key, to the
   * `rowExpansionConfig`. Refused when the page is built on a resource with no primary
   * key, naming the resource: a key-less row has no key to open by.
   */
  enableRowExpansion?: boolean;
  rowExpansionConfig?: ChildResourceConfig;
  /**
   * Whether the grid renders the rows in view alone and scrolls the rest, measuring the
   * row height from the first rows drawn. For a page served whole (a key-less resource,
   * whose page is the whole list) it is what keeps a long list light; row expansion is
   * switched off while it is on.
   */
  enableVirtualScroll?: boolean;
  listColumns: ColumnConfig[];
  relatedConfigs?: ChildResourceConfig[];
  /**
   * The field of the primary resource a row opens by. Its target is that field's
   * `enumeratedResource` in the metadata, the route the target's page, and the key the
   * row's value in the field; the View column is drawn when the caller may Read the
   * target. Without it a row opens the write resource — the metadata's `rowsOf` when the
   * list is a view over a table, else the primary resource — by its single primary key,
   * and a compound-key resource with no rowRoute draws no View column. A rowRoute naming
   * a field with no target in the metadata throws when the page is built, naming the field.
   */
  rowRoute?: FieldName;
  actionType?: ActionType;
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  filter?: (parentResource: any) => string;
  /**
   * Force the list request into a POST, which the browser never caches. The list posts
   * on its own when its filter, a column filter or the search box reaches a field the
   * metadata marks `pii`; set this for a filter the metadata cannot read.
   */
  disableCacheForFilterPii?: boolean;
  rpcConfigs?: RPCConfig[];
  /** The sorts the list is requested in until a header is clicked; none, and the resource's declared order applies. */
  sorts?: FieldSort[];
  shouldRenderActions?: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  /** Show the server's total row count in the title. */
  showRowCount?: boolean;
  /**
   * Whether the page draws the search box over the resource's search fields
   * (`ResourceMeta.searchFields`): drawn above the table when the metadata lists any,
   * unless false. Which fields it searches is the resource's, narrowed by the caller's
   * grants, never the page's; the box's request travels in the body. Default true.
   */
  showSearchBox?: boolean;
  /**
   * The server page size: rows per page, up to the resource's declared maximum (`@page`).
   * Omitted, the resource's declared default applies, so the descriptor decides. A size
   * over the maximum is refused by the server and the list shows its message. The list
   * pages on the server: one page of rows is requested and rendered, First, Previous,
   * and Next follow the server's cursors, and the browser never gathers every row. A
   * resource with no primary key is served whole and has no page, so a size on its page
   * is refused when the page is built, naming the resource.
   */
  pageSize?: number;
  /**
   * Whether the list page stays current without polling: each list request subscribes
   * the tab to changes of the resource in its domain, and the page refetches when the
   * change feed reports one (and the row page it opens does the same for its row). The
   * browser caches a live answer for five minutes, so a page left and reopened inside
   * the window asks the server for nothing. The default is the generated metadata's word
   * (ResourceMeta.live): a page on a resource whose changes are published, every
   * table-backed one, is live unless it says false here, and a page on one whose changes
   * are not, a view or a computed resource, stays plain, true here announced once and
   * ignored; metadata from a generator that predates the field makes no page live by
   * itself. It takes effect only when the application provides a change feed
   * (CHANGE_FEED) and the outlet serves live subscriptions; otherwise the page asks as a
   * plain one.
   */
  live?: boolean;
}
export interface ListViewConfig extends BaseConfig {
  type: 'ListView';
  showViewButton: boolean;
  loadCreatedResource: boolean;
  collapsible: boolean;
  enableRowExpansion: boolean;
  rowExpansionConfig: ChildResourceConfig;
  /** Whether the grid virtual-scrolls the page; see ListViewConfigOptions.enableVirtualScroll. */
  enableVirtualScroll: boolean;
  listColumns: ColumnConfig[];
  relatedConfigs: ChildResourceConfig[];
  rowRoute?: FieldName;
  actionType: ActionType;
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  filter: (parentResource: any) => string;
  disableCacheForFilterPii: boolean;
  rpcConfigs?: RPCConfig[];
  sorts: FieldSort[];
  shouldRenderActions: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  showRowCount: boolean;
  /** Whether the page draws the search box; see ListViewConfigOptions.showSearchBox. */
  showSearchBox: boolean;
  /** The server page size; see ListViewConfigOptions.pageSize. */
  pageSize?: number;
  /**
   * The page's own word on being live, as the options gave it; unset when they said
   * nothing, so the resource's metadata decides. See ListViewConfigOptions.live.
   */
  live?: boolean;
}

export function listViewConfig(config: ListViewConfigOptions): ListViewConfig {
  return {
    ...listViewConfigDefaults,
    ...config,
  } satisfies ListViewConfig;
}
export const listViewConfigDefaults = {
  title: '',
  primaryResource: '' as Resource,
  type: 'ListView',
  createTitle: '',
  createButtonLabel: 'Create',
  loadCreatedResource: false,
  showViewButton: true,
  collapsible: false,
  listColumns: [],
  elements: [],
  parentClass: '',
  fieldClass: '',
  createConfig: {} as ListViewConfig,
  createNavigation: [],
  relatedConfigs: [],
  parentRelation: {
    parentKey: '' as FieldName,
    childKey: '' as FieldName,
  },
  enableRowExpansion: false,
  rowExpansionConfig: {} as ChildResourceConfig,
  enableVirtualScroll: false,
  showBackButton: true,
  filter: (): string => '',
  disableCacheForFilterPii: false,
  sorts: [] as FieldSort[],
  actionType: 'function' as ActionType,
  rpcConfigs: [],
  shouldRenderActions: {
    create: (): boolean => true,
    edit: (): boolean => true,
    delete: (): boolean => true,
  },
  showRowCount: true,
  showSearchBox: true,
} satisfies ListViewConfig;

export type ViewType = 'OneToOne' | 'OneToMany';

export interface ViewConfigOptions extends BaseConfigOptions {
  collapsible?: boolean;
  connectorResource?: Resource;
  relatedConfigs?: ChildResourceConfig[];
  rpcConfigs?: RPCConfig[];
  shouldRenderActions?: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  /**
   * Whether the record page stays current without polling: the read subscribes the tab
   * to changes of the row, and the page refetches when the change feed reports one. The
   * browser caches a live answer for five minutes, so a page left and reopened inside
   * the window asks the server for nothing. The default is the generated metadata's word
   * (ResourceMeta.live): a page on a resource whose changes are published, every
   * table-backed one, is live unless it says false here, and a page on one whose changes
   * are not, a view or a computed resource, stays plain, true here announced once and
   * ignored; metadata from a generator that predates the field makes no page live by
   * itself. It takes effect only when the application provides a change feed
   * (CHANGE_FEED) and the outlet serves live subscriptions; otherwise the page asks as a
   * plain one.
   */
  live?: boolean;
}
export interface ViewConfig extends BaseConfig {
  type: 'View';
  collapsible: boolean;
  // todo: remove this comment once full documentation has been added. Populate this value when a database view resource is used to connect the ViewConfig's parent resource to its primary resource
  connectorResource: Resource;
  rpcConfigs?: RPCConfig[];
  relatedConfigs: ChildResourceConfig[];
  /** A set of functions that allows for
   * conditional display of non-RPC
   * actions based on the pristine state of the resource
   * (if such a state is applicable. Creation for example won't
   * have such data)
   * This is separate from ABAC control which takes
   * precedence and is based on a user's attributes
   */
  shouldRenderActions: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  /**
   * The page's own word on being live, as the options gave it; unset when they said
   * nothing, so the resource's metadata decides. See ViewConfigOptions.live.
   */
  live?: boolean;
}

export function viewConfig(config: ViewConfigOptions): ViewConfig {
  return {
    ...viewConfigDefaults,
    ...config,
  } satisfies ViewConfig;
}
export const viewConfigDefaults = {
  title: '',
  primaryResource: '' as Resource,
  type: 'View',
  createTitle: '',
  createButtonLabel: 'Create',
  parentClass: '',
  fieldClass: '',
  elements: [],
  collapsible: true,
  connectorResource: '' as Resource,
  relatedConfigs: [],
  createConfig: {} as ViewConfig,
  shouldRenderActions: {
    create: (): boolean => true,
    edit: (): boolean => true,
    delete: (): boolean => true,
  },
  createNavigation: [],
  parentRelation: {
    parentKey: '' as FieldName,
    childKey: '' as FieldName,
  },
  showBackButton: true,
  rpcConfigs: [],
} satisfies ViewConfig;

export interface arrayConfigOptions {
  iteratedConfig: ChildResourceConfig;
  viewType?: ViewType;
  collapsible?: boolean;
  createConfig?: ConfigType;
  connectorResource?: Resource;
  connectorField?: FieldName;
  primaryResource: Resource;
  listFilter: (parentResource: any) => string;
  disableCacheForFilterPii?: boolean;
  sorts?: FieldSort[];
  title?: string;
  createButtonLabel?: string;
  limit?: number;
  shouldRenderActions?: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
}
export interface ArrayConfig {
  type: 'Array';
  iteratedConfig: ChildResourceConfig;
  viewType: ViewType;
  collapsible: boolean;
  createConfig: ConfigType;
  connectorResource: Resource;
  connectorField: FieldName;
  primaryResource: Resource;
  listFilter: (parentResource: any) => string;
  disableCacheForFilterPii: boolean;
  sorts: FieldSort[];
  title: string;
  createButtonLabel: string;
  shouldRenderActions: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  limit: number;
}
export function arrayConfig(config: arrayConfigOptions): ArrayConfig {
  return {
    ...arrayConfigDefaults,
    ...config,
  } satisfies ArrayConfig;
}
export const arrayConfigDefaults = {
  type: 'Array',
  iteratedConfig: {} as ChildResourceConfig,
  viewType: 'OneToMany' as ViewType,
  collapsible: true,
  createConfig: {} as ViewConfig,
  connectorResource: '' as Resource,
  connectorField: '' as FieldName,
  primaryResource: '' as Resource,
  listFilter: (): string => '',
  disableCacheForFilterPii: false,
  sorts: [] as FieldSort[],
  title: '',
  createButtonLabel: 'Create',
  limit: Infinity,
  shouldRenderActions: {
    create: (): boolean => true,
    edit: (): boolean => true,
    delete: (): boolean => true,
  },
} satisfies ArrayConfig;
