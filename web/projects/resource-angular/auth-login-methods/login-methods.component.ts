import { DOCUMENT } from '@angular/common';
import {
  ChangeDetectorRef,
  Component,
  computed,
  inject,
  input,
  OnInit,
  output,
  PendingTasks,
  signal,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { ActivatedRoute } from '@angular/router';
import {
  EmailCodeResult,
  EmailCodeStep,
  invitationAddressCode,
  LoginMethod,
  LoginMethods,
  OpenedInvitation,
  passwordChangeRequiredCode,
  PendingSignIn,
  returnPath,
  SignInRefusal,
  SignInResult,
} from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import { FRONTEND_LOGIN_PATH, FRONTEND_SIGN_OUT_PATH, SessionInfo } from '@impulseframework/resource-angular/types';
import { MailHold, UiCoreService } from '@impulseframework/resource-angular/ui-core-service';
import { firstValueFrom } from 'rxjs';
import { map } from 'rxjs/operators';

/**
 * The form an account with a password is created on: from an opened invitation, which the
 * browser carries, or from a sign-up link, whose token the form posts back. The address is
 * the one the link was mailed to; the username is that address where the password method
 * declares them one, and the person's to choose otherwise.
 */
export interface AccountForm {
  readonly source: 'invitation' | 'signUp';
  readonly email: string;
  readonly usernameIsEmail: boolean;
  /** The sign-up link's token; absent for an invitation, which the browser carries. */
  readonly token?: string;
}

/**
 * Where a sign-in lands. A page of the application, the router URL the login guard
 * recorded or `home`, is emitted as `signedIn` and the page routes there. An address the
 * server named, in this page's `returnUrl` or in the pending status, is a browser path the
 * browser is sent to with a full navigation (`load`): it may be a page the server serves
 * that no route of the application draws, and an application served at the root cannot
 * tell such a page from its own by the path, so where the address came from decides.
 */
export interface Landing {
  readonly url: string;
  readonly load: boolean;
}

/**
 * The login page's sign-in methods, rendered from what the server offers. On arrival the
 * component reads the methods list through the application's client and renders it: the
 * password form when the list has a password method, and one button per redirect method,
 * in the server's order; nothing it shows is named here, so the bundle is the same in
 * every environment and a method that is not registered has no button. The server's label
 * is the default for each method and `labels` overrides it by registration name; nothing
 * else about a method is the page's to change. Where the list says the password method's
 * username is the person's email address, the form asks for the address.
 *
 * It owns the journey from the list to the session. A password sign-in posts through the
 * client, which reads the list again first so the POST carries a live XSRF token; the
 * session established, the component checks it through AuthService (loading the digest,
 * the domains and the feature flags) and emits `signedIn` with the router URL of the page
 * to go to: the page the login guard turned away from, else `home`. The page navigates
 * there and starts whatever it starts on a sign-in (the idle service). A redirect method
 * sends the browser to the method's path, naming the same page as the one to return to;
 * the server's callback brings the browser back there signed in, or back to this page with
 * `?code=` for a refusal or `?pending=` for a step that waits.
 *
 * A `returnUrl` in this page's own URL names a browser path to return to, as the server
 * sends one: the persona sign-in's picker, a page the server serves behind the session,
 * sends a browser without one here naming itself. The component keeps the address through
 * the sign-in's steps, names it as the page a redirect method returns to, and once the
 * session stands sends the browser there with a full navigation instead of emitting
 * `signedIn`, since no route of the application draws it; the address the pending status
 * reports is followed the same way. Where the address came from decides, not its path: a
 * page the guard recorded is the application's and is routed to, one the server handed
 * over is loaded. Only a path in this application in canonical form is kept
 * (`returnPath`), and never this page, the sign-out page or another origin.
 *
 * After a refusal, every redirect button asks the provider for its account chooser
 * (`prompt=select_account` on the method's path), so a person refused with the wrong
 * account picks another instead of being sent round the same loop by a provider that
 * signs its one signed-in account in again without asking; on a stop without a refusal, a
 * "Use a different account" button under each redirect button asks the same, and the
 * redirect button itself asks nothing, so the single-account person pays no click. An
 * arrival from a deep link, the guard having recorded the page, into an application whose
 * only method is a redirect forwards to it at once, rendering nothing: the provider signs
 * the person in and returns them to the page, an expired session included. The page
 * stops, with its buttons, when the guard recorded nothing (a direct visit, a bookmark, the
 * page sign-out lands on), when the URL carries a refusal code or a step or a mailed link's
 * token, when the list offers more than one method or a password, and when a session is
 * found standing.
 *
 * A refusal arriving in the URL renders the sentence the application holds for the code
 * (`LOGIN_MESSAGES`, through UiCoreService) and nothing for a code it does not know; one
 * the server answers on a step renders that sentence when its body carries a code, whatever
 * the status, and the server's words otherwise (a wrong password). A name whose failed
 * attempts reached the server's limit is refused with 429, `too_many_attempts` and the wait,
 * rendered after the sentence: "Too many attempts. Try again in 14 minutes."; the step it
 * was refused on stays where it is. A pending step, entered with `?pending=` or
 * answered to a password sign-in, is read from the pending status route and takes the
 * list's place: for a confirmation, the password form of the user the sign-in matched,
 * posting to the confirmation route, with a cancel that returns to the list; for a step
 * the server names a page for, the browser goes there. A sign-in held for the second
 * factor, when the policy accepts the mailed code (the status carries `emailCode`), runs
 * the code step: the code is mailed at once to the address the account holds, and the step
 * says where it went over the code field, a "Remember this browser" box, and a button for
 * a new code; a person whose account holds no address is asked for one first. A wrong code
 * (`invalid_code`) or one entered after the code sent died (`code_expired`) keeps the form
 * with its sentence; with the box checked, the server remembers the browser, and this
 * person's next sign-ins from it skip the code. An address the server will not mail yet
 * (`too_many_emails`, 429 with the wait: codes and links sent to it and not used) renders
 * the sentence and the wait, and the buttons that ask for mail stay disabled until the wait
 * has passed. A step this page does not render, a reason
 * it does not know or a second-factor step without the mailed code, shows the note that the
 * page is older than the server, asking for a reload, with the cancel; the steps the page
 * renders are declared in `SIGN_IN_STEPS` and, for the framework's release check, under
 * `impulseframework.signInSteps` in the package's package.json.
 *
 * The mailed links the server sends all lead here, each with its token in the URL. An
 * invitation (`?invitation=`) is opened through the client on arrival and the card becomes
 * the invitation screen: "You're invited to <tenant>" and the address, over every way that
 * can accept this invitation for that address and none that cannot (`invitationPaths`). The
 * browser now carries the invitation, so the sign-in that follows accepts it and seats the
 * person with the roles it carried. A directory registration the server says could admit
 * the address (`methods`) is a "Continue with <label> as <address>" button, which sends the
 * address to the provider as its login hint, so the provider takes that account; the
 * server accepts a directory sign-in only as the invited address, refusing another with
 * `invitation_address`, and the screen then says so, naming the address. The password form
 * shows when an account with a password holds the address (`accountExists`, `hasPassword`),
 * with a note saying it accepts; and where no account holds the address and the server
 * offers a password method, the account form opens by itself, without a Cancel, but only
 * where no directory could admit the address, since the invited person has that account
 * already. "Use a different account" is not offered while an invitation stands: the
 * invitation names the account. A person the page finds already signed in accepts it with
 * one click, from a button under the heading, when their account holds the invited address
 * or none; signed in as an account that holds another address, they are told whose the
 * invitation is and how to accept it, since the server refuses that acceptance too
 * (`acceptOffered`). Under an older server that does not say what
 * holds the address every path is offered, the account form behind a "Create an account
 * with a password" button that comes last, under the password form and the redirect
 * buttons, where the open "Create an account" step stands when no invitation does. The
 * browser keeps carrying the invitation when a redirect method refuses the sign-in and
 * brings it back here with `?code=` and no token; the methods list then names the
 * invitation under `invitation.standing`, and the screen stands again, with the refusal's
 * sentence under it, so the person sees what they were invited to, the ways to sign in, and
 * why the last attempt was refused, together. A token in the URL is opened instead, which
 * also refreshes what the browser carries. A sign-up link
 * (`?signUp=`) is opened the same way and renders the account form in the list's place; the
 * list of a server whose password method offers sign-up also carries the "Create an account"
 * step, which asks for an address and mails it the link. A confirmation link (`?verify=`) is
 * posted once a session stands, on arrival when the page finds one, else after the
 * sign-in it waits for (a redirect method returns to this page with the token); the
 * component then shows what happened and emits `signedIn` on Continue. On every arrival
 * the component renders nothing until the reads have answered (`arriving`, which a page
 * holds its card back on, so a load shows nothing and then the whole card), so the form
 * shows only to a person found signed out; one found signed in goes straight on to what
 * the link asks for, or is offered Continue and Sign out, and is never shown a form first.
 * Reads that fail (no answer, or an answer with no words to show: a gateway's 502 or 504)
 * render the sentence held for `unreachable` and a Try again button in the list's place,
 * never the status; Try again runs the arrival again, the card held back meanwhile.
 *
 * A session the sign-in establishes may carry `passwordChangeRequired`: an operator set
 * the password, and the server serves nothing but the password change until the person
 * sets one of their own. The component then renders the change form in the list's place,
 * with the sentence the application holds for `password_change_required` over it, posts
 * the old and the new password through the client to the change path the methods list
 * names, checks the session again and emits `signedIn` with the page recorded at the
 * sign-in; a refusal (the old password did not match, the new one fails the server's
 * rule) is rendered in place. Canceling the change ends the session and shows the list
 * again. Everything around the methods, the branding, a manifest, a forgotten-password
 * link, is the page's, composed around the component.
 */
@Component({
  selector: 'impulse-login-methods',
  imports: [FormsModule, MatButtonModule, MatCheckboxModule, MatFormFieldModule, MatInputModule],
  templateUrl: './login-methods.component.html',
  styleUrl: './login-methods.component.scss',
})
export class LoginMethodsComponent implements OnInit {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);
  private ui = inject(UiCoreService);
  private route = inject(ActivatedRoute);
  private document = inject(DOCUMENT);
  private loginPath = inject(FRONTEND_LOGIN_PATH);
  private signOutPath = inject(FRONTEND_SIGN_OUT_PATH);
  /** Every request is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);
  private changes = inject(ChangeDetectorRef);

  /** The application's own words for a method, by registration name; the server's label otherwise. */
  labels = input<Readonly<Record<string, string>>>({});
  /** The router URL a sign-in lands on when the login guard recorded no page: the root by default. */
  home = input('/');
  /**
   * The sign-in is complete and the session checked: the router URL of the page to go to.
   * Not emitted when the browser is sent to an address the server named (`returnUrl`):
   * the page unloads instead.
   */
  signedIn = output<string>();
  /** The person cancelled the pending step, the password change, the account form or the sign-up; the list is shown again. */
  cancelled = output<void>();

  username = '';
  password = '';
  confirmation = '';
  /** The sign-in code typed, and whether this browser is to be remembered so it is not asked for a code next time. */
  signInCode = '';
  rememberDevice = false;
  /** The address a person whose account holds none gives for the code. */
  codeEmail = '';
  oldPassword = '';
  newPassword = '';
  newPasswordAgain = '';
  /** The account form's fields: the username where the person chooses one, and the password, typed twice. */
  accountUsername = '';
  accountPassword = '';
  accountPasswordAgain = '';
  /** The address a sign-up asks its link for. */
  signUpEmail = '';
  /** A request of the component is in flight; its controls are disabled meanwhile. */
  busy = signal(false);
  /**
   * The server holds its mail to the address: a code or link asked for was refused with
   * `too_many_emails`, and the wait it named has not passed. The buttons that ask for mail
   * (Send code, Send a new code, Send me a link) stay disabled until it has.
   */
  private mailHold = new MailHold();
  mailHeld = this.mailHold.held;

  /** The methods list as the client last read it; undefined before the first read answers. */
  private list = storeSignal(this.client.auth.snapshot);
  /** The methods the server offers, in its order; empty until the list answers. */
  methods = computed<readonly LoginMethod[]>(() => this.list()?.methods ?? []);
  /** The password form's method, when the server offers one. */
  passwordMethod = computed(() => this.methods().find((method) => method.kind === 'password'));
  /** The redirect methods, one button each, in the server's order. */
  redirectMethods = computed(() => this.methods().filter((method) => method.kind === 'redirect'));
  /**
   * The redirect methods offered on screen: while an invitation stands that names the
   * registrations able to admit the invited address (`methods`), those alone, in the
   * server's order, and none when it names none; every redirect method otherwise.
   */
  offeredRedirectMethods = computed(() => {
    const admitting = this.invitation()?.methods;
    const redirects = this.redirectMethods();
    return admitting ? redirects.filter((method) => admitting.includes(method.name)) : redirects;
  });
  /**
   * What the card offers for the invitation it carries, from what the server said of the
   * invited address: the directory buttons that could admit it (`redirects`); the password
   * form when an account with a password holds the address (`password`); and the account
   * form when no account holds it and the server offers a password method, only where no
   * directory could admit it, since the invited person has that account already (`create`).
   * `known` is false under an older server that did not say whether an account exists:
   * every path is then offered, as before. Undefined while no invitation stands.
   */
  invitationPaths = computed(() => {
    const invited = this.invitation();
    if (!invited) {
      return undefined;
    }
    const redirects = this.offeredRedirectMethods();
    const known = invited.accountExists !== undefined;
    const passwordListed = this.passwordMethod() !== undefined;
    return {
      known,
      redirects,
      password: passwordListed && (!known || invited.hasPassword === true),
      create: invited.signUp && (!known || (!invited.accountExists && redirects.length === 0)),
    };
  });
  /** The password method's username is the person's email address: the form asks for the address. */
  usernameIsEmail = computed(() => this.list()?.usernameIsEmail ?? false);
  /** The words over the password form's first field. */
  usernameLabel = computed(() => (this.usernameIsEmail() ? 'Email address' : 'Username'));
  /** The server's password method lets a stranger create an account from a mailed link. */
  signUpOffered = computed(() => this.list()?.signUp !== undefined);
  /** The step the sign-in waits on, taking the list's place; undefined while the list is shown. */
  pending = signal<PendingSignIn | undefined>(undefined);
  /** The note for a step this page does not render (PAGE_OLDER_THAN_SERVER), for the template. */
  readonly pageOlderThanServer = PAGE_OLDER_THAN_SERVER;
  /** The session carries `passwordChangeRequired`: the change form takes the list's place until the person sets a password of their own. */
  changing = signal(false);
  /** The sentence over the change form: the one the application holds for the server's code. */
  changeNote = computed(() => this.ui.loginMessage(passwordChangeRequiredCode));
  /** The landing the sign-in recorded, handed on once the password is changed. */
  private afterChange: Landing = { url: '/', load: false };
  /** The invitation the URL's token opened, or the one the browser was found carrying; its note stands over the list until the sign-in that accepts it. */
  invitation = signal<OpenedInvitation | undefined>(undefined);
  /** The username of the session the page found standing on arrival: with an invitation, who may accept it as they are; alone, who may continue or sign out. */
  signedInAs = signal<string | undefined>(undefined);
  /** The address the signed-in person's account holds, if any: with an invitation, whether they may accept it as they are. */
  signedInEmail = signal<string | undefined>(undefined);
  /**
   * The person found signed in may accept the invitation as they are: their account holds
   * the invited address, or no address at all (it takes the invited one by accepting). An
   * account that holds another address may not, and the server refuses it too: the screen
   * says so and offers the ways to sign in as the invited account instead.
   */
  acceptOffered = computed(() => {
    const invited = this.invitation();
    if (!this.signedInAs() || !invited) {
      return false;
    }
    const email = this.signedInEmail();
    return email === undefined || email === '' || sameAddress(email, invited.email);
  });
  /** The username of a session found standing with nothing in the URL to act on: Continue and Sign out take the list's place. */
  signedInAlone = computed(() => (this.invitation() ? undefined : this.signedInAs()));
  /** The account form, taking the list's place: an account with a password, from the invitation or a sign-up link. */
  account = signal<AccountForm | undefined>(undefined);
  /** The account form opened by itself, the one way to accept the invitation: no Cancel, since there is nothing to go back to. */
  accountOpenedItself = signal(false);
  /** The sign-up's first step is shown: the address field. */
  starting = signal(false);
  /** The address a sign-up link was sent to; the note takes the list's place. */
  started = signal<string | undefined>(undefined);
  /** A session stands and a note about the followed link shows; the landing waits on Continue. */
  landed = signal<Landing | undefined>(undefined);
  /** The address the followed link confirmed, shown while `landed`. */
  verified = signal<string | undefined>(undefined);
  /**
   * The arrival's reads are in flight: the component renders nothing until they have
   * answered, and a page holds its card back on this signal (`methods.arriving()` through a
   * template reference), so a load shows nothing and then the whole card, the way a page
   * loads. True from the start, so the first render is already held.
   */
  arriving = signal(true);
  /**
   * The arrival's reads failed: no answer at all, or one with no words the page could
   * show (a gateway's 502 or 504, a 500). The card offers Try again in the list's place
   * and never renders the status; the browser's network log has it.
   */
  unreachable = signal(false);
  /** The sentence shown while `unreachable`: the one held for the code `unreachable`, reworded by an application like any other. */
  readonly unreachableNote = this.ui.loginMessage('unreachable');
  /** The confirmation link's token, posted once a session stands. */
  private verifyToken = '';
  /**
   * The browser path this page's URL named to return to (`returnUrl`), kept through the
   * sign-in's steps; undefined when the URL names none the server would keep, or names
   * this page or the sign-out page.
   */
  private returnAddress: string | undefined;

  /** The refusal code the URL carries, or empty: the only thing the URL says. */
  private code = toSignal(this.route.queryParamMap.pipe(map((params) => params.get('code') ?? '')), {
    initialValue: '',
  });
  /** The sentence of a refusal answered on a step, or empty; it replaces the URL's once a step has run. */
  private refusal = signal<string | undefined>(undefined);
  /**
   * The refusal sentence on screen: a step's answer once there is one, else the sentence
   * held for the URL's code; nothing while the server is unreachable, so one message
   * stands at a time, and the URL's sentence shows again once Try again succeeds.
   */
  message = computed(() => {
    if (this.unreachable()) {
      return undefined;
    }
    const refusal = this.refusal();
    if (refusal !== undefined) {
      return refusal;
    }
    const invited = this.invitation();
    if (this.code() === invitationAddressCode && invited) {
      return `This invitation is for ${invited.email}. Sign in with that account.`;
    }
    return this.ui.loginMessage(this.code());
  });
  /** The URL carries a refusal: the redirect buttons ask the provider for its account chooser by themselves, and "Use a different account" is not offered. */
  afterRefusal = computed(() => this.code() !== '');
  /** "Use a different account" was pressed: the redirect buttons ask the provider for its account chooser, and a note stands where the link was. */
  differentAccount = signal(false);
  /**
   * The redirect buttons ask the provider for its account chooser, and say so ("Choose a
   * Google account"): after a refusal in the URL, or once "Use a different account" was
   * pressed. Otherwise a redirect button asks nothing, so the single-account person pays
   * no click.
   */
  choosing = computed(() => this.afterRefusal() || this.differentAccount());

  ngOnInit(): void {
    void this.tracked(() => this.arrive());
  }

  /**
   * Fills the password form for the person, who still presses Sign in: a page that offers
   * its people as cards calls this with the one picked. The form's fields are bound to its
   * inputs, and the component is drawn again when something of its own changes, so a field
   * set from outside would not reach its input. This sets both and has the form drawn.
   */
  prefill(username: string, password: string): void {
    this.username = username;
    this.password = password;
    this.changes.markForCheck();
  }

  /** Try again: runs the arrival again, the card held back meanwhile as on a first load. */
  retry(): void {
    void this.tracked(() => this.arrive());
  }

  /** The sentence over the confirmation form: the account that matched, named by the identity's email when it asserted one. */
  confirmationNote(waiting: PendingSignIn): string {
    const matched = waiting.email
      ? `An account with a password matches this sign-in, ${waiting.email}.`
      : 'An account with a password matches this sign-in.';
    return `${matched} Enter that account's password to link the two.`;
  }

  /** The invitation screen's heading: where the person is invited. */
  invitationTitle(invitation: OpenedInvitation): string {
    return invitation.tenant === '' ? "You're invited" : `You're invited to ${invitation.tenant}`;
  }

  /**
   * The sentence under the heading: the address the invitation is for, and, under an older
   * server that did not say what holds the address, what accepts it.
   */
  invitationNote(invitation: OpenedInvitation): string {
    if (invitation.accountExists !== undefined) {
      return `The invitation is for ${invitation.email}.`;
    }
    const accepts = invitation.signUp
      ? 'Sign in to accept it, or create an account with a password.'
      : 'Sign in to accept it.';
    return `The invitation is for ${invitation.email}. ${accepts}`;
  }

  /** The sentence over the account form: what the person chooses, and the address the account holds. */
  accountNote(form: AccountForm): string {
    return form.usernameIsEmail
      ? `Choose a password for your account. You will sign in as ${form.email}.`
      : `Choose a username and a password for your account. ${form.email} is its email address.`;
  }

  /** The words for a method: the application's override by name, else the server's label. */
  labelOf(method: LoginMethod): string {
    return this.labels()[method.name] ?? method.label;
  }

  /**
   * A redirect button's words: "Continue with Google as pat@lakeside.edu" while an invitation
   * stands, since the invitation names the account; else "Sign in with Google", or "Choose a
   * Google account" while the chooser is asked for.
   */
  redirectCaption(method: LoginMethod): string {
    const label = this.labelOf(method);
    const invited = this.invitation();
    if (invited) {
      return `Continue with ${label} as ${invited.email}`;
    }
    if (!this.choosing()) {
      return `Sign in with ${label}`;
    }

    return `Choose ${/^[aeiou]/i.test(label) ? 'an' : 'a'} ${label} account`;
  }

  /** "Use a different account": from here every redirect button asks the provider for its account chooser. */
  useDifferentAccount(): void {
    if (this.busy()) {
      return;
    }
    this.differentAccount.set(true);
  }

  /** Posts the password form: the session, a pending step, or a refusal rendered in place. */
  login(): Promise<void> {
    const method = this.passwordMethod();
    if (this.busy() || !method) {
      return Promise.resolve();
    }
    return this.step(async () => {
      const result = await this.client.auth.signInWithPassword(method, {
        username: this.username,
        password: this.password,
      });
      await this.follow(result, () => this.landing());
    });
  }

  /** The sentence over the code step: where the code went, or where one goes. */
  codeNote(step: EmailCodeStep): string {
    return step.codeSent
      ? `We sent a sign-in code to ${step.codeEmail}. Enter it to finish signing in.`
      : `A sign-in code can be sent to ${step.codeEmail}.`;
  }

  /** Has the sign-in code mailed, replacing one standing. */
  sendCode(): Promise<void> {
    if (this.busy() || this.mailHeld() || !this.pending()?.emailCode) {
      return Promise.resolve();
    }
    return this.step(() => this.mailCode(() => this.client.auth.sendCode()));
  }

  /** Posts the address a person whose account holds none typed, and has the code mailed to it. */
  enrollEmail(): Promise<void> {
    if (this.busy() || this.mailHeld() || !this.pending()?.emailCode) {
      return Promise.resolve();
    }
    return this.step(() => this.mailCode(() => this.client.auth.enrollEmail(this.codeEmail)));
  }

  /** Enters the sign-in code, asking for this browser to be remembered when the box is checked. */
  verifyCode(): Promise<void> {
    const waiting = this.pending();
    if (this.busy() || !waiting?.emailCode) {
      return Promise.resolve();
    }
    return this.step(async () => {
      const result = await this.client.auth.verifyCode(this.signInCode, this.rememberDevice);
      await this.follow(result, () => this.returned(waiting));
    });
  }

  /**
   * Sends the browser to a redirect method's path, naming the page the sign-in returns to:
   * the address the URL named, the page the guard recorded, or this page with the
   * confirmation link's token when one waits to be posted, since the round trip through
   * the identity provider reloads the application. The provider is asked for its account
   * chooser while the card is choosing (after a refusal, or once "Use a different account"
   * was pressed), and is told the account to take while an invitation stands: the invited
   * address, as its login hint.
   */
  signInThrough(method: LoginMethod): void {
    if (this.busy()) {
      return;
    }
    const page: Landing =
      this.verifyToken === ''
        ? this.landing()
        : { url: `${this.loginPath}?verify=${encodeURIComponent(this.verifyToken)}`, load: false };
    const invited = this.invitation();
    this.leaveFor(
      this.client.auth.signInUrl(method, this.returnTo(page), {
        selectAccount: this.choosing(),
        ...(invited ? { loginHint: invited.email } : {}),
      }),
    );
  }

  /** Confirms the pending sign-in with the password of the user it matched. */
  confirm(): Promise<void> {
    const waiting = this.pending();
    if (this.busy() || !waiting) {
      return Promise.resolve();
    }
    return this.step(async () => {
      const result = await this.client.auth.confirmPending(this.confirmation);
      await this.follow(result, () => this.returned(waiting));
    });
  }

  /** Discards the pending sign-in and shows the list again. */
  cancel(): Promise<void> {
    if (this.busy() || !this.pending()) {
      return Promise.resolve();
    }
    return this.step(async () => {
      await this.client.auth.cancelPending();
      this.pending.set(undefined);
      this.confirmation = '';
      this.clearCodeForm();
      this.cancelled.emit();
    });
  }

  /**
   * Accepts the opened invitation as the person the page found signed in, then checks the
   * session and hands the page on. A refusal for another address (403) keeps the
   * invitation standing and shows the server's sentence; any other refusal means the
   * invitation is gone.
   */
  accept(): Promise<void> {
    if (this.busy() || !this.acceptOffered()) {
      return Promise.resolve();
    }
    return this.step(async () => {
      const result = await this.client.auth.acceptInvitation();
      if (result.outcome === 'refused') {
        if (result.status === 403) {
          this.refusal.set(this.sentenceOf(result));
          return;
        }
        this.leaveInvitation(result);
        return;
      }
      await this.complete(this.landing());
    });
  }

  /** The sentence for a person signed in as an account that may not accept the invitation: whose it is, and how to accept it. */
  signedInOtherNote(name: string, invitation: OpenedInvitation): string {
    return `You are signed in as ${name}. To accept this invitation, sign in as ${invitation.email}.`;
  }

  /**
   * Shows the account form for the opened invitation: an account with a password, which
   * accepts it. `itself` marks a form the screen opened without being asked, the one way
   * to accept, which offers no Cancel.
   */
  openAccountForm(itself = false): void {
    const invitation = this.invitation();
    if (this.busy() || !invitation?.signUp) {
      return;
    }
    this.refusal.set(undefined);
    this.accountOpenedItself.set(itself);
    this.account.set({ source: 'invitation', email: invitation.email, usernameIsEmail: invitation.usernameIsEmail });
  }

  /**
   * Posts the account form: the password, typed twice, and the username where the person
   * chooses one. Two passwords that differ are refused here, with no request; the server's
   * refusal is rendered in place, and one that says the link is gone returns to the list.
   * An account the server creates is signed in, and the sign-in is followed as any other.
   */
  createAccount(): Promise<void> {
    const form = this.account();
    if (this.busy() || !form) {
      return Promise.resolve();
    }
    if (this.accountPassword !== this.accountPasswordAgain) {
      this.refusal.set(PASSWORDS_DIFFER);
      return Promise.resolve();
    }
    return this.step(async () => {
      const account = {
        password: this.accountPassword,
        ...(form.usernameIsEmail ? {} : { username: this.accountUsername }),
      };
      const result =
        form.source === 'invitation'
          ? await this.client.auth.signUpFromInvitation(account)
          : await this.client.auth.completeSignUp(form.token ?? '', account);
      await this.follow(result, () => this.landing());
    });
  }

  /** Leaves the account form and shows the list again; nothing is posted. */
  cancelAccount(): void {
    if (this.busy() || !this.account()) {
      return;
    }
    this.clearAccountForm();
    this.refusal.set(undefined);
    this.cancelled.emit();
  }

  /** Shows the sign-up's first step: the address its link is mailed to. */
  beginSignUp(): void {
    if (this.busy() || !this.signUpOffered()) {
      return;
    }
    this.refusal.set(undefined);
    this.starting.set(true);
  }

  /** Posts the address: a link to finish the sign-up is mailed to it, and the note says so whatever the address's state. */
  sendSignUpLink(): Promise<void> {
    if (this.busy() || this.mailHeld() || !this.starting()) {
      return Promise.resolve();
    }
    return this.step(async () => {
      const result = await this.client.auth.startSignUp(this.signUpEmail);
      if (result.outcome === 'refused') {
        this.mailHold.hold(result);
        this.refusal.set(this.sentenceOf(result));
        return;
      }
      this.started.set(this.signUpEmail);
      this.starting.set(false);
      this.signUpEmail = '';
    });
  }

  /** Leaves the sign-up, before or after its link was sent, and shows the list again. */
  backToSignIn(): void {
    if (this.busy() || (!this.starting() && !this.started())) {
      return;
    }
    this.starting.set(false);
    this.started.set(undefined);
    this.signUpEmail = '';
    this.refusal.set(undefined);
    this.cancelled.emit();
  }

  /** Hands on the page a session that followed a link waits with. */
  proceed(): void {
    const page = this.landed();
    if (!page) {
      return;
    }
    this.landed.set(undefined);
    this.verified.set(undefined);
    this.land(page);
  }

  /** Keeps the session the page found standing: it is checked again and `signedIn` emitted with the page the guard recorded, else `home`. */
  continueAs(): Promise<void> {
    if (this.busy() || !this.signedInAlone()) {
      return Promise.resolve();
    }
    return this.step(() => this.complete(this.landing()));
  }

  /** Ends the session the page found standing and shows the list: the sign-in that follows posts on no session. */
  signOut(): Promise<void> {
    if (this.busy() || !this.signedInAlone()) {
      return Promise.resolve();
    }
    return this.step(async () => {
      await firstValueFrom(this.auth.logout());
      this.signedInAs.set(undefined);
      this.signedInEmail.set(undefined);
    });
  }

  /**
   * Posts the change form: the old password and the new one, typed twice. Two new
   * passwords that differ are refused here, with no request; the server's refusal is
   * rendered in place; a change the server accepts checks the session again and emits
   * the page the sign-in recorded.
   */
  changePassword(): Promise<void> {
    if (this.busy() || !this.changing()) {
      return Promise.resolve();
    }
    if (this.newPassword !== this.newPasswordAgain) {
      this.refusal.set(PASSWORDS_DIFFER);
      return Promise.resolve();
    }
    return this.step(async () => {
      const result = await this.client.auth.changePassword(this.oldPassword, this.newPassword);
      if (result.outcome === 'refused') {
        this.refusal.set(this.sentenceOf(result));
        return;
      }
      this.oldPassword = '';
      this.newPassword = '';
      this.newPasswordAgain = '';
      await this.complete(this.afterChange);
    });
  }

  /** Leaves the password as the operator set it: ends the session and shows the list again. */
  cancelChange(): Promise<void> {
    if (this.busy() || !this.changing()) {
      return Promise.resolve();
    }
    return this.step(async () => {
      await firstValueFrom(this.auth.logout());
      this.changing.set(false);
      this.oldPassword = '';
      this.newPassword = '';
      this.newPasswordAgain = '';
      this.cancelled.emit();
    });
  }

  /**
   * Reads the list, the page's first request, then what the URL asks for: the pending
   * step when one waits, the invitation or the sign-up a token names, and the
   * confirmation link's token, posted at once when the page finds a session standing.
   * An invitation the list says the browser carries, with no token in the URL to open,
   * is kept over the list the way an opened one is, the session read for who may accept
   * it. Throughout, `arriving` holds the list back: nothing renders until the reads have
   * answered, so a person found signed in goes straight on to what the link asks for and
   * is never shown a form to fill in, and nothing stands in for the page meanwhile. With nothing in the URL and
   * no invitation carried, the page asks for the session: the page ends none on arrival,
   * so one it finds standing is offered Continue or Sign out in the list's place, and one
   * that must change its password gets the change form; the form appears only once that
   * read says no session stands. When that read finds none, the list's only method is a
   * redirect, the URL carries no refusal and the guard recorded a page, the browser is
   * forwarded to the method at once and the card stays held back, so a deep link into an
   * application with one way to sign in never shows this page. A read that fails ends the
   * arrival in `unreachable`: the card offers Try again, which runs the arrival again.
   */
  private async arrive(): Promise<void> {
    const params = this.route.snapshot.queryParamMap;
    const invitation = params.get('invitation') ?? '';
    const signUp = params.get('signUp') ?? '';
    this.verifyToken = params.get('verify') ?? '';
    this.returnAddress = this.addressNamed(params.get('returnUrl'));
    const linked = params.has('pending') || invitation !== '' || signUp !== '' || this.verifyToken !== '';
    const refused = (params.get('code') ?? '') !== '';
    let forwarded = false;
    this.arriving.set(true);
    this.unreachable.set(false);
    this.refusal.set(undefined);
    try {
      const list = await this.client.auth.methods();
      const carried = this.carriedInvitation(list, invitation);
      if (params.has('pending')) {
        await this.readPending();
      }
      if (invitation !== '') {
        await this.openInvitation(invitation);
      } else if (carried) {
        await this.keepInvitation(carried);
      }
      if (signUp !== '') {
        await this.openSignUp(signUp);
      }
      if (this.verifyToken !== '') {
        const session = await this.sessionStanding();
        if (session) {
          await this.complete(this.landing());
        }
      } else if (!linked && !carried) {
        await this.findSession();
        const only = refused ? undefined : this.onlyRedirectMethod(list);
        if (only) {
          forwarded = true;
          this.leaveFor(this.client.auth.signInUrl(only, this.returnTo(this.landing())));
        }
      }
    } catch {
      this.unreachable.set(true);
    } finally {
      if (!forwarded) {
        this.arriving.set(false);
      }
    }
  }

  /**
   * The redirect method a deep link is forwarded to: the list's only method, when it is a
   * redirect, the URL named an address to return to or the guard recorded a page other
   * than this one or the sign-out page, and no session was found standing; undefined when
   * the page stops instead.
   */
  private onlyRedirectMethod(list: LoginMethods): LoginMethod | undefined {
    const only = list.methods.length === 1 ? list.methods[0] : undefined;
    if (!only || only.kind !== 'redirect' || this.signedInAs() !== undefined || this.changing()) {
      return undefined;
    }
    if (this.returnAddress !== undefined) {
      return only;
    }
    const recorded = this.auth.redirectUrl();
    if (recorded === '' || recorded.startsWith(this.loginPath) || recorded.startsWith(this.signOutPath)) {
      return undefined;
    }
    return only;
  }

  /**
   * Opens the invitation the URL's token names and keeps it over the list; a person found
   * signed in is offered to accept it as they are. A link the server refuses renders its
   * words, and the list alone.
   */
  private async openInvitation(token: string): Promise<void> {
    const result = await this.client.auth.openInvitation(token);
    if (result.outcome === 'refused') {
      this.refusal.set(this.sentenceOf(result));
      return;
    }
    await this.keepInvitation({
      email: result.email,
      tenant: result.tenant,
      expiresAt: result.expiresAt,
      signUp: result.signUp,
      usernameIsEmail: result.usernameIsEmail,
      ...(result.methods === undefined ? {} : { methods: result.methods }),
      ...(result.accountExists === undefined
        ? {}
        : { accountExists: result.accountExists, hasPassword: result.hasPassword === true }),
    });
  }

  /**
   * The invitation the list says the browser carries, as the page shows one, when the URL
   * names no token: a token is opened instead, since opening it also refreshes what the
   * browser carries. The account with a password is offered where the block names the
   * sign-up route. Undefined when the browser carries no live invitation.
   */
  private carriedInvitation(list: LoginMethods, token: string): OpenedInvitation | undefined {
    const block = list.invitation;
    const standing = block?.standing;
    if (token !== '' || !block || !standing) {
      return undefined;
    }
    return {
      email: standing.email,
      tenant: standing.tenant,
      expiresAt: standing.expiresAt,
      signUp: block.signUp !== undefined,
      usernameIsEmail: standing.usernameIsEmail,
      ...(standing.methods === undefined ? {} : { methods: standing.methods }),
      ...(standing.accountExists === undefined
        ? {}
        : { accountExists: standing.accountExists, hasPassword: standing.hasPassword === true }),
    };
  }

  /**
   * Keeps the invitation over the list and reads the session: a person found signed in is
   * offered to accept it as they are. For a person signed out whose one way to accept is a
   * new account with a password (no account holds the address and no directory could admit
   * it), the account form opens by itself.
   */
  private async keepInvitation(invitation: OpenedInvitation): Promise<void> {
    this.invitation.set(invitation);
    const session = await this.sessionStanding();
    this.signedInAs.set(session?.username);
    this.signedInEmail.set(session?.email ?? undefined);
    const paths = this.invitationPaths();
    if (!session?.username && paths?.known && paths.create) {
      this.openAccountForm(true);
    }
  }

  /** Opens the sign-up the URL's token names and renders its account form; a link the server refuses renders its words. */
  private async openSignUp(token: string): Promise<void> {
    const result = await this.client.auth.openSignUp(token);
    if (result.outcome === 'refused') {
      this.refusal.set(this.sentenceOf(result));
      return;
    }
    this.account.set({ source: 'signUp', token, email: result.email, usernameIsEmail: result.usernameIsEmail });
  }

  /**
   * Reads the session the browser arrived with, nothing in the URL asking for anything: a
   * session standing is named for Continue or Sign out; one marked for a password change
   * renders the change form, keeping the page the guard recorded for after the change.
   */
  private async findSession(): Promise<void> {
    const session = await firstValueFrom(this.auth.checkUserSession());
    if (!session?.authenticated) {
      return;
    }
    if (session.passwordChangeRequired) {
      this.afterChange = this.landing();
      this.changing.set(true);
      return;
    }
    this.signedInAs.set(session.username);
    this.signedInEmail.set(session.email ?? undefined);
  }

  /** The session the browser arrived with, when one stands and may act; undefined otherwise. */
  private async sessionStanding(): Promise<SessionInfo | undefined> {
    const session = await firstValueFrom(this.auth.checkUserSession());
    return session?.authenticated && !session.passwordChangeRequired ? session : undefined;
  }

  /**
   * Reads the pending status and renders what it says: the step while one waits, the list
   * with the refusal's sentence (`pending_expired`) when the sign-in is gone, the list alone
   * when none waits. A sign-in that waits for the code, with no code standing and an
   * address to send it to, has the code mailed at once, so the step opens with the mail
   * on its way; a reload finds the code standing and sends none.
   */
  private async readPending(): Promise<void> {
    const status = await this.client.auth.pending();
    switch (status.outcome) {
      case 'waiting':
        this.pending.set({
          reason: status.reason,
          email: status.email,
          expiresAt: status.expiresAt,
          returnUrl: status.returnUrl,
          ...(status.factors ? { factors: status.factors } : {}),
          ...(status.emailCode ? { emailCode: status.emailCode } : {}),
        });
        if (status.emailCode && !status.emailCode.codeSent && !status.emailCode.enrollEmail) {
          await this.mailCode(() => this.client.auth.sendCode());
        }
        return;
      case 'none':
        this.pending.set(undefined);
        return;
      case 'refused':
        this.pending.set(undefined);
        this.refusal.set(this.sentenceOf(status));
        return;
    }
  }

  /**
   * Acts on a step's result: the session is checked and `signedIn` emitted with the page
   * `page` names; a pending step is read, or the browser sent where the server chose; a
   * refusal is rendered, and one that ends the pending sign-in, or says the link an
   * account form came from is gone, returns to the list.
   */
  private async follow(result: SignInResult, page: () => Landing): Promise<void> {
    switch (result.outcome) {
      case 'session':
        await this.complete(page());
        return;
      case 'pending':
        if (result.redirectUrl) {
          this.leaveFor(result.redirectUrl);
          return;
        }
        this.confirmation = '';
        this.clearCodeForm();
        await this.readPending();
        return;
      case 'refused':
        if (result.code === 'pending_expired' || result.status === 404 || result.status === 409) {
          this.pending.set(undefined);
        }
        if (result.status === 404 && this.account()) {
          this.leaveInvitation(result);
          this.clearAccountForm();
          return;
        }
        this.refusal.set(this.sentenceOf(result));
        return;
    }
  }

  /**
   * Has the code mailed through `send` and renders the answer: the step now says a code
   * stands, at the address the server named; a refusal is rendered in place, and one that
   * ends the pending sign-in, or says it does not wait for the code, returns to the list.
   */
  private async mailCode(send: () => Promise<EmailCodeResult>): Promise<void> {
    const result = await send();
    if (result.outcome === 'refused') {
      if (result.code === 'pending_expired' || result.status === 404 || result.status === 409) {
        this.pending.set(undefined);
      }
      this.mailHold.hold(result);
      this.refusal.set(this.sentenceOf(result));
      return;
    }
    const waiting = this.pending();
    if (!waiting) {
      return;
    }
    this.clearCodeForm();
    this.pending.set({
      ...waiting,
      emailCode: {
        codeEmail: result.codeEmail,
        enrollEmail: false,
        codeSent: true,
        codeExpiresAt: result.codeExpiresAt,
      },
    });
  }

  private clearCodeForm(): void {
    this.signInCode = '';
    this.rememberDevice = false;
    this.codeEmail = '';
  }

  /** Renders the refusal of an invitation that is gone and drops its note: the list stands alone. */
  private leaveInvitation(refusal: SignInRefusal): void {
    this.invitation.set(undefined);
    this.signedInAs.set(undefined);
    this.signedInEmail.set(undefined);
    this.refusal.set(this.sentenceOf(refusal));
  }

  private clearAccountForm(): void {
    this.account.set(undefined);
    this.accountOpenedItself.set(false);
    this.accountUsername = '';
    this.accountPassword = '';
    this.accountPasswordAgain = '';
  }

  /**
   * Checks the session the step established and hands the page on; a check that finds
   * none renders the library's sentence. A session that must change its password renders
   * the change form instead, keeping the page for after the change. A confirmation link's
   * token waiting to be posted is posted now, and what happened shows until Continue.
   */
  private async complete(page: Landing): Promise<void> {
    const session = await firstValueFrom(this.auth.checkUserSession());
    if (!session?.authenticated) {
      this.refusal.set(this.ui.loginMessage('internal_error'));
      return;
    }
    this.password = '';
    this.confirmation = '';
    this.clearCodeForm();
    this.pending.set(undefined);
    this.invitation.set(undefined);
    this.signedInAs.set(undefined);
    this.signedInEmail.set(undefined);
    this.clearAccountForm();
    this.refusal.set(undefined);
    if (session.passwordChangeRequired) {
      this.afterChange = page;
      this.changing.set(true);
      return;
    }
    this.changing.set(false);
    if (this.verifyToken !== '') {
      await this.verify(page);
      return;
    }
    this.land(page);
  }

  /** Posts the confirmation link's token for the session that stands, and keeps the page for Continue. */
  private async verify(page: Landing): Promise<void> {
    const token = this.verifyToken;
    this.verifyToken = '';
    const result = await this.client.auth.verifyEmail(token);
    if (result.outcome === 'refused') {
      this.refusal.set(this.sentenceOf(result));
    } else {
      this.verified.set(result.email);
      // The session read carries the address now; the application's view of it follows.
      await firstValueFrom(this.auth.checkUserSession());
    }
    this.landed.set(page);
  }

  /**
   * The sentence for a refusal: the one the application holds for its code, followed by the
   * wait the server named (`too_many_attempts` arrives with 429 and `retryAfter`), else the
   * server's words. The code is read from the refusal's body whatever its status.
   */
  private sentenceOf(refusal: SignInRefusal): string {
    const held = refusal.code === undefined ? '' : this.ui.loginMessage(refusal.code, refusal.retryAfter);
    return held || refusal.message;
  }

  /** Runs one step of the journey as pending work, busy meanwhile, rendering a failure in place. */
  private step(work: () => Promise<void>): Promise<void> {
    return this.tracked(async () => {
      this.busy.set(true);
      this.refusal.set(undefined);
      try {
        await work();
      } catch (error) {
        this.refusal.set(messageOf(error));
      } finally {
        this.busy.set(false);
      }
    });
  }

  /** Runs one piece of work as a pending task of the application, released when it settles. */
  private async tracked<T>(work: () => Promise<T>): Promise<T> {
    const release = this.pendingTasks.add();
    try {
      return await work();
    } finally {
      release();
    }
  }

  /** The router URL a sign-in lands on: the page the login guard turned away from, read once, else `home`; the login and sign-out pages themselves count as none. */
  /**
   * Where the sign-in lands: the address this page's URL named, loaded by the browser;
   * else the page the guard recorded, read once, or `home`, routed to. A recorded page
   * that is this page or the sign-out page would loop, and lands on `home`.
   */
  private landing(): Landing {
    if (this.returnAddress !== undefined) {
      return { url: this.returnAddress, load: true };
    }
    const recorded = this.auth.redirectUrl();
    this.auth.redirectUrl.set('');
    if (recorded === '' || recorded.startsWith(this.loginPath) || recorded.startsWith(this.signOutPath)) {
      return { url: this.home(), load: false };
    }
    return { url: recorded, load: false };
  }

  /**
   * Where a pending step's sign-in lands: the address the server reported for it, loaded
   * by the browser; else the landing. The status answers `/` for a sign-in that had none
   * (a password sign-in: its sanitizer's fallback, which its own redirect leaves out too),
   * so `/` is the landing's to decide, never an address.
   */
  private returned(waiting: PendingSignIn): Landing {
    const address = waiting.returnUrl;
    return address && address !== '/' ? { url: address, load: true } : this.landing();
  }

  /** Hands the landing on: a page of the application is emitted as `signedIn`; an address the server named is loaded by the browser. */
  private land(landing: Landing): void {
    if (landing.load) {
      this.leaveFor(landing.url);
      return;
    }
    this.signedIn.emit(landing.url);
  }

  /** The browser path a redirect method returns to: the address as named, or the page resolved against the application's mount. */
  private returnTo(landing: Landing): string {
    return landing.load ? landing.url : this.browserPath(landing.url);
  }

  /**
   * The browser path the URL's `returnUrl` names, when the server would keep it: a path in
   * this application in canonical form (`returnPath`), and not this page or the sign-out
   * page, which would loop. Anything else is dropped, and the sign-in lands as if the URL
   * named nothing.
   */
  private addressNamed(value: string | null): string | undefined {
    const path = returnPath(value);
    if (
      path === undefined ||
      path.startsWith(this.browserPath(this.loginPath)) ||
      path.startsWith(this.browserPath(this.signOutPath))
    ) {
      return undefined;
    }
    return path;
  }

  /** The browser path of a router URL: resolved against the document's base, the application's mount. */
  private browserPath(url: string): string {
    const resolved = new URL(url.replace(/^\//, ''), this.document.baseURI);
    return resolved.pathname + resolved.search;
  }

  /** Sends the browser to a URL: a redirect method's, or the page the server chose for a pending step. */
  protected leaveFor(url: string): void {
    this.document.location.assign(url);
  }
}

/** The sentence for a new password typed differently the second time; nothing is posted. */
export const PASSWORDS_DIFFER = 'The new passwords do not match. Type the same password twice.';

/**
 * The sign-in steps this page renders, as the server's pending status names them: the
 * pending reason, and for the second factor the factor this page drives after a colon.
 * The package declares the same list under `impulseframework.signInSteps` in its package.json, which
 * impulse's release check holds the steps a generated server asks for to, so a framework
 * release never pairs a server with a page that cannot render what the server asks; the
 * component's spec holds the two lists together and each step to its branch.
 */
export const SIGN_IN_STEPS = ['confirmation', 'mfa:email-code'] as const;

/** The note for a step this page does not render: the page is older than the server that asked for it. */
export const PAGE_OLDER_THAN_SERVER =
  "We can't finish this sign-in on this page, because the page is older than the server. " +
  'Reload the page and try again. If this keeps happening, tell whoever runs this application.';

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Whether two addresses are one, without regard to case and surrounding space. */
function sameAddress(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase();
}
