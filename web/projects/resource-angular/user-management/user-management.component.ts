import { CdkMenuModule } from '@angular/cdk/menu';
import { ConnectedPosition } from '@angular/cdk/overlay';
import { DatePipe } from '@angular/common';
import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  input,
  PendingTasks,
  signal,
  TemplateRef,
  untracked,
  viewChild,
  WritableSignal,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatAutocompleteModule } from '@angular/material/autocomplete';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { MatDividerModule } from '@angular/material/divider';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import {
  Capability,
  Domain,
  InvitationRow,
  Method,
  Page,
  RoleRow,
  UserIdentity,
  UserPartitionClient,
  userPartition,
  UserRow,
  UserSurfaceDescriptor,
  userSurfaces,
  WithCapabilities,
} from '@impulseframework/resource';
import { askInDialog } from '@impulseframework/resource-angular/dialogs';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

import { roleTitle, RoleTitlePipe } from './role-title';

/** A user row as the component reads it: with the capability envelope the list asked for. */
export type ManagedUser = WithCapabilities<UserRow>;

/** A standing invitation as the component reads it: with the envelope that says whether the caller may revoke it. */
export type ManagedInvitation = WithCapabilities<InvitationRow>;

/** The provider behind each library method, as a person names it. */
const providerNames: Readonly<Record<string, string>> = { google: 'Google', azure: 'Microsoft', workos: 'WorkOS' };

/** Which side panel stands open beside the table, and whose row it is over. */
export interface OpenPanel {
  kind: 'access' | 'new';
  /** The row's id; none for the new-user panel. */
  user?: string;
}

/**
 * One row of the roles list in the access panel and the new-user panel: a role the user
 * holds, or one being chosen. The picker's value is the chosen role's name once an option
 * is taken and what was typed until then; `role` is the name the value resolves to.
 */
export interface RoleChoice {
  /** The row's identity in the list, so a removed row's neighbors keep their fields. */
  key: number;
  value: string;
  role: string;
  /** Why the row cannot be changed, when it cannot; undefined when it can. */
  locked?: string;
  /** The row is a seat the user holds, as the server listed it: the picker is not edited, and the trash can unseats it. */
  seated?: true;
}

/** The words under a role the directory writes: the page cannot seat or unseat the user by hand. */
export const assignedByDirectory = 'Assigned by the directory.';
/** The words under a role the caller may not change. */
export const cannotChangeRole = 'You cannot change this role.';
/** The words under a seat held in every tenant, shown in a tenant's partition where it is not the tenant's to change. */
export const assignedForEveryTenant = 'Assigned for every tenant. You cannot change it here.';
/** The words for where a global role reaches, in an application with tenants. */
export const everywhereWords = 'Everywhere';
/** The words for where a seat held in every tenant reaches. */
export const everyTenantWords = 'Every tenant';

/** The capabilities the list asks the server to evaluate per row: what each row's controls follow. */
const askedCapabilities: Capability[] = ['Update', 'Delete', 'Execute'];

/** The capability the invitations list asks for per row: the revoke. */
const askedInvitationCapabilities: Capability[] = ['Delete'];

/** The side panel's shape: anchored to the right edge, the viewport's height, a sheet's width; focus lands on its heading, which says what the panel is for. */
const panelConfig = {
  panelClass: 'impulse-users-panel',
  position: { top: '0', right: '0' },
  height: '100%',
  maxHeight: '100%',
  width: '32rem',
  maxWidth: '100vw',
  autoFocus: 'first-heading' as const,
};

/** The form a new user is entered on. */
interface NewUser {
  username: string;
  password: string;
  disabled: boolean;
  email: string;
}

const emptyNewUser = (): NewUser => ({ username: '', password: '', disabled: false, email: '' });

/** The form an invitation is entered on: the address, and the roles its acceptance grants. */
interface NewInvitation {
  email: string;
  roles: string[];
}

const emptyNewInvitation = (): NewInvitation => ({ email: '', roles: [] });

/**
 * Where a row's actions menu opens: the places its trigger tries in order, the first that
 * fits taken. Below the button with the right edges aligned, the usual dropdown; above it
 * the same way; beside it to its left with the top edges aligned; then with the bottom
 * edges aligned. Material's own menu knows below and above alone and, when neither fits, is
 * pushed into the window over the button that opened it; a place beside the button keeps
 * that button and the other rows' buttons in view, and when neither alignment fits the
 * menu is shifted along the button to stay in the window, still beside it.
 */
export const rowMenuPositions: ConnectedPosition[] = [
  { originX: 'end', originY: 'bottom', overlayX: 'end', overlayY: 'top' },
  { originX: 'end', originY: 'top', overlayX: 'end', overlayY: 'bottom' },
  { originX: 'start', originY: 'top', overlayX: 'end', overlayY: 'top' },
  { originX: 'start', originY: 'bottom', overlayX: 'end', overlayY: 'bottom' },
];

/**
 * The user-management page over one partition of an Auth's surface. The component
 * configures itself from the descriptor's `userSurfaces` block: `auth` names the surface
 * (the first one by default) and `domain` binds the tenant partition, the global one when
 * it is omitted. Nothing about the application is named here; what the page offers
 * follows from the surface's shape and the caller's grants.
 *
 * The table has one row per user: the username, the email address and whether the person
 * confirmed it, the status, how they sign in, their roles one per line (each by its title,
 * `FeatureAdministrator` reading "Feature Administrator", with where it reaches beside it
 * where the application has tenants: "Everywhere" for a global role, "Every tenant" for a
 * seat held in every tenant, the tenant's name in its partition) and the last sign-in. A
 * row the directory seats says so. Each row ends in a menu button that names every action
 * the caller may take on that user: Edit access, Rename, Change email address, Set a
 * temporary password, Forget remembered browsers, Unlink for each linked sign-in method,
 * then Disable or Enable and Delete; an action the caller may not take is not listed.
 *
 * Edit access opens a side panel on the right listing the user's roles, one per row with
 * where it reaches and its description under it, a trash can per row and a button for
 * another role. Each row acts at once: choosing a role in a new row seats it in one call,
 * and the trash can asks in a dialog, then unseats in one call; the page is read again
 * after each, so the rows say what the server says, and Done closes the panel. On the
 * global page a seat held in every tenant is a row like the others, seated and unseated
 * through the global seats resource, and in the tenant partition it is shown locked. A
 * role the caller may not change, or a seat the directory writes, is shown with the
 * reason under it and cannot be removed; where the caller may seat but the user holds
 * every role there is, the panel says so where the add button would be. The picker is a
 * searchable list: typing narrows it by title, name and description.
 *
 * Every other action is one step with one call: Rename, Change email address (an
 * operator's entry is unverified and reserves the address) and Set a temporary password
 * (where the Auth has a password method) each open a dialog with its one field and its
 * one button; Forget remembered browsers (where the caller may run the reset method),
 * Unlink, Disable or Enable and Delete ask once in a dialog and then make their call.
 * Nothing can half-succeed. Where the password method declares the username as the
 * address, the username column is the address and neither is edited by hand: a verified
 * new address renames the person. The new-user panel takes the account fields and the
 * roles, one required in the tenant partition, whose create seats the new user in the
 * tenant.
 *
 * Below the users stand the partition's invitations, where the descriptor names the
 * resource: an address invited to hold roles, until whoever follows the mailed link signs
 * in and is seated with them, or the invitation is revoked or expires.
 *
 * Every control follows a capability: the digest decides whether users are created, seats
 * written and invitations sent, and the row's capability envelope, asked for on every
 * read, decides the rename, the disable, the delete, the methods and the revoke per row.
 * A refusal, by the server or by the network, is shown in place in the server's words,
 * and the lists are read again after every write so the rows say what the server says.
 *
 *     <impulse-user-management />
 *     <impulse-user-management auth="Members" />
 *     <impulse-user-management [domain]="tenant()" />
 */
@Component({
  selector: 'impulse-user-management',
  imports: [
    CdkMenuModule,
    DatePipe,
    FormsModule,
    MatAutocompleteModule,
    MatButtonModule,
    MatCheckboxModule,
    MatDividerModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    RoleTitlePipe,
  ],
  templateUrl: './user-management.component.html',
  styleUrl: './user-management.component.scss',
})
export class UserManagementComponent {
  private client = inject(RESOURCE_CLIENT);
  private dialog = inject(MatDialog);
  /** Every request is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);
  /** The side panel's content; the one template serves the access and new-user panels. */
  private panelTemplate = viewChild.required<TemplateRef<unknown>>('panelTemplate');
  /** The open side panel's dialog; undefined while none is open. */
  private dialogRef: MatDialogRef<unknown> | undefined;
  /** The next roles row's key. */
  private nextKey = 0;
  /** Where a row's actions menu opens, handed to the row's trigger. */
  readonly menuPositions = rowMenuPositions;

  /** The Auth whose surface the page serves, by name; the descriptor's first surface when omitted. */
  auth = input<string | undefined>(undefined);
  /** The tenant whose partition the page serves; the global partition when omitted. */
  domain = input<Domain | undefined>(undefined);

  /** The surface `auth` names, or the first; undefined when the descriptor carries none, or no such Auth. */
  surface = computed<UserSurfaceDescriptor | undefined>(() => {
    const surfaces = userSurfaces(this.client.descriptor);
    const name = this.auth();
    return name === undefined ? surfaces[0] : surfaces.find((surface) => surface.auth === name);
  });
  /** The partition `domain` selects on the surface; undefined when the surface has none of that kind. */
  partition = computed(() => {
    const scope = this.domain() === undefined ? 'global' : 'domain';
    return this.surface()?.partitions.find((partition) => partition.scope === scope);
  });
  /** Why the page serves nothing: no surface, no such Auth, or no tenant partition. */
  unserved = computed<string | undefined>(() => {
    const name = this.auth();
    if (userSurfaces(this.client.descriptor).length === 0) {
      return 'This application serves no user management.';
    }
    if (!this.surface()) {
      return `This application has no ${name} sign-in.`;
    }
    if (!this.partition()) {
      return `The ${this.surface()?.label} users are not partitioned by tenant.`;
    }
    return undefined;
  });
  /** The partition's handles and methods, bound to the tenant where there is one. */
  users = computed<UserPartitionClient | undefined>(() => {
    const surface = this.surface();
    const partition = this.partition();
    return surface && partition ? userPartition(this.client, surface, partition, this.domain()) : undefined;
  });

  /** The page of users on screen, with each row's capability envelope. */
  page = signal<Page<ManagedUser> | undefined>(undefined);
  rows = computed<readonly ManagedUser[]>(() => this.page()?.rows ?? []);
  /** The roles a seat may take in the partition; empty when the caller may not list them. */
  roles = signal<readonly RoleRow[]>([]);
  /** The invitations standing into the partition, with each one's envelope; empty when the caller may not list them. */
  invitations = signal<readonly ManagedInvitation[]>([]);
  /** True until the first read of a partition answers. */
  loading = signal(true);
  /** The refusal of the list itself, in the server's words. */
  error = signal<string | undefined>(undefined);
  /** The refusal of the last action, in the server's words; cleared by the next action. */
  notice = signal<string | undefined>(undefined);
  /** An action is in flight; the controls are disabled meanwhile. */
  busy = signal(false);
  /** The side panel that is open, if any. */
  panel = signal<OpenPanel | undefined>(undefined);
  /** The row the open panel is over, as last read; undefined once it is gone from the page. */
  panelUser = computed<ManagedUser | undefined>(() => {
    const id = this.panel()?.user;
    return id === undefined ? undefined : this.rows().find((row) => row.id === id);
  });
  /** The access panel's rows: one per role held, plus the ones being added. */
  accessRows = signal<RoleChoice[]>([]);
  /** The new-user panel's roles rows. */
  newRoles = signal<RoleChoice[]>([]);
  /** The invitation form is shown. */
  inviting = signal(false);

  newUser: NewUser = emptyNewUser();
  newInvitation: NewInvitation = emptyNewInvitation();

  /** The words the picker shows for a value: the role's title. */
  readonly titleOf = (name: string | null): string => roleTitle(name ?? '');
  /** The dates as the tables show them, to the minute: "Oct 28, 2026, 6:34 PM". */
  readonly dateFormat = 'MMM d, y, h:mm a';

  constructor() {
    effect(() => {
      const partition = this.users();
      untracked(() => void this.tracked(() => this.load(partition)));
    });
    inject(DestroyRef).onDestroy(() => this.dialogRef?.close());
  }

  /** The words for the partition: the surface's label, and the tenant where there is one. */
  heading(): string {
    const label = this.surface()?.label ?? '';
    const domain = this.domain();
    return domain === undefined ? `${label} users` : `${label} users of ${domain}`;
  }

  /** The open panel's heading: whose access, or the new user. */
  panelHeading(): string {
    const panel = this.panel();
    const username = this.panelUser()?.username ?? '';
    switch (panel?.kind) {
      case 'access':
        return `${this.accessWords(this.panelUser())} for ${username}`;
      case 'new':
        return 'New user';
      default:
        return '';
    }
  }

  /** The password method declares the username as the person's email address: the username column is the address. */
  usernameIsEmail(): boolean {
    return this.surface()?.usernameIsEmail ?? false;
  }

  /** The words over the username fields: the address where the username is one. */
  usernameLabel(): string {
    return this.usernameIsEmail() ? 'Email address' : 'Username';
  }

  /** The words for a row's address: the address with its state, or none. */
  emailOf(row: UserRow): string {
    if (row.email === null) {
      return 'None';
    }
    return row.emailVerifiedAt === null ? `${row.email} · unconfirmed` : row.email;
  }

  /** The words for the inviter: the username the row carries, nothing when the sender is unknown. */
  invitedBy(invitation: InvitationRow): string {
    return invitation.invitedBy ?? '';
  }

  /** The words for how a user signs in: the password and each linked identity's registration. */
  signsInWith(row: UserRow): string {
    const means = row.identities.map((identity) => this.identityLabel(identity));
    if (row.hasPassword) {
      means.unshift('Password');
    }
    return means.length === 0 ? 'No way to sign in' : means.join(', ');
  }

  /**
   * The words for an identity: the provider's name, with the organization the link carries
   * ("Google (fleet.example)"). The row's connection is the directory's key, not a name a
   * person knows, and the registration the identity came through is not recorded on the link.
   */
  identityLabel(identity: UserIdentity): string {
    const provider = providerNames[identity.method] ?? identity.method;
    return identity.organization ? `${provider} (${identity.organization})` : provider;
  }

  /** Whether the application has tenants: the surface has a tenant partition, so where a role reaches is worth saying. */
  tenanted(): boolean {
    return this.surface()?.partitions.some((partition) => partition.scope === 'domain') ?? false;
  }

  /**
   * The words for where a listed role reaches. In the tenant partition every listed role
   * is the tenant's; in the global partition of an application with tenants a global role
   * reaches everywhere and a domain role seated there is held in every tenant. Nothing
   * where the application has no tenants, since every role then reaches the whole of it.
   */
  kindOf(role: RoleRow): string {
    if (!this.tenanted()) {
      return '';
    }
    if (this.partition()?.scope === 'domain') {
      return `${this.domain()}`;
    }
    switch (role.kind) {
      case 'global':
        return everywhereWords;
      case 'domain':
        return everyTenantWords;
      default:
        return '';
    }
  }

  /** The words for where one of a user's seats reaches: by the role's kind, or every tenant for an everywhere seat. */
  reachOf(role: string, everywhere = false): string {
    if (!this.tenanted()) {
      return '';
    }
    if (everywhere) {
      return everyTenantWords;
    }
    if (this.partition()?.scope === 'domain') {
      return `${this.domain()}`;
    }
    const listed = this.roles().find((candidate) => candidate.name === role);
    return listed ? this.kindOf(listed) : '';
  }

  /**
   * The seats a row holds in every tenant: in the tenant partition shown beside the
   * tenant's own, and on the global page rows like the others (a domain role seated
   * there is held in every tenant), left out where the global list names them already.
   */
  everywhereOf(row: UserRow): string[] {
    if (this.partition()?.scope === 'domain') {
      return row.everywhereRoles;
    }
    return row.everywhereRoles.filter((role) => !row.roles.includes(role));
  }

  /** The words for a role in a list: its title, and where it reaches when that is worth saying. */
  roleLabel(role: RoleRow): string {
    const kind = this.kindOf(role);
    return kind === '' ? roleTitle(role.name) : `${roleTitle(role.name)} · ${kind}`;
  }

  /** The words for a list of role names: their titles. */
  titlesOf(roles: readonly string[]): string {
    return roles.map((role) => roleTitle(role)).join(', ');
  }

  /** The words under a roles row: why it cannot be changed, else where the role reaches and what it is for. */
  wordsFor(row: RoleChoice): string {
    if (row.locked !== undefined) {
      return row.locked;
    }
    const listed = this.roles().find((candidate) => candidate.name === row.role);
    if (!listed) {
      return '';
    }
    return [this.kindOf(listed), listed.description ?? ''].filter((words) => words !== '').join(' · ');
  }

  /**
   * The roles a row's picker offers: every listed role no other row of the list has
   * taken, the whole of them once the row has a role (so a chosen one can be changed),
   * and otherwise the ones whose title, name or description contains what was typed.
   */
  optionsFor(row: RoleChoice, rows: readonly RoleChoice[]): RoleRow[] {
    const taken = takenBy(row, rows);
    const candidates = this.roles().filter((role) => !taken.has(role.name));
    const query = row.value.trim().toLowerCase();
    if (row.role !== '' || query === '') {
      return candidates;
    }
    return candidates.filter((role) =>
      [roleTitle(role.name), role.name, role.description ?? ''].some((words) => words.toLowerCase().includes(query)),
    );
  }

  /** The roles the row does not hold yet, here or in every tenant. */
  seatable(row: UserRow): RoleRow[] {
    return this.roles().filter((role) => !row.roles.includes(role.name) && !row.everywhereRoles.includes(role.name));
  }

  canCreate(): boolean {
    return this.users()?.canCreateUsers() ?? false;
  }

  /** Whether the create form asks for a password: the Auth has a password method. */
  asksPassword(): boolean {
    return this.surface()?.password ?? false;
  }

  /** Whether the create form asks for a role: the tenant partition seats the new user in the tenant. */
  asksRole(): boolean {
    return this.partition()?.scope === 'domain';
  }

  /** Whether the new-user panel lists roles: the tenant partition's create takes one, and the digest lets the caller seat. */
  asksRoles(): boolean {
    return this.asksRole() || this.canSeatMore();
  }

  /** Whether the new-user panel takes more roles than the create carries: the caller seats the rest. */
  canSeatMore(): boolean {
    return this.users()?.canSeat() ?? false;
  }

  canRename(row: ManagedUser): boolean {
    return this.users()?.users.fieldEditable(row, 'username') ?? false;
  }

  /** Whether the caller may enter the row's address by hand: never where the username is the address. */
  canSetEmail(row: ManagedUser): boolean {
    return !this.usernameIsEmail() && (this.users()?.users.fieldEditable(row, 'email') ?? false);
  }

  canDisable(row: ManagedUser): boolean {
    return this.users()?.users.fieldEditable(row, 'disabled') ?? false;
  }

  canDelete(row: ManagedUser): boolean {
    return this.users()?.users.rowCan(row, 'Delete') ?? false;
  }

  canSetPassword(row: ManagedUser): boolean {
    const method = this.partition()?.setPassword;
    return method !== undefined && (this.users()?.users.rowCan(row, 'Execute', method as Method) ?? false);
  }

  canUnlink(row: ManagedUser): boolean {
    const method = this.partition()?.unlinkIdentity;
    return (
      method !== undefined &&
      row.identities.length > 0 &&
      (this.users()?.users.rowCan(row, 'Execute', method as Method) ?? false)
    );
  }

  /** Whether the partition has the reset method and the caller may run it on the row: the row's remembered browsers can be forgotten. */
  canResetFactors(row: ManagedUser): boolean {
    const method = this.partition()?.resetFactors;
    return method !== undefined && (this.users()?.users.rowCan(row, 'Execute', method as Method) ?? false);
  }

  canSeat(row: ManagedUser): boolean {
    return !row.rolesSynced && (this.users()?.canSeat() ?? false) && this.seatable(row).length > 0;
  }

  canUnseat(row: ManagedUser): boolean {
    return !row.rolesSynced && (this.users()?.canUnseat() ?? false);
  }

  /** Whether the partition has invitations the caller may list or send: the invitations block is shown. */
  invitable(): boolean {
    const partition = this.users();
    return partition !== undefined && (partition.canListInvitations() || partition.canInvite());
  }

  canInvite(): boolean {
    return this.users()?.canInvite() ?? false;
  }

  canRevoke(invitation: ManagedInvitation): boolean {
    return this.users()?.invitations?.rowCan(invitation, 'Delete') ?? false;
  }

  /** Opens the access panel over a row: one roles row per seat, locked where the caller may not change it. */
  openAccess(row: ManagedUser): void {
    this.accessRows.set(this.heldRows(row));
    this.show({ kind: 'access', user: row.id });
  }

  /** The menu item and the panel heading for a row's roles: Edit access where the caller may change them, View access otherwise. */
  accessWords(row: ManagedUser | undefined): string {
    return row === undefined || this.canSeat(row) || this.canUnseat(row) ? 'Edit access' : 'View access';
  }

  /** Where the caller may seat but the user holds every role there is: said where the add button would be. */
  holdsEveryRole(row: ManagedUser): boolean {
    return !row.rolesSynced && (this.users()?.canSeat() ?? false) && this.seatable(row).length === 0;
  }

  /** The sentence for a user who holds every role: every role the application has, or every role there is in the tenant. */
  everyRoleWords(row: ManagedUser): string {
    const where = this.partition()?.scope === 'domain' ? 'there is here' : 'this application has';
    return `${row.username} holds every role ${where}.`;
  }

  /** The words for the button that adds a roles row: the first role, or another. */
  addRoleWords(rows: readonly RoleChoice[]): string {
    return rows.length === 0 ? 'Add a role' : 'Add another role';
  }

  /** Opens the new-user panel, empty, with one roles row where the panel takes roles. */
  openNew(): void {
    this.newUser = emptyNewUser();
    this.newRoles.set(this.asksRoles() ? [this.emptyChoice()] : []);
    this.show({ kind: 'new' });
  }

  /** Closes the open panel, discarding what was typed in it. */
  closePanel(): void {
    this.dialogRef?.close();
  }

  /** Appends an empty roles row to a list. */
  addRole(rows: WritableSignal<RoleChoice[]>): void {
    rows.update((current) => [...current, this.emptyChoice()]);
  }

  /** Takes a roles row off a list. */
  removeRole(rows: WritableSignal<RoleChoice[]>, row: RoleChoice): void {
    rows.update((current) => current.filter((other) => other !== row));
  }

  /** Records what a roles row's picker holds: the typed text, or the chosen role's name, and the role it resolves to among the ones no other row of the list has taken. */
  typed(row: RoleChoice, value: string | null, rows: readonly RoleChoice[]): void {
    row.value = value ?? '';
    row.role = this.resolve(row.value, takenBy(row, rows));
  }

  /** Shows the invitation form, or hides it. */
  toggleInvite(): void {
    this.inviting.update((shown) => !shown);
    this.newInvitation = emptyNewInvitation();
    this.notice.set(undefined);
  }

  /**
   * Creates the user the panel describes: a password where one was typed, the address
   * where one was, the first role in the tenant partition's create, and a seat for each
   * further role once the server has said the new user's id.
   */
  create(): Promise<void> {
    const partition = this.users();
    if (!partition || this.panel()?.kind !== 'new') {
      return Promise.resolve();
    }
    const form = this.newUser;
    const roles = this.chosen(this.newRoles());
    if (this.asksRole() && roles.length === 0) {
      this.notice.set('Choose a role for the new user.');
      return Promise.resolve();
    }
    return this.act(async () => {
      const id = await partition.users.create({
        username: form.username,
        ...(this.asksPassword() && form.password !== '' ? { password: form.password } : {}),
        ...(form.disabled ? { disabled: true } : {}),
        ...(!this.usernameIsEmail() && form.email !== '' ? { email: form.email } : {}),
        ...(this.asksRole() ? { role: roles[0] } : {}),
      });
      const rest = this.asksRole() ? roles.slice(1) : roles;
      if (rest.length > 0 && id === undefined) {
        throw new Error(
          `${form.username} was created, but the server did not say the new user's id, so no role was assigned. Edit the user's access to assign them.`,
        );
      }
      for (const role of rest) {
        try {
          await partition.seats.create({ user: id as string, role });
        } catch (error) {
          throw new Error(
            `${form.username} was created, but the ${roleTitle(role)} role could not be assigned: ${messageOf(error)}`,
            { cause: error },
          );
        }
      }
      this.newUser = emptyNewUser();
      this.closePanel();
    });
  }

  /**
   * Seats the user in the role chosen in a new row of the access panel, in one call, and
   * reads the page again; the rows are then rebuilt from the user as the server lists
   * them. A refusal says which role and what the server said.
   */
  chooseRole(row: RoleChoice, role: string): Promise<void> {
    const partition = this.users();
    const user = this.panelUser();
    if (!partition || !user || this.panel()?.kind !== 'access' || row.seated || role === '') {
      return Promise.resolve();
    }
    row.value = role;
    row.role = role;
    const { id, username } = user;
    return this.act(async () => {
      try {
        await partition.seats.create({ user: id, role });
      } catch (error) {
        throw new Error(`Could not add the ${roleTitle(role)} role to ${username}: ${messageOf(error)}`, {
          cause: error,
        });
      }
    }).then(() => this.rebuildAccessRows());
  }

  /**
   * Takes a row off the access panel: an empty row goes at once; a seat the user holds is
   * unseated in one call after a dialog asks, and the page is read again.
   */
  async dropRole(row: RoleChoice): Promise<void> {
    const partition = this.users();
    const user = this.panelUser();
    if (!partition || !user || this.panel()?.kind !== 'access') {
      return;
    }
    if (!row.seated) {
      this.removeRole(this.accessRows, row);
      return;
    }
    const role = row.role;
    const { id, username } = user;
    const confirmed = await askInDialog(this.dialog, {
      title: `Remove the ${roleTitle(role)} role from ${username}?`,
      words: `${username} loses what it allows at once.`,
      confirm: 'Remove',
    });
    if (confirmed !== true) {
      return;
    }
    await this.act(async () => {
      try {
        await partition.seats.remove([id, role]);
      } catch (error) {
        throw new Error(`Could not remove the ${roleTitle(role)} role from ${username}: ${messageOf(error)}`, {
          cause: error,
        });
      }
    });
    this.rebuildAccessRows();
  }

  /** The access panel's rows again, from the user as the server now lists them, while the panel stays open over the row. */
  private rebuildAccessRows(): void {
    const fresh = this.panelUser();
    if (fresh && this.panel()?.kind === 'access') {
      this.accessRows.set(this.heldRows(fresh));
    }
  }

  /** Sends the invitation the form describes: the address is mailed a link carrying the roles. */
  invite(): Promise<void> {
    const invitations = this.users()?.invitations;
    if (!invitations || !this.inviting()) {
      return Promise.resolve();
    }
    const form = this.newInvitation;
    return this.act(async () => {
      await invitations.create({ email: form.email, roles: form.roles });
      this.newInvitation = emptyNewInvitation();
      this.inviting.set(false);
    });
  }

  /** Revokes a standing invitation: the mailed link no longer opens. */
  revoke(invitation: InvitationRow): Promise<void> {
    const invitations = this.users()?.invitations;
    if (!invitations) {
      return Promise.resolve();
    }
    return this.act(() => invitations.remove([invitation.id]));
  }

  /** Asks for the new username in a dialog, then renames the row in one call. */
  async rename(row: ManagedUser): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const username = await askInDialog(this.dialog, {
      title: `Rename ${row.username}`,
      field: { label: this.usernameLabel(), value: row.username },
      confirm: 'Rename',
    });
    if (typeof username !== 'string' || username === row.username) {
      return;
    }
    await this.act(() => partition.users.patch([row.id], { username }));
  }

  /** Asks for the address in a dialog, then enters it by hand, unverified: it reserves the address until the person confirms it. */
  async changeEmail(row: ManagedUser): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const email = await askInDialog(this.dialog, {
      title: `Change the email address of ${row.username}`,
      words: 'Not confirmed until the person confirms it themselves.',
      field: { label: 'Email address', type: 'email', value: row.email ?? '' },
      confirm: 'Change',
    });
    if (typeof email !== 'string' || email === row.email) {
      return;
    }
    await this.act(() => partition.users.patch([row.id], { email }));
  }

  /** Asks whether to disable the row, or to enable it again, then patches the flag in one call. */
  async setDisabled(row: ManagedUser, disabled: boolean): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const confirmed = await askInDialog(
      this.dialog,
      disabled
        ? {
            title: `Disable ${row.username}?`,
            words: `${row.username} cannot sign in until enabled again.`,
            confirm: 'Disable',
          }
        : { title: `Enable ${row.username}?`, words: `${row.username} can sign in again.`, confirm: 'Enable' },
    );
    if (confirmed !== true) {
      return;
    }
    await this.act(() => partition.users.patch([row.id], { disabled }));
  }

  /** Asks once in a dialog, then deletes the row in one call; an open panel closes with the row. */
  async remove(row: ManagedUser): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const confirmed = await askInDialog(this.dialog, {
      title: `Delete ${row.username}?`,
      words: 'This cannot be undone.',
      confirm: 'Delete',
    });
    if (confirmed !== true) {
      return;
    }
    await this.act(async () => {
      await partition.users.remove([row.id]);
      this.closePanel();
    });
  }

  /** Asks for a temporary password in a dialog, then sets it in one call; the person changes it at their next sign-in. */
  async setPassword(row: ManagedUser): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const password = await askInDialog(this.dialog, {
      title: `Set a temporary password for ${row.username}`,
      words: 'The person changes it at their next sign-in.',
      field: { label: 'Temporary password', type: 'password', autocomplete: 'new-password' },
      confirm: 'Set password',
    });
    if (typeof password !== 'string') {
      return;
    }
    await this.act(() => partition.setPassword(row.id, password));
  }

  /** Asks once in a dialog, then unlinks the identity in one call. */
  async unlink(row: ManagedUser, identity: UserIdentity): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const confirmed = await askInDialog(this.dialog, {
      title: `Unlink ${this.identityLabel(identity)} from ${row.username}?`,
      words: `${row.username} can no longer sign in that way.`,
      confirm: 'Unlink',
    });
    if (confirmed !== true) {
      return;
    }
    await this.act(() => partition.unlinkIdentity(row.id, identity.id));
  }

  /** Asks once in a dialog, then forgets the browsers the row's person asked to be remembered: their next sign-in asks for the code again. */
  async resetFactors(row: ManagedUser): Promise<void> {
    const partition = this.users();
    if (!partition) {
      return;
    }
    const confirmed = await askInDialog(this.dialog, {
      title: `Forget the browsers ${row.username} asked to be remembered?`,
      words: 'Their next sign-in from each of them asks for the code again.',
      confirm: 'Forget',
    });
    if (confirmed !== true) {
      return;
    }
    await this.act(() => partition.resetFactors(row.id));
  }

  /** The same page again, after a write or on request. */
  reload(): Promise<void> {
    return this.act(() => Promise.resolve());
  }

  next(): Promise<void> {
    return this.turn(this.page()?.next);
  }

  previous(): Promise<void> {
    return this.turn(this.page()?.prev);
  }

  /** Opens the side panel on a kind, or turns the open one to it. */
  private show(panel: OpenPanel): void {
    this.notice.set(undefined);
    this.panel.set(panel);
    if (this.dialogRef) {
      return;
    }
    const ref = this.dialog.open(this.panelTemplate(), panelConfig);
    this.dialogRef = ref;
    ref.afterClosed().subscribe(() => {
      if (this.dialogRef === ref) {
        this.dialogRef = undefined;
      }
      this.panel.set(undefined);
      this.accessRows.set([]);
      this.newRoles.set([]);
    });
  }

  /**
   * The roles rows a user's seats make: one per role held in the partition, locked with
   * the reason where the directory writes the seats or the caller may not unseat, and one
   * per seat held in every tenant: on the global page a row like the others, since the
   * global seats resource seated it and unseats it; in the tenant partition locked, since
   * the tenant's page cannot change it.
   */
  private heldRows(row: ManagedUser): RoleChoice[] {
    const reason = row.rolesSynced ? assignedByDirectory : this.canUnseat(row) ? undefined : cannotChangeRole;
    const held = row.roles.map((role) => this.choice(role, reason));
    const tenant = this.partition()?.scope === 'domain';
    const everywhere = this.everywhereOf(row).map((role) =>
      this.choice(role, row.rolesSynced ? assignedByDirectory : tenant ? assignedForEveryTenant : reason),
    );
    return [...held, ...everywhere];
  }

  private choice(role: string, locked: string | undefined): RoleChoice {
    return { key: this.nextKey++, value: role, role, seated: true, ...(locked === undefined ? {} : { locked }) };
  }

  private emptyChoice(): RoleChoice {
    return { key: this.nextKey++, value: '', role: '' };
  }

  /** The roles the editable rows of a list name, each once, in the rows' order; a role a locked row holds is held already and is not named again. */
  private chosen(rows: readonly RoleChoice[]): string[] {
    const held = new Set(rows.filter((row) => row.locked !== undefined).map((row) => row.role));
    const names: string[] = [];
    for (const row of rows) {
      if (row.locked === undefined && row.role !== '' && !held.has(row.role) && !names.includes(row.role)) {
        names.push(row.role);
      }
    }
    return names;
  }

  /** The listed role a picker's value names, by name or by title, among the ones not taken; else none. */
  private resolve(value: string, taken: ReadonlySet<string>): string {
    const text = value.trim().toLowerCase();
    if (text === '') {
      return '';
    }
    return (
      this.roles().find(
        (role) =>
          !taken.has(role.name) && (role.name.toLowerCase() === text || roleTitle(role.name).toLowerCase() === text),
      )?.name ?? ''
    );
  }

  /** Runs one action as pending work, busy meanwhile, then reads the page again whatever happened. */
  private act(work: () => Promise<void>): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.tracked(async () => {
      this.busy.set(true);
      this.notice.set(undefined);
      try {
        await work();
      } catch (error) {
        this.notice.set(messageOf(error));
      } finally {
        await this.refresh();
        this.busy.set(false);
      }
    });
  }

  /** Follows one page relation when the server named one. */
  private turn(relation: (() => Promise<Page<ManagedUser>>) | undefined): Promise<void> {
    if (!relation || this.busy()) {
      return Promise.resolve();
    }
    return this.tracked(async () => {
      this.busy.set(true);
      this.notice.set(undefined);
      try {
        this.page.set(await relation());
        this.closePanel();
      } catch (error) {
        this.notice.set(messageOf(error));
      } finally {
        this.busy.set(false);
      }
    });
  }

  /** The first read of a partition: its page of users, the roles a seat may take, and the invitations standing into it. */
  private async load(partition: UserPartitionClient | undefined): Promise<void> {
    this.page.set(undefined);
    this.roles.set([]);
    this.invitations.set([]);
    this.closePanel();
    this.inviting.set(false);
    this.error.set(undefined);
    this.notice.set(undefined);
    if (!partition) {
      this.loading.set(false);
      return;
    }
    this.loading.set(true);
    try {
      this.page.set(await partition.users.page({ capabilities: askedCapabilities }));
    } catch (error) {
      this.error.set(messageOf(error));
    } finally {
      this.loading.set(false);
    }
    try {
      this.roles.set(await partition.roles.list({ limit: 'all' }));
    } catch {
      // A caller who may not list the roles seats nobody; the picker stays empty.
      this.roles.set([]);
    }
    await this.loadInvitations(partition);
  }

  /** The invitations standing into the partition, when it has them and the caller may list them; none otherwise. */
  private async loadInvitations(partition: UserPartitionClient): Promise<void> {
    if (!partition.invitations || !partition.canListInvitations()) {
      this.invitations.set([]);
      return;
    }
    try {
      this.invitations.set(
        await partition.invitations.list({ limit: 'all', capabilities: askedInvitationCapabilities }),
      );
    } catch (error) {
      this.notice.set(this.notice() ?? messageOf(error));
      this.invitations.set([]);
    }
  }

  /** The same page again, keeping its position, and the invitations; a failed refresh is a notice, not a blank page. */
  private async refresh(): Promise<void> {
    const current = this.page();
    const partition = this.users();
    if (!partition) {
      return;
    }
    try {
      this.page.set(current ? await current.reload() : await partition.users.page({ capabilities: askedCapabilities }));
    } catch (error) {
      this.notice.set(this.notice() ?? messageOf(error));
    }
    await this.loadInvitations(partition);
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
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** The roles the other rows of a list name: held already, or chosen on another row, so not this row's to choose. */
function takenBy(row: RoleChoice, rows: readonly RoleChoice[]): Set<string> {
  return new Set(rows.filter((other) => other !== row && other.role !== '').map((other) => other.role));
}
