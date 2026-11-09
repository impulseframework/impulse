import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { ResourceResolverComponent } from './resource-resolver.component';

@Component({
  template: '',
  standalone: true,
})
class DummyComponent {}

describe('ResourceResolverComponent', () => {
  let component: ResourceResolverComponent;
  let fixture: ComponentFixture<ResourceResolverComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceResolverComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceResolverComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', DummyComponent);
    fixture.componentRef.setInput('parentData', {});
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
