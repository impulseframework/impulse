import { Component, computed, effect, input, Type, untracked, viewChild, ViewContainerRef } from '@angular/core';
import { ChildResourceConfig, ComponentConfig, FieldName, RecordData } from '@impulseframework/resource-angular/types';
import { ResourceStore } from '../resource-store.service';

@Component({
  selector: 'impulse-resource-resolver',
  imports: [],
  templateUrl: './resource-resolver.component.html',
  styleUrl: './resource-resolver.component.scss',
  providers: [ResourceStore],
})
export class ResourceResolverComponent {
  /* eslint-disable  @typescript-eslint/no-explicit-any */
  compoundResourceComponent = input.required<Type<any>>();
  resourceConfig = input<ChildResourceConfig>();

  config = computed(() => {
    return this.resourceConfig() as ComponentConfig;
  });

  dynamicSlot = viewChild.required<ViewContainerRef, ViewContainerRef>('dynamicSlot', { read: ViewContainerRef });

  parentData = input.required<RecordData>();

  constructor() {
    effect(() => {
      const parentData = this.parentData();
      const config = this.config();
      const params = config?.params;
      if (params === undefined || config === undefined) {
        return;
      }

      untracked(() => {
        this.dynamicSlot().clear();
        const component = config.component;
        if (component === 'SwitchResolver') {
          let uuid = '';
          let caseConfig = {} as ChildResourceConfig;
          for (const c of params.cases) {
            if (parentData) {
              if (parentData[c.parentField] === c.caseId) {
                uuid = String(parentData[c.childId]);
                caseConfig = c.config;
                if ('parentRelation' in caseConfig) {
                  caseConfig.parentRelation.childKey = '' as FieldName;
                  caseConfig.parentRelation.parentKey = '' as FieldName;
                }
                break;
              }
            }
          }

          if (uuid == '') {
            return;
          }

          const primaryComponentRef = this.dynamicSlot().createComponent(this.compoundResourceComponent());
          primaryComponentRef.setInput('resourceConfig', caseConfig);
          primaryComponentRef.setInput('uuid', uuid);
          primaryComponentRef.setInput('parentData', parentData);
        } else if (typeof component === 'function') {
          const primaryComponentRef = this.dynamicSlot().createComponent(component);
          primaryComponentRef.setInput('uuid', parentData['uuid'] || '');
          primaryComponentRef.setInput('config', config);
          primaryComponentRef.setInput('parentData', parentData);
        }
      });
    });
  }
}
