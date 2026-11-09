import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, FormGroup } from '@angular/forms';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { field, FieldMeta, FieldName, ResourceMeta } from '@impulseframework/resource-angular/types';

import { ResourceStore } from '../../../resource-store.service';
import { NullBooleanFieldComponent } from './nullboolean-field.component';

describe('NullBooleanFieldComponent', () => {
  let component: NullBooleanFieldComponent;
  let fixture: ComponentFixture<NullBooleanFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [NullBooleanFieldComponent],
      providers: [provideResourceTesting(), ResourceStore],
    }).compileComponents();

    const fieldMeta: FieldMeta = { fieldName: 'insured', displayType: 'nullboolean', required: false, isIndex: false };
    const meta: ResourceMeta = { route: 'squadrons', fields: [fieldMeta] };
    fixture = TestBed.createComponent(NullBooleanFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('meta', meta);
    fixture.componentRef.setInput('fieldMeta', fieldMeta);
    fixture.componentRef.setInput('fieldConfig', field({ name: 'insured' as FieldName, label: 'Insured' }));
    fixture.componentRef.setInput('editMode', 'edit');
    fixture.componentRef.setInput('form', new FormGroup({ insured: new FormControl<boolean | null>(null) }));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
