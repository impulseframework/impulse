import { Component, computed, effect, HostBinding, input, model, OnInit, untracked } from '@angular/core';
import { FormGroup } from '@angular/forms';
import { ConfigElement, DataType, FieldElement, Meta, RecordData } from '@impulseframework/resource-angular/types';
import { flattenElements, maxLayoutNestingDepth } from '../gui-constants';
import { PaddingElementComponent } from '../padding-element/padding-element.component';
import { ComputedFieldComponent } from '../resource-field/fields/computed-field/computed-field.component';
import { ResourceFieldComponent } from '../resource-field/resource-field.component';

@Component({
  selector: 'impulse-resource-layout-template',
  imports: [PaddingElementComponent, ResourceFieldComponent, ComputedFieldComponent],
  templateUrl: './resource-layout.component.html',
  styleUrls: ['./resource-layout.component.scss'],
})
export class ResourceLayoutComponent implements OnInit {
  element = input.required<ConfigElement>();
  meta = input.required<Meta>();
  fieldClass = input<string>();
  editMode = input.required<'edit' | 'view'>();
  form = input.required<FormGroup>();
  formDataState = model<RecordData>();
  pristineValue = input<DataType | null>('');
  relatedData = input<RecordData>();
  parentClass = input<string>();
  layoutNestingDepth = input<number>(1);

  maxLayoutNestingDepth = maxLayoutNestingDepth;

  children = computed(() => {
    const element = this.element();
    if (element.type === 'section') {
      return element.children;
    }
    return [];
  });

  layoutChildrenConfig = computed(() => {
    return flattenElements(this.children());
  });
  previouslyNulled = false;

  nullIfConditionallyHidden = computed(() => {
    const element = this.element();
    if (element.type === 'field') {
      return element.nullIfConditionallyHidden;
    }
    if (element.type === 'section') {
      return element.nullAllChildrenIfConditionallyHidden;
    }
    return false;
  });

  showLayout = computed(() => {
    const element = this.element();
    const shouldRender = element.shouldRender;
    const conditionallyNull = this.nullIfConditionallyHidden();
    const form = this.form();
    const label = 'label' in element ? element.label : 'padding';
    const isForeignKeyDefault = element.type === 'field' && element.default?.type === 'foreignKey';

    if (typeof shouldRender === 'boolean') {
      return shouldRender && !isForeignKeyDefault;
    }

    const formValues = this.formDataState();
    if (!formValues) {
      return true;
    }

    console.debug('Layout: ', label, ' | Values to be used in showLayout calculation ', formValues);
    try {
      const showLayout = shouldRender(formValues) && !isForeignKeyDefault;

      if (!conditionallyNull) {
        return showLayout;
      }
      const previouslyNulled = this.previouslyNulled;

      untracked(() => {
        this.layoutChildrenConfig().forEach((configElement) => {
          const isFormControl = configElement.type === 'field';
          if (!isFormControl) {
            return;
          }

          const control = form?.controls[configElement.name];
          const pristineFieldValue = this.relatedData()?.[configElement.name];

          if (!showLayout && !previouslyNulled) {
            this.previouslyNulled = true;
            control?.setValue(null);
          }

          if (showLayout && previouslyNulled) {
            this.previouslyNulled = false;
            control?.setValue(pristineFieldValue);
          }
        });
      });

      return showLayout;
    } catch (e) {
      console.error('Failed to calculate value for should Render function for layout: ', label);
      console.error(e);
      return true;
    }
  });

  @HostBinding('class') class = '';

  constructor() {
    // TODO: Remove this effect once Angular supports signals in Reactive forms to obtain changes to form values.
    // This effect has been necessary because translating from Observables to Signals, specifically via an input signal
    // doesn't have a clean solution

    effect((onCleanup) => {
      const element = this.element();
      if (element === undefined) {
        return;
      }
      const currentForm = this.form();
      const shouldRender = element.shouldRender;
      const nestingDepth = this.layoutNestingDepth();
      const layoutChildren = this.layoutChildrenConfig();
      const label = 'label' in element ? element.label : 'padding';
      if (!this.shouldInitializeFormData(currentForm, nestingDepth, shouldRender, layoutChildren)) {
        return;
      }
      console.debug('USAGE | New subscription for formValuesSignal created for layout: ', label);

      this.formDataState.set(currentForm.getRawValue());
      const subscription = currentForm.valueChanges.subscribe((value) => {
        this.formDataState.set(value);
      });

      onCleanup(() => {
        console.debug('USAGE | Unsubscribing from valueChanges for layout: ', label);
        subscription.unsubscribe();
      });
    });
  }

  ngOnInit(): void {
    this.class = 'col-' + this.element().cols;
  }

  shouldInitializeFormData(
    form: FormGroup,
    layoutNestingDepth: number,
    shouldRender: ConfigElement['shouldRender'],
    layoutChildren: ConfigElement[],
  ): boolean {
    const childrenNeedFormDataState = layoutChildren.some((config) => {
      const shouldRenderIsFunction = typeof config.shouldRender === 'function';
      const isComputedField = config.type === 'computedDisplayField';
      let validatorsIsFunction = false;

      const castConfig = config as FieldElement;
      if (castConfig.validators && typeof castConfig.validators === 'function') {
        validatorsIsFunction = true;
      }

      return shouldRenderIsFunction || isComputedField || validatorsIsFunction;
    });

    if (!form || layoutNestingDepth > 1) {
      return false;
    }

    if (!childrenNeedFormDataState && typeof shouldRender !== 'function') {
      return false;
    }

    return true;
  }
}
