import { ComponentFixture, TestBed } from '@angular/core/testing';
import { createClient } from '@impulseframework/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

import { ImpulseInputFieldComponent } from './field.component';

describe('ImpulseFieldComponent', () => {
  let component: ImpulseInputFieldComponent;
  let fixture: ComponentFixture<ImpulseInputFieldComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ImpulseInputFieldComponent],
      // The application's client is required; a bare one over an empty descriptor serves the stub.
      providers: [
        {
          provide: RESOURCE_CLIENT,
          useValue: createClient(
            {
              resources: {},
              methods: {},
              permissionDigestRoute: 'permission-digest',
              userDomainsRoute: 'user-domains',
              features: { route: 'features' },
            },
            { baseUrl: '/api' },
          ),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ImpulseInputFieldComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('mode', 'read');
    fixture.componentRef.setInput('resource', 'users');
    fixture.componentRef.setInput('domain', 'default');
    fixture.componentRef.setInput('value', 'value');
    fixture.componentRef.setInput('name', 'Field Name');
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
