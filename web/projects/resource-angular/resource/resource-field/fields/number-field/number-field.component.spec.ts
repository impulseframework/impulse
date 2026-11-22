import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta } from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { NumberFieldComponent } from './number-field.component';

describe('NumberFieldComponent', () => {
  let component: NumberFieldComponent;
  let fixture: ComponentFixture<NumberFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [NumberFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();

    const fieldMeta: FieldMeta = { fieldName: 'tonnage', displayType: 'number', required: false, isIndex: false };
    const meta: ResourceMeta = { route: 'squadrons', fields: [fieldMeta] };
    fixture = TestBed.createComponent(NumberFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'tonnage' as FieldName, label: 'Tonnage' }));
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.componentRef.setInput('form', new FormGroup({ tonnage: new FormControl<number | null>(null) }));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// A decimal is text in JSON, in both directions, and the metadata marks it beside its
// number display type (FieldMeta.decimal). The control writes what the person typed into
// the form as a string, so the request carries every digit; a field with no mark is an
// integer or a float and writes a JavaScript number, as it always did.
describe('NumberFieldComponent, a decimal and a number', () => {
  let fixture: ComponentFixture<NumberFieldComponent>;
  let control: FormControl<unknown>;

  const create = async (
    extra: Partial<FieldMeta>,
    value: unknown,
    editMode: 'edit' | 'view' = 'edit',
  ): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [NumberFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    const fieldMeta: FieldMeta = { fieldName: 'fee', displayType: 'number', required: false, isIndex: false, ...extra };
    const meta: ResourceMeta = { route: 'missions', fields: [fieldMeta] };
    control = new FormControl<unknown>(value);
    fixture = TestBed.createComponent(NumberFieldComponent);
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'fee' as FieldName, label: 'Fee' }));
    fixture.componentRef.setInput('editMode', editMode);
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ fee: control }));
    fixture.detectChanges();
  };

  const input = (): HTMLInputElement => {
    const element = (fixture.nativeElement as HTMLElement).querySelector<HTMLInputElement>('input[type=number]');
    if (!element) {
      throw new Error('the field drew no number input');
    }
    return element;
  };

  const type = (text: string): void => {
    const element = input();
    element.value = text;
    element.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  };

  it('a decimal is written as the text typed, with every digit', async () => {
    await create({ decimal: true }, null);
    // More digits than a JavaScript number holds: as a number this is 12345678901234568.
    type('12345678901234567.25');
    expect(control.value).toBe('12345678901234567.25');
    expect(control.dirty).toBe(true);
  });

  it('a decimal keeps the zeros the person typed', async () => {
    await create({ decimal: true }, null);
    type('950.50');
    expect(control.value).toBe('950.50');
  });

  it('a decimal emptied is no value, not an empty text', async () => {
    await create({ decimal: true }, '900');
    type('');
    expect(control.value).toBeNull();
  });

  it("a decimal opens with the row's text and leaves the form as it was", async () => {
    await create({ decimal: true }, '900.00');
    expect(input().value).toBe('900.00');
    expect(control.value).toBe('900.00');
    expect(control.pristine).toBe(true);
  });

  it('a decimal takes any fraction: the input sets no step', async () => {
    await create({ decimal: true }, null);
    expect(input().getAttribute('step')).toBe('any');
  });

  it('a decimal follows the control when it is disabled', async () => {
    await create({ decimal: true }, '900');
    control.disable();
    fixture.detectChanges();
    expect(input().disabled).toBe(true);
    control.enable();
    fixture.detectChanges();
    expect(input().disabled).toBe(false);
  });

  it('a decimal in a view is read-only and shows its text', async () => {
    await create({ decimal: true }, '900.5', 'view');
    expect(input().readOnly).toBe(true);
    expect(input().value).toBe('900.5');
  });

  it('a field with no mark is written as a number', async () => {
    await create({}, null);
    type('42');
    expect(control.value).toBe(42);
    type('');
    expect(control.value).toBeNull();
  });
});
