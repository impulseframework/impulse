import { computed, Directive, inject, Injector, input, ResourceRef, signal } from '@angular/core';
import { FormGroup } from '@angular/forms';
import {
  DataType,
  FieldElement,
  FieldMeta,
  Meta,
  RecordData,
  RESOURCE_META,
  RPCFieldMeta,
} from '@impulseframework/resource-angular/types';
import { ResourceStore } from '../resource-store.service';
import { isUUID } from '../resources-helpers';
/*
 * Base class for all resource input components.
 */
@Directive()
export abstract class BaseInputComponent {
  resourceMeta = inject(RESOURCE_META);
  injector = inject(Injector);
  store = inject(ResourceStore);

  meta = input.required<Meta>();
  pristineValue = input<DataType | null>();
  editMode = input.required<'edit' | 'view'>();
  showField = input<boolean>();
  fieldConfig = input.required<FieldElement>();
  fieldClass = input<string>();
  fieldMeta = input.required<FieldMeta | RPCFieldMeta>();
  form = input.required<FormGroup>();
  relatedData = input<RecordData>();

  /**
   * Resets the field back to its pristine value.
   */
  reset(): void {
    const control = this.form().get(this.fieldConfig().name as string);
    if (control) {
      control.setValue(this.pristineValue());
      control.markAsPristine();
    }
  }

  private affixResources = new Map<string, ResourceRef<RecordData>>();

  prefixString = computed(() => {
    const relatedData = this.relatedData();
    let builtPrefix = '';
    this.fieldConfig().prefixes.forEach((prefix) => {
      if (typeof prefix === 'string') {
        builtPrefix += prefix;
      } else if ('resource' in prefix) {
        if (!relatedData || !relatedData[prefix.id]) {
          return;
        }
        const id = relatedData[prefix.id];
        if (typeof id !== 'string' || !id || !isUUID(id)) {
          return;
        }

        const cacheKey = `${prefix.resource}-${id}`;
        let resourceRef = this.affixResources.get(cacheKey);

        if (!resourceRef) {
          const prefixResourceMeta = this.resourceMeta(prefix.resource);
          resourceRef = this.store.resourceView(signal(prefixResourceMeta.route), signal(id));
          this.affixResources.set(cacheKey, resourceRef!);
        }

        const resource = resourceRef.value();
        if (resource && prefix.field in resource) {
          builtPrefix += resource[String(prefix.field)];
        }
      } else if ('field' in prefix) {
        if (relatedData) {
          const fieldValue = relatedData[prefix.field];
          builtPrefix += fieldValue ? fieldValue : '';
        }
      }
    });
    return builtPrefix;
  });

  suffixString = computed(() => {
    const relatedData = this.relatedData();
    let builtSuffix = '';
    this.fieldConfig().suffixes.forEach((suffix) => {
      if (typeof suffix === 'string') {
        builtSuffix += suffix;
      } else if ('resource' in suffix) {
        if (!relatedData || !relatedData[suffix.id]) {
          return;
        }
        const id = relatedData[suffix.id];
        if (typeof id !== 'string' || !id || !isUUID(id)) {
          return;
        }

        const cacheKey = `${suffix.resource}-${id}`;
        let resourceRef = this.affixResources.get(cacheKey);

        if (!resourceRef) {
          const suffixResourceMeta = this.resourceMeta(suffix.resource);
          resourceRef = this.store.resourceView(signal(suffixResourceMeta.route), signal(id));
          this.affixResources.set(cacheKey, resourceRef!);
        }

        const resource = resourceRef.value();
        if (resource && suffix.field in resource) {
          builtSuffix += resource[suffix.field];
        }
      } else if ('field' in suffix) {
        if (relatedData) {
          const fieldValue = relatedData[suffix.field];
          builtSuffix += fieldValue ? fieldValue : '';
        }
      }
    });
    return builtSuffix;
  });

  floatLabel = computed(() => {
    if (this.fieldConfig().prefixes.length > 0 || this.fieldConfig().suffixes.length > 0) {
      return 'always';
    } else {
      return 'auto';
    }
  });
}
