import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup, Validators } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta, ValidDisplayTypes } from '@impulseframework/resource-angular/types';

import { resourceValidators } from '../gui-constants';
import { ResourceStore } from '../resource-store.service';
import { FieldRenderer } from './renderer';
import { ResourceFieldComponent } from './resource-field.component';

describe('ResourceFieldComponent', () => {
  let component: ResourceFieldComponent;
  let fixture: ComponentFixture<ResourceFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceFieldComponent],
      // The field it renders for a string column, TextFieldComponent, takes the store of the view it is in.
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();

    const callsign: FieldMeta = { fieldName: 'callsign', displayType: 'string', required: false, isIndex: false };
    const meta: ResourceMeta = { route: 'squadrons', fields: [callsign] };
    fixture = TestBed.createComponent(ResourceFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'callsign' as FieldName, label: 'Callsign' }));
    fixture.componentRef.setInput('form', new FormGroup({ callsign: new FormControl('') }));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});

// The field dispatches on its display type: the three read-only shapes draw their own
// presentation and stay in view mode whatever the form's mode, a write-only field is
// absent from a view and a blank input in edit, and an empty list is the placeholder.
describe('ResourceFieldComponent dispatch', () => {
  let fixture: ComponentFixture<ResourceFieldComponent>;

  const create = async (fieldMeta: FieldMeta, value: unknown, editMode: 'edit' | 'view'): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [ResourceFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    const meta: ResourceMeta = { route: 'probes', fields: [fieldMeta] };
    fixture = TestBed.createComponent(ResourceFieldComponent);
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldConfig', field({ name: fieldMeta.fieldName as FieldName, label: 'Probe' }));
    fixture.componentRef.setInput('editMode', editMode);
    fixture.componentRef.setInput('form', new FormGroup({ [fieldMeta.fieldName]: new FormControl<unknown>(value) }));
    fixture.detectChanges();
  };

  const probe = (displayType: ValidDisplayTypes, extra: Partial<FieldMeta> = {}): FieldMeta =>
    ({ fieldName: 'probe', displayType, required: false, isIndex: false, ...extra }) as FieldMeta;

  describe('draws each shape read-only in edit mode', () => {
    const cases: { displayType: ValidDisplayTypes; value: unknown; renderer: FieldRenderer; selector: string }[] = [
      { displayType: 'bytes', value: 'AAAA', renderer: 'bytes', selector: 'impulse-bytes-field' },
      { displayType: 'number[]', value: [1, 2], renderer: 'array', selector: 'impulse-array-field' },
      { displayType: 'object', value: { a: 1 }, renderer: 'object', selector: 'impulse-object-field' },
    ];

    for (const tt of cases) {
      it(`${tt.displayType} through ${tt.selector}, in view mode`, async () => {
        await create(probe(tt.displayType), tt.value, 'edit');
        const component = fixture.componentInstance;
        expect(component.renderer()).toBe(tt.renderer);
        expect(component.mode()).toBe('view');
        const element: HTMLElement = fixture.nativeElement;
        expect(element.querySelector(tt.selector)).not.toBeNull();
        expect(element.querySelector('input:not([type=hidden])[formcontrolname]')).toBeNull();
      });
    }
  });

  it('a string stays the text control in edit mode', async () => {
    await create(probe('string'), 'Kestrel', 'edit');
    expect(fixture.componentInstance.renderer()).toBe('text');
    expect(fixture.componentInstance.mode()).toBe('edit');
    expect((fixture.nativeElement as HTMLElement).querySelector('impulse-text-field input')).not.toBeNull();
  });

  it('a write-only field draws nothing in view mode, not even the placeholder', async () => {
    await create(probe('string', { writeOnly: true }), null, 'view');
    const element: HTMLElement = fixture.nativeElement;
    expect(fixture.componentInstance.showField()).toBe(false);
    expect(element.querySelector('div.hidden-field')).not.toBeNull();
    expect(element.querySelector('input:not([type=hidden])')).toBeNull();
    expect(element.querySelector('impulse-empty-readonly-field')).toBeNull();
  });

  it('a write-only field is a blank input in edit mode', async () => {
    await create(probe('string', { writeOnly: true }), null, 'edit');
    const element: HTMLElement = fixture.nativeElement;
    expect(fixture.componentInstance.showField()).toBe(true);
    const input = element.querySelector<HTMLInputElement>('impulse-text-field input:not([type=hidden])');
    expect(input).not.toBeNull();
    expect(input?.value).toBe('');
    expect(input?.readOnly).toBe(false);
  });

  it('a field the form has no control for renders nothing and binds nothing', async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    const caseNumber = probe('string', { readOnly: true });
    fixture = TestBed.createComponent(ResourceFieldComponent);
    fixture.componentRef.setInput('meta', { route: 'probes', fields: [caseNumber] } as ResourceMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'probe' as FieldName, label: 'Case number' }));
    fixture.componentRef.setInput('editMode', 'edit');
    // The create form built no control for the server-owned field.
    fixture.componentRef.setInput('form', new FormGroup({ summary: new FormControl<unknown>('') }));
    expect(() => fixture.detectChanges()).not.toThrow();
    const element: HTMLElement = fixture.nativeElement;
    expect(fixture.componentInstance.hasControl()).toBe(false);
    expect(fixture.componentInstance.showField()).toBe(false);
    expect(element.querySelectorAll('input').length).toBe(0);
    expect(element.querySelector('impulse-text-field')).toBeNull();
  });

  it('an empty list is the placeholder, as a null is', async () => {
    await create(probe('string[]'), [], 'view');
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('impulse-empty-readonly-field')).not.toBeNull();
    expect(element.querySelector('impulse-array-field')).toBeNull();
  });
});

// A config whose validators are a function of the form's values replaces the control's
// validators whenever the values change; the limit the field's metadata set stays with them.
describe('ResourceFieldComponent with a validators function', () => {
  it('keeps the limit beside the rules the function returns', async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    const callsign: FieldMeta = {
      fieldName: 'callsign',
      displayType: 'string',
      required: false,
      isIndex: false,
      maxLength: 5,
    };
    // The control as the form owner built it: the limit from the metadata, and nothing from the config yet.
    const control = new FormControl<string | null>('Kestrel', Validators.maxLength(5));
    const fixture = TestBed.createComponent(ResourceFieldComponent);
    fixture.componentRef.setInput('meta', { route: 'squadrons', fields: [callsign] } as ResourceMeta);
    fixture.componentRef.setInput(
      'fieldConfig',
      field({ name: 'callsign' as FieldName, label: 'Callsign', validators: () => [resourceValidators.REQUIRED] }),
    );
    fixture.componentRef.setInput('form', new FormGroup({ callsign: control }));
    fixture.componentRef.setInput('formDataState', { callsign: 'Kestrel' });
    fixture.detectChanges();

    expect(control.hasValidator(resourceValidators.REQUIRED)).toBe(true);
    expect(control.errors).toEqual({ maxlength: { requiredLength: 5, actualLength: 7 } });
    control.setValue('');
    expect(control.errors).toEqual({ required: true });
  });
});
