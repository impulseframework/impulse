import { FormControl, ValidationErrors } from '@angular/forms';
import { FieldMeta } from '@impulseframework/resource';

import { maxLengthValidator, unchangedFieldWarning } from './resources-helpers';

describe('maxLengthValidator', () => {
  const field = (overrides: Partial<FieldMeta>): FieldMeta => ({
    fieldName: 'registry',
    displayType: 'string',
    required: true,
    isIndex: false,
    ...overrides,
  });

  it('adds nothing for a field without a limit', () => {
    expect(maxLengthValidator(field({}))).toBeUndefined();
  });

  it('adds nothing for a string array, whose limit is per element', () => {
    expect(maxLengthValidator(field({ displayType: 'string[]', maxLength: 4 }))).toBeUndefined();
  });

  it('refuses a value over the limit and names the lengths', () => {
    const validator = maxLengthValidator(field({ maxLength: 16 }));
    expect(validator).toBeDefined();

    const control = new FormControl('LS-REGISTRY-00017');
    expect(validator!(control)).toEqual({ maxlength: { requiredLength: 16, actualLength: 17 } });
  });

  it('accepts a value at the limit', () => {
    const validator = maxLengthValidator(field({ maxLength: 16 }));

    expect(validator!(new FormControl('LS-REGISTRY-0016'))).toBeNull();
  });

  it('accepts an empty value, leaving required to say whether one is needed', () => {
    const validator = maxLengthValidator(field({ maxLength: 16 }));

    expect(validator!(new FormControl(''))).toBeNull();
    expect(validator!(new FormControl(null))).toBeNull();
  });

  it('applies to a picker over a sized key column', () => {
    const validator = maxLengthValidator(field({ displayType: 'enumerated', maxLength: 64 }));

    expect(validator).toBeDefined();
    expect(validator!(new FormControl('courier'))).toBeNull();
  });
});

describe('unchangedFieldWarning', () => {
  const cases: { name: string; errors: ValidationErrors; want: string }[] = [
    {
      name: 'a value over the limit names both lengths',
      errors: { maxlength: { requiredLength: 20, actualLength: 31 } },
      want: 'Summary is 31 characters long, more than the 20 this form allows. The save left it as it was.',
    },
    {
      name: 'a missing value the form requires',
      errors: { required: true },
      want: 'Summary is empty, which this form does not allow. The save left it as it was.',
    },
    {
      name: "a library validator's own message",
      errors: { errorMsg: 'Value must be a positive number' },
      want: "Summary fails this form's rule: Value must be a positive number. The save left it as it was.",
    },
    {
      name: 'any other rule by its name',
      errors: { email: true },
      want: "Summary fails this form's email rule. The save left it as it was.",
    },
    {
      name: 'several rules in one sentence',
      errors: { maxlength: { requiredLength: 20, actualLength: 31 }, email: true },
      want:
        "Summary is 31 characters long, more than the 20 this form allows, and fails this form's email rule. " +
        'The save left it as it was.',
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(unchangedFieldWarning('Summary', tt.errors)).toBe(tt.want);
    });
  }
});
