import { EnvironmentProviders, inject, makeEnvironmentProviders, provideEnvironmentInitializer } from '@angular/core';
import { MAINTENANCE_HANDLER, VERSION_REFUSAL_HANDLER } from '@impulseframework/resource-angular/types';
import { AppUpdateService } from './app-update.service';
import { MaintenanceService } from './maintenance.service';

/**
 * Starts AppUpdateService with the application and makes it the handler of the server's
 * refusal of this build's release, and makes MaintenanceService the handler of the
 * server's maintenance answer: add it to the application's providers beside
 * provideServiceWorker. No component references either service; the notices they raise
 * render in the alert area every application already has.
 */
export function provideAppUpdate(): EnvironmentProviders {
  return makeEnvironmentProviders([
    { provide: VERSION_REFUSAL_HANDLER, useExisting: AppUpdateService },
    { provide: MAINTENANCE_HANDLER, useExisting: MaintenanceService },
    provideEnvironmentInitializer(() => inject(AppUpdateService).start()),
  ]);
}
