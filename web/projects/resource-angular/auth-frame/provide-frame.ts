import {
  computed,
  EnvironmentProviders,
  inject,
  Injector,
  provideEnvironmentInitializer,
  Provider,
} from '@angular/core';
import { Domain } from '@impulseframework/resource';
import { RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { AccountLook } from './account-look';
import { FRAME_CONFIG, FrameConfig } from './frame';
import { TenantService } from './tenant.service';

/**
 * Provides the frame: the configuration, and the selected tenant every library page reads
 * (RESOURCE_DOMAIN) as the tenant the URL is open in. It also starts AccountLook, which
 * keeps the look a person chose with their account, so the look follows them to every
 * browser they sign in from. Place it in the application's providers after
 * provideResourceClient.
 */
export function provideFrame(config: FrameConfig): (Provider | EnvironmentProviders)[] {
  return [
    { provide: FRAME_CONFIG, useValue: config },
    {
      provide: RESOURCE_DOMAIN,
      // The tenant service is resolved when the signal is first read, not when the token
      // is built: AuthService injects the token, and TenantService injects AuthService.
      useFactory: () => {
        const injector = inject(Injector);
        return computed(() => (injector.get(TenantService).current() || undefined) as Domain | undefined);
      },
    },
    provideEnvironmentInitializer(() => {
      inject(AccountLook);
    }),
  ];
}
