import { ComponentFixture, TestBed } from '@angular/core/testing';
import { createClient } from '@impulseframework/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

import { ActionAccessControlWrapperComponent } from './action-access-control-wrapper.component';

describe('ActionAccessControlWrapperComponent', () => {
  let component: ActionAccessControlWrapperComponent;
  let fixture: ComponentFixture<ActionAccessControlWrapperComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ActionAccessControlWrapperComponent],
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

    fixture = TestBed.createComponent(ActionAccessControlWrapperComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
