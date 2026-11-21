import {
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  effect,
  ElementRef,
  inject,
  input,
  OnInit,
  output,
  signal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, FormsModule, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { ActivatedRoute, Router, RouterModule } from '@angular/router';
import { ApiError } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { cleanStringForm } from '@impulseframework/resource-angular/forms';
import {
  ChildResourceConfig,
  CreatePermission,
  DataType,
  FieldDefault,
  FieldElement,
  FieldMeta,
  ListViewConfig,
  RecordData,
  Resource,
  RESOURCE_META,
  ResourceMeta,
  RootConfig,
  ViewConfig,
  writeResource,
} from '@impulseframework/resource-angular/types';
import { pageRouteResolver } from '@impulseframework/resource-angular/resource-nav';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { tap } from 'rxjs';
import { camelCase } from '../concat-fns';
import { flattenElements } from '../gui-constants';
import { refusedSentence } from '../refused-fields';
import { ResourceLayoutComponent } from '../resource-layout/resource-layout.component';
import { ResourceStore } from '../resource-store.service';
import { rendererFor } from '../resource-field/renderer';
import { maxLengthValidator, metadataTypeCoercion } from '../resources-helpers';
import { rowMessage, rowWordsOf } from '../row-words';

/** The display types the create form presents and never takes: their control starts null and a null control leaves the add operation. */
const READ_ONLY_SHAPES = new Set(['bytes', 'array', 'object']);

/**
 * Where a create form starts a yes or no field. What the page's config gives it comes
 * first. With nothing given, a field the resource requires starts unchecked: the resource
 * declares no default for it, an empty box already reads as a no, and a form saved
 * untouched sends that. A field the resource does not require starts at the default the
 * schema declares for it, which the generated metadata carries (`schemaDefault`): the form
 * shows what a create that left the field out would store, and a form saved untouched
 * sends it. Where the schema declares none the form holds nothing for the field, which a
 * box draws unchecked, and sends nothing unless the person answers.
 */
function yesOrNoStart(field: FieldMeta, given: FieldDefault): boolean | null {
  if (given?.type === 'static' && typeof given.value === 'boolean') {
    return given.value;
  }
  if (field.required) {
    return false;
  }
  return field.schemaDefault ?? null;
}

@Component({
  selector: 'impulse-resource-create',
  imports: [
    FormsModule,
    ReactiveFormsModule,
    MatInputModule,
    RouterModule,
    MatButtonModule,
    MatIconModule,
    MatProgressSpinnerModule,
    MatDividerModule,
    ResourceLayoutComponent,
  ],
  templateUrl: './resource-create.component.html',
  styleUrl: './resource-create.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  providers: [ResourceStore],
})
export class ResourceCreateComponent implements OnInit {
  resourceMeta = inject(RESOURCE_META);
  auth = inject(AuthService);
  activatedRoute = inject(ActivatedRoute);
  notifications = inject(NotificationService);
  store = inject(ResourceStore);
  router = inject(Router);
  destroyRef = inject(DestroyRef);
  formState = inject(FormStateService);
  private host = inject<ElementRef<HTMLElement>>(ElementRef);

  /**
   * Whether the form holds something not yet created. While it does, the form is one of
   * the unsaved forms the leave-page question counts.
   */
  isDirty = signal(false);
  submitted = signal<boolean>(false);
  complete = output<boolean>();
  resourceConfig = input<ChildResourceConfig>();
  parentData = input<RecordData>({});
  loadCreatedResource = input<boolean>(false);
  /**
   * Set by the dialog a list's create opens in: the dialog draws the title and the
   * Cancel and create buttons, so the form draws its fields alone, and a create the
   * server refuses is kept in `refusal` for the dialog to show, where on a page it is
   * the page's notice.
   */
  inDialog = input<boolean>(false);
  /** Whether a create is on its way to the server. */
  saving = signal(false);
  /** The server's refusal of the last create in a dialog, in its words; empty while there is none. */
  refusal = signal('');
  /**
   * What the form says about the last create it did not send because it refuses a field, as
   * the form stood when the create was asked for; empty until one is refused.
   */
  incomplete = signal('');
  /**
   * Whether that create left the form nothing to bring into view: every field it refuses
   * is one that is not on it, and the sentence alone tells the person.
   */
  nothingRefusedIsDrawn = signal(false);

  rootConfig = computed(() => {
    return this.activatedRoute.snapshot.data['config'] as RootConfig;
  });

  /** Puts the page's tenant into the route the new row is opened on, where the route names the tenant parameter. */
  private readonly resolvePageRoute = pageRouteResolver();

  /**
   * Whether the form creates under a parent row (a child list's or an array view's
   * create inside a row page): the parent row is on hand. A page's own create carries
   * an empty object, never a row.
   */
  private readonly underParentRow = computed(() => Object.keys(this.parentData()).length > 0);

  config = computed(() => {
    const inputConfig = this.resourceConfig() as ViewConfig;
    if (inputConfig !== undefined) {
      return inputConfig;
    }
    return this.rootConfig().parentConfig as ListViewConfig;
  });

  indentTitle = computed(() => {
    if (this.config().collapsible || this.resourceConfig() === undefined) {
      return false;
    }

    return true;
  });

  /**
   * The digest's field-level Create entries for this resource: the inputs worth
   * rendering. A non-key field without one is denied — no control, no input, and the
   * value never travels. Undefined means the digest carries no field information for
   * Create (denied outright, or a keys-only resource) and nothing narrows. Conditional
   * entries render; the server judges the write.
   */
  creatableFields = computed<ReadonlySet<string> | undefined>(() => {
    const name = this.store.resourceName();
    if (!name) {
      return undefined;
    }
    const fields = Object.keys(this.auth.fieldPermissionStates({ resource: name, permission: CreatePermission }));
    return fields.length > 0 ? new Set(fields) : undefined;
  });

  form = computed(() => {
    const meta = this.store.resourceMeta();
    const fg = new FormGroup({});
    const allElements = flattenElements(this.config().elements);
    const creatable = this.creatableFields();

    for (const field of meta.fields || []) {
      if (fg.get(field.fieldName)) {
        continue;
      }
      if (creatable && !field.primaryKey && !creatable.has(field.fieldName)) {
        continue;
      }

      let control: FormControl<DataType | null> = new FormControl<DataType | null>('');
      // A bytes, array, or object field has no editor yet: its control is null, which
      // the cleaning step before ops.add leaves out, so the add sends no value for it.
      if (READ_ONLY_SHAPES.has(rendererFor(field.displayType))) {
        control = new FormControl<DataType | null>(null);
      }

      const findElement = allElements.find((element) => element.type === 'field' && element.name === field.fieldName);
      const fieldConfig = findElement as FieldElement | undefined;
      if (!fieldConfig) {
        continue;
      }
      const fieldDefault = fieldConfig.default;
      if (field.displayType === 'boolean') {
        control = new FormControl<boolean | null>(yesOrNoStart(field, fieldDefault));
      } else if (field.displayType === 'nullboolean' && field.schemaDefault !== undefined) {
        // A yes or no field that can be empty starts at the schema's default as well; with
        // none declared it starts empty.
        control = new FormControl<boolean | null>(yesOrNoStart(field, fieldDefault));
      }

      if (fieldDefault?.type === 'foreignKey' && this.parentData()) {
        const parentValue = this.parentData()[fieldDefault.parentId];
        if (parentValue !== undefined) {
          control.setValue(parentValue);
        }
      } else if (fieldDefault?.type == 'static') {
        const staticDefault = fieldDefault;
        if (staticDefault.value) {
          control.setValue(staticDefault.value);
        }
      }

      control.setValidators([]);
      if (fieldConfig.validators.length > 0) {
        control.addValidators(fieldConfig.validators);
      }

      if (field.required && !control.hasValidator(Validators.required)) {
        control.addValidators(Validators.required);
      }
      const maxLength = maxLengthValidator(field);
      if (maxLength) {
        control.addValidators(maxLength);
      }
      fg.addControl(field.fieldName, control);
    }
    return fg;
  });

  route = computed(() => {
    const meta = this.store.resourceMeta();
    if (!meta) return '';
    return meta.consolidatedRoute || meta.route;
  });

  primaryKeys = computed(() => {
    const meta = this.store.resourceMeta();
    if (!meta) return [];
    return meta.fields
      .filter((field) => field.primaryKey)
      .sort((a, b) => a.primaryKey!.ordinalPosition - b.primaryKey!.ordinalPosition);
  });

  hasRequiredPrimaryKey = computed(() => {
    const meta = this.store.resourceMeta();
    if (!meta) {
      return false;
    }
    return meta.fields.some((field) => field.primaryKey && field.required);
  });

  camelCaseToTitlePipe = new CamelCaseToTitlePipe();

  /**
   * The words the created row goes by: the listed resource's, as its metadata carries
   * them. The message after a create names the row by them.
   */
  rowWords = computed(() => rowWordsOf(this.resourceMeta(this.config().primaryResource as Resource)));

  ngOnInit(): void {
    // The form is built for the resource the create goes into: the listed resource,
    // or its table when the list is a view declaring one (rowsOf).
    const primary = this.config().primaryResource as Resource;
    const resource = writeResource(primary, this.resourceMeta(primary));
    if (this.resourceMeta(resource)) {
      this.store.resourceName.set(resource);
      this.store.resourceMeta.set(this.resourceMeta(resource));
    }
  }

  /**
   * Brings the first field the form refuses into view, in whatever scrolls around the
   * form (the frame's page, a dialog): the field with its label and the line under it
   * that says what is wrong, in the middle of the room, clear of a row the page keeps at
   * its top. The field is found among this form's own elements, by the marks Angular's
   * forms keep: `ng-invalid` on what does not hold, and `ng-touched` or `ng-untouched`
   * on every element they keep a mark on at all. An element that carries the form's
   * group is marked for the whole form, whatever its own control holds, and a text, a
   * number and a date field carry it on their `mat-form-field`: a field that is filled
   * is marked as refused while another is not. So a refused control is a marked element
   * that holds no other element of the form's. A field that is not shown keeps its control
   * in the form, inside an element the library's stylesheet takes off the screen
   * (`hidden-field`), and it can be the first one refused: there is nothing of it to bring
   * into view, so the field brought into view is the first refused one that is drawn.
   * The answer is whether there was one.
   */
  private showFirstRefused(): boolean {
    const marked = Array.from(this.host.nativeElement.querySelectorAll('form .ng-invalid'));
    const control = marked.find(
      (element) =>
        element.querySelector('.ng-touched, .ng-untouched') === null && element.closest('.hidden-field') === null,
    );
    const field = control?.closest('mat-form-field') ?? control;
    field?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    return field !== undefined;
  }

  /**
   * What the form says about a create it does not send: the sentence every form of the
   * library says about the fields it refuses (see `refusedSentence`), over the fields that
   * do not hold. One that is not on the form is named by its label.
   */
  private refusedSentence(): string {
    return refusedSentence(
      this.host.nativeElement,
      this.config().elements,
      (name) => this.form().get(name)?.invalid === true,
    );
  }

  saveForm(): void {
    if (!this.form().valid) {
      this.submitted.set(true);
      this.form().markAllAsTouched();
      this.incomplete.set(this.refusedSentence());
      this.nothingRefusedIsDrawn.set(!this.showFirstRefused());
      return;
    }

    const resourceMeta = this.store.resourceMeta();
    if (!resourceMeta) {
      return;
    }
    const cleanedForm = cleanStringForm<Record<string, string>>(this.form());
    if (cleanedForm === undefined || cleanedForm === null) {
      return;
    }
    const coercedCleanedData = metadataTypeCoercion(cleanedForm, this.store.resourceMeta());
    // Where the new row opens, settled before the create is sent: a route naming the
    // tenant parameter with no tenant known is refused here, and nothing is created.
    const createdRoute = this.loadCreatedResource() ? this.createdRowRoute(resourceMeta) : '';

    // ops.add lifts key fields out of the value into the operation path (all key
    // fields or none), so client-assigned keys travel as path segments and a
    // server-generated key is simply absent.
    const handle = this.store.handle();
    const operation = handle.ops.add(coercedCleanedData);

    this.refusal.set('');
    this.saving.set(true);
    void this.store
      .apply([operation], rowMessage(this.rowWords(), 'added', this.store.resourceName()))
      .then((response) => {
        // Created: nothing in the form is unsaved any more.
        this.form().markAsPristine();

        if (!response) {
          this.complete.emit(true);
          return;
        }

        // The consolidated route keys the new rows' ids by resource. A route of the
        // resource's own answers them under `ids` (`iDs` on a server from before the key
        // changed), as the client's own create reads them.
        const createIds: string[] =
          response[handle.descriptor.property] ??
          response[camelCase(this.store.resourceName())] ??
          response.ids ??
          response.iDs ??
          [];

        {
          if (this.loadCreatedResource() && createIds.length === 1) {
            // The dialog closes before the new row's page opens under it.
            if (this.inDialog()) {
              this.complete.emit(true);
            }
            const navigationRoutes = this.config().createNavigation;
            if (navigationRoutes.length === 0) {
              this.router.navigate([createdRoute, createIds[0]]);
            } else {
              if (createIds[0]) {
                navigationRoutes.push(createIds[0]);
              }
              this.router.navigate(navigationRoutes);
            }
          } else {
            this.complete.emit(true);
          }
        }
      })
      .catch((error: unknown) => {
        // On a page a refused create is left to the error handler, which raises the
        // page's notice. A dialog covers that notice, so the form keeps the server's
        // words for the dialog to show and stays as it was filled.
        if (!this.inDialog() || !(error instanceof ApiError)) {
          throw error;
        }
        this.refusal.set(error.message);
      })
      .finally(() => {
        this.saving.set(false);
      });
  }

  /**
   * The route the new row opens on. The page's own create opens it on the page's route
   * as the config wrote it, the tenant put in where the route names the parameter; a
   * create under a parent row, or on a page naming no route, opens it on the resource's
   * metadata route.
   */
  private createdRowRoute(resourceMeta: ResourceMeta): string {
    const page = this.underParentRow() ? undefined : this.rootConfig().routeData.route;
    return this.resolvePageRoute(page || resourceMeta.route);
  }

  cancelForm(): void {
    // What was typed is dropped, so nothing in the form is unsaved any more.
    this.form().markAsPristine();
    this.complete.emit(true);
  }

  constructor() {
    effect(() => {
      console.debug('USAGE | New FormGroup subscription for: ', this.config().title);

      // The form counts as unsaved exactly while it is dirty, whichever way it got
      // there: a typed input changes the value, a picker writes the value and marks
      // the form afterwards, and a create or a cancel marks it pristine again. Every
      // one of those is an event of the form, where a change of value is only the first.
      this.form()
        .events.pipe(
          tap(() => {
            const dirty = this.form().dirty;

            if (dirty !== this.isDirty()) {
              this.isDirty.set(dirty);
              if (dirty) {
                this.formState.incrementDirtyForms();
              } else {
                this.formState.decrementDirtyForms();
              }
            }
          }),
          takeUntilDestroyed(this.destroyRef),
        )
        .subscribe();
    });
  }
}
