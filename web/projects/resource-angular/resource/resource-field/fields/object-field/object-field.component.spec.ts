import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta } from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { ObjectFieldComponent } from './object-field.component';

// An object field presents the value as indented JSON, whatever JSON it is, and offers no input.

describe('ObjectFieldComponent', () => {
  let fixture: ComponentFixture<ObjectFieldComponent>;
  const positionMeta: FieldMeta = { fieldName: 'position', displayType: 'object', required: false, isIndex: false };
  const meta: ResourceMeta = { route: 'distress-calls', fields: [positionMeta] };

  const create = async (value: unknown): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [ObjectFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();
    fixture = TestBed.createComponent(ObjectFieldComponent);
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', positionMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'position' as FieldName, label: 'Position' }));
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('showField', true);
    fixture.componentRef.setInput('form', new FormGroup({ position: new FormControl<unknown>(value) }));
    fixture.detectChanges();
  };

  const point = { type: 'Point', coordinates: [-118.2437, 34.0522] };

  const cases: { name: string; value: unknown; want: string }[] = [
    { name: 'a derived or imported object, pretty-printed', value: point, want: JSON.stringify(point, null, 2) },
    { name: 'an unknown that is a string prints as JSON', value: 'free text', want: '"free text"' },
    { name: 'an unknown that is a number', value: 42, want: '42' },
    { name: 'an unknown that is an array', value: [1, 2], want: JSON.stringify([1, 2], null, 2) },
    { name: 'a null value prints nothing', value: null, want: '' },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      await create(tt.value);
      const element: HTMLElement = fixture.nativeElement;
      expect(element.querySelector('pre.object-json')?.textContent).toBe(tt.want);
      expect(element.querySelector('.object-label')?.textContent?.trim()).toBe('Position');
      expect(element.querySelector('input:not([type=hidden])')).toBeNull();
    });
  }
});
