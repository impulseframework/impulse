import { computed, inject, Injectable, resource } from '@angular/core';
import { LoginMethods } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/**
 * Account answers which sections the person's account page holds, from what the person's
 * sign-in offers: their name where the server serves its change, the email address where
 * the server sends mail, the password change where the person's auth has the password
 * method, and forgetting this browser where it is remembered for them. A page with none
 * of them is not offered in the person's menu.
 */
@Injectable({ providedIn: 'root' })
export class Account {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);

  /** What the sign-in offers, read once; undefined until it answers or when it fails. */
  private methods = resource({ loader: (): Promise<LoginMethods> => this.client.auth.methods() });

  /**
   * The server serves the change of a person's own name, and the session is the person's
   * own: an impersonated session may not write the name of the person it acts as.
   */
  readonly name = computed(
    () =>
      this.auth.sessionInfo().impersonation === undefined &&
      this.methods.hasValue() &&
      this.methods.value().changeName !== undefined,
  );
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
  readonly any = computed(() => this.name() || this.email() || this.password() || this.browser());
}
