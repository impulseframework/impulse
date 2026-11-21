import { Directive, Injector, input, Type } from '@angular/core';
import { TooltipPosition } from '@angular/material/tooltip';
import { IconName } from './icon-names';
import { FieldName, Method, PermissionScope, Resource } from './permissions';
import { NullBoolean } from './resource-meta';
import { ConcatFn, defaultEmptyFieldValue } from './resource-types';
import { ResourceValidatorFn } from './validators';

export interface FieldPointer {
  field: FieldName;
}

/**
 * How a config-driven page shows in the application's menus: its label and its icon.
 * resourceRoutes makes the page from it at the page's route and carries it on the route,
 * and resourcePage answers the same page for the menu file to place. The page needs the
 * resource's List permission and sits behind the resource's feature flag unless these
 * say otherwise. A config without one makes a page no menu reaches, such as one only a
 * row link opens. A page that lists its resource across tenants needs no List permission:
 * no one tenant's digest speaks for it, so the menus show it to every signed-in person
 * unless `permission` names what it needs.
 */
export interface ConfigPage {
  label: string;
  /** A Material Icon, drawn before the label. */
  icon?: IconName;
  /** The permission the page needs, where it is not List on the resource; a list means any one of them. */
  permission?: PermissionScope | readonly PermissionScope[];
  /** The feature flag the page sits behind, where it is not the resource's. */
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

export type ActionType = 'function' | 'link' | 'menu';

/** One entry of a row's actions menu: its words, an optional icon, and what a click does. */
export interface RowAction {
  id: string;
  label: string;
  icon?: string;
  run: () => void;
  /**
   * The entry says something and does nothing: the words a row shows in the place of a
   * method it does not offer (MethodPresentation.otherwise). It is drawn grayed out and
   * a click runs nothing.
   */
  disabled?: boolean;
}

/**
 * The display-row key a row's actions ride under: the list writes the entries the row
 * offers while it builds the display row and the grid's menu column reads them. A
 * generated field name never carries a colon, so the key collides with none.
 */
export const rowActionsKey = 'actions:menu';

/**
 * A button every row of a list carries, for what a row offers that is no method of the
 * server's: something a person only reads, opened in a pop-up (a flag's history). It is
 * drawn as an icon button named by its label, ahead of the row's menu of methods, on
 * every row; what the server lets the caller do is never decided here, so a button that
 * reads asks the server and shows its answer, a refusal included.
 */
export interface RowButton {
  /** The button's name: its tooltip and what a screen reader says. */
  label: string;
  /** The Material Icon the button draws. */
  icon: IconName;
  /**
   * What a click does. The row is the one the server answered, not the one the columns
   * wrote (a column's getter and formatter change what a cell shows, never what the row
   * holds), and the injector is the list's own, for a dialog or a service.
   */
  run: (row: RecordData, injector: Injector) => void;
}

/**
 * The display-row key the row as the server answered it rides under, which a row button
 * is handed: the grid hands a button the display row, whose cells the columns wrote.
 */
export const rowAnswerKey = 'row:answer';

/** The id of the column the n-th row button of a list is drawn in. */
export function rowButtonKey(index: number): string {
  return `button:${index}`;
}

/**
 * How a page presents one method it offers, where the generated metadata's own words
 * are not the ones the page wants. Every part is optional but the method's name: a
 * method the page does not name is offered with its name in words.
 */
export interface MethodPresentation {
  method: Method;
  /** The words of the menu entry or the button, and the dialog's title. */
  label?: string;
  /** The icon beside the words. */
  icon?: string;
  /** Leaves the method off the page. */
  hidden?: boolean;
  /**
   * Whether a row offers the method, beyond the server's answer for the row: for a
   * method that only makes sense in one state of the row (Enable on a disabled
   * account). The server's answer still decides whether the caller may run it. For a
   * method above a list (`listMethods`) the row is the one the list sits under, and
   * `listed` the rows the list shows: a method that only makes sense while the list
   * holds something (the removal of a person's password, above the directory sign-ins
   * linked to them) says so from them.
   */
  when?: (row: RecordData, listed?: readonly RecordData[]) => boolean;
  /**
   * What a row says in the method's place where `when` answers no: words drawn where the
   * method's entry would be, which a click does nothing with (a group that still has
   * people in it says what is left, where Delete would be). Answering nothing draws
   * nothing. It is said only on a row the server lets the caller run the method on, so
   * nobody reads about an act that is not theirs to take.
   */
  otherwise?: (row: RecordData) => string | undefined;
  /**
   * The question the dialog asks before a method that takes nothing but the row. For a
   * method above a list it is asked of the row the list sits under, with `listed`, the
   * rows the list shows.
   */
  confirm?: string | ((row: RecordData, listed?: readonly RecordData[]) => string);
  /** What the notice says once the method has run. */
  successMessage?: string;
  /**
   * What the notice says for the method's answer, where the answer itself says how the
   * method went: a method that mails a person answers whether the message was sent and,
   * where it was not, why. `failed` draws the notice as a failure. Undefined, or an
   * undefined result, leaves `successMessage`.
   */
  answered?: (answer: unknown) => { message: string; failed?: boolean } | undefined;
  /**
   * The words above a request field's input, by the field's name, where the name in
   * words is not what the page wants (`domain` as the application's own word for its
   * tenant). A field the page does not name is labeled with its name in words.
   */
  labels?: Record<string, string>;
  /**
   * The request fields picked as a day that mean through the end of that day: the last
   * day of a period. The dialog sends the first instant of the day after the one picked,
   * in the viewer's time zone, so what is given "until 1 December" still counts on
   * 1 December. A day picked for a field not named is sent as its first instant.
   */
  throughDay?: readonly string[];
  /**
   * What the page fills in the method's request from a row: the row the method is
   * offered on, or, for a method above a list, the row the list sits under (a child
   * list's parent). A filled field is not asked, and a value left undefined fills
   * nothing, so the dialog asks for that field. A field the row answers with nothing
   * (a role held in no one tenant) is filled with null: it is sent as nothing and is
   * not asked. It is how a method that names no target row and takes what a row holds
   * is run from that row: a role given to a person by the id of their account, above the
   * list of the roles the person holds. Named among `rowMethods`, a method with no
   * target row and a `fill` is offered on every row while the digest grants the
   * caller Execute on it; the server gives no answer for a row it does not name.
   */
  fill?: (row: RecordData) => Record<string, unknown>;
  /**
   * What a picker among the request's inputs lists each option by, by the request
   * field's name, where the option's name alone does not say enough to choose by: the
   * fields of the listed resource, joined in order (a role with its description). A
   * field the page does not name lists its options by the source's own name.
   */
  shows?: Record<string, string[]>;
}

export type ListConcatFn =
  'space-concat' | 'hyphen-concat' | 'space-hyphen-concat' | 'hyphen-space-concat' | 'no-space-concat';
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
  /**
   * The display-row field holding the row's own link, for a list whose rows open in
   * different places: a list across tenants, where each row opens inside its tenant.
   * Set, the link is the field's value, viewRoute and keyField are not read, and a row
   * whose field is empty draws no link.
   */
  linkField?: string;
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
  linkField?: string;
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
   * Draws a list field's items a line each, in place of one line of them joined by
   * commas and cut short: for a short list a person reads whole (the roles a group
   * holds). A row is as tall as its longest list. On a field that holds no list it
   * changes nothing.
   */
  lines?: boolean;
  /**
   * Opt the column out of its filter control. A control is drawn only where the
   * generated metadata says the server filters the field (`filterable`); `false` removes
   * it there too. It never adds one where the server would refuse the filter.
   */
  filterable?: boolean;
  /**
   * Opt the column out of sorting: its header is drawn as words, with nothing to press.
   * For a column whose shown value is not the value the server orders the field by (a
   * moment the server works out for each row), where a sort would order the rows by
   * something the person cannot see. It never makes a table sortable that is not.
   */
  sortable?: boolean;
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
  /**
   * What the cell says for a row with nothing to concatenate, given the row: the row
   * names no related record (a role held in the whole application names no tenant), or
   * the related records could not be read (the caller holds no List on them). Absent,
   * the cell shows `emptyDataValue`.
   */
  fallback?: (data: any) => string;
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
  /** How the page shows in the menus. */
  page?: ConfigPage;
  parentConfig: ParentResourceConfig;
  relatedConfigs?: ChildResourceConfig[];
  rpcConfigs?: RPCConfig[];
}
export interface RootConfig {
  routeData: RootRouteData;
  /** How the page shows in the menus. */
  page?: ConfigPage;
  parentConfig: ParentResourceConfig;
  relatedConfigs: ChildResourceConfig[];
  rpcConfigs?: RPCConfig[];
}
export const rootConfigDefaults = {
  routeData: {} as RootRouteData,
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
  /**
   * What a row's page is headed by, from the row: the row's own name (a group's page is
   * headed by the group's name). Until the row has been read the page is headed by
   * nothing. Unset, the page is headed by `title`, as the list is.
   */
  rowTitle?: (row: RecordData) => string;
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
  /** What a row's page is headed by; see BaseConfigOptions.rowTitle. */
  rowTitle?: (row: RecordData) => string;
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
  /**
   * The methods that run against a row of the list: the generated methods whose target
   * row (`@target`) is a row of the listed resource. Unset, every row offers, in a menu
   * at its end, each of them the server says the caller may run on that row (the list
   * asks for the row's Execute answer), under the method's name in words; a method
   * that takes more than the row opens a dialog drawn from its request fields, and one
   * that takes nothing more asks before it runs. A list of presentations says how the
   * methods it names are shown (words, icon, a row predicate, hidden) and leaves the
   * rest as they are. `false` offers none and asks the server nothing.
   */
  rowMethods?: false | MethodPresentation[];
  /**
   * The methods offered above the list: methods that name no row (a create, an
   * invitation), each a button drawn when the digest grants the caller Execute on it,
   * opening the same dialog. A list under a row may also name a method that runs
   * against that row (`@target` naming it), which belongs with what the list shows and
   * is no row of it (a person's password, above the identities the person signs in
   * with): its button is drawn when the server's answer for the row lists the method,
   * the row's key fills its request, and the page's question is asked of that row. The
   * list is read again once one has run.
   */
  listMethods?: MethodPresentation[];
  /** The sorts the list is requested in until a header is clicked; none, and the resource's declared order applies. */
  sorts?: FieldSort[];
  shouldRenderActions?: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  /**
   * Show the server's total row count in the title. The count comes with the list's first
   * answer, so a title that carries it is drawn with the rows and not before.
   */
  showRowCount?: boolean;
  /**
   * Further lists drawn under this one on its page, in order, each a list of its own
   * with its own title, rows and methods: for a page that is more than one list of the
   * same kind of thing (what is held right now, then what has ended). Only the list a
   * page is built on draws them; a list under a row's page names its lists among the
   * row's related configs.
   */
  below?: ListViewConfig[];
  /**
   * A standing notice above the list, drawn in the library's warning colors under a
   * warning icon: what the page must not let a person overlook for as long as it holds
   * (a sign-in method that is open to sign-up). It is said from the rows on the page,
   * again each time they are read, and answering nothing draws none.
   */
  notice?: (rows: RecordData[]) => string | undefined;
  /**
   * What the empty table says where the list holds no row at all: the page's own
   * sentence in place of "No records found" ("This application declares no feature
   * flags."). It is said only of the whole list: a list narrowed by a filter or the
   * search box that leaves no row says the plain text, and a refusal, a failed request
   * and a list with no column the person may see keep their own words.
   */
  emptyMessage?: string;
  /**
   * The buttons every row carries ahead of its menu of methods; see RowButton. None by
   * default.
   */
  rowButtons?: RowButton[];
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
  /**
   * Whether the page lists the resource across tenants: one list of its rows in every
   * tenant the person reaches in which they may list it, where a page inside a tenant
   * lists that tenant's. The resource must be declared for it (`@listAcrossTenants`; its
   * descriptor says `acrossTenants`), and the page is refused when it is built otherwise,
   * naming the resource. The table gains a column naming each row's tenant, under the
   * application's word for a tenant: its second column, after the page's first, which names
   * the row and is the one the table holds while the rest scroll sideways. A row's View
   * link opens the row itself inside its tenant, on the record's own row route there, drawn
   * where the person may read the record in that tenant and the application mounts it a row
   * route. The page is shown, and its route opened, to whoever may list the resource in at
   * least one tenant they reach, as the server tells the global digest; the config names no
   * permission for it. The server decides, tenant by tenant, which of the page's columns a
   * row shows, so the page asks for no column list and a cell the person may not read in
   * a row's tenant stays empty. The page reads: it offers no create, no delete and no
   * method, since every write stays inside one tenant, on the row's page; row expansion
   * is refused when the page is built. It is plain unless the page asks: only
   * `live: true` makes it live, with one subscription over the tenants the list reads,
   * where a tenant's list is live unless its page says otherwise. A column that reads a
   * referenced tenant-scoped resource shows nothing, since the page has no one tenant to
   * read it in.
   */
  acrossTenants?: boolean;
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
  /** The methods a row offers; see ListViewConfigOptions.rowMethods. */
  rowMethods?: false | MethodPresentation[];
  /** The methods offered above the list; see ListViewConfigOptions.listMethods. */
  listMethods?: MethodPresentation[];
  sorts: FieldSort[];
  shouldRenderActions: Record<'edit' | 'delete' | 'create', (data: any) => boolean>;
  showRowCount: boolean;
  /** The lists drawn under this one on its page; see ListViewConfigOptions.below. */
  below?: ListViewConfig[];
  /** The page's standing notice; see ListViewConfigOptions.notice. */
  notice?: (rows: RecordData[]) => string | undefined;
  /** What the empty table says of a list with no row; see ListViewConfigOptions.emptyMessage. */
  emptyMessage?: string;
  /** The buttons every row carries; see ListViewConfigOptions.rowButtons. */
  rowButtons?: RowButton[];
  /** Whether the page draws the search box; see ListViewConfigOptions.showSearchBox. */
  showSearchBox: boolean;
  /** The server page size; see ListViewConfigOptions.pageSize. */
  pageSize?: number;
  /**
   * The page's own word on being live, as the options gave it; unset when they said
   * nothing, so the resource's metadata decides. See ListViewConfigOptions.live.
   */
  live?: boolean;
  /** Whether the page lists the resource across tenants; see ListViewConfigOptions.acrossTenants. */
  acrossTenants?: boolean;
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
   * The methods the row page offers on its row; see ListViewConfigOptions.rowMethods,
   * which a page with a list shares with its row page.
   */
  rowMethods?: false | MethodPresentation[];
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
  /** The methods the row page offers on its row; see ListViewConfigOptions.rowMethods. */
  rowMethods?: false | MethodPresentation[];
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
