import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';

import { BaseRPCModalComponent } from './base-rpc-modal.component';

describe('BaseRPCModalComponent', () => {
  let component: BaseRPCModalComponent;
  let fixture: ComponentFixture<BaseRPCModalComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [BaseRPCModalComponent],
      providers: [
        {
          provide: MAT_DIALOG_DATA,
          useValue: {
            elements: [],
            label: 'Test RPC',
            method: 'testMethod',
            width: '400px',
          },
        },
        {
          provide: MatDialogRef,
          useValue: {
            updateSize: () => undefined,
            close: () => undefined,
          },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(BaseRPCModalComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
