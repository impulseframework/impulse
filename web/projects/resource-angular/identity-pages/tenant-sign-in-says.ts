import {
  DirectoryOnlyCount,
  DirectoryOnlyShare,
  Method,
  Resource,
  TenantSignInSettings,
} from '@impulseframework/resource';

// What a tenant's Sign-in settings page reads from its route, and the two things it says
// from the read of the tenant's sign-in settings: the one line on how the tenant
// stands and the line on the people the switch keeps out. They live apart from the
// functions that place the page, which load the page's component, so the component reads
// them without a cycle.

/** The route data key the Sign-in settings page's names are carried under. */
export const tenantSignInKey = 'impulseTenantSignIn';

/**
 * What the Sign-in settings page reads from its route: the tenant record in words, the
 * record a tenant's one directory is a row of, the record a tenant's people are rows of,
 * and the tenant's methods that set how its people enter, as the description names them
 * (a method it does not name draws nothing).
 */
export interface TenantSignIn {
  /** The tenant record in words, lowercase ("team", "cost center"); "tenant" where the description names none. */
  word: string;
  /** The record a tenant's one directory is a row of. */
  directories: Resource;
  /** The record a tenant's people are rows of. */
  people?: Resource;
  /**
   * The tenant record and its directory-only field, where the description names both: what
   * the page reads the switch from for a reader who is served no read of the sign-in
   * settings.
   */
  tenantRecord?: { resource: Resource; directoryOnlyField: string };
  /** The tenant's methods that set how its people enter. */
  methods: {
    openToDirectory?: Method;
    makeDirectoryInviteOnly?: Method;
    makeDirectoryOnly?: Method;
    endDirectoryOnly?: Method;
    vouchForDirectorySecondFactor?: Method;
    stopVouchingForDirectorySecondFactor?: Method;
  };
}

/** What the one line on how a tenant stands is said from. */
export interface TenantStands {
  /** The tenant record in words, lowercase ("team"). */
  word: string;
  /** The tenant's name, else "this <word>". */
  name: string;
  /** The read of the tenant's sign-in settings, where the reader may read it. */
  facts?: TenantSignInSettings;
  /** Whether the reader may list the tenant's directory. */
  lists: boolean;
  /** Whether the list holds the tenant's directory. */
  row: boolean;
}

/**
 * The one line at the top of the Sign-in settings page: how people enter the tenant, as
 * far as the reader may know it. The read of the sign-in settings says it whole; the list
 * of the tenant's directory says only whether there is one; a reader with neither is told
 * their role does not cover the page.
 */
export function tenantStandsLine(stands: TenantStands): string {
  const { facts, name, word } = stands;
  if (facts) {
    const directory = facts.directory;
    if (!directory) {
      return facts.passwords
        ? `People enter ${name} with a password or by invitation. No directory is set.`
        : `People enter ${name} by invitation. No directory is set.`;
    }
    if (facts.directoryOnly) {
      return `People enter ${name} through ${directory} only.`;
    }
    return facts.passwords
      ? `People enter ${name} through ${directory}, with a password or by invitation.`
      : `People enter ${name} through ${directory} or by invitation.`;
  }
  if (stands.lists) {
    return stands.row ? `This ${word} has a directory.` : 'No directory is set.';
  }
  return `Your role in this ${word} does not cover its sign-in settings.`;
}

/**
 * How many of the tenant's people the directory-only switch keeps out: the people who
 * hold a role in the tenant (`seated`) and have never signed in through its directory
 * (`outside`), said as what the switch would do while it is off and what it does while it
 * is on.
 */
export function tenantPeopleLine(people: DirectoryOnlyCount, on: boolean): string {
  const { seated, outside } = people;
  if (outside === 0) {
    return on ? 'Nobody here is kept out.' : 'Nobody here would be kept out.';
  }
  const one = on ? 'and is kept out.' : 'and would be kept out.';
  if (outside === 1 && seated === 1) {
    return `The one person here has never signed in through the directory ${one}`;
  }
  if (outside === 1) {
    return `1 of ${seated} people here has never signed in through the directory ${one}`;
  }
  return `${outside} of ${seated} people here have never signed in through the directory ${on ? 'and are kept out.' : 'and would be kept out.'}`;
}

/**
 * One auth's share of the people the directory-only switch keeps out, on a line of its own
 * under the people line: the words a page calls the auth's people by and how many of them
 * are kept out ("Crew: 3 of them").
 */
export function tenantShareLine(share: DirectoryOnlyShare): string {
  return `${share.label}: ${share.outside} of them`;
}
