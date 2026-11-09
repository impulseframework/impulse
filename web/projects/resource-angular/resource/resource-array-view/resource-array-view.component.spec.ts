import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Resource } from '@impulseframework/resource';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { arrayConfig, viewConfig } from '@impulseframework/resource-angular/types';

import { CompoundResourceComponent } from '../compound-resource/compound-resource.component';
import { ResourceArrayViewComponent } from './resource-array-view.component';

describe('ResourceArrayViewComponent', () => {
  let component: ResourceArrayViewComponent;
  let fixture: ComponentFixture<ResourceArrayViewComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ResourceArrayViewComponent],
      providers: [provideResourceTesting({ transport: scriptedTransport({ status: 200, body: [] }) })],
    }).compileComponents();

    fixture = TestBed.createComponent(ResourceArrayViewComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('compoundResourceComponent', CompoundResourceComponent);
    fixture.componentRef.setInput(
      'resourceConfig',
      arrayConfig({
        primaryResource: 'Squadrons' as Resource,
        iteratedConfig: viewConfig({ primaryResource: 'Squadrons' as Resource, elements: [] }),
        listFilter: (row: { id: string }): string => `id:ne:${row.id}`,
      }),
    );
    // The children belong to a row the page hands over; the view asks nothing without it.
    fixture.componentRef.setInput('parentData', { id: 'sq-1' });
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
