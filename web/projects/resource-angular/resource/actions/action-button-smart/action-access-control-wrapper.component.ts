import { Component, computed, inject, input } from '@angular/core';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  CreatePermission,
  Domain,
  ExecutePermission,
  Method,
  RecordData,
  Resource,
  ResourceMeta,
  RowCapabilities,
} from '@impulseframework/resource-angular/types';

/**
 * What an action button needs to decide whether it renders. Structural gates come from
 * the generated metadata (the *Disabled flags); permission gates come from the
 * permission digest for create and RPC actions (no row exists yet, or the action is a
 * method) and from the viewed row's capability envelope for edit and delete. Every
 * permission field is optional: a context that omits it gates structurally only.
 */
export type ActionButtonContext =
  | {
      actionType: 'create';
      meta: ResourceMeta;
      /** The resource being created; with it, the digest's Create entry gates the button. */
      resource?: Resource;
      domain?: Domain;
      resourceData: RecordData;
      shouldRender: (data: RecordData) => boolean;
    }
  | {
      actionType: 'edit' | 'delete';
      meta: ResourceMeta;
      /** The viewed row's envelope; with it, Update's field list gates edit and Delete gates delete. */
      capabilities?: RowCapabilities;
      resourceData: RecordData;
      shouldRender: (data: RecordData) => boolean;
    }
  | {
      actionType: 'rpc';
      /** The RPC method; with it, the digest's Execute entry gates the button. */
      method?: Method;
      domain?: Domain;
      resourceData: RecordData;
      shouldRender: (data: RecordData) => boolean;
    };

@Component({
  selector: 'action-access-control-wrapper',
  styles: `
    :host {
      margin-top: auto;
      margin-bottom: auto;
    }
  `,
  template: `@if (showAction()) {
    <ng-content />
  }`,
})
export class ActionAccessControlWrapperComponent {
  private auth = inject(AuthService);

  actionContext = input<ActionButtonContext>();

  showAction = computed(() => {
    const context = this.actionContext();

    if (context === undefined) {
      return true;
    }

    if (!this.permitted(context)) {
      console.debug('ACCESS CONTROL | ', context.actionType, ' action not permitted for user in context: ', context);
      return false;
    }

    let showAction = true;
    try {
      showAction = context.shouldRender(context.resourceData);
    } catch (e) {
      console.error('Failed to calculate value for should Render function for action: ', context.actionType);
      console.error(e);
    }
    return showAction;
  });

  private permitted(context: ActionButtonContext): boolean {
    switch (context.actionType) {
      case 'create':
        return (
          !context.meta.createDisabled &&
          (context.resource === undefined ||
            this.auth.hasPermission({
              resource: context.resource,
              permission: CreatePermission,
              domain: context.domain,
            }))
        );
      case 'edit':
        return !context.meta.updateDisabled && (context.capabilities?.Update?.length ?? 1) > 0;
      case 'delete':
        return !context.meta.deleteDisabled && context.capabilities?.Delete !== false;
      case 'rpc':
        return (
          context.method === undefined ||
          this.auth.hasPermission({ resource: context.method, permission: ExecutePermission, domain: context.domain })
        );
    }
  }
}
