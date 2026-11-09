import { ComponentFixture, TestBed } from '@angular/core/testing';

import { ComputedFieldComponent } from './computed-field.component';

describe('ComputedFieldComponent', () => {
  let component: ComputedFieldComponent;
  let fixture: ComponentFixture<ComputedFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ComputedFieldComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(ComputedFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('fieldConfig', {
      type: 'computedDisplayField',
      name: 'computed',
      label: 'Computed',
      cols: 12,
      shouldRender: true,
      calculatedValue: () => 'value',
    });
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
