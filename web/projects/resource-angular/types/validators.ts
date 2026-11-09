import { AbstractControl, ValidationErrors, ValidatorFn } from '@angular/forms';

export const validatorsPresent = (
  control: AbstractControl<unknown, unknown>,
  validators: ValidatorFn[],
  previousValidatorCount: number,
): boolean => {
  if (previousValidatorCount !== validators.length) {
    return false;
  }

  return validators.every((validator) => control.hasValidator(validator));
};

export const requiredIf = (predicate: () => boolean, validator: ValidatorFn) => {
  return (formControl: AbstractControl): ValidationErrors | null => {
    if (!formControl.parent) {
      return null;
    }
    if (predicate()) {
      return validator(formControl);
    }
    return null;
  };
};

declare const __singletonValidatorBrand: unique symbol;
export type ResourceValidatorFn = ValidatorFn & {
  readonly [__singletonValidatorBrand]: true;
};
export function createResourceValidator(validator: ValidatorFn): ResourceValidatorFn {
  return validator as ResourceValidatorFn;
}
