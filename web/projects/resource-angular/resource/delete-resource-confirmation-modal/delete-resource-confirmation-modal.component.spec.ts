import { TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';

import {
  DeleteConfirmation,
  DeleteResourceConfirmationModalComponent,
  deleteQuestion,
} from './delete-resource-confirmation-modal.component';

// The confirmation before a delete says in plain words what is about to go: the row by
// its own name where the page has one, the list by its title, and a button that says
// what it does.
describe('DeleteResourceConfirmationModal', () => {
  const textOf = (element: Element | null): string => (element?.textContent ?? '').replace(/\s+/g, ' ').trim();

  async function drawn(asked?: DeleteConfirmation): Promise<HTMLElement> {
    await TestBed.configureTestingModule({
      imports: [DeleteResourceConfirmationModalComponent],
      providers: [
        provideNoopAnimations(),
        { provide: MatDialogRef, useValue: { close: (): void => undefined } },
        ...(asked ? [{ provide: MAT_DIALOG_DATA, useValue: asked }] : []),
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(DeleteResourceConfirmationModalComponent);
    fixture.detectChanges();

    return fixture.nativeElement as HTMLElement;
  }

  afterEach(() => {
    TestBed.resetTestingModule();
    document.querySelectorAll('.cdk-overlay-container').forEach((overlay) => (overlay.innerHTML = ''));
  });

  it('asks about a row of a list by the list it is in', async () => {
    const dialog = await drawn({ from: 'Passes', inList: true });

    expect(textOf(dialog.querySelector('[mat-dialog-title]'))).toBe('Delete');
    expect(textOf(dialog.querySelector('.question'))).toBe('Delete this row from Passes?');
  });

  it('names the row where the page knows its name', async () => {
    const dialog = await drawn({ name: 'Night Watch', from: 'Groups' });

    expect(textOf(dialog.querySelector('.question'))).toBe('Delete Night Watch from Groups?');
  });

  it('asks about the record of a page that has no name for it', async () => {
    const dialog = await drawn({ from: 'Missions' });

    expect(textOf(dialog.querySelector('.question'))).toBe('Delete this record from Missions?');
  });

  it('asks in plain words when it is told nothing', async () => {
    const dialog = await drawn();

    expect(textOf(dialog.querySelector('.question'))).toBe('Delete this record?');
    expect(textOf(dialog)).not.toMatch(/resource|Are you sure|Confirm/);
  });

  it('carries Cancel and a button in the words of what it does', async () => {
    const dialog = await drawn({ from: 'Passes', inList: true });

    expect([...dialog.querySelectorAll('button')].map(textOf)).toEqual(['Cancel', 'Delete']);
  });

  it('answers true from Delete, false from Cancel and nothing when it is closed any other way', async () => {
    TestBed.configureTestingModule({ providers: [provideNoopAnimations()] });
    const dialogs = TestBed.inject(MatDialog);
    const answers: unknown[] = [];
    const ask = (): MatDialogRef<DeleteResourceConfirmationModalComponent> => {
      const ref = dialogs.open(DeleteResourceConfirmationModalComponent, { data: { from: 'Passes', inList: true } });
      ref.afterClosed().subscribe((answer) => answers.push(answer));

      return ref;
    };
    const rest = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 20));
    const press = async (words: string): Promise<void> => {
      await rest();
      const button = [...document.querySelectorAll<HTMLButtonElement>('.cdk-overlay-container button')].find(
        (candidate) => textOf(candidate) === words,
      );
      expect(button, `the dialog has no "${words}" button`).toBeTruthy();
      button?.click();
      await rest();
    };

    ask();
    await press('Delete');
    ask();
    await press('Cancel');
    const closed = ask();
    closed.close();
    await rest();

    expect(answers).toEqual([true, false, undefined]);
  });
});

describe('deleteQuestion', () => {
  it('names the row and the list where it has both', () => {
    expect(deleteQuestion({ name: 'Night Watch', from: 'Groups', inList: true })).toBe(
      'Delete Night Watch from Groups?',
    );
  });

  it('names the row alone where the list has no title', () => {
    expect(deleteQuestion({ name: 'Night Watch' })).toBe('Delete Night Watch?');
    expect(deleteQuestion({ name: 'Night Watch', from: '  ' })).toBe('Delete Night Watch?');
  });

  it('says "this row" in a list and "this record" on a page for a row with no name', () => {
    expect(deleteQuestion({ inList: true })).toBe('Delete this row?');
    expect(deleteQuestion({ name: ' ', from: 'Passes', inList: true })).toBe('Delete this row from Passes?');
    expect(deleteQuestion({ from: 'Missions' })).toBe('Delete this record from Missions?');
    expect(deleteQuestion()).toBe('Delete this record?');
  });
});
