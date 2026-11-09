import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';

import { RpcButtonComponent } from './rpc-button.component';

describe('RpcButtonComponent', () => {
  let component: RpcButtonComponent;
  let fixture: ComponentFixture<RpcButtonComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [RpcButtonComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(RpcButtonComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('relatedData', {});
    fixture.componentRef.setInput('rpcConfig', {
      label: 'Test RPC',
      conditions: [],
      method: 'testMethod',
      elements: [],
      methodBodyTemplate: {},
    });
    fixture.componentRef.setInput('primaryResource', 'users');
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
