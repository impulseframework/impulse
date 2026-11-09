import { ValidDisplayTypes } from '@impulseframework/resource-angular/types';
import { elementTypeOf, FieldRenderer, rendererFor } from './renderer';

describe('rendererFor', () => {
  const cases: { displayType: ValidDisplayTypes | undefined; want: FieldRenderer }[] = [
    { displayType: 'string', want: 'text' },
    { displayType: 'uuid', want: 'text' },
    { displayType: undefined, want: 'text' },
    { displayType: 'number', want: 'number' },
    { displayType: 'boolean', want: 'boolean' },
    { displayType: 'nullboolean', want: 'boolean' },
    { displayType: 'date', want: 'date' },
    { displayType: 'civildate', want: 'date' },
    { displayType: 'enumerated', want: 'enumerated' },
    { displayType: 'bytes', want: 'bytes' },
    { displayType: 'object', want: 'object' },
    { displayType: 'string[]', want: 'array' },
    { displayType: 'number[]', want: 'array' },
    { displayType: 'boolean[]', want: 'array' },
    { displayType: 'date[]', want: 'array' },
    { displayType: 'civildate[]', want: 'array' },
    { displayType: 'uuid[]', want: 'array' },
    { displayType: 'object[]', want: 'array' },
    { displayType: 'bytes[]', want: 'array' },
  ];

  for (const tt of cases) {
    it(`${tt.displayType} draws with ${tt.want}`, () => {
      expect(rendererFor(tt.displayType)).toBe(tt.want);
    });
  }
});

describe('elementTypeOf', () => {
  const cases: { displayType: ValidDisplayTypes; want: ValidDisplayTypes }[] = [
    { displayType: 'number[]', want: 'number' },
    { displayType: 'civildate[]', want: 'civildate' },
    { displayType: 'object[]', want: 'object' },
    { displayType: 'bytes[]', want: 'bytes' },
    { displayType: 'string', want: 'string' },
  ];

  for (const tt of cases) {
    it(`${tt.displayType} has element type ${tt.want}`, () => {
      expect(elementTypeOf(tt.displayType)).toBe(tt.want);
    });
  }
});
