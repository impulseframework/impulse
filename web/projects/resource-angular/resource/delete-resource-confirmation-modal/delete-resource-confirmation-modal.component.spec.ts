import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DeleteResourceConfirmationModalComponent } from './delete-resource-confirmation-modal.component';

describe('DeleteResourceConfirmationModal', () => {
  let component: DeleteResourceConfirmationModalComponent;
  let fixture: ComponentFixture<DeleteResourceConfirmationModalComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [DeleteResourceConfirmationModalComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(DeleteResourceConfirmationModalComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
