import { NgTemplateOutlet } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  HostBinding,
  inject,
  input,
  Signal,
  untracked,
} from '@angular/core';
import { FormGroup, Validators } from '@angular/forms';
import { MatFormFieldModule } from '@angular/material/form-field';
import {
  DataType,
  FieldElement,
  FieldMeta,
  Meta,
  RecordData,
  validatorsPresent,
  ValidDisplayTypes,
} from '@impulseframework/resource-angular/types';
import { EmptyReadonlyFieldComponent } from '../empty-readonly-field/empty-readonly-field.component';
import { ResourceStore } from '../resource-store.service';
import { maxLengthValidator } from '../resources-helpers';
import { ArrayFieldComponent } from './fields/array-field/array-field.component';
import { BooleanFieldComponent } from './fields/boolean-field/boolean-field.component';
import { BytesFieldComponent } from './fields/bytes-field/bytes-field.component';
import { DateFieldComponent } from './fields/date-field/date-field.component';
import { EnumeratedFieldComponent } from './fields/enumerated-field/enumerated-field.component';
import { NullBooleanFieldComponent } from './fields/nullboolean-field/nullboolean-field.component';
import { NumberFieldComponent } from './fields/number-field/number-field.component';
import { ObjectFieldComponent } from './fields/object-field/object-field.component';
import { TextFieldComponent } from './fields/text-field/text-field.component';
import { FieldRenderer, rendererFor } from './renderer';

/** The renderers that present a value and never take one: their field is in view mode whatever the form's mode. */
const READ_ONLY_RENDERERS: ReadonlySet<FieldRenderer> = new Set<FieldRenderer>(['bytes', 'array', 'object']);

@Component({
  selector: 'impulse-resource-field',
  imports: [
    DateFieldComponent,
    BooleanFieldComponent,
    TextFieldComponent,
    NumberFieldComponent,
    EnumeratedFieldComponent,
    BytesFieldComponent,
    ArrayFieldComponent,
    ObjectFieldComponent,
    MatFormFieldModule,
    EmptyReadonlyFieldComponent,
    NullBooleanFieldComponent,
    NgTemplateOutlet,
  ],
  templateUrl: './resource-field.component.html',
  styleUrl: './resource-field.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResourceFieldComponent {
  // The nearest resource store — the view this field belongs to — carries the viewed
  // row's capability envelope. Absent outside a resource view (modals, custom hosts).
  private store = inject(ResourceStore, { optional: true });

  fieldConfig = input.required<FieldElement>();
  meta = input.required<Meta>();
  fieldClass = input<string>();
  editMode = input<'edit' | 'view'>('edit');
  form = input.required<FormGroup>();
  formDataState = input<RecordData>();
  pristineValue = input<DataType | null>();
  data = input<RecordData>();
  previouslyNulled: boolean | null = null;

  fieldMeta: Signal<FieldMeta> = computed(() => {
    return (
      (this.meta().fields?.find((field) => field.fieldName === this.fieldConfig().name) as FieldMeta) ||
      ({} as FieldMeta)
    );
  });

  /** The control that draws the field, from its display type (see rendererFor). */
  renderer = computed<FieldRenderer>(() => {
    return rendererFor(this.fieldMeta().displayType);
  });

  // A field is read-only when the config says so, when the server never accepts it
  // (FieldMeta.readOnly: output-only, @state, tenant key), or when its shape has no
  // editor yet (bytes, an array, an object: presented, never typed, so the control is
  // never dirtied and the patch never carries it) — structural, before any permission
  // question. In edit mode the viewed row's capability envelope, when the read carried
  // one, is the positive list of editable fields: a field absent from it stays in view
  // mode for this row. Advisory — the server judges the patch.
  mode = computed(() => {
    if (this.fieldConfig().readOnly || this.fieldMeta().readOnly || READ_ONLY_RENDERERS.has(this.renderer())) {
      return 'view';
    }
    const editMode = this.editMode();
    if (editMode !== 'edit') {
      return editMode;
    }
    const editable = this.store?.viewCapabilities()?.Update;
    if (editable && !editable.includes(this.fieldConfig().name)) {
      return 'view';
    }
    return editMode;
  });

  /**
   * Whether the form has a control for this field. The form owner decides which fields
   * exist: the create form omits controls the digest's field-level Create entries deny
   * and the server-owned fields it never accepts, and every rendered input must be
   * form-backed, so without a control the field renders nothing at all, not even the
   * hidden control that keeps a hidden field in the group.
   */
  hasControl = computed(() => {
    return this.form().get(this.fieldConfig().name) !== null;
  });

  showField = computed(() => {
    if (!this.hasControl()) {
      return false;
    }
    // A write-only field is never returned by the server, so a view has nothing to show
    // for it: neither the control nor the empty placeholder. In create and edit it
    // renders through its own display type with a blank value.
    if (this.fieldMeta().writeOnly && this.mode() === 'view') {
      return false;
    }

    const shouldRender = this.fieldConfig().shouldRender;
    const conditionallyNull = this.fieldConfig().nullIfConditionallyHidden;
    const isForeignKeyDefault = this.fieldConfig().default?.type === 'foreignKey';

    if (typeof shouldRender === 'boolean') {
      return shouldRender && !isForeignKeyDefault;
    }
    const formValues = this.formDataState();
    if (!formValues) {
      return true;
    }

    console.debug('Field: ', this.fieldConfig().name, ' | Values to be used in showField calculation ', formValues);

    try {
      const showField = shouldRender(formValues) && !isForeignKeyDefault;

      if (!conditionallyNull) {
        return showField;
      }

      const formControlName = this.fieldConfig()?.name;
      const control = this.form()?.controls[formControlName];
      const pristineFieldValue = this.data()?.[formControlName];
      const previouslyNulled = this.previouslyNulled;

      untracked(() => {
        if (!showField && !previouslyNulled) {
          this.previouslyNulled = true;
          control?.setValue(null);
        }

        if (showField && previouslyNulled) {
          this.previouslyNulled = false;
          control?.setValue(pristineFieldValue);
        }
      });

      return showField;
    } catch (e) {
      console.error('Failed to calculate value for should Render function for field: ', this.fieldConfig().name);
      console.error(e);
      return true;
    }
  });

  showEmptyField = computed(() => {
    const editMode = this.mode();
    const showField = this.showField();

    if (!showField || editMode === 'edit') {
      return false;
    }

    const form = this.form();
    const config = this.fieldConfig();
    const meta = this.fieldMeta();
    if (form === undefined || config === undefined || meta === undefined) {
      return false;
    }

    if (meta.displayType === 'nullboolean') {
      return false;
    }

    const value: unknown = form.get(config.name)?.value;
    if (value === null || value === '') {
      return true;
    }
    // An empty list has nothing to draw as chips: the placeholder, as for a null.
    if (Array.isArray(value) && value.length === 0) {
      return true;
    }

    return false;
  });

  @HostBinding('class') class = '';

  booleanEditDisplayType: Signal<BooleanDisplayTypes> = computed(() => {
    const fieldConfig = this.fieldConfig();
    const fieldMeta = this.fieldMeta();
    const form = this.form();

    if (fieldConfig === undefined || fieldMeta === undefined || form === undefined) {
      return 'boolean';
    }

    const control = form.get(fieldConfig.name);
    if (control === null) {
      console.warn("Unable to find control for field '" + fieldConfig.name + "' to determine boolean display type");
      return 'boolean';
    }

    // Frontend forcing users to make a choice displays nullboolean to
    // trigger form validation - users must interact and explicitly
    // choose a true or false value
    if (control.hasValidator(Validators.required) || fieldMeta.displayType === 'nullboolean') {
      return 'nullboolean';
    }

    return 'boolean';
  });

  previousValidatorCount = 0;

  constructor() {
    effect(() => {
      this.class = this.showField() ? 'col-' + this.fieldConfig()?.cols : 'hidden-field';
    });

    effect(() => {
      const getValidators = this.fieldConfig().validators;

      if (typeof getValidators !== 'function') {
        return;
      }

      const formValues = this.formDataState();

      if (!formValues) {
        return;
      }

      console.debug('Field: ', this.fieldConfig().name, ' | Values to be used in validators calculation ', formValues);

      try {
        const newValidators = getValidators(formValues);

        const formControlName = this.fieldConfig().name;
        const control = this.form().get(formControlName);

        if (control === null) {
          throw new Error(`Control with name ${this.fieldConfig().name} not found during forceRequired calculation`);
        }

        const addValidators = !validatorsPresent(control, newValidators, this.previousValidatorCount);

        if (addValidators) {
          // The config's validators replace the control's, but the limit the field's
          // metadata set stays: the server's character limit holds whatever the config says.
          const maxLength = maxLengthValidator(this.fieldMeta());
          control.setValidators(maxLength ? [...newValidators, maxLength] : newValidators);
          control.updateValueAndValidity();
        }

        this.previousValidatorCount = newValidators.length;
      } catch (e) {
        console.error('Failed to calculate value for forceRequired function for field: ', this.fieldConfig().name);
        console.error(e);
        return;
      }
    });
  }
}

type BooleanDisplayTypes = Extract<ValidDisplayTypes, 'boolean' | 'nullboolean'>;
