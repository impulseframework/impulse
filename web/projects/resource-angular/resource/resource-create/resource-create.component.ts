import {
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  effect,
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
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { cleanStringForm } from '@impulseframework/resource-angular/forms';
import {
  ChildResourceConfig,
  CreatePermission,
  DataType,
  FieldElement,
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
import { ResourceLayoutComponent } from '../resource-layout/resource-layout.component';
import { ResourceStore } from '../resource-store.service';
import { rendererFor } from '../resource-field/renderer';
import { maxLengthValidator, metadataTypeCoercion } from '../resources-helpers';

/** The display types the create form presents and never takes: their control starts null and a null control leaves the add operation. */
const READ_ONLY_SHAPES = new Set(['bytes', 'array', 'object']);

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

  isDirty = signal(false);
  submitted = signal<boolean>(false);
  complete = output<boolean>();
  resourceConfig = input<ChildResourceConfig>();
  parentData = input<RecordData>({});
  loadCreatedResource = input<boolean>(false);

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
      if (field.displayType === 'boolean') {
        control = new FormControl<boolean>(false);
      }
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
        const booleanDefaultValue =
          fieldDefault?.type == 'static' && typeof fieldDefault?.value === 'boolean' ? fieldDefault.value : null;
        if (booleanDefaultValue === null) {
          console.error(
            `Default value for boolean field, ${field.fieldName}, is null, add a default value to the config`,
          );
        }
        control = new FormControl<boolean | null>(booleanDefaultValue);
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

  saveForm(): void {
    if (!this.form().valid) {
      this.submitted.set(true);
      this.form().markAllAsTouched();
      const formElement = document.getElementById('resource-form');
      const invalidField = formElement?.querySelector('.mdc-text-field--invalid');
      if (invalidField) {
        invalidField.scrollIntoView({ behavior: 'smooth' });
      }
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

    void this.store.apply([operation], `${this.store.resourceName()} created successfully`).then((response) => {
      this.formState.decrementDirtyForms();

      if (!response) {
        this.complete.emit(true);
        return;
      }

      const createIds: string[] =
        response[handle.descriptor.property] ?? response[camelCase(this.store.resourceName())] ?? [];

      {
        if (this.loadCreatedResource() && createIds.length === 1) {
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
    if (this.form().dirty) {
      this.formState.decrementDirtyForms();
    }
    this.complete.emit(true);
  }

  constructor() {
    effect(() => {
      console.debug('USAGE | New FormGroup subscription for: ', this.config().title);

      this.form()
        .valueChanges.pipe(
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
