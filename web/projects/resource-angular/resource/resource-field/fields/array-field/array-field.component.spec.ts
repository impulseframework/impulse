import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta, ValidDisplayTypes } from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { ArrayFieldComponent } from './array-field.component';

// An array field presents the elements as chips, each written the way a cell writes a
// value of the element type, and offers no input.

describe('ArrayFieldComponent', () => {
  let fixture: ComponentFixture<ArrayFieldComponent>;

  const create = async (displayType: ValidDisplayTypes, value: unknown): Promise<void> => {
    const fieldMeta: FieldMeta = { fieldName: 'items', displayType, required: false, isIndex: false };
    const meta: ResourceMeta = { route: 'ships', fields: [fieldMeta] };
    await TestBed.configureTestingModule({
      imports: [ArrayFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    fixture = TestBed.createComponent(ArrayFieldComponent);
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'items' as FieldName, label: 'Cargo bays' }));
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ items: new FormControl<unknown>(value) }));
    fixture.detectChanges();
  };

  const chips = (): string[] =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('mat-chip')).map((chip) => chip.textContent?.trim() ?? '');

  const cases: { name: string; displayType: ValidDisplayTypes; value: unknown; want: string[] }[] = [
    { name: 'a number array, one chip per element', displayType: 'number[]', value: [60, 60, 30], want: ['60', '60', '30'] },
    { name: 'a string array', displayType: 'string[]', value: ['Hammerfall', 'Anvil Actual'], want: ['Hammerfall', 'Anvil Actual'] },
    { name: 'a date array, each element as a date', displayType: 'date[]', value: ['2026-09-21T12:00:00Z'], want: ['9/21/2026'] },
    { name: 'an object array, each element as its JSON', displayType: 'object[]', value: [{ a: 1 }], want: ['{"a":1}'] },
    { name: 'a null value draws no chip', displayType: 'number[]', value: null, want: [] },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      await create(tt.displayType, tt.value);
      expect(chips()).toEqual(tt.want);
      expect((fixture.nativeElement as HTMLElement).querySelector('.array-label')?.textContent?.trim()).toBe('Cargo bays');
      expect((fixture.nativeElement as HTMLElement).querySelector('input:not([type=hidden])')).toBeNull();
    });
  }
});
