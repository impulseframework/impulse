import { ApplicationRef } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { afterEach, describe, expect, it } from 'vitest';
import { ActionDialogAnswer, ActionDialogData, askInDialog } from './action-dialog.component';

const settle = async (): Promise<void> => {
  await TestBed.inject(ApplicationRef).whenStable();
  await new Promise((resolve) => setTimeout(resolve, 0));
};
const overlay = (): HTMLElement => document.querySelector('.cdk-overlay-container') as HTMLElement;
const text = (selector: string): string => overlay().querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
const click = (selector: string): void => overlay().querySelector<HTMLButtonElement>(selector)!.click();

/** Opens the dialog and hands back its pending answer (boxed: an async function would otherwise await the answer itself). */
async function open(data: ActionDialogData): Promise<{ answer: Promise<ActionDialogAnswer> }> {
  await TestBed.configureTestingModule({ imports: [NoopAnimationsModule] }).compileComponents();
  const answer = askInDialog(TestBed.inject(MatDialog), data);
  await settle();
  return { answer };
}

describe('askInDialog', () => {
  afterEach(() => TestBed.resetTestingModule());

  it('asks with the title and the words, and answers true on the verb', async () => {
    const { answer } = await open({ title: 'Revoke nightly export?', words: 'A program using it stops working.', confirm: 'Revoke' });
    expect(text('.impulse-action-title')).toBe('Revoke nightly export?');
    expect(text('.impulse-action-words')).toBe('A program using it stops working.');
    expect(text('.impulse-action-confirm')).toBe('Revoke');
    expect(text('.impulse-action-cancel')).toBe('Cancel');
    expect(overlay().querySelector('input')).toBeNull();
    click('.impulse-action-confirm');
    expect(await answer).toBe(true);
  });

  it('answers undefined on Cancel, and nothing else', async () => {
    const { answer } = await open({ title: 'Delete hollis?', confirm: 'Delete' });
    click('.impulse-action-cancel');
    expect(await answer).toBeUndefined();
  });

  it('takes one field, waits for it to hold something, and answers its value trimmed', async () => {
    const { answer } = await open({
      title: 'Rename hollis',
      field: { label: 'New username', value: 'hollis', type: 'text', maxLength: 40 },
      confirm: 'Rename',
    });
    const input = overlay().querySelector<HTMLInputElement>('input')!;
    expect(input.value).toBe('hollis');
    expect(input.getAttribute('maxlength')).toBe('40');
    expect(text('mat-label')).toBe('New username');
    input.value = '   ';
    input.dispatchEvent(new Event('input'));
    await settle();
    expect(overlay().querySelector<HTMLButtonElement>('.impulse-action-confirm')!.disabled).toBe(true);
    input.value = '  hollis-2  ';
    input.dispatchEvent(new Event('input'));
    await settle();
    expect(overlay().querySelector<HTMLButtonElement>('.impulse-action-confirm')!.disabled).toBe(false);
    click('.impulse-action-confirm');
    expect(await answer).toBe('hollis-2');
  });
});
