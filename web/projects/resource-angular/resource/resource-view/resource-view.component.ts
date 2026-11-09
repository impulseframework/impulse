import { CommonModule, Location } from '@angular/common';
import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  Injector,
  input,
  model,
  OnInit,
  output,
  signal,
  WritableSignal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { AbstractControl, FormControl, FormGroup, FormsModule, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatDividerModule } from '@angular/material/divider';
import { MatExpansionPanel, MatExpansionPanelHeader, MatExpansionPanelTitle } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { ActivatedRoute, Router, RouterModule } from '@angular/router';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { sparseFormData } from '@impulseframework/resource-angular/forms';
import {
  AlertType,
  DataType,
  FieldElement,
  ListViewConfig,
  RecordData,
  Resource,
  RESOURCE_META,
  RootConfig,
  RPCConfig,
  ViewConfig,
  writeResource,
} from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { ApiError } from '@impulseframework/resource';
import { tap } from 'rxjs';
import {
  ActionAccessControlWrapperComponent,
  ActionButtonContext,
} from '../actions/action-button-smart/action-access-control-wrapper.component';
import { RpcButtonComponent } from '../actions/rpc-button/rpc-button.component';
import { DeleteResourceConfirmationModalComponent } from '../delete-resource-confirmation-modal/delete-resource-confirmation-modal.component';
import { civildateCoercion, flattenElements } from '../gui-constants';
import { ResourceCreateComponent } from '../resource-create/resource-create.component';
import { ResourceLayoutComponent } from '../resource-layout/resource-layout.component';
import { ResourceStore } from '../resource-store.service';
import { patchFromForm } from '@impulseframework/resource-angular/resource-client';
import { maxLengthValidator, metadataTypeCoercion, unchangedFieldWarning } from '../resources-helpers';

@Component({
  selector: 'impulse-resource-view',
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
    MatExpansionPanel,
    MatExpansionPanelHeader,
    MatExpansionPanelTitle,
    RpcButtonComponent,
    CommonModule,
    ActionAccessControlWrapperComponent,
    ResourceCreateComponent,
  ],
  templateUrl: './resource-view.component.html',
  styleUrl: './resource-view.component.scss',
})
/**
 * One row as a form. The view provides no store of its own: it injects the nearest
 * ResourceStore. Inside the compound page's primary slot that is the page's store, so
 * the form, the capabilities, the patch, the remove, and the reload all run against the
 * row every other child of the page reads, and a save refreshes them all. A view over a
 * different row (the compound's related-row branch, or one an application places by
 * hand) sits on an element carrying `impulseRowStore`, which provides its own store; a view
 * with no store in scope fails at construction naming ResourceStore.
 */
export class ResourceViewComponent implements OnInit {
  resourceMeta = inject(RESOURCE_META);
  location = inject(Location);
  router = inject(Router);
  activatedRoute = inject(ActivatedRoute);
  notifications = inject(NotificationService);
  store = inject(ResourceStore);
  injector = inject(Injector);
  destroyRef = inject(DestroyRef);
  formState = inject(FormStateService);
  dialog = inject(MatDialog);

  editMode: WritableSignal<'edit' | 'view'> = signal('view');
  uuid = input.required<string>();
  config = input.required<ViewConfig | ListViewConfig>();
  relatedData = input<RecordData>({});
  compoundResourceView = input<boolean>(false);
  isDirty = signal(false);

  displayFormInvalidMessage = signal<boolean>(false);
  deleted = output<boolean>();
  navAfterDelete = input(true);

  showCreateForm = model(false);
  /**
   * What a view that could not load says in place of its form: the server's own
   * message when the read was refused or failed. A refusal must never render as an
   * empty record.
   */
  viewErrorMessage = computed<string | undefined>(() => {
    const error = this.store.viewError();
    if (error instanceof ApiError && error.status === 403) {
      return `This record is not available to you: ${error.message}`;
    }
    if (error) {
      return `This record could not be loaded: ${error instanceof Error ? error.message : String(error)}`;
    }
    return undefined;
  });

  createConfig = computed(() => {
    if (Object.keys(this.config().createConfig || {}).length > 0) {
      return this.config().createConfig;
    }
    return this.config();
  });

  rootConfig = computed(() => {
    return this.activatedRoute.snapshot.data['config'] as RootConfig;
  });

  showCloseButton = computed(() => {
    const config = this.config();
    const showBackButton = config.showBackButton ?? true;
    if (config) {
      return showBackButton && config.primaryResource === this.rootConfig().parentConfig.primaryResource;
    }
    return showBackButton;
  });

  commonButtonConfig = computed(() => {
    const config = this.config();

    if (config === undefined) {
      return undefined;
    }

    if (config.shouldRenderActions === undefined) {
      return undefined;
    }

    return {
      meta: this.store.resourceMeta(),
      resourceData: this.relatedData(),
      config,
    };
  });

  editButtonContext = computed(() => {
    const commonConfig = this.commonButtonConfig();

    if (commonConfig === undefined) {
      return undefined;
    }

    return {
      actionType: 'edit',
      meta: commonConfig.meta,
      capabilities: this.store.viewCapabilities(),
      resourceData: commonConfig.resourceData,
      shouldRender: commonConfig.config.shouldRenderActions.edit,
    } satisfies ActionButtonContext;
  });

  deleteButtonContext = computed(() => {
    const commonConfig = this.commonButtonConfig();

    if (commonConfig === undefined) {
      return undefined;
    }

    return {
      actionType: 'delete',
      meta: commonConfig.meta,
      capabilities: this.store.viewCapabilities(),
      resourceData: commonConfig.resourceData,
      shouldRender: (data: RecordData): boolean => commonConfig.config.shouldRenderActions.delete(data),
    } satisfies ActionButtonContext;
  });

  inlineRpcConfigs = computed(() => {
    const config = this.config();
    if (config === undefined) {
      return [];
    }

    const rpcConfigs = config.rpcConfigs?.filter((rpc: RPCConfig) => rpc.placement === 'inline');

    return rpcConfigs?.map((config) => {
      return {
        config,
        context: {
          actionType: 'rpc',
          method: config.method,
          shouldRender: config.shouldRender,
          resourceData: this.relatedData(),
        } satisfies ActionButtonContext,
      };
    });
  });

  endRpcConfigs = computed(() => {
    const config = this.config();

    if (config === undefined) {
      return [];
    }

    const rpcConfigs = config.rpcConfigs?.filter((rpc: RPCConfig) => rpc.placement === 'end');

    return rpcConfigs?.map((config) => {
      return {
        config,
        context: {
          actionType: 'rpc',
          method: config.method,
          shouldRender: config.shouldRender,
          resourceData: this.relatedData(),
        } satisfies ActionButtonContext,
      };
    });
  });

  useExpansionPanel = computed(() => {
    const config = this.config();
    // TODO: Investigate why we're doing this check, it's weird because viewType is not part of the input configs
    if (config && 'viewType' in config && config.viewType !== 'View') {
      return false;
    }

    return config.collapsible || !this.compoundResourceView();
  });

  resourceConfigRouteSnapshot = computed(() => {
    return (
      this.config() ||
      (this.activatedRoute.snapshot.data['config'] as RootConfig)?.parentConfig ||
      ({} as ViewConfig | ListViewConfig)
    );
  });

  emptyFormGroup = new FormGroup({});

  form = computed(() => {
    const meta = this.store.resourceMeta();
    const resourceData = this.store.viewData();
    const config = this.config();

    if (meta === undefined || resourceData === undefined || config === undefined) {
      return this.emptyFormGroup;
    }

    const fg = new FormGroup({});
    const pristineValues: Record<string, DataType | null> = {};
    const allElements = flattenElements(config.elements);

    for (const field of meta.fields || []) {
      const isFieldNameRegistered = fg.get(field.fieldName) !== null;
      if (isFieldNameRegistered) {
        continue;
      }

      const findConfig = allElements.find((element) => element.type === 'field' && element.name === field.fieldName);
      const fieldConfig = findConfig as FieldElement | undefined;
      if (!fieldConfig) {
        continue;
      }

      let value: DataType | null = null;
      const stringValue = resourceData?.[field.fieldName] ? String(resourceData[field.fieldName]) : '';

      if (field.displayType === 'civildate' && stringValue) {
        value = civildateCoercion(stringValue);
      } else if (resourceData[field.fieldName] !== undefined) {
        value = resourceData[field.fieldName];
      }

      const control = new FormControl(value);

      if (fieldConfig.validators.length > 0) {
        control.setValidators(fieldConfig.validators);
      }
      const maxLength = maxLengthValidator(field);
      if (maxLength) {
        control.addValidators(maxLength);
      }

      fg.addControl(field.fieldName, control);
      pristineValues[field.fieldName] = value;
    }
    this.pristineFormValues = pristineValues;

    return fg;
  });

  pristineFormValues: Record<string, DataType | null> = {};

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


  ngOnInit(): void {
    // The view reads and writes the resource whose row it shows: the listed resource,
    // or its table when the list is a view declaring one (rowsOf).
    const primary = this.config().primaryResource as Resource;
    const resource = writeResource(primary, this.resourceMeta(primary));
    if (this.resourceMeta(resource)) {
      this.store.resourceName.set(resource);
      this.store.resourceMeta.set(this.resourceMeta(resource));
    }

    this.inlineRpcConfigs();
    this.endRpcConfigs();
  }

  setEditMode(mode: 'edit' | 'view'): void {
    if (mode === 'view') {
      const changeData = sparseFormData(this.form(), this.pristineFormValues);
      if (Object.keys(changeData).length !== 0) {
        this.notifications.addGlobalNotification({
          message: 'You have unsaved changes.',
          link: '',
          type: AlertType.ERROR,
        });
        return;
      }
      this.editMode.set('view');
    } else if (mode === 'edit') {
      this.editMode.set('edit');
    }
  }

  /**
   * Raises one warning per field the person did not change whose value fails this
   * form's rules, naming the field by its label and the rule it fails (see
   * unchangedFieldWarning), through the same notifications the save's refusals use.
   */
  private warnAboutUnchangedInvalidFields(changed: string[]): void {
    const labels = new Map<string, string>();
    for (const element of flattenElements(this.config().elements)) {
      if (element.type === 'field') {
        labels.set(element.name, element.label);
      }
    }
    for (const name of Object.keys(this.form().controls)) {
      const errors = this.form().get(name)?.errors;
      if (changed.includes(name) || !errors) {
        continue;
      }
      this.notifications.addGlobalNotification({
        message: unchangedFieldWarning(labels.get(name) || name, errors),
        link: '',
        type: AlertType.INFO,
      });
    }
  }

  /** The names of the fields whose values differ from the viewed row's: the fields the patch carries. */
  private changedFieldNames(): string[] {
    return Object.keys(sparseFormData(this.form(), this.pristineFormValues));
  }

  /** The controls of the changed fields (see changedFieldNames). */
  private changedControls(): AbstractControl[] {
    return this.changedFieldNames()
      .map((name) => this.form().get(name))
      .filter((control) => control !== null);
  }

  /**
   * Whether a changed field fails this form's rules: what refuses a save, and what the
   * refusal's message follows, so the message clears once the changed fields pass,
   * whatever an untouched field holds. displayFormInvalidMessage says a save was refused.
   */
  changedFieldsInvalid(): boolean {
    return this.changedControls().some((control) => control.invalid);
  }

  saveForm(): void {
    // The patch carries only the fields whose values differ from the viewed row's, and
    // the server checks only those, so only they must pass this form's rules. A field the
    // person did not change does not block the save when its value fails them: the value
    // is one this build cannot produce but a newer release allows (a longer limit), or one
    // the page config requires and the row lacks (a field an RPC emptied), and either way
    // it is not this save's to fix. The view warns about it instead.
    if (this.changedFieldsInvalid()) {
      this.displayFormInvalidMessage.set(true);
      for (const control of this.changedControls()) {
        control.markAsTouched();
      }
      return;
    }

    this.displayFormInvalidMessage.set(false);
    this.warnAboutUnchangedInvalidFields(this.changedFieldNames());

    const resourceMeta = this.store.resourceMeta();
    if (!resourceMeta) {
      return;
    }

    // The client's changes() diffs the coerced raw form value against the viewed
    // row and refuses a diff outside the patchable fields — never a silent drop.
    const coercedRawValue = metadataTypeCoercion(this.form().getRawValue(), resourceMeta);
    // A write-only field is never in the row, so it has no pristine value to differ
    // from: an empty input is nothing typed and leaves the after image, where null
    // would read as a clear; a typed value is the patch.
    for (const field of resourceMeta.fields ?? []) {
      const value = coercedRawValue[field.fieldName];
      if (field.writeOnly && (value === null || value === '')) {
        delete coercedRawValue[field.fieldName];
      }
    }
    let operation;
    try {
      operation = patchFromForm(this.store.handle(), this.store.viewData(), { getRawValue: () => coercedRawValue });
    } catch (error) {
      this.notifications.addGlobalNotification({
        message: error instanceof Error ? error.message : String(error),
        link: '',
        type: AlertType.ERROR,
      });
      return;
    }

    this.pristineFormValues = this.form().getRawValue();

    if (!operation) {
      return;
    }
    void this.store.apply([operation], `${this.store.resourceName()} updated successfully`).then(() => {
      this.form().markAsPristine();
      this.setEditMode('view');
      this.formState.decrementDirtyForms();
      this.store.reloadViewData();
    });
  }

  resetForm(): void {
    this.form().reset();
    this.form().patchValue(this.pristineFormValues);
    this.setEditMode('view');
    if (this.form().dirty) {
      this.formState.decrementDirtyForms();
    }
  }

  confirmDeleteResource(): void {
    const existingDialog = this.dialog.openDialogs.find(
      (d) => d.componentInstance instanceof DeleteResourceConfirmationModalComponent,
    );

    const dialogRef =
      existingDialog ?? this.dialog.open(DeleteResourceConfirmationModalComponent, { delayFocusTrap: false });

    const result = dialogRef.afterClosed().pipe(
      tap((value) => {
        if (value === true) {
          console.debug('Deleting resource | ', this.config().title, '|', this.relatedId());
          this.deleteResource();
        }
      }),
      takeUntilDestroyed(this.destroyRef),
    );

    result.subscribe();
  }

  deleteResource(): void {
    const handle = this.store.handle();
    const operation = handle.ops.remove(handle.keyOf(this.store.viewData()));

    void this.store.apply([operation], `${this.store.resourceName()} updated successfully`).then(() => {
      this.deleted.emit(true);
      if (!this.compoundResourceView()) {
        return;
      }
      const navAfterDelete = this.navAfterDelete() !== false;
      if (navAfterDelete) {
        this.location.back();
      } else {
        this.showCreateForm.set(true);
      }
    });
  }

  /**
   * The key of the row this view reads. The compound page's primary view is handed the
   * key the page resolved (compoundResourceView); any other view reads the related
   * row's key off `relatedData` when its config relates it by `parentKey`, else its
   * `uuid`. A computed, so the effect below re-runs when the key changes and not when
   * `relatedData` changes identity.
   */
  relatedId = computed((): string => {
    const parentKey = this.config().parentRelation?.parentKey;
    if (!this.compoundResourceView() && parentKey) {
      return String(this.relatedData()[parentKey]);
    }
    return this.uuid();
  });

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

    // The store setup: on a store of its own (impulseRowStore) this is the only setup; on
    // the compound page's shared store it repeats the page's values, and the idempotent
    // build adds no reader.
    effect(() => {
      const id = this.relatedId();
      const create = this.showCreateForm();
      this.store.uuid.set(id);
      this.store.liveChoice.set(this.config().live);
      if (!create) {
        this.store.buildStoreViewData();
      }
    });
  }

  createResource(): void {
    this.showCreateForm.set(false);
    this.store.reloadViewData();
  }
}
