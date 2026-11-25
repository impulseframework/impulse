import { TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef, MatDialogState } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ApiError, DeleteLeft, RemoveOptions } from '@impulseframework/resource';

import {
  DeleteConfirmation,
  DeleteResourceConfirmationModalComponent,
  deleteQuestion,
} from './delete-resource-confirmation-modal.component';

// The confirmation before a delete says in plain words what is about to go: the row by
// its own name where the page has one, the list by its title, and a button that says
// what it does. Handed the delete, it runs it and keeps the server's refusal where the
// question was asked.
describe('DeleteResourceConfirmationModal', () => {
  const textOf = (element: Element | null): string => (element?.textContent ?? '').replace(/\s+/g, ' ').trim();
  const rest = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 20));
  const buttons = (): HTMLButtonElement[] => [
    ...document.querySelectorAll<HTMLButtonElement>('.cdk-overlay-container button'),
  ];
  const press = async (words: string): Promise<void> => {
    await rest();
    const button = buttons().find((candidate) => textOf(candidate) === words);
    expect(button, `the dialog has no "${words}" button`).toBeTruthy();
    button?.click();
    await rest();
  };

  // The dialog opened as a page opens it, with the answers it closes with.
  function opened(asked: DeleteConfirmation): {
    ref: MatDialogRef<DeleteResourceConfirmationModalComponent>;
    answers: unknown[];
  } {
    TestBed.configureTestingModule({ providers: [provideNoopAnimations()] });
    const answers: unknown[] = [];
    const ref = TestBed.inject(MatDialog).open(DeleteResourceConfirmationModalComponent, { data: asked });
    ref.afterClosed().subscribe((answer) => answers.push(answer));

    return { ref, answers };
  }

  // A delete whose every request the test answers in turn: `answer` settles the request
  // that waits, with what the server left, nothing, or a failure, and rests so the dialog
  // draws what it heard.
  function answered(): {
    run: (options?: RemoveOptions) => Promise<DeleteLeft | void>;
    said: unknown[];
    answer: (value: DeleteLeft | Error | undefined) => Promise<void>;
  } {
    const said: unknown[] = [];
    let settle: (value: DeleteLeft | Error | undefined) => void = () => undefined;
    const run = (options?: RemoveOptions): Promise<DeleteLeft | void> => {
      said.push(options);

      return new Promise<DeleteLeft | void>((resolve, reject) => {
        settle = (value) => (value instanceof Error ? reject(value) : resolve(value));
      });
    };
    const answer = async (value: DeleteLeft | Error | undefined): Promise<void> => {
      settle(value);
      await rest();
    };

    return { run, said, answer };
  }
  const overlay = (): Element | null => document.querySelector('.cdk-overlay-container');
  const asksFirst = (message: string): ApiError => new ApiError('PATCH', '/api/resources', 428, { message });

  async function drawn(asked?: DeleteConfirmation): Promise<HTMLElement> {
    await TestBed.configureTestingModule({
      imports: [DeleteResourceConfirmationModalComponent],
      providers: [
        provideNoopAnimations(),
        {
          provide: MatDialogRef,
          useValue: { close: (): void => undefined, getState: (): MatDialogState => MatDialogState.OPEN },
        },
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

  it("asks about a row with no name by the word the resource's rows go by", async () => {
    const dialog = await drawn({ from: 'Passes', word: 'hangar pass', inList: true });

    expect(textOf(dialog.querySelector('.question'))).toBe('Delete this hangar pass?');
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

    ask();
    await press('Delete');
    ask();
    await press('Cancel');
    const closed = ask();
    closed.close();
    await rest();

    expect(answers).toEqual([true, false, undefined]);
  });

  it('runs the delete it was handed and answers true once the row is gone', async () => {
    let runs = 0;
    const { answers } = opened({
      from: 'Squadrons',
      delete: () => {
        runs++;

        return Promise.resolve();
      },
    });

    await press('Delete');

    expect(runs).toBe(1);
    expect(answers).toEqual([true]);
  });

  it('waits for the server with its buttons off, and cannot be closed meanwhile', async () => {
    let gone: () => void = () => undefined;
    const { ref, answers } = opened({
      from: 'Squadrons',
      delete: () => new Promise<void>((resolve) => (gone = resolve)),
    });

    await press('Delete');

    expect(buttons().map((button) => [textOf(button), button.disabled])).toEqual([
      ['Cancel', true],
      ['Delete', true],
    ]);
    expect(ref.disableClose).toBe(true);
    expect(answers).toEqual([]);

    gone();
    await rest();

    expect(answers).toEqual([true]);
  });

  it("keeps a refused delete open with the server's sentence and one button, Close", async () => {
    const sentence = 'This squadron has 3 missions. Delete them first.';
    const { ref, answers } = opened({
      name: 'Hammer',
      from: 'Squadrons',
      delete: () => Promise.reject(new ApiError('POST', '/api/batch', 409, { message: sentence })),
    });

    await press('Delete');

    const overlay = document.querySelector('.cdk-overlay-container');
    expect(answers).toEqual([]);
    expect(textOf(overlay?.querySelector('.question') ?? null)).toBe('Delete Hammer from Squadrons?');
    expect(textOf(overlay?.querySelector('.refusal') ?? null)).toBe(sentence);
    expect(overlay?.querySelector('.refusal')?.getAttribute('role')).toBe('alert');
    expect(buttons().map((button) => [textOf(button), button.disabled])).toEqual([['Close', false]]);
    expect(ref.disableClose).toBeFalsy();

    await press('Close');

    expect(answers).toEqual([false]);
  });

  it("asks the server's question in the first one's place, and deletes everything under the row at the next press", async () => {
    const sentence =
      "Delete this mission and everything under it: 2 sorties, 1 mission document. This can't be undone.";
    const said: unknown[] = [];
    const { ref, answers } = opened({
      from: 'Missions',
      delete: (options) => {
        said.push(options);

        return options?.everythingUnder
          ? Promise.resolve()
          : Promise.reject(new ApiError('PATCH', '/api/resources', 428, { message: sentence }));
      },
    });

    await press('Delete');

    const overlay = document.querySelector('.cdk-overlay-container');
    expect(answers).toEqual([]);
    expect(said).toEqual([undefined]);
    expect(textOf(overlay?.querySelector('.question') ?? null)).toBe(sentence);
    expect(overlay?.querySelector('.refusal')).toBeNull();
    expect(buttons().map((button) => [textOf(button), button.disabled])).toEqual([
      ['Cancel', false],
      ['Delete', false],
    ]);
    expect(ref.disableClose).toBeFalsy();

    await press('Delete');

    expect(said).toEqual([undefined, { everythingUnder: true }]);
    expect(answers).toEqual([true]);
  });

  it("deletes nothing when the server's question is answered with Cancel", async () => {
    let runs = 0;
    const { answers } = opened({
      from: 'Missions',
      delete: () => {
        runs++;

        return Promise.reject(
          new ApiError('PATCH', '/api/resources', 428, { message: 'Delete this mission and everything under it?' }),
        );
      },
    });

    await press('Delete');
    await press('Cancel');

    expect(runs).toBe(1);
    expect(answers).toEqual([false]);
  });

  it("keeps a delete refused after the server's question open with the refusal and one button, Close", async () => {
    const question = "Delete this mission and everything under it: 2 sorties. This can't be undone.";
    const refusal = "This mission has sorties that you can't delete, so it can't be deleted.";
    const { answers } = opened({
      from: 'Missions',
      delete: (options) =>
        Promise.reject(
          options?.everythingUnder
            ? new ApiError('PATCH', '/api/resources', 403, { message: refusal })
            : new ApiError('PATCH', '/api/resources', 428, { message: question }),
        ),
    });

    await press('Delete');
    await press('Delete');

    const overlay = document.querySelector('.cdk-overlay-container');
    expect(textOf(overlay?.querySelector('.question') ?? null)).toBe(question);
    expect(textOf(overlay?.querySelector('.refusal') ?? null)).toBe(refusal);
    expect(buttons().map((button) => textOf(button))).toEqual(['Close']);
    expect(answers).toEqual([]);
  });

  it('shows no line of what is gone for a delete that finishes at once', async () => {
    const server = answered();
    const { answers } = opened({ from: 'Squadrons', delete: server.run });

    await press('Delete');

    expect(overlay()?.querySelector('.progress')).toBeNull();

    await server.answer(undefined);

    expect(server.said).toEqual([undefined]);
    expect(answers).toEqual([true]);
  });

  it('carries a delete too large for one request through its rounds from one press, saying how many records are gone, and answers true once nothing is left', async () => {
    const server = answered();
    const { ref, answers } = opened({ from: 'Missions', delete: server.run });

    await press('Delete');
    await server.answer(asksFirst("Delete this mission and everything under it: 38000 sorties. This can't be undone."));
    await press('Delete');
    await server.answer({ deleted: 6800, left: 31200 });

    expect(textOf(overlay()?.querySelector('.progress') ?? null)).toBe('Deleted 6,800 of 38,000 records…');
    expect(buttons().map((button) => [textOf(button), button.disabled])).toEqual([
      ['Cancel', true],
      ['Delete', true],
    ]);
    expect(ref.disableClose).toBe(true);
    expect(answers).toEqual([]);

    await server.answer({ deleted: 30000, left: 1200 });

    expect(textOf(overlay()?.querySelector('.progress') ?? null)).toBe('Deleted 36,800 of 38,000 records…');
    expect(answers).toEqual([]);

    await server.answer(undefined);

    expect(server.said).toEqual([
      undefined,
      { everythingUnder: true },
      { everythingUnder: true },
      { everythingUnder: true },
    ]);
    expect(answers).toEqual([true]);
  });

  it('sends the delete again after an answer that leaves no record, since the row still stands, and answers true on the answer after it', async () => {
    const server = answered();
    const { answers } = opened({ from: 'Missions', delete: server.run });

    await press('Delete');
    await server.answer(asksFirst("Delete this mission and everything under it: 84 sorties. This can't be undone."));
    await press('Delete');
    await server.answer({ deleted: 84, left: 0 });

    expect(textOf(overlay()?.querySelector('.progress') ?? null)).toBe('Deleted 84 of 84 records…');
    expect(server.said).toEqual([undefined, { everythingUnder: true }, { everythingUnder: true }]);
    expect(answers).toEqual([]);

    await server.answer(undefined);

    expect(server.said).toHaveLength(3);
    expect(answers).toEqual([true]);
  });

  it("keeps a delete refused part way open with the server's sentence and how many records were deleted before it, and sends it no more", async () => {
    const refusal = 'A sortie that is being flown cannot be deleted.';
    const server = answered();
    const { answers } = opened({ from: 'Missions', delete: server.run });

    await press('Delete');
    await server.answer(asksFirst("Delete this mission and everything under it: 600 sorties. This can't be undone."));
    await press('Delete');
    await server.answer({ deleted: 500, left: 100 });
    await server.answer(new ApiError('PATCH', '/api/resources', 400, { message: refusal }));

    expect(textOf(overlay()?.querySelector('.refusal') ?? null)).toBe(refusal);
    expect(textOf(overlay()?.querySelector('.progress') ?? null)).toBe(
      '500 records were deleted before this. Press Delete again once that is put right.',
    );
    expect(buttons().map((button) => [textOf(button), button.disabled])).toEqual([['Close', false]]);
    expect(server.said).toHaveLength(3);
    expect(answers).toEqual([]);

    await press('Close');

    expect(server.said).toHaveLength(3);
    expect(answers).toEqual([false]);
  });

  it('closes with false on any other failure and leaves it to the page', async () => {
    const closedWith: unknown[] = [];
    await TestBed.configureTestingModule({
      imports: [DeleteResourceConfirmationModalComponent],
      providers: [
        provideNoopAnimations(),
        {
          provide: MatDialogRef,
          useValue: {
            close: (answer: unknown): void => void closedWith.push(answer),
            getState: (): MatDialogState => MatDialogState.OPEN,
          },
        },
        { provide: MAT_DIALOG_DATA, useValue: { delete: () => Promise.reject(new Error('the network is down')) } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(DeleteResourceConfirmationModalComponent);
    fixture.detectChanges();

    await expect(fixture.componentInstance.confirm()).rejects.toThrow('the network is down');

    expect(closedWith).toEqual([false]);
    expect(fixture.componentInstance.refusal()).toBe('');
    expect(fixture.componentInstance.busy()).toBe(false);
  });

  it('leaves closing to the page that already closed it by leaving', async () => {
    const closedWith: unknown[] = [];
    await TestBed.configureTestingModule({
      imports: [DeleteResourceConfirmationModalComponent],
      providers: [
        provideNoopAnimations(),
        {
          provide: MatDialogRef,
          useValue: {
            close: (answer: unknown): void => void closedWith.push(answer),
            getState: (): MatDialogState => MatDialogState.CLOSED,
          },
        },
        { provide: MAT_DIALOG_DATA, useValue: { delete: () => Promise.resolve() } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(DeleteResourceConfirmationModalComponent);
    fixture.detectChanges();

    await fixture.componentInstance.confirm();

    expect(closedWith).toEqual([]);
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

  it("says the word the resource's rows go by for a row with no name, in a list and on a page", () => {
    expect(deleteQuestion({ word: 'hangar pass', from: 'Passes', inList: true })).toBe('Delete this hangar pass?');
    expect(deleteQuestion({ word: 'hangar pass', from: 'Passes' })).toBe('Delete this hangar pass?');
    expect(deleteQuestion({ name: ' ', word: ' hangar pass ' })).toBe('Delete this hangar pass?');
  });

  it('names the row by its own name before the word', () => {
    expect(deleteQuestion({ name: 'Night Watch', from: 'Groups', word: 'group', inList: true })).toBe(
      'Delete Night Watch from Groups?',
    );
  });

  it('says "this row" in a list and "this record" on a page for a row with no name and a resource with no word', () => {
    expect(deleteQuestion({ inList: true })).toBe('Delete this row?');
    expect(deleteQuestion({ name: ' ', from: 'Passes', inList: true })).toBe('Delete this row from Passes?');
    expect(deleteQuestion({ from: 'Missions' })).toBe('Delete this record from Missions?');
    expect(deleteQuestion()).toBe('Delete this record?');
  });
});
