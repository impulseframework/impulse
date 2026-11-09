import { inject } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { CanDeactivateFn, UrlTree } from '@angular/router';
import { LeavePageConfirmationModalComponent } from '@impulseframework/resource-angular/resource-modals';
import { FormStateService } from '@impulseframework/resource-angular/resource-services';
import { FRONTEND_LOGIN_PATH } from '@impulseframework/resource-angular/types';
import { firstValueFrom, Observable, tap } from 'rxjs';

export type CanDeactivateType = Observable<boolean | UrlTree> | Promise<boolean | UrlTree> | boolean | UrlTree;

export interface CanComponentDeactivate {
  canDeactivate: () => CanDeactivateType;
}

export const canDeactivateGuard: CanDeactivateFn<CanComponentDeactivate> = async (_, __, ___, nextState) => {
  const dialog = inject(MatDialog);
  const formStateService = inject(FormStateService);
  const loginPath = inject(FRONTEND_LOGIN_PATH);

  if (nextState?.url.includes(loginPath)) {
    return true;
  }

  if (!formStateService.isDirty()) {
    return true;
  }

  const existingDialog = dialog.openDialogs.find(
    (d) => d.componentInstance instanceof LeavePageConfirmationModalComponent,
  );

  const dialogRef = existingDialog ?? dialog.open(LeavePageConfirmationModalComponent, { delayFocusTrap: false });

  const result = dialogRef.afterClosed().pipe(
    tap((value) => {
      if (value === true) {
        formStateService.resetDirtyForms();
      }
    }),
  );

  return firstValueFrom(result);
};
