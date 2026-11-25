import { ApplicationRef } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { notNamedFor } from './frame-testing';
import { askToLeaveTenant, LeaveTenantDialogData } from './leave-tenant-dialog.component';

// Leave on the person's account page asks in a window of its own: it names the tenant and
// what is lost, and its button stays locked until the person types what they are doing,
// "Delete me". Cancel, Escape and a press outside answer no.

/** A tenant of several, where the person holds two roles. */
const acme: LeaveTenantDialogData = { name: 'Acme', roles: ['Reader', 'Warden'], word: 'customer' };
/** A tenant the person holds no role of their own in. */
const beta: LeaveTenantDialogData = { name: 'Beta', roles: [], word: 'customer' };

describe('askToLeaveTenant', () => {
  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await TestBed.inject(ApplicationRef).whenStable();
  };
  const part = <T extends HTMLElement = HTMLElement>(name: string): T | null =>
    document.querySelector<T>(`.impulse-leave-tenant-dialog-${name}`);
  const text = (name: string): string | undefined => part(name)?.textContent?.replace(/\s+/g, ' ').trim();
  const box = (): HTMLInputElement => {
    const input = part<HTMLInputElement>('words');
    if (!input) throw new Error('no window is open');
    return input;
  };
  const button = (): HTMLButtonElement => {
    const leave = part<HTMLButtonElement>('leave');
    if (!leave) throw new Error('no window is open');
    return leave;
  };
  const type = async (value: string): Promise<void> => {
    box().value = value;
    box().dispatchEvent(new Event('input'));
    await settle();
  };
  const enter = (): void => {
    box().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  };
  /** Presses Escape where the window listens for it. */
  const escape = (): void => {
    const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true });
    Object.defineProperty(event, 'keyCode', { get: () => 27 });
    document.body.dispatchEvent(event);
  };

  /** Opens the window and hands back its pending answer (boxed: an async function would otherwise await the answer itself). */
  const open = async (data: LeaveTenantDialogData): Promise<{ answer: Promise<boolean>; dialog: MatDialog }> => {
    await TestBed.configureTestingModule({ providers: [provideNoopAnimations()] }).compileComponents();
    const dialog = TestBed.inject(MatDialog);
    const answer = askToLeaveTenant(dialog, data);
    await settle();
    return { answer, dialog };
  };

  afterEach(() => TestBed.resetTestingModule());

  it('names the tenant and every role lost, asks for "Delete me", and opens as an alert with the focus in the box', async () => {
    await open(acme);
    expect(text('title')).toBe('Leave Acme?');
    expect(text('what')).toBe(
      'You lose every role you hold in Acme (Reader, Warden), right away. Only an administrator there can bring you back.',
    );
    expect(document.querySelector('.impulse-leave-tenant-dialog-field mat-label')?.textContent?.trim()).toBe(
      'Type "Delete me" to go on',
    );
    expect(text('leave')).toBe('Leave Acme');
    expect(text('cancel')).toBe('Cancel');
    expect(button().disabled).toBe(true);
    const container = document.querySelector('mat-dialog-container');
    expect(container?.getAttribute('role')).toBe('alertdialog');
    expect(container?.getAttribute('aria-describedby')).toBe(part('what')?.id);
    expect(container?.getAttribute('aria-labelledby')).toBe(part('title')?.id);
    expect(document.activeElement).toBe(box());
    expect([
      box().getAttribute('autocomplete'),
      box().getAttribute('autocapitalize'),
      box().getAttribute('spellcheck'),
    ]).toEqual(['off', 'none', 'false']);
  });

  it('says the person loses their place in a tenant where they hold no role there', async () => {
    await open(beta);
    expect(text('what')).toBe(
      'You lose your place in Beta, right away. Only an administrator there can bring you back.',
    );
  });

  for (const tt of [
    { name: 'nothing typed', data: acme, typed: '', want: true },
    { name: 'other words', data: acme, typed: 'leave', want: true },
    { name: 'the start of the words', data: acme, typed: 'Delete m', want: true },
    { name: 'the words with more after them', data: acme, typed: 'Delete me now', want: true },
    { name: 'the words in other capitals with space around them', data: acme, typed: ' delete ME ', want: false },
  ]) {
    it(`keeps the button locked until the box holds the words: ${tt.name}`, async () => {
      await open(tt.data);
      await type(tt.typed);
      expect(button().disabled).toBe(tt.want);
    });
  }

  it('answers yes on the button once the words are typed', async () => {
    const { answer } = await open(acme);
    await type('Delete me');
    button().click();
    expect(await answer).toBe(true);
  });

  it('answers yes on Enter in the box once the words are typed, and Enter before them does nothing', async () => {
    const { answer } = await open(acme);
    await type('Delete m');
    enter();
    await settle();
    expect(part('title')).not.toBeNull();
    await type('Delete me');
    enter();
    expect(await answer).toBe(true);
  });

  for (const tt of [
    { name: 'Cancel', close: () => part<HTMLButtonElement>('cancel')?.click() },
    { name: 'Escape', close: escape },
    { name: 'a press outside', close: () => document.querySelector<HTMLElement>('.cdk-overlay-backdrop')?.click() },
  ]) {
    it(`answers no on ${tt.name}, the words typed or not`, async () => {
      const { answer } = await open(acme);
      await type('Delete me');
      tt.close();
      expect(await answer).toBe(false);
    });
  }

  it('opens at most 460px wide and never wider than the screen less 16px at either side', async () => {
    await TestBed.configureTestingModule({ providers: [provideNoopAnimations()] }).compileComponents();
    const dialog = TestBed.inject(MatDialog);
    const opened = vi.spyOn(dialog, 'open');
    void askToLeaveTenant(dialog, acme);
    await settle();
    expect(opened.mock.calls[0]?.[1]).toMatchObject({ width: '460px', maxWidth: 'calc(100vw - 32px)' });
  });

  it("names every element for the window: an application's rule for its own .title or .what lands on none", async () => {
    await open(acme);
    const host = document.querySelector('impulse-leave-tenant-dialog');
    if (!host) throw new Error('no window is open');
    expect(notNamedFor('impulse-leave-tenant-dialog-', host)).toEqual([]);
  });
});
