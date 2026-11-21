import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  inject,
  OnInit,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { ReactiveFormsModule, Validators } from '@angular/forms';
import { MatAutocompleteModule } from '@angular/material/autocomplete';
import { MatButtonModule } from '@angular/material/button';
import { MatOptionModule } from '@angular/material/core';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, Router, RouterModule } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  defaultEmptyFieldValue,
  EnumeratedConfig,
  EnumerationOption,
  FieldName,
  keyFields,
  ListPermission,
  Permission,
  ReadPermission,
  Resource,
  RootConfig,
} from '@impulseframework/resource-angular/types';
import { pickerSort, readMode } from '@impulseframework/resource';
import { concatFunctions, hyphenConcat } from '../../../concat-fns';
import { PagedListRequest } from '../../../paged-list';
import { pageLabel } from '../../../resource-list/list-request';
import { SearchBoxComponent } from '../../../search-box/search-box.component';
import {
  filterReachesPII,
  listableFieldsOf,
  searchedFields,
  searchFieldLabel,
  searchPlaceholder,
  searchReachesPII,
} from '../../../search-box/search-fields';
import { BaseInputComponent } from '../../base-field.directive';
import {
  closedRecordText,
  closedSourceText,
  displayFromOptions,
  matchOptions,
  narrowedFilter,
  notPermitted,
  optionColumns,
  optionDisplay,
  pickerConsoleLine,
  PickerOption,
  pickerNarrowing,
  pickerRefusal,
  refuseUnsearchablePicker,
  withChosen,
} from './enumerated-options';

/**
 * The picker for a field that holds another resource's identifier. Which resource it
 * lists is the generated metadata's statement alone: `enumeratedResource` names the
 * resource (a field-scope @enumerate, or the schema's foreign key), or `enumeration`
 * carries a fixed value set the picker renders with no request. The configuration
 * narrows and presents those rows — `filter`, `sorts`, the display columns — and never
 * chooses another resource.
 *
 * How the resource is read is the generated descriptor's statement alone: its declared
 * maximum page size (readMode). A source with no maximum is read whole in one request,
 * the options are every row, and the chosen value's display resolves from that list, so
 * a source with no read route serves. A source with a maximum is paged one server page
 * at a time, Previous and Next by the server's cursors inside the panel, sorted by the
 * configured sorts, else the source's @order, else the display column, and the chosen
 * value is read by key and shown ahead of the page's options, so the control shows what
 * is stored whichever page is open. `searchable` narrows a whole source's options in the
 * browser, every typed word found in the display, the id, or a search field of the row;
 * on a paged source it narrows on the server, one page at a time, by the search box when
 * the source has search fields the caller may list, else by a prefix match on the display
 * field when it answers startswith, and a paged source with neither is refused when the
 * field is built. Which fields the box searches is the source resource's, never the
 * picker's.
 *
 * The picker offers only what the person may see of its source. Where the source is
 * closed to them (sourceClosed: the permission digest grants nothing of what the picker
 * asks, or the server refused the list itself as not permitted) it asks nothing, shows
 * the stored key read-only, and says one sentence of its own under the field. Where the
 * source is open and the row the field holds is not theirs to read (rowClosed: a
 * condition on the row, say) it shows the stored key with a sentence of its own, asks no
 * more for that value, and stays a picker while it is edited, so a row they may read can
 * be chosen. Neither touches the form's value. Any other failure of a request shows in
 * the words pickerRefusal gives it, never as an empty list. What the server said of a
 * request it refused or that failed is never on the screen: it is written to the
 * browser's console, once.
 */
@Component({
  selector: 'impulse-enumerated-field',
  imports: [
    MatFormFieldModule,
    MatInputModule,
    MatDatepickerModule,
    ReactiveFormsModule,
    MatOptionModule,
    MatSelectModule,
    MatIconModule,
    MatButtonModule,
    MatTooltipModule,
    RouterModule,
    MatAutocompleteModule,
    SearchBoxComponent,
  ],
  templateUrl: './enumerated-field.component.html',
  styleUrl: './enumerated-field.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class EnumeratedFieldComponent extends BaseInputComponent implements OnInit {
  activatedRoute = inject(ActivatedRoute);
  router = inject(Router);
  auth = inject(AuthService);
  reloadSignal = signal(false);

  /** The typed text: a whole source's options are narrowed by it in the browser, a paged source's page on the server. */
  query = signal('');

  private readonly searchBox = viewChild(SearchBoxComponent);

  /**
   * The fixed value set the metadata carries for a field naming an @enumerate table.
   * When present the picker renders from it and never lists a resource: the values are
   * the program's constants, so there is nothing to fetch and no List grant to hold.
   */
  enumeration = computed((): EnumerationOption[] | undefined => {
    const meta = this.fieldMeta();
    return meta && 'enumeration' in meta ? meta.enumeration : undefined;
  });

  /** The resource whose rows the picker lists, as the metadata names it; none for a fixed enumeration. */
  resource = computed(() => {
    if (this.enumeration()) {
      return undefined;
    }
    return this.fieldMeta()?.enumeratedResource as Resource;
  });

  tooltipMessage = computed(() => {
    const label = this.fieldConfig().label;
    return 'View ' + label + ' details';
  });

  route = computed(() => {
    const resource = this.resource();
    return resource ? this.resourceMeta(resource)?.route : '';
  });

  /**
   * The listed resource's generated descriptor, which says how it is read. A resource
   * the generated API does not describe (one registered by hand) has none and is read whole.
   */
  private descriptor = computed(() => {
    const resource = this.resource();
    return resource ? this.store.client.descriptor.resources[resource] : undefined;
  });

  /** Whether the source is paged: its descriptor declares a maximum page size. */
  paged = computed(() => {
    const descriptor = this.descriptor();
    return descriptor !== undefined && readMode(descriptor) === 'paged';
  });

  /** The listed resource's metadata; none for a fixed enumeration. */
  private sourceMeta = computed(() => {
    const resource = this.resource();
    return resource ? this.resourceMeta(resource) : undefined;
  });

  /**
   * How the picker presents the source's rows: the field's configuration, and where it
   * names no display of its own (a form drawn from the metadata alone), the field the
   * source's metadata says its rows are called by (`nameField`: a tenant's name), so a
   * picker over a record keyed by an id lists names and never ids.
   */
  private picker = computed((): EnumeratedConfig => {
    const config = this.fieldConfig().enumeratedConfig;
    const name = this.sourceMeta()?.nameField;
    if (!name || config.listDisplay.length > 0 || config.viewDisplay.length > 0) {
      return config;
    }
    return { ...config, viewDisplay: [name as FieldName] };
  });

  /** The digest's List field set for any resource (listableFieldsOf): the source's own and a search branch's child. */
  private readonly listableOf = listableFieldsOf(this.auth);

  /** The digest's List field set for the source itself. */
  private sourceListable = computed<ReadonlySet<string> | undefined>(() => {
    const resource = this.resource();
    return resource ? this.listableOf(resource) : undefined;
  });

  /** The fields the box over the source searches for this caller: the source's, less the denied, a branch judged on its child. */
  sourceSearchFields = computed(() => searchedFields(this.sourceMeta(), this.sourceListable(), this.listableOf));

  /**
   * Whether the source's request must travel in the body: the config asked for it, the
   * configured filter names a PII field, the box reaches one, or the prefix match runs on
   * a PII display field. The server refuses a PII field in a URL with a 400.
   */
  private sensitive = computed(() => {
    const config = this.picker();
    if (config.disableCacheForFilterPii || filterReachesPII(this.optionFilter(), this.sourceMeta())) {
      return true;
    }
    if (this.query().trim() === '') {
      return false;
    }
    const narrowing = this.narrowing();
    if (narrowing === 'box') {
      return searchReachesPII(this.sourceMeta(), this.sourceSearchFields());
    }
    return narrowing === 'prefix' && searchReachesPII(this.sourceMeta(), [this.displayColumn() ?? '']);
  });

  /** The source's own search fields (not a child's), the columns a whole source's matcher reads. */
  private ownSearchFields = computed(() => this.sourceSearchFields().filter((field) => !field.includes('.')));

  /** How a searchable paged source narrows as the user types: the box, a prefix on the display field, or nothing. */
  narrowing = computed(() => {
    if (!this.paged() || !this.picker().searchable) {
      return undefined;
    }
    return pickerNarrowing(this.sourceMeta(), this.displayColumn(), this.sourceSearchFields());
  });

  /** What the panel's box says: the fields it searches, or the display field a prefix narrows. */
  searchPlaceholder = computed(() => {
    if (this.narrowing() === 'prefix') {
      return `Starts with ${searchFieldLabel(this.displayColumn() ?? 'id')}`;
    }
    return searchPlaceholder(this.sourceSearchFields().map((field) => searchFieldLabel(field)));
  });

  /** Whether the read field links to the chosen row's page: never to a row closed to the person. */
  viewDetails = computed(() => {
    return this.editMode() === 'view' && this.picker().viewDetails === true && this.closedTo() === undefined;
  });

  sorts = computed(() => {
    return this.picker().sorts;
  });

  /**
   * The columns the option list asks for: the id plus whatever the display concatenates,
   * and on a searchable whole source its search fields, which the browser's matcher reads.
   */
  private columns = computed(() => {
    const config = this.picker();
    return optionColumns(config, config.searchable && !this.paged() ? this.ownSearchFields() : [], this.keyField());
  });

  /**
   * The field of the source a picked row is stored by: the source's own key field, by
   * its metadata (a picker source is keyed by one column), `id` where the metadata
   * names none (a resource registered by hand).
   */
  private keyField = computed((): string => keyFields(this.sourceMeta())[0]?.fieldName ?? 'id');

  /** The display column shown first: the sort a paged source with no @order pages by. */
  private displayColumn = computed((): string | undefined => {
    const config = this.picker();
    return config.listDisplay[0] ?? config.viewDisplay[0];
  });

  rootResourceRef = computed(() => {
    const rootConfig = this.activatedRoute.snapshot.data['config'] as RootConfig;
    const uuid = (this.activatedRoute.snapshot.params['uuid'] || '') as string;
    const rootMeta = this.resourceMeta(rootConfig.parentConfig.primaryResource);
    return this.store.resourceView(signal(rootMeta.route), signal(uuid));
  });

  /** The configured filter over the options, evaluated against the root or the parent row as configured. */
  private optionFilter = computed((): string => {
    const config = this.picker();
    return (
      (config.filterType === 'rootResource'
        ? config.filter?.(this.rootResourceRef()?.value() || {})
        : config.filter?.(this.relatedData() || {})) ?? ''
    );
  });

  /**
   * The chosen value as the control holds it, followed as a signal by value: it moves
   * when the user picks (select flips reloadSignal) or when the form is rebuilt over a
   * row holding another value, and it stays put when the form is rebuilt over the same
   * value or the mode toggles, so nothing downstream re-runs then. Empty for no value.
   */
  private readonly chosenValue = computed((): string => {
    this.reloadSignal();
    const value: unknown = this.form().get(this.fieldConfig().name)?.value;
    // Several picked keys name no one row to read: their source is read whole.
    return value === null || value === undefined || Array.isArray(value) ? '' : String(value);
  });

  /**
   * Several keys are picked: the metadata's display type is `enumerated[]`, a method's
   * request field that holds a list (the roles a new user is given). The control then
   * holds a list of keys, the panel ticks each one, and no empty choice is offered: an
   * empty list is the empty value.
   */
  multiple = computed((): boolean => this.fieldMeta()?.displayType === 'enumerated[]');

  /** The picked keys of a field that takes several, in the order they were picked. */
  private readonly chosenKeys = computed((): string[] => {
    this.reloadSignal();
    const value: unknown = this.form().get(this.fieldConfig().name)?.value;
    return Array.isArray(value) ? value.map((key) => String(key)) : [];
  });

  /**
   * Whether the permission digest at hand says the person does not hold the permission
   * on the picker's source. A conditional grant is held: the server narrows it. False too
   * while nothing is known: the digest that answers for the source has not loaded, no
   * tenant is selected for a domain-scoped source, or the generated API does not describe
   * the source (a resource registered by hand has no word in the digest).
   */
  private deniedByDigest(permission: Permission): boolean {
    const resource = this.resource();
    const descriptor = this.descriptor();
    if (!resource || !descriptor || (descriptor.scope === 'domain' && !this.store.domain())) {
      return false;
    }
    const scope = { resource, permission };
    return this.auth.hasDigestFor(scope) && !this.auth.hasPermission(scope);
  }

  /** The source in the selected tenant: what a refusal of its list is remembered for. */
  private readonly sourceKey = computed((): string => `${this.route() ?? ''}|${this.store.domain() ?? ''}`);

  /** The chosen value in its source: what a refusal of its read is remembered for. */
  private readonly rowKey = computed((): string => `${this.sourceKey()}|${this.chosenValue()}`);

  /** The source (sourceKey) whose list the server refused as not permitted, once it has. */
  private readonly listRefusedFor = signal<string | undefined>(undefined);

  /** The chosen values (rowKey) whose read the server refused as not permitted. */
  private readonly rowsRefused = signal<ReadonlySet<string>>(new Set<string>());

  /**
   * Whether the source is closed to the person: there is nothing to pick from, and no
   * name for what is stored. The digest says so where it grants nothing of what the
   * picker asks: List on a source read whole, whose list is all the picker asks; on a
   * paged source, List while the field is edited (the page of options), and both List and
   * Read while it is read (the chosen row is all a read field asks, so Read alone keeps
   * it open). The server says so by refusing the list itself as not permitted, which is
   * remembered for the source. A closed source is asked nothing.
   */
  private readonly sourceClosed = computed((): boolean => {
    if (!this.resource() || this.showField() === false) {
      return false;
    }
    if (this.listRefusedFor() === this.sourceKey()) {
      return true;
    }
    if (!this.deniedByDigest(ListPermission)) {
      return false;
    }
    return !this.paged() || this.editMode() === 'edit' || this.deniedByDigest(ReadPermission);
  });

  /**
   * Whether the row the field holds is closed to the person while its source is not: the
   * digest grants no Read on a paged source they may list, or the server refused the read
   * of this value as not permitted (a condition on the row keeps it from them), which is
   * remembered for each such value. Such a row is not asked for again; another value is.
   */
  private readonly rowClosed = computed((): boolean => {
    if (!this.paged() || this.showField() === false || this.chosenValue() === '' || this.sourceClosed()) {
      return false;
    }
    return this.deniedByDigest(ReadPermission) || this.rowsRefused().has(this.rowKey());
  });

  /**
   * The route the chosen row is read from: the paged source's, while the field is shown.
   * Empty for a whole source (its list holds every row), a fixed enumeration, a hidden
   * field, a source closed to the person, and a chosen row closed to them, so the reader
   * below stays idle with no request.
   */
  private readonly chosenRoute = computed((): string => {
    if (this.showField() === false || !this.resource() || !this.paged() || this.sourceClosed() || this.rowClosed()) {
      return '';
    }
    return this.route() ?? '';
  });

  /**
   * The chosen row read by key, on a paged source: the row the field names, whichever
   * page is open. One reader, built once, keyed on the route and the value alone: a
   * change of the chosen value reads the new row, and nothing else does; an edit-mode
   * toggle, a rebuild of the form group over the same value, and a reload of the page's
   * row read nothing. A paged source serves a read; the generator refuses one that does not.
   */
  private readonly chosenRowRef = this.store.resourceView(this.chosenRoute, this.chosenValue);

  /** The chosen row's reader while there is a row to read; undefined otherwise. */
  singleEnumResourceRef = computed(() => {
    return this.chosenRoute() !== '' && this.chosenValue() !== '' ? this.chosenRowRef : undefined;
  });

  /**
   * The chosen row as the paged source answered it: nothing while there is no row to
   * read, while the first read is out, and when the read was refused or failed. A person
   * may read a row and not the row its field names, so a refused read is an answer the
   * field shows (refusal), never an error thrown at the page.
   */
  private readonly chosenRow = computed((): Record<string, string> | undefined => {
    const ref = this.singleEnumResourceRef();
    return ref?.hasValue() ? (ref.value() as Record<string, string>) : undefined;
  });

  /** Whether the read of the chosen row was refused or failed. */
  private readonly chosenRowFailed = computed((): boolean => this.singleEnumResourceRef()?.status() === 'error');

  /** Every row of a whole source, read in one request; nothing on a paged source, and nothing of a source closed to the person. */
  enumResourceRef = computed(() => {
    if (this.showField() === false || !this.resource() || this.paged() || this.sourceClosed()) return undefined;
    const enumeratedMeta = this.resourceMeta(this.resource() as Resource);
    const config = this.picker();
    return this.store.resourceList(
      signal(enumeratedMeta.route),
      signal(this.optionFilter()),
      this.columns,
      signal(config.disableCacheForFilterPii || filterReachesPII(this.optionFilter(), this.sourceMeta())),
      this.sorts,
    );
  });

  /**
   * The paged source's request while the picker is open for editing: the page the panel
   * shows, in the picker's sort. Nothing in view mode, where the chosen row is read by
   * key, and nothing of a source closed to the person.
   */
  private pageRequest = computed((): PagedListRequest | undefined => {
    const descriptor = this.descriptor();
    if (
      !this.paged() ||
      !descriptor ||
      this.showField() === false ||
      this.editMode() !== 'edit' ||
      this.sourceClosed()
    ) {
      return undefined;
    }
    const config = this.picker();
    const configured = config.sorts.map((sort) => ({ field: sort.field as string, direction: sort.direction }));
    return {
      route: this.resourceMeta(this.resource() as Resource).route,
      filter: narrowedFilter(this.optionFilter(), this.narrowing(), this.displayColumn(), this.query()),
      sensitive: this.sensitive(),
      columns: this.columns(),
      sorts: pickerSort<Record<string, unknown>>(descriptor, configured, this.displayColumn()),
    };
  });

  /** The pager over a paged source, following pageRequest; nothing on a whole source. */
  pager = computed(() => (this.paged() ? this.store.resourcePage(this.pageRequest) : undefined));

  /** Whether the list the picker asked for, whole or a page of it, stands refused as not permitted. */
  private readonly listRefused = computed((): boolean => {
    const pager = this.pager();
    if (pager) {
      return pager.status() === 'error' && notPermitted(pager.error());
    }
    const list = this.enumResourceRef();
    return list !== undefined && list.status() === 'error' && notPermitted(list.error());
  });

  /** Whether the read of the chosen row stands refused as not permitted. */
  private readonly rowRefused = computed((): boolean => {
    const row = this.singleEnumResourceRef();
    return row !== undefined && row.status() === 'error' && notPermitted(row.error());
  });

  /**
   * What is closed to the person, as the field shows it: the source (nothing to pick
   * from, the stored key read-only), the row the field holds (the stored key, and still a
   * picker while edited), or nothing. A refusal the server has just answered counts at
   * once, before it is remembered and its reader goes idle.
   */
  closedTo = computed((): 'source' | 'row' | undefined => {
    if (this.sourceClosed() || this.listRefused()) {
      return 'source';
    }
    return this.rowClosed() || this.rowRefused() ? 'row' : undefined;
  });

  /**
   * Every option a whole source answered, in the mode's display; none while the list is
   * out, and none when it failed, which the field says under itself.
   */
  private allEnumOptions = computed((): PickerOption[] => {
    const list = this.enumResourceRef();
    const records = list?.hasValue() ? list.value() : undefined;
    if (!records || !records.length) return [];
    return records.map((record) => this.toEnumerated(record as Record<string, string>));
  });

  /** The open page's rows on a paged source, in the mode's display. */
  private pageOptions = computed((): PickerOption[] =>
    (this.pager()?.rows() ?? []).map((record) => this.toEnumerated(record as Record<string, string>)),
  );

  singleEnumDisplayText = computed(() => {
    const showField = this.showField();
    if (showField === false) {
      return undefined;
    }

    const form = this.form();
    const fieldConfig = this.fieldConfig();
    if (form === undefined || fieldConfig === undefined) {
      return defaultEmptyFieldValue;
    }
    if (this.multiple()) {
      const picked = this.chosenOptions();
      return picked.length > 0 ? picked.map((option) => option.display).join(', ') : defaultEmptyFieldValue;
    }
    const value = form.get(fieldConfig.name)?.value as string | null | undefined;
    const fixed = this.enumeration();
    if (fixed) {
      return fixed.find((option) => option.id === value)?.display ?? defaultEmptyFieldValue;
    }
    // Nothing names what is stored where the source is closed to the person: its key shows.
    if (this.closedTo() === 'source') {
      return value ? String(value) : defaultEmptyFieldValue;
    }
    // A whole source holds every row in its list: the display comes from there, and a
    // value the list does not hold shows as itself.
    if (!this.paged()) {
      return value ? displayFromOptions(this.allEnumOptions(), value).display : defaultEmptyFieldValue;
    }
    // A row the person may not read, or one whose read failed, shows as its key, beside
    // what the field says under itself.
    if (this.closedTo() === 'row' || this.chosenRowFailed()) {
      return value ? String(value) : defaultEmptyFieldValue;
    }
    const record = this.chosenRow();
    if (record === undefined) {
      return defaultEmptyFieldValue;
    }

    const enumeratedValues = this.toEnumerated(record);
    return enumeratedValues.display;
  });

  hasRequiredValidator = computed(() => {
    const form = this.form();
    const fieldConfig = this.fieldConfig();

    if (form === undefined || fieldConfig === undefined) {
      return false;
    }

    const control = form.get(fieldConfig.name);
    if (!control) {
      return false;
    }

    return control.hasValidator(Validators.required);
  });

  /**
   * The options behind the picked keys of a field that takes several: a fixed value's
   * own display, a whole source's row, and a key the source does not hold as itself.
   */
  private readonly chosenOptions = computed((): PickerOption[] => {
    const keys = this.chosenKeys();
    const fixed = this.enumeration();
    if (fixed) {
      return keys.map((key) => {
        const option = fixed.find((candidate) => candidate.id === key);
        return option ? toOption(option) : { id: key, display: key };
      });
    }
    const options = this.allEnumOptions();
    return keys.map((key) => displayFromOptions(options, key));
  });

  singleEnumValue = computed((): PickerOption[] | undefined => {
    if (this.showField() === false) return undefined;
    if (this.multiple()) return this.chosenOptions();
    const currentValue = this.form().get(this.fieldConfig().name)?.value;
    const fixed = this.enumeration();
    if (fixed) {
      return fixed.filter((option) => option.id === currentValue).map(toOption);
    }
    if (!currentValue) return [];
    if (!this.paged()) {
      return [displayFromOptions(this.allEnumOptions(), String(currentValue))];
    }
    if (this.closedTo() === 'row' || this.chosenRowFailed()) {
      return [{ id: String(currentValue), display: String(currentValue) }];
    }
    const record = this.chosenRow();
    if (!record || Object.keys(record).length === 0) return [];
    return [this.toEnumerated(record)];
  });

  listEnumValues = computed((): PickerOption[] => {
    if (this.showField() === false) return [];
    const fixed = this.enumeration();
    if (fixed) {
      return fixed.map(toOption);
    }
    if (this.paged()) {
      // The page's rows, with the chosen row ahead of them when this page does not hold it.
      return withChosen(this.pageOptions(), this.singleEnumValue()?.[0]);
    }
    const options = this.allEnumOptions();
    if (!options.length) return this.singleEnumValue() || [];
    return options;
  });

  // The searchable autocomplete narrows a whole source's loaded options in the browser,
  // every typed word found in the display, the id (so an identifier can be pasted), or
  // a search field of the row. A paged source renders the paged panel, narrowed by the
  // server, and never narrows here.
  availableEnumOptions = computed(() => {
    const editMode = this.editMode() === 'edit';
    const loaded = editMode ? this.listEnumValues() : this.singleEnumValue();
    const options = editMode && loaded && !this.paged() ? matchOptions(loaded, this.query()) : loaded;
    const meta = this.fieldMeta();
    const metaRequired = 'required' in meta && meta.required;

    if (
      editMode &&
      !this.multiple() &&
      !this.hasRequiredValidator() &&
      options &&
      options.length > 0 &&
      !metaRequired
    ) {
      return [{ id: null, display: defaultEmptyFieldValue }, ...options];
    }

    return options;
  });

  /** The pager's label inside a paged picker's panel: this page's range of the total. */
  pagerLabel = computed(() => {
    const pager = this.pager();
    if (!pager) return '';
    const page = pager.page();
    return pageLabel(page.offset, pager.rows().length, page.total);
  });

  /**
   * The one sentence under the field when something is closed to the person, in the
   * library's own words: the source (with nothing stored, only while the field is
   * edited, where it says nothing can be chosen), or the row the field holds. While the
   * field is edited the open page may hold that row, and then its name shows and nothing
   * is said: the sentence is for a key shown as itself.
   */
  notice = computed((): string | undefined => {
    const closed = this.closedTo();
    if (closed === 'source') {
      const stored = this.chosenValue() !== '' || this.chosenKeys().length > 0;
      return stored || this.editMode() === 'edit' ? closedSourceText(stored) : undefined;
    }
    if (closed !== 'row') {
      return undefined;
    }
    if (this.editMode() === 'edit') {
      const held = this.pageOptions().some((option) => option.id === this.chosenValue());
      if (held || this.pager()?.status() === 'loading') {
        return undefined;
      }
    }
    return closedRecordText;
  });

  /**
   * What the picker says when a request of its own failed for a reason that is no
   * permission's, in the library's own words; nothing while it works, and nothing for a
   * refusal of permission, which notice says. A paged source has two requests: the page
   * of options, and the read of the chosen row. Of a list that failed it says what the
   * field then shows: a form that is read shows the value it holds as its id, and a field
   * that is edited is a picker with nothing in it to choose from.
   */
  refusal = computed((): string | undefined => {
    const showsId = this.editMode() !== 'edit' && (this.chosenValue() !== '' || this.chosenKeys().length > 0);
    const pager = this.pager();
    if (pager) {
      if (pager.status() === 'error') {
        return pickerRefusal(pager.error(), 'list', showsId);
      }
      return this.chosenRowFailed()
        ? pickerRefusal(this.singleEnumResourceRef()?.error(), 'record', showsId)
        : undefined;
    }
    const ref = this.enumResourceRef();
    return ref && ref.status() === 'error' ? pickerRefusal(ref.error(), 'list', showsId) : undefined;
  });

  /** The refusals and failures already written to the console, so each is written once. */
  private logged = new WeakSet<object>();

  constructor() {
    super();
    // What the server said of a request of the picker's that it refused, or how the request
    // failed, is for whoever looks into it: it is written to the console, once, with the
    // resource and the read it belongs to. The field says it in the library's words.
    effect(() => {
      const list = this.pager() ?? this.enumResourceRef();
      const row = this.singleEnumResourceRef();
      const failed: ['list' | 'record', unknown][] = [
        ['list', list?.status() === 'error' ? list.error() : undefined],
        ['record', row?.status() === 'error' ? row.error() : undefined],
      ];
      for (const [read, error] of failed) {
        if (error === undefined || error === null) {
          continue;
        }
        if (typeof error === 'object') {
          if (this.logged.has(error)) {
            continue;
          }
          this.logged.add(error);
        }
        console.error(pickerConsoleLine(String(untracked(this.resource) ?? ''), read), error);
      }
    });
    // What the server refused as not permitted is remembered, so the picker asks nothing
    // more of a source the person may not list and never again for a row they may not
    // read. The readers go idle once it is, which is why it is kept here and not read
    // from them.
    effect(() => {
      if (this.listRefused()) {
        this.listRefusedFor.set(untracked(this.sourceKey));
      }
      if (this.rowRefused()) {
        const key = untracked(this.rowKey);
        this.rowsRefused.update((refused) => (refused.has(key) ? refused : new Set(refused).add(key)));
      }
    });
  }

  toEnumerated(resource: Record<string, string>): PickerOption {
    const enumeratedConfig = this.picker();
    const displayFields =
      this.editMode() === 'edit' && enumeratedConfig.listDisplay.length > 0
        ? enumeratedConfig.listDisplay
        : enumeratedConfig.viewDisplay;
    const concat =
      this.editMode() === 'edit'
        ? enumeratedConfig.listConcatFn || enumeratedConfig.viewConcatFn
        : enumeratedConfig.viewConcatFn;

    const concatFunction = concatFunctions[concat] || hyphenConcat;

    const key = resource[this.keyField()] ?? '';
    const option: PickerOption = {
      id: key,
      display: optionDisplay(resource, key, displayFields, concatFunction),
    };
    if (enumeratedConfig.searchable && !this.paged()) {
      option.text = this.ownSearchFields().map((field) => String(resource[field] ?? ''));
    }
    return option;
  }

  getDisplayText = (value: string | Record<string, string> | null): string => {
    if (!value) return '';

    if (typeof value === 'string') {
      const option = this.availableEnumOptions()?.find((o) => o.id === value);
      // An id with no matching option displays as itself rather than blanking the field.
      return option ? option.display : value;
    }

    return value['display'] || this.formatDisplay(value);
  };

  private formatDisplay(resource: Record<string, string>): string {
    return this.toEnumerated(resource).display;
  }

  search(value: string): void {
    this.query.set(value);
  }

  /** The paged panel opened: the box takes the focus, so typing narrows at once. */
  panelOpened(opened: boolean): void {
    if (opened && this.narrowing()) {
      setTimeout(() => this.searchBox()?.focus());
    }
  }

  ngOnInit(): void {
    // A searchable picker over a paged source nothing narrows is a configuration error,
    // raised here naming the resource and the DDL way out, before anything is requested.
    // The judgment is the metadata's alone: a caller whose grants leave the box no field
    // gets a panel with no box, never an error.
    const resource = this.resource();
    const meta = this.sourceMeta();
    if (
      resource &&
      this.picker().searchable &&
      this.paged() &&
      pickerNarrowing(meta, this.displayColumn(), meta?.searchFields ?? []) === undefined
    ) {
      refuseUnsearchablePicker(resource, this.displayColumn());
    }
  }

  select(value: string | string[]): void {
    this.form().patchValue({ [this.fieldConfig().name]: value });
    this.form().markAsDirty();
    this.form().markAsTouched();
    this.reloadSignal.update((prev) => !prev);
  }
}

/** A fixed enumeration value in the shape the option list and display helpers share. */
function toOption(option: EnumerationOption): PickerOption {
  return { id: option.id, display: option.display };
}
