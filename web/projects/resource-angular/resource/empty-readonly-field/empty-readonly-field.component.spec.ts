import { ComponentFixture, TestBed } from '@angular/core/testing';

import { EmptyReadonlyFieldComponent } from './empty-readonly-field.component';

describe('EmptyReadonlyFieldComponent', () => {
  let component: EmptyReadonlyFieldComponent;
  let fixture: ComponentFixture<EmptyReadonlyFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [EmptyReadonlyFieldComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(EmptyReadonlyFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('label', 'Test Label');
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
