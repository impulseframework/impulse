import { Component, computed, inject, input, ResourceRef } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { ActivatedRoute, Router } from '@angular/router';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import {
  AlertType,
  FieldPointer,
  ListViewConfig,
  METHOD_META,
  RecordData,
  Resource,
  RootConfig,
  RPCConfig,
  RPCFieldMeta,
  RpcMethod,
  RPCRecordData,
  ViewConfig,
} from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { filter, tap } from 'rxjs';
import { BaseRPCModalComponent } from '../../base-rpc-modal/base-rpc-modal.component';
import { ResourceStore } from '../../resource-store.service';
import { metadataTypeCoercion } from '../../resources-helpers';
import { holdsValue } from '../../row-methods/row-methods';

@Component({
  selector: 'impulse-rpc-button',
  imports: [MatButtonModule],
  templateUrl: './rpc-button.component.html',
  styleUrls: ['./rpc-button.component.scss'],
  providers: [ResourceStore],
})
export class RpcButtonComponent {
  activatedRoute = inject(ActivatedRoute);
  router = inject(Router);
  notifications = inject(NotificationService);
  formState = inject(FormStateService);
  dialog = inject(MatDialog);
  store = inject(ResourceStore);

  methodMeta = inject(METHOD_META);

  relatedData = input.required<RecordData>({});
  rpcConfig = input.required<RPCConfig>();
  primaryResource = input.required<Resource>();
  dependentResources = input<ResourceRef<RecordData[]>[]>([]);

  resourceConfigRouteSnapshot = computed(() => {
    return (
      this.rpcConfig() ||
      (this.activatedRoute.snapshot.data['config'] as RootConfig)?.parentConfig ||
      ({} as ViewConfig | ListViewConfig)
    );
  });

  showRPCButton = computed(() => {
    const rpcConfig = this.rpcConfig();
    if (!rpcConfig) {
      return false;
    }
    let canView = true;
    for (const condition of rpcConfig.conditions || []) {
      const filterField = condition.field;
      const matchValues = condition.matchValues;
      const parentFieldData = this.relatedData()[filterField ? filterField : ''];
      for (const matchValue of matchValues) {
        if (parentFieldData === matchValue) {
          canView = true;
          break;
        }
        canView = false;
      }
      if (!canView) {
        break;
      }
    }
    return canView;
  });

  submitRPC(): void {
    const rpcConfig = this.rpcConfig();
    if (!rpcConfig) {
      console.error('RPC config is not defined');
      return;
    }

    const submitBody: RPCRecordData = {};
    if (rpcConfig.elements.length === 0) {
      this.buildRpcBody(rpcConfig, submitBody);
      return;
    }

    const dialogRef = this.dialog.open(BaseRPCModalComponent, {
      data: {
        label: rpcConfig.label,
        elements: rpcConfig.elements,
        method: rpcConfig.method,
        width: rpcConfig.defaultModalWidth,
      },
    });
    const meta = this.methodMeta(rpcConfig.method);
    dialogRef
      .afterClosed()
      .pipe(
        filter((data) => !!data),
        tap((data) => {
          const coercedData = metadataTypeCoercion(data as RPCRecordData, meta);
          // A value the form holds is sent as it is: false and 0 are values, and only an
          // input left empty sends nothing.
          Object.keys(coercedData).forEach((key: string) => {
            const value = coercedData[key];
            if (holdsValue(value)) {
              submitBody[key] = value;
            }
          });
          this.buildRpcBody(rpcConfig, submitBody);
        }),
        tap(() => {
          this.reloadDependentResources();
        }),
      )
      .subscribe();
  }

  buildRpcBody(rpcConfig: RPCConfig, submitBody: RPCRecordData): void {
    let canSubmitRPC = true;
    const method = this.methodMeta(rpcConfig.method);
    if (method.fields) {
      method.fields.forEach((methodField: RPCFieldMeta) => {
        const key = methodField.fieldName;
        if (submitBody[key] === undefined || submitBody[key] === null || submitBody[key] === '') {
          if (!method.fields) {
            return;
          }
          const value = rpcConfig.methodBodyTemplate[key as keyof RpcMethod] as string | FieldPointer;
          if (value == undefined) {
            return;
          }
          if (typeof value !== 'string') {
            const returnValue = this.relatedData()[value.field];
            if (returnValue === undefined) {
              this.notifications.addGlobalNotification({
                message: 'Value not found for field: ' + value.field,
                link: '',
                type: AlertType.ERROR,
              });
              canSubmitRPC = false;
              return;
            }
            if (holdsValue(returnValue)) submitBody[key] = returnValue as string | number | string[] | number[] | boolean;
          } else {
            submitBody[key] = value;
          }
        }
      });
    }

    if (!canSubmitRPC) {
      return;
    }
    this.callRpc(rpcConfig, submitBody);
  }

  callRpc(rpc: RPCConfig, submitBody: RPCRecordData): void {
    this.store
      .rpcCall(rpc, submitBody)
      .pipe(
        tap(() => {
          this.reloadDependentResources();
        }),
      )
      .subscribe();
  }

  reloadDependentResources(): void {
    for (const ref of this.dependentResources()) {
      ref.reload();
    }
  }
}
