import { ModelSignal } from '@angular/core';
import { FormControl, FormGroup, ValidationErrors, ValidatorFn, Validators } from '@angular/forms';
import { FieldMeta } from '@impulseframework/resource';
import {
  ConfigElement,
  DataType,
  FieldElement,
  MethodMeta,
  RecordData,
  ResourceMeta,
  RPCRecordData,
  ViewConfig,
} from '@impulseframework/resource-angular/types';
import { format, isValid } from 'date-fns';
import { civildateCoercion, flattenElements } from './gui-constants';

export type PristineData = Record<string, DataType | null>;

/**
 * The validator a field's metadata adds to its form control: the server's character
 * limit (FieldMeta.maxLength) as Validators.maxLength, so a value the server would refuse
 * with 400 never leaves the browser. Undefined for a field without a limit, and for a
 * 'string[]' field, whose limit is per element while the array validator would count the
 * elements.
 */
export function maxLengthValidator(field: FieldMeta): ValidatorFn | undefined {
  if (!field.maxLength || field.displayType === 'string[]') {
    return undefined;
  }
  return Validators.maxLength(field.maxLength);
}

/**
 * What a save says about a field the person did not change whose value fails this
 * form's rules: the field by its label, then each rule it fails, then that the save left
 * the field alone. The limit is the server's character limit as this build knows it; a
 * newer release may allow more, and a newer app may have saved more. A rule from the
 * library's own validators carries its message (errorMsg), Angular's maxlength and
 * required are spelled out, and any other rule is named.
 */
export function unchangedFieldWarning(label: string, errors: ValidationErrors): string {
  const clauses = Object.entries(errors).map(([rule, detail]) => {
    if (rule === 'maxlength') {
      const lengths = detail as { requiredLength: number; actualLength: number };
      return `is ${lengths.actualLength} characters long, more than the ${lengths.requiredLength} this form allows`;
    }
    if (rule === 'required') {
      return 'is empty, which this form does not allow';
    }
    if (rule === 'errorMsg') {
      return `fails this form's rule: ${String(detail)}`;
    }
    return `fails this form's ${rule} rule`;
  });
  return `${label} ${clauses.join(', and ')}. The save left it as it was.`;
}

export const createFormGroup = (
  meta: ResourceMeta,
  resourceData: RecordData,
  config: ViewConfig,
  formDataState?: ModelSignal<RecordData>,
): {
  formGroup: FormGroup;
  pristineFormValues: PristineData;
} => {
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

    fg.addControl(field.fieldName, control);
    pristineValues[field.fieldName] = value;
  }
  console.debug(formDataState);

  return {
    formGroup: fg,
    pristineFormValues: pristineValues,
  };
};

export function extractFieldNames(elements: ConfigElement[]): string[] {
  const fields: string[] = [];
  for (const element of elements) {
    if (element.type === 'section') {
      fields.push(...extractFieldNames(element.children));
    } else if (element.type === 'field') {
      fields.push(element.name as string);
    }
  }
  return fields;
}


export function isUUID(str: string): boolean {
  const uuidRegex = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
  return uuidRegex.test(str);
}

export function metadataTypeCoercion(record: RecordData, meta: ResourceMeta): RecordData;
export function metadataTypeCoercion(record: RPCRecordData, meta: MethodMeta): RPCRecordData;
export function metadataTypeCoercion(
  record: RecordData | RPCRecordData,
  meta: ResourceMeta | MethodMeta,
): RecordData | RPCRecordData {
  if (!meta?.fields) return record;

  const displayTypeByField = new Map(meta.fields.map((f) => [f.fieldName, f.displayType]));

  for (const [key, value] of Object.entries(record)) {
    const displayType = displayTypeByField.get(key);
    if (displayType === 'civildate' && value && isValid(value)) {
      const date = value as Date;
      record[key] = format(date, 'yyyy-MM-dd');
    }
  }

  return record;
}
