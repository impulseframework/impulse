import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormGroup } from '@angular/forms';

import { ResourceLayoutComponent } from './resource-layout.component';

describe('ResourceLayoutComponent', () => {
  let component: ResourceLayoutComponent;
  let fixture: ComponentFixture<ResourceLayoutComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceLayoutComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceLayoutComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('element', { type: 'padding', cols: 6 });
    fixture.componentRef.setInput('meta', {});
    fixture.componentRef.setInput('editMode', 'view');
    fixture.componentRef.setInput('form', new FormGroup({}));
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
