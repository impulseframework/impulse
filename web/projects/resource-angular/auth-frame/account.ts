import { computed, inject, Injectable, resource } from '@angular/core';
import { LoginMethods } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/**
 * Account answers which sections the person's account page holds, from what the person's
 * sign-in offers: the email address where the server sends mail, the password change where
 * the person's auth has the password method, and forgetting this browser where it is
 * remembered for them. A page with none of them is not offered in the person's menu: a
 * portal whose people sign in through their company's directory has nothing there.
 */
@Injectable({ providedIn: 'root' })
export class Account {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);

  /** What the sign-in offers, read once; undefined until it answers or when it fails. */
  private methods = resource({ loader: (): Promise<LoginMethods> => this.client.auth.methods() });

  /** The server sends mail: the person adds or changes their address. */
  readonly email = computed(() => this.methods.hasValue() && this.methods.value().email !== undefined);
  /** The person's auth has the password method: they change their password. */
  readonly password = computed(() => this.methods.hasValue() && this.methods.value().changePassword !== undefined);
  /** This browser is remembered for the person, and the server serves forgetting it. */
  readonly browser = computed(
    () =>
      this.auth.sessionInfo().browserRemembered === true &&
      this.methods.hasValue() &&
      this.methods.value().forgetBrowser !== undefined,
  );
  /** The page holds anything at all. */
  readonly any = computed(() => this.email() || this.password() || this.browser());
}
