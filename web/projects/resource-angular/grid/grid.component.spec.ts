import { Component, effect } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ColumnConfig, iconCellKey, RecordData } from '@impulseframework/resource-angular/types';

import { AppGridComponent, MIN_TEXT_COLUMN_WIDTH } from './grid.component';
import { ColumnFilter, ColumnFilterInputs, RowKey } from './grid-types';

// The grid identifies a row by `rowKey`, the caller's function from a row to its identity,
// or by the row's position in the page when the caller passes none. The track expression,
// the selection, and the expansion are keyed by it, so two rows alike in every field, or
// alike in a field called `id`, are still two rows.

describe('AppGridComponent', () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let component: AppGridComponent;

  const create = async (rows: RecordData[], rowKey: RowKey | undefined): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('rowData', rows);
    fixture.componentRef.setInput(
      'columnDefs',
      Object.keys(rows[0]).map((id) => ({ id })),
    );
    fixture.componentRef.setInput('selectionType', 'multiple');
    fixture.componentRef.setInput('enableRowExpansion', true);
    fixture.componentRef.setInput('rowKey', rowKey);
    fixture.detectChanges();
  };

  const selectedInDom = (): number =>
    (fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row.selected').length;

  const cases: { name: string; rows: RecordData[]; rowKey: RowKey | undefined; pick: number; wantKey: unknown }[] = [
    {
      name: 'a compound key the caller joins',
      rows: [
        { squadronId: 'sq-1', pilotId: 'p-1' },
        { squadronId: 'sq-1', pilotId: 'p-2' },
      ],
      rowKey: (row): string => {
        return `${row['squadronId']}/${row['pilotId']}`;
      },
      pick: 0,
      wantKey: 'sq-1/p-1',
    },
    {
      name: 'a key not named id, with id alike on every row',
      rows: [
        { code: 'A', id: 'same' },
        { code: 'B', id: 'same' },
      ],
      rowKey: (row): unknown => {
        return row['code'];
      },
      pick: 1,
      wantKey: 'B',
    },
    {
      name: 'no key: the position, with every field alike',
      rows: [{ section: 'General' }, { section: 'General' }],
      rowKey: undefined,
      pick: 1,
      wantKey: 1,
    },
  ];

  for (const tt of cases) {
    describe(tt.name, () => {
      const other = 1 - tt.pick;

      beforeEach(async () => {
        await create(tt.rows, tt.rowKey);
      });

      it('identifies the row by it', () => {
        expect(component.keyOf(tt.rows[tt.pick])).toEqual(tt.wantKey);
        expect(component.keyOf(tt.rows[other])).not.toEqual(tt.wantKey);
      });

      it('selects the one row, and selecting all selects both', () => {
        const emitted: RecordData[][] = [];
        component.selectedRows.subscribe((rows) => emitted.push(rows));

        component.toggleRow(tt.rows[tt.pick]);
        fixture.detectChanges();
        expect(component.isSelected(tt.rows[tt.pick])).toBe(true);
        expect(component.isSelected(tt.rows[other])).toBe(false);
        expect(component.allSelected()).toBe(false);
        expect(component.someSelected()).toBe(true);
        expect(selectedInDom()).toBe(1);
        expect(emitted.at(-1)).toEqual([tt.rows[tt.pick]]);

        component.toggleSelectAll();
        fixture.detectChanges();
        expect(component.allSelected()).toBe(true);
        expect(selectedInDom()).toBe(2);
        expect(emitted.at(-1)?.length).toBe(2);
      });

      it('expands the one row', () => {
        component.toggleExpand(tt.rows[tt.pick]);
        expect(component.isExpanded(tt.rows[tt.pick])).toBe(true);
        expect(component.isExpanded(tt.rows[other])).toBe(false);

        component.toggleExpand(tt.rows[tt.pick]);
        expect(component.isExpanded(tt.rows[tt.pick])).toBe(false);
      });

      it('draws both rows', () => {
        expect((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row').length).toBe(2);
      });
    });
  }

  // jsdom, where an application's specs run, has no ResizeObserver: the grid keeps the
  // one measurement of its container and draws the page instead of failing in its render hook.
  it('draws an icon column from the display row: the icon as an image named by the words, the words alone where the row has no icon', async () => {
    const rows: RecordData[] = [
      {
        id: 'p-1',
        limitStatus: 'Grandfathered: Jane Doe has 4 active learners. The partner limit is 2.',
        [iconCellKey('limitStatus')]: 'warning',
      },
      { id: 'p-2', limitStatus: 'ok', [iconCellKey('limitStatus')]: null },
    ];
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', rows);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'id' },
      {
        id: 'limitStatus',
        header: 'Limit',
        iconConfig: { icons: { grandfathered: 'warning' } },
        tooltipPosition: 'right',
      },
    ]);
    fixture.detectChanges();
    const element = fixture.nativeElement as HTMLElement;
    const cells = Array.from(element.querySelectorAll<HTMLElement>('td.icon-col'));
    expect(cells).toHaveLength(2);
    const icon = cells[0].querySelector('mat-icon');
    expect(icon?.textContent?.trim()).toBe('warning');
    expect(icon?.getAttribute('aria-label')).toBe(
      'Grandfathered: Jane Doe has 4 active learners. The partner limit is 2.',
    );
    expect(icon?.getAttribute('role')).toBe('img');
    expect(icon?.getAttribute('tabindex')).toBe('0');
    expect(cells[1].querySelector('mat-icon')).toBeNull();
    expect(cells[1].textContent?.trim()).toBe('ok');
    expect(Array.from(element.querySelectorAll('th .col-header')).map((h) => h.textContent?.trim())).toEqual([
      'Id',
      'Limit',
    ]);
  });

  it('draws a header as a button that asks for a sort, and as words alone on a table whose rows have one order', async () => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [{ changedAt: 'Nov 17, 2026, 3:42 PM', changedBy: 'morgan' }]);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'changedAt', header: 'When' },
      { id: 'changedBy', header: 'By' },
    ]);
    const asked: unknown[] = [];
    fixture.componentInstance.sortChange.subscribe((sorts) => asked.push(sorts));
    fixture.detectChanges();
    const element = fixture.nativeElement as HTMLElement;
    const headers = (): string[] =>
      Array.from(element.querySelectorAll('th .col-header')).map((h) => h.textContent?.trim() ?? '');

    // Sortable unless told otherwise: each header is a button, and a click asks for the sort.
    expect(headers()).toEqual(['When', 'By']);
    expect(element.querySelectorAll('th button.header-label')).toHaveLength(2);
    element.querySelector<HTMLButtonElement>('th button.header-label')?.click();
    expect(asked).toEqual([[{ field: 'changedAt', direction: 'asc' }]]);

    fixture.componentRef.setInput('sortable', false);
    fixture.detectChanges();

    // The same words, and nothing to press.
    expect(headers()).toEqual(['When', 'By']);
    expect(element.querySelectorAll('th button')).toHaveLength(0);
    expect(element.querySelectorAll('th span.header-words')).toHaveLength(2);
    element.querySelector<HTMLElement>('th span.header-words')?.click();
    expect(asked).toHaveLength(1);
  });

  it('draws the header of a column that opts out of sorting as words, and leaves the other headers buttons', async () => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [{ name: 'debriefs', updatedAt: 'Nov 17, 2026, 3:42 PM' }]);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'name', header: 'Flag' },
      { id: 'updatedAt', header: 'Last changed', sortable: false },
    ]);
    const asked: unknown[] = [];
    fixture.componentInstance.sortChange.subscribe((sorts) => asked.push(sorts));
    fixture.detectChanges();
    const element = fixture.nativeElement as HTMLElement;
    const words = (selector: string): string[] =>
      Array.from(element.querySelectorAll(selector)).map((h) => h.textContent?.trim() ?? '');

    expect(words('th button.header-label .col-header')).toEqual(['Flag']);
    expect(words('th span.header-words .col-header')).toEqual(['Last changed']);
    element.querySelector<HTMLElement>('th span.header-words')?.click();
    expect(asked).toHaveLength(0);
    element.querySelector<HTMLButtonElement>('th button.header-label')?.click();
    expect(asked).toEqual([[{ field: 'name', direction: 'asc' }]]);
  });

  it('draws a yes-or-no value as a check or a dash, each named for a screen reader, and every other value as its text', async () => {
    const rows: RecordData[] = [
      { username: 'ada', disabled: true, count: 0, note: 'false' },
      { username: 'mo', disabled: false, count: 2, note: '' },
    ];
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', rows);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'username' },
      { id: 'disabled' },
      { id: 'count' },
      { id: 'note' },
    ]);
    fixture.detectChanges();
    const element = fixture.nativeElement as HTMLElement;
    const cells = Array.from(element.querySelectorAll<HTMLElement>('tr.impulse-row')).map((row) =>
      Array.from(row.querySelectorAll<HTMLElement>('td.data-col')),
    );
    expect(cells).toHaveLength(2);

    const yes = cells[0][1].querySelector('mat-icon.cell-yes');
    expect(yes?.textContent?.trim()).toBe('check');
    expect(yes?.getAttribute('aria-label')).toBe('Yes');
    expect(cells[0][1].textContent?.trim()).toBe('check');

    const no = cells[1][1].querySelector('.cell-no');
    expect(no?.getAttribute('aria-label')).toBe('No');
    expect(cells[1][1].querySelector('mat-icon')).toBeNull();
    expect(cells[1][1].textContent).not.toContain('false');

    // A zero, and the word false as text, are not yes-or-no values.
    expect(cells[0][2].textContent?.trim()).toBe('0');
    expect(cells[0][3].textContent?.trim()).toBe('false');
    expect(cells[0][2].querySelector('.cell-no, .cell-yes')).toBeNull();
    expect(cells[0][3].querySelector('.cell-no, .cell-yes')).toBeNull();
  });

  it('draws the page under virtual scroll where the environment has no ResizeObserver', async () => {
    expect(typeof ResizeObserver).toBe('undefined');
    await create([{ section: 'General' }, { section: 'Flight' }], undefined);
    fixture.componentRef.setInput('enableVirtualScroll', true);
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).querySelectorAll('tr.impulse-row').length).toBe(2);
  });
});

// The filter menu's value control follows the column's field: a date control on a date
// column, a date and a time of day on a timestamp column, typed text elsewhere and for a
// list, and the value travels as the server reads it, the whole day or minute.
describe("the filter menu's value control", () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let emitted: ColumnFilter[] | undefined;

  const create = async (filterInputs: ColumnFilterInputs): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [
      { deadline: '2026-12-15T09:30:00Z', expiresOn: '2026-12-20', title: 'Survey' },
    ]);
    fixture.componentRef.setInput('columnDefs', [{ id: 'deadline' }, { id: 'expiresOn' }, { id: 'title' }]);
    fixture.componentRef.setInput('filterability', { deadline: 'always', expiresOn: 'always', title: 'always' });
    fixture.componentRef.setInput('filterInputs', filterInputs);
    emitted = undefined;
    fixture.componentInstance.filterChange.subscribe((filters) => {
      emitted = filters;
    });
    fixture.detectChanges();
  };
  const settle = async (): Promise<void> => {
    fixture.detectChanges();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };
  /** Opens the column's filter menu by its header button and hands back the menu's panel in the overlay. */
  const openMenu = async (column: number): Promise<HTMLElement> => {
    const triggers = (fixture.nativeElement as HTMLElement).querySelectorAll<HTMLButtonElement>(
      'button.filter-trigger',
    );
    triggers[column].click();
    await settle();
    const menu = document.body.querySelector<HTMLElement>('.cdk-overlay-container .impulse-grid-filter-menu');
    if (!menu) {
      throw new Error('no menu open');
    }
    return menu;
  };
  const controls = (menu: HTMLElement): string[] =>
    Array.from(menu.querySelectorAll('input')).map((input) => input.type);
  const give = (input: HTMLInputElement | null, value: string): void => {
    if (!input) {
      throw new Error('no input');
    }
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  };
  const choose = (menu: HTMLElement, operator: string): void => {
    const select = menu.querySelector<HTMLSelectElement>('select.filter-operator');
    if (!select) {
      throw new Error('no operator select');
    }
    select.value = operator;
    select.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  };
  const apply = async (menu: HTMLElement): Promise<void> => {
    const button = Array.from(menu.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Apply');
    if (!button) {
      throw new Error('no Apply button');
    }
    button.click();
    await settle();
  };
  /** Closes the open menu as a click beside it does. */
  const closeMenu = async (): Promise<void> => {
    const backdrop = document.body.querySelector<HTMLElement>('.cdk-overlay-backdrop');
    if (!backdrop) {
      throw new Error('no menu backdrop');
    }
    backdrop.click();
    await settle();
  };

  it('a timestamp column takes a day and a time of day, sent as the minute', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    expect(controls(menu)).toEqual(['date', 'time']);
    give(menu.querySelector('input[type=date]'), '2026-12-15');
    give(menu.querySelector('input[type=time]'), '09:30');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'deadline', operator: 'eq', value: '2026-12-15T09:30' }]);
  });

  it('a timestamp column with the day alone sends the day', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    give(menu.querySelector('input[type=date]'), '2026-12-15');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'deadline', operator: 'eq', value: '2026-12-15' }]);
  });

  it('a new operator waits for Apply, as a value does, and keeps the day the control shows', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    give(menu.querySelector('input[type=date]'), '2026-12-15');
    await apply(menu);
    fixture.componentRef.setInput('filters', emitted);
    emitted = undefined;
    choose(menu, 'gte');
    expect(menu.querySelector<HTMLInputElement>('input[type=date]')?.value).toBe('2026-12-15');
    expect(emitted).toBeUndefined();
    give(menu.querySelector('input[type=time]'), '12:00');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'deadline', operator: 'gte', value: '2026-12-15T12:00' }]);
  });

  it('a null test chosen alone commits as the menu closes', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(2);
    choose(menu, 'isnull');
    expect(emitted).toBeUndefined();
    await closeMenu();
    expect(emitted).toEqual([{ field: 'title', operator: 'isnull', value: '' }]);
  });

  it('closing the menu on an unfinished draft keeps the committed filter', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(1);
    choose(menu, 'in');
    give(menu.querySelector('input'), '2026-12-01, 2027');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'expiresOn', operator: 'in', value: '2026-12-01, 2027' }]);
    fixture.componentRef.setInput('filters', emitted);
    emitted = undefined;
    choose(menu, 'eq');
    expect(menu.querySelector<HTMLInputElement>('input[type=date]')?.value).toBe('');
    await closeMenu();
    expect(emitted).toBeUndefined();
  });

  it('a time of day without a day is nothing yet', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(0);
    give(menu.querySelector('input[type=time]'), '09:30');
    await apply(menu);
    expect(emitted).toBeUndefined();
  });

  it('a date column takes a day alone', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(1);
    expect(controls(menu)).toEqual(['date']);
    give(menu.querySelector('input[type=date]'), '2026-12-20');
    await apply(menu);
    expect(emitted).toEqual([{ field: 'expiresOn', operator: 'eq', value: '2026-12-20' }]);
  });

  it('a list on a date column is typed in the forms the server reads, and a return to one value starts over', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(1);
    choose(menu, 'in');
    expect(controls(menu)).toEqual(['text']);
    const text = menu.querySelector<HTMLInputElement>('input');
    expect(text?.placeholder).toBe('2026-12-15, 12/2026');
    give(text, '2026-12-01, 2027');
    choose(menu, 'eq');
    expect(controls(menu)).toEqual(['date']);
    expect(menu.querySelector<HTMLInputElement>('input[type=date]')?.value).toBe('');
    expect(emitted).toBeUndefined();
  });

  it('a column with no control keeps its text box', async () => {
    await create({ deadline: 'datetime', expiresOn: 'date' });
    const menu = await openMenu(2);
    expect(controls(menu)).toEqual(['text']);
    expect(menu.querySelector<HTMLInputElement>('input')?.placeholder).toBe('Value');
  });
});

// Every cell of the grid is one line, and a text wider than its column ends in an
// ellipsis. The grid offers the whole text in the tooltip while the pointer or the
// keyboard's focus is on a text that is cut short, and nothing where the text fits. It
// measures when the pointer or the focus arrives; jsdom lays nothing out, so each case
// says how wide a text is and how wide the box that holds it.
describe('a text cut short', () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let element: HTMLElement;

  const notes = 'Hold at the outer marker until the convoy clears, then follow the tender in on the night side.';
  const notesHeader = 'Notes the dispatcher left for the next shift';

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [
      { callSign: 'Anvil 1', notes, cleared: true },
      { callSign: 'Anvil 2', notes: 'None', cleared: false },
    ]);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'callSign' },
      { id: 'notes', header: notesHeader },
      { id: 'cleared' },
    ]);
    fixture.detectChanges();
    element = fixture.nativeElement as HTMLElement;
  });

  const settle = async (): Promise<void> => {
    fixture.detectChanges();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };
  const cell = (row: number, column: number): HTMLElement => {
    return element.querySelectorAll<HTMLElement>('tr.impulse-row')[row].querySelectorAll<HTMLElement>('td.data-col')[
      column
    ];
  };
  const header = (column: number): HTMLElement => {
    return element.querySelectorAll<HTMLElement>('th .col-header')[column];
  };
  /** Says how wide the text is and how wide the box that holds it, as a browser's layout would. */
  const measured = (text: HTMLElement, textWidth: number, boxWidth: number): HTMLElement => {
    Object.defineProperty(text, 'scrollWidth', { configurable: true, value: textWidth });
    Object.defineProperty(text, 'clientWidth', { configurable: true, value: boxWidth });
    return text;
  };
  const pointerOnto = async (target: HTMLElement): Promise<void> => {
    target.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
    await settle();
  };
  const pointerOff = async (target: HTMLElement, onto: Element | null = null): Promise<void> => {
    target.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, relatedTarget: onto }));
    await settle();
  };
  const tooltipSurface = (): HTMLElement | null => {
    return document.body.querySelector<HTMLElement>(
      '.cdk-overlay-container .mat-mdc-tooltip-show .mat-mdc-tooltip-surface',
    );
  };
  /** The words of the tooltip that is open, null while none is. */
  const tooltip = (): string | null => {
    return tooltipSurface()?.textContent?.trim() ?? null;
  };
  /** The browser's next frame, in which a text the focus arrived on is offered. */
  const nextFrame = async (): Promise<void> => {
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => {
        resolve();
      });
    });
    await settle();
  };

  it('a cell whose text is wider than its column offers the whole text while the pointer is on it', async () => {
    const cut = measured(cell(0, 1), 620, 180);
    await pointerOnto(cut);
    expect(tooltip()).toBe(notes);

    await pointerOff(cut);
    expect(tooltip()).toBeNull();
  });

  it('a cell whose text fits offers nothing', async () => {
    await pointerOnto(measured(cell(1, 1), 40, 180));
    expect(tooltip()).toBeNull();

    await pointerOnto(measured(cell(0, 0), 180, 180));
    expect(tooltip()).toBeNull();
  });

  it('measures when the pointer arrives, so a column made wider or narrower is followed', async () => {
    const text = measured(cell(0, 1), 620, 700);
    await pointerOnto(text);
    expect(tooltip()).toBeNull();
    await pointerOff(text);

    measured(text, 620, 180);
    await pointerOnto(text);
    expect(tooltip()).toBe(notes);
    await pointerOff(text);

    measured(text, 620, 700);
    await pointerOnto(text);
    expect(tooltip()).toBeNull();
  });

  it('moves from one cut-short cell to the next, and closes on a cell that fits', async () => {
    const first = measured(cell(0, 1), 620, 180);
    const second = measured(cell(1, 0), 90, 48);
    await pointerOnto(first);
    expect(tooltip()).toBe(notes);

    first.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, relatedTarget: second }));
    await pointerOnto(second);
    expect(tooltip()).toBe('Anvil 2');

    second.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, relatedTarget: cell(1, 1) }));
    await pointerOnto(cell(1, 1));
    expect(tooltip()).toBeNull();
  });

  it('stays open while the pointer goes onto the tooltip, so a long text can be read', async () => {
    const cut = measured(cell(0, 1), 620, 180);
    await pointerOnto(cut);
    const surface = tooltipSurface();
    expect(surface).not.toBeNull();

    await pointerOff(cut, surface);
    expect(tooltip()).toBe(notes);
  });

  it('closes when the table scrolls under it, and the next move of the pointer offers what it is on then', async () => {
    const cut = measured(cell(0, 1), 620, 180);
    await pointerOnto(cut);
    expect(tooltip()).toBe(notes);

    element.querySelector('.impulse-grid-scroll')?.dispatchEvent(new Event('scroll'));
    await settle();
    expect(tooltip()).toBeNull();

    // The same cell is still under the pointer, so the browser announces no arrival.
    cut.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
    await settle();
    expect(tooltip()).toBe(notes);
  });

  it('hears one move of the pointer after a scroll that closed the tooltip, and no other', async () => {
    const cut = measured(cell(0, 1), 620, 180);
    const move = async (): Promise<void> => {
      cut.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
      await settle();
    };
    const scroll = async (): Promise<void> => {
      element.querySelector('.impulse-grid-scroll')?.dispatchEvent(new Event('scroll'));
      await settle();
    };

    // A move with no scroll before it is not heard.
    await move();
    expect(tooltip()).toBeNull();

    // A scroll that closed nothing listens for nothing.
    await scroll();
    await move();
    expect(tooltip()).toBeNull();

    // The one move after a scroll that closed the tooltip is heard, and the next is not.
    await pointerOnto(cut);
    await scroll();
    await move();
    expect(tooltip()).toBe(notes);
    await pointerOff(cut);
    await move();
    expect(tooltip()).toBeNull();
  });

  describe('under the keyboard', () => {
    /** The rows drawn again, as after a read. */
    const drawnAgain = async (): Promise<void> => {
      fixture.componentRef.setInput('rowData', [...(fixture.componentInstance.rowData() as RecordData[])]);
      await settle();
    };
    /** A key pressed with the focus somewhere on the page, outside the list. */
    const press = async (key: string): Promise<void> => {
      document.body.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
      await settle();
    };
    /** A text measured as `measured` says, that counts how often its width is asked for. */
    const watched = (text: HTMLElement, textWidth: number, boxWidth: number): { looks: number } => {
      const seen = { looks: 0 };
      Object.defineProperty(text, 'scrollWidth', {
        configurable: true,
        get: (): number => {
          seen.looks++;
          return textWidth;
        },
      });
      Object.defineProperty(text, 'clientWidth', { configurable: true, value: boxWidth });
      return seen;
    };

    it('a cell is a stop of the Tab key where its text is cut, and no stop where it fits or draws its value, once the key is pressed', async () => {
      measured(cell(0, 1), 620, 180);
      measured(cell(1, 1), 40, 180);
      // A yes-or-no cell draws a mark: it has no text to cut, however narrow it is.
      measured(cell(0, 2), 620, 20);
      await drawnAgain();
      // Drawn, and the Tab key not pressed: no cell is a stop, and another key makes none.
      expect(cell(0, 1).hasAttribute('tabindex')).toBe(false);
      await press('ArrowDown');
      expect(cell(0, 1).hasAttribute('tabindex')).toBe(false);

      await press('Tab');
      expect(cell(0, 1).getAttribute('tabindex')).toBe('0');
      expect(cell(1, 1).hasAttribute('tabindex')).toBe(false);
      expect(cell(0, 0).hasAttribute('tabindex')).toBe(false);
      expect(cell(0, 2).hasAttribute('tabindex')).toBe(false);
    });

    it('looks at no cell when the list is drawn, at each once when the Tab key is pressed, and not again until what the list draws has changed', async () => {
      const seen = watched(cell(0, 1), 620, 180);
      await drawnAgain();
      expect(seen.looks).toBe(0);

      await press('Tab');
      expect(seen.looks).toBe(1);
      await press('Tab');
      await press('Tab');
      expect(seen.looks).toBe(1);

      await drawnAgain();
      expect(seen.looks).toBe(1);
      await press('Tab');
      expect(seen.looks).toBe(2);
    });

    it('looks again once a column has been dragged to another width', async () => {
      const seen = watched(cell(0, 1), 620, 180);
      await press('Tab');
      expect(seen.looks).toBe(1);

      const handle = element.querySelectorAll<HTMLElement>('thead th.data-col')[1].querySelector('.resize-handle');
      handle?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, clientX: 400 }));
      document.dispatchEvent(new MouseEvent('mousemove', { clientX: 900 }));
      document.dispatchEvent(new MouseEvent('mouseup'));
      await settle();
      expect(seen.looks).toBe(1);

      await press('Tab');
      expect(seen.looks).toBe(2);
    });

    it('a cut cell takes the focus and shows its text whole there, as under the pointer', async () => {
      const cut = measured(cell(0, 1), 620, 180);
      await press('Tab');

      cut.focus();
      expect(document.activeElement).toBe(cut);
      cut.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      await nextFrame();
      expect(tooltip()).toBe(notes);

      cut.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
      await settle();
      expect(tooltip()).toBeNull();
    });

    it('a cell is no stop any more once its text fits', async () => {
      const text = measured(cell(0, 1), 620, 180);
      await press('Tab');
      expect(text.getAttribute('tabindex')).toBe('0');

      measured(text, 620, 700);
      await drawnAgain();
      await press('Tab');
      expect(text.hasAttribute('tabindex')).toBe(false);
    });
  });

  describe('the anchor the tooltip is hung on', () => {
    const box = (left: number, top: number, width: number, height: number): DOMRect => {
      return { left, top, width, height, right: left + width, bottom: top + height } as DOMRect;
    };
    const anchor = (): HTMLElement => {
      return element.querySelector('.cut-short-anchor') as HTMLElement;
    };
    /** The list's own box, which the anchor is placed in, and a cell's, as a browser's layout would give them. */
    const laidOut = (cut: HTMLElement, list: DOMRect, cellBox: DOMRect): void => {
      const host = element.querySelector('.impulse-grid-host') as HTMLElement;
      // jsdom lays nothing out, so it knows no box an element is positioned in.
      Object.defineProperty(anchor(), 'offsetParent', { configurable: true, value: host });
      host.getBoundingClientRect = (): DOMRect => list;
      // The list's box has a line of a pixel around it, and what it holds lies inside the line.
      Object.defineProperty(host, 'clientLeft', { configurable: true, value: 1 });
      Object.defineProperty(host, 'clientTop', { configurable: true, value: 1 });
      Object.defineProperty(host, 'clientWidth', { configurable: true, value: list.width - 2 });
      Object.defineProperty(host, 'clientHeight', { configurable: true, value: list.height - 2 });
      cut.getBoundingClientRect = (): DOMRect => cellBox;
    };
    const placed = (): string[] => {
      const style = anchor().style;
      return [style.left, style.top, style.width, style.height];
    };

    it('is laid over the text, in the list', async () => {
      const cut = measured(cell(0, 1), 620, 180);
      laidOut(cut, box(20, 100, 336, 400), box(120, 150, 180, 49));
      await pointerOnto(cut);

      expect(tooltip()).toBe(notes);
      expect(placed()).toEqual(['99px', '49px', '180px', '49px']);
    });

    it("is laid over no more of a text than is inside the list: a cell the list has partly scrolled out of view is offered over the part in view, and the anchor never reaches past the list's edge", async () => {
      const cut = measured(cell(0, 1), 620, 180);
      // The cell starts 40 before the list's right edge, 39 before the inside of its line,
      // and runs 140 past it.
      laidOut(cut, box(20, 100, 336, 400), box(316, 150, 180, 49));
      await pointerOnto(cut);

      expect(tooltip()).toBe(notes);
      expect(placed()).toEqual(['295px', '49px', '39px', '49px']);
      const [left, , width] = placed().map((length) => parseFloat(length));
      expect(left + width).toBeLessThanOrEqual(334);
    });

    it('is laid over the part in view of a text the list has scrolled out of view at its left edge', async () => {
      const cut = measured(cell(0, 1), 620, 180);
      // The cell starts 30 before the inside of the list's line and runs 150 into it.
      laidOut(cut, box(20, 100, 336, 400), box(-9, 150, 180, 49));
      await pointerOnto(cut);

      expect(tooltip()).toBe(notes);
      expect(placed()).toEqual(['0px', '49px', '150px', '49px']);
    });

    it('lies in a box that clips what it holds and is never scrolled, by a script or by the browser bringing something into view', () => {
      const sheet = Array.from(document.querySelectorAll('style'))
        .map((style) => style.textContent ?? '')
        .join('\n');
      const host = Array.from(sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g))
        .filter(([, selector]) => selector.trim().startsWith('.impulse-grid-host') && !selector.trim().includes(' '))
        .map(([, , declarations]) => declarations)
        .join('\n');
      expect(host).toContain('overflow: clip');
      expect(host).not.toContain('overflow: hidden');
    });

    it('is taken back to nothing once the tooltip has closed and faded, so it is not left where the list may no longer be', async () => {
      const cut = measured(cell(0, 1), 620, 180);
      laidOut(cut, box(20, 100, 336, 400), box(120, 150, 180, 49));
      await pointerOnto(cut);
      await pointerOff(cut);

      // While the tooltip fades it stays where it was hung.
      expect(placed()).toEqual(['99px', '49px', '180px', '49px']);
      await new Promise((resolve) => setTimeout(resolve, 260));
      expect(placed()).toEqual(['0px', '0px', '0px', '0px']);
    });

    it('is kept where a text is offered again before the wait is over', async () => {
      const cut = measured(cell(0, 1), 620, 180);
      laidOut(cut, box(20, 100, 336, 400), box(120, 150, 180, 49));
      await pointerOnto(cut);
      await pointerOff(cut);
      await pointerOnto(cut);
      await new Promise((resolve) => setTimeout(resolve, 260));

      expect(tooltip()).toBe(notes);
      expect(placed()).toEqual(['99px', '49px', '180px', '49px']);
    });
  });

  describe('where the whole pixels call it a fit', () => {
    // jsdom has no measure of a text's own width; a browser's is put in its place.
    const ranges = Range.prototype as { getBoundingClientRect?: () => DOMRect };
    const widthOfText = (width: number): void => {
      ranges.getBoundingClientRect = (): DOMRect => {
        return { width } as DOMRect;
      };
    };
    /**
     * A cell the browser reports as 222 pixels wide, 12 of them padding on each side, and
     * its text as no wider: room for a text of 198 by the whole pixels, and of 197.6 in a
     * cell that is 221.6 wide before the browser rounds it.
     */
    const fitted = (text: HTMLElement): HTMLElement => {
      text.style.whiteSpace = 'nowrap';
      text.style.paddingLeft = '12px';
      text.style.paddingRight = '12px';
      return measured(text, 222, 222);
    };

    const own = ranges.getBoundingClientRect;

    afterEach(() => {
      if (own) {
        ranges.getBoundingClientRect = own;
      } else {
        delete ranges.getBoundingClientRect;
      }
    });

    it('a text a fraction of a pixel too wide is cut all the same, and offered', async () => {
      widthOfText(197.8);
      await pointerOnto(fitted(cell(0, 1)));
      expect(tooltip()).toBe(notes);
    });

    it('a text more than a pixel short of its box offers nothing', async () => {
      widthOfText(196.9);
      await pointerOnto(fitted(cell(0, 1)));
      expect(tooltip()).toBeNull();
    });

    it('a text that wraps is not a one-line text cut short', async () => {
      widthOfText(197.8);
      const wrapped = fitted(cell(0, 1));
      wrapped.style.whiteSpace = 'normal';
      await pointerOnto(wrapped);
      expect(tooltip()).toBeNull();
    });
  });

  it('a header cut short offers the whole header, and a header that fits offers nothing', async () => {
    const cut = measured(header(1), 300, 96);
    await pointerOnto(cut);
    expect(tooltip()).toBe(notesHeader);
    await pointerOff(cut);
    expect(tooltip()).toBeNull();

    await pointerOnto(measured(header(0), 60, 96));
    expect(tooltip()).toBeNull();
  });

  it('a header cut short offers the whole header to the keyboard: its sort button is the tab stop', async () => {
    measured(header(1), 300, 96);
    const button = header(1).closest('button');
    expect(button).not.toBeNull();
    button?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    await nextFrame();
    expect(tooltip()).toBe(notesHeader);

    button?.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    await settle();
    expect(tooltip()).toBeNull();

    // A header that fits says nothing more when its button takes the focus.
    measured(header(0), 60, 96);
    header(0)
      .closest('button')
      ?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    await nextFrame();
    expect(tooltip()).toBeNull();
  });

  it('offers what the focus arrived on once the scroll that brings it into view is over, so that scroll does not close it', async () => {
    measured(header(1), 300, 96);
    const button = header(1).closest('button');
    // The focus lands on a header out of view: the list scrolls it into view at once.
    button?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    element.querySelector('.impulse-grid-scroll')?.dispatchEvent(new Event('scroll'));
    await nextFrame();
    expect(tooltip()).toBe(notesHeader);
  });

  it('offers nothing for a focus that has left before its frame came', async () => {
    measured(header(1), 300, 96);
    const button = header(1).closest('button');
    button?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    button?.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    await nextFrame();
    expect(tooltip()).toBeNull();
  });

  it("offers an empty list's message whole where it is cut short, and nothing where it fits", async () => {
    const refusal = 'You do not have permission to see this list. The sector keeps its roster to its own marshal.';
    fixture.componentRef.setInput('rowData', []);
    fixture.componentRef.setInput('emptyMessage', refusal);
    fixture.detectChanges();
    const words = element.querySelector('tr.no-records-row td .impulse-grid-across') as HTMLElement;
    expect(words.textContent?.trim()).toBe(refusal);

    await pointerOnto(measured(words, 560, 296));
    expect(tooltip()).toBe(refusal);
    await pointerOff(words);
    expect(tooltip()).toBeNull();

    await pointerOnto(measured(words, 280, 296));
    expect(tooltip()).toBeNull();
  });

  it('offers nothing for a cell that draws its value: the check of a yes is not a text', async () => {
    // The check is an icon font's ligature, so the cell's text is the icon's name.
    await pointerOnto(measured(cell(0, 2), 60, 20));
    expect(tooltip()).toBeNull();
  });

  it('leaves a table inside one of its cells to that table', async () => {
    const inner = document.createElement('table');
    inner.innerHTML = '<tbody><tr><td>Inner text that is cut short</td></tr></tbody>';
    const holder = cell(1, 1);
    holder.appendChild(inner);
    const innerCell = inner.querySelector('td') as HTMLElement;

    await pointerOnto(measured(innerCell, 300, 96));
    expect(tooltip()).toBeNull();
  });
});

/**
 * The control a screen reader announces under the role with the name: a button or a link
 * whose accessible name is the words given. The name is the control's `aria-label`, else
 * the text it holds that is not hidden from assistive technology.
 */
const byRole = (root: HTMLElement, role: 'button' | 'link', name: string): HTMLElement | undefined => {
  const heard = (node: Element): string => {
    return Array.from(node.childNodes)
      .map((child) => {
        if (!(child instanceof Element)) {
          return child.textContent ?? '';
        }
        return child.getAttribute('aria-hidden') === 'true' ? '' : (child.getAttribute('aria-label') ?? heard(child));
      })
      .join('');
  };
  return Array.from(root.querySelectorAll<HTMLElement>(role === 'button' ? 'button' : 'a[href]')).find((control) => {
    return (control.getAttribute('aria-label') ?? heard(control)).trim() === name;
  });
};

// A row's button is an icon alone, and Material hides an icon from assistive technology,
// so the button's label is its accessible name: a screen reader announces "View, link"
// and "Delete, button", not an unnamed control. The label is the tooltip too.
describe("a row's buttons", () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let element: HTMLElement;
  let deleted: unknown[];

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    deleted = [];
    fixture.componentRef.setInput('rowData', [
      { id: 'm-1', name: 'Anvil run', opensAt: '/sectors/anvil/missions/m-1' },
    ]);
    fixture.componentRef.setInput('columnDefs', [
      { id: 'name' },
      {
        id: 'view',
        hideHeader: true,
        buttonConfig: { label: 'View', icon: 'arrow_forward', viewRoute: 'missions', actionType: 'link' },
      },
      {
        id: 'open',
        hideHeader: true,
        buttonConfig: { label: 'Open in its sector', icon: 'arrow_forward', linkField: 'opensAt', actionType: 'link' },
      },
      {
        id: 'delete',
        hideHeader: true,
        buttonConfig: {
          label: 'Delete',
          icon: 'delete',
          actionType: 'function',
          action: (row: unknown): void => {
            deleted.push(row);
          },
        },
      },
    ]);
    fixture.detectChanges();
    element = fixture.nativeElement as HTMLElement;
  });

  it('a link to the row by a route is found by its label', () => {
    expect(byRole(element, 'link', 'View')?.getAttribute('href')).toBe('/missions/m-1');
  });

  it('a link the row carries is found by its label', () => {
    expect(byRole(element, 'link', 'Open in its sector')?.getAttribute('href')).toBe('/sectors/anvil/missions/m-1');
  });

  it('a button that runs a function is found by its label, and runs it with the row', () => {
    const button = byRole(element, 'button', 'Delete');
    expect(button).toBeDefined();
    button?.click();
    expect(deleted).toEqual([{ id: 'm-1', name: 'Anvil run', opensAt: '/sectors/anvil/missions/m-1' }]);
  });
});

// A list drawn a line each, a link that opens nowhere, and an entry of a row's menu that
// only says something: what a page of groups and of a person's roles asks of the table.
describe('what a row says of a list, a missing key and a method it does not offer', () => {
  let fixture: ComponentFixture<AppGridComponent>;
  let element: HTMLElement;

  const draw = async (rows: RecordData[], columns: unknown[]): Promise<void> => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', rows);
    fixture.componentRef.setInput('columnDefs', columns);
    fixture.detectChanges();
    element = fixture.nativeElement as HTMLElement;
  };
  const cellsOf = (row: number): HTMLElement[] =>
    Array.from(
      element.querySelectorAll<HTMLElement>('tr.impulse-row')[row].querySelectorAll<HTMLElement>('td.data-col'),
    );

  it('draws a list a line each where its column says so, and one line of text otherwise', async () => {
    await draw(
      [
        { name: 'Night shift', roles: ['Reader', 'Warden in every sector'], tags: ['a', 'b'] },
        { name: 'Empty', roles: [], tags: [] },
      ],
      [{ id: 'name' }, { id: 'roles', lines: true }, { id: 'tags' }],
    );
    const lines = Array.from(cellsOf(0)[1].querySelectorAll<HTMLElement>('.cell-line')).map((line) =>
      line.textContent?.trim(),
    );
    expect(lines).toEqual(['Reader', 'Warden in every sector']);
    // A column that does not ask for lines draws its value as one text.
    expect(cellsOf(0)[2].querySelector('.cell-line')).toBeNull();
    // An empty list draws no line.
    expect(cellsOf(1)[1].querySelector('.cell-line')).toBeNull();
    expect(cellsOf(1)[1].textContent?.trim()).toBe('');
  });

  it('draws no link to a row whose key field is empty: the row opens nowhere', async () => {
    await draw(
      [
        { role: 'Reader', groupId: 'g-1' },
        { role: 'Warden', groupId: '' },
        { role: 'Scribe', groupId: null },
      ],
      [
        { id: 'role' },
        {
          id: 'view',
          hideHeader: true,
          buttonConfig: {
            label: 'View',
            icon: 'arrow_forward',
            viewRoute: 'groups',
            keyField: 'groupId',
            actionType: 'link',
          },
        },
      ],
    );
    const links = [0, 1, 2].map((row) => cellsOf(row)[1].querySelector('a')?.getAttribute('href') ?? null);
    expect(links).toEqual(['/groups/g-1', null, null]);
    // The key the column opens a row by is nothing for such a row, and for one that
    // carries no such field: never an empty text, which is a key to whoever reads it.
    const view = fixture.componentInstance.columnDefs()[1];
    const keys = [...fixture.componentInstance.rowData(), { role: 'Clerk' } as RecordData].map((row) =>
      fixture.componentInstance.linkKeyOf(row, view),
    );
    expect(keys).toEqual(['g-1', undefined, undefined, undefined]);
  });

  it("draws an entry that only says something grayed out in the row's menu, and runs nothing from it", async () => {
    const ran: string[] = [];
    await draw(
      [
        {
          name: 'Night shift',
          'actions:menu': [
            { id: 'Describe', label: 'Change the description', run: (): void => void ran.push('describe') },
            {
              id: 'Delete',
              label: '3 people and 2 roles: empty it first',
              run: (): void => void ran.push('delete'),
              disabled: true,
            },
          ],
        } as unknown as RecordData,
      ],
      [
        { id: 'name' },
        {
          id: 'actions:menu',
          hideHeader: true,
          buttonConfig: { label: 'Actions', icon: 'more_vert', actionType: 'menu' },
        },
      ],
    );
    byRole(element, 'button', 'Actions')?.click();
    fixture.detectChanges();
    await fixture.whenStable();
    const entries = Array.from(document.querySelectorAll<HTMLButtonElement>('button.row-action'));
    expect(entries.map((entry) => [entry.textContent?.trim(), entry.disabled])).toEqual([
      ['Change the description', false],
      ['3 people and 2 roles: empty it first', true],
    ]);
    expect(entries[1].classList.contains('row-action-says')).toBe(true);
    entries[1].click();
    entries[0].click();
    expect(ran).toEqual(['describe']);
  });
});

// A column keeps a readable width: the table is never narrower than its columns at the
// narrowest each is drawn, so a frame too narrow for them scrolls sideways, the frame and
// never the page, and the first column is then held at the left edge with the select and
// expand columns, while the widest of the other columns can be seen whole beside it; no
// second column is ever held. A list whose columns fit is drawn as it always was. jsdom lays nothing
// out and has no ResizeObserver, so each case says how wide the frame is, as the browser
// would report it.
describe('a list and the width of its frame', () => {
  const text = MIN_TEXT_COLUMN_WIDTH;
  const rows: RecordData[] = [
    { name: 'Commendations', description: 'Cites a pilot for a sortie flown well.', enabled: true, changedBy: 'ada' },
    { name: 'Debriefs', description: 'Files a debrief after each sortie.', enabled: false, changedBy: 'mo' },
    { name: 'Salvage', description: 'Lists what the salvage hold took in.', enabled: true, changedBy: 'ada' },
  ];
  // Three columns of text, one that declares its width, and a button column.
  const columns = [
    { id: 'name' },
    { id: 'description' },
    { id: 'enabled', width: 64 },
    { id: 'changedBy' },
    {
      id: 'history',
      hideHeader: true,
      buttonConfig: { label: 'History', icon: 'history', actionType: 'function', action: (): void => undefined },
    },
  ] as unknown as ColumnConfig[];
  const columnsAtTheirNarrowest = text + text + 64 + text + 66;
  // What a wide window leaves the list, and a phone held upright: one 390 wide, and one 360.
  const wide = 1226;
  const phone = 336;
  const smallPhone = 306;
  // The least frame in which the list with a select and an expand column holds its first
  // column: those two, the first column, and the widest of the others whole beside them.
  const holdsWithSelectAndExpand = 48 + 40 + text + text;

  /** A list with a select column and rows that expand, as a page that offers both hands them to the grid. */
  @Component({
    standalone: true,
    imports: [AppGridComponent],
    template: `
      <impulse-grid
        [rowData]="rows"
        [columnDefs]="columns"
        [rowKey]="key"
        selectionType="multiple"
        [enableRowExpansion]="true"
        [detailTemplate]="detail" />
      <ng-template #detail let-row>
        <p class="detail-words">More about {{ row.name }}</p>
      </ng-template>
    `,
  })
  class SelectAndExpandComponent {
    rows = rows;
    columns = columns;
    key = (row: RecordData): unknown => {
      return row['name'];
    };
  }

  /** The frames a ResizeObserver was asked to watch, each with the call that reports a new size. */
  let watched: { frame: Element; report: () => void }[];
  let element: HTMLElement;
  let detect: () => void;
  /** Hands the plain list an input, as its page would. */
  let give: (name: string, value: unknown) => void;
  /** Takes the list off the page. */
  let leave: () => void;
  const ground = document.body.style.backgroundColor;

  class FrameObserver {
    constructor(private readonly report: () => void) {}

    observe(frame: Element): void {
      watched.push({ frame, report: this.report });
    }

    unobserve(): void {
      // Nothing to let go of: the specs end by forgetting every frame.
    }

    disconnect(): void {
      watched = watched.filter((entry) => entry.report !== this.report);
    }
  }

  beforeEach(async () => {
    watched = [];
    (globalThis as { ResizeObserver?: unknown }).ResizeObserver = FrameObserver;
    await TestBed.configureTestingModule({
      imports: [AppGridComponent, SelectAndExpandComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
  });

  afterEach(() => {
    delete (globalThis as { ResizeObserver?: unknown }).ResizeObserver;
    document.body.style.backgroundColor = ground;
    document.documentElement.removeAttribute('data-palette');
    document.body.removeAttribute('data-look');
    vi.restoreAllMocks();
  });

  const settle = async (fixture: ComponentFixture<unknown>): Promise<void> => {
    fixture.detectChanges();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    fixture.detectChanges();
  };
  const plain = async (data: RecordData[] = rows): Promise<AppGridComponent> => {
    const fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', data);
    fixture.componentRef.setInput('columnDefs', columns);
    element = fixture.nativeElement as HTMLElement;
    detect = (): void => fixture.detectChanges();
    give = (name, value): void => fixture.componentRef.setInput(name, value);
    leave = (): void => fixture.destroy();
    await settle(fixture);
    return fixture.componentInstance;
  };
  /**
   * What the list's stylesheet, as the page is given it, declares in the rules whose
   * selector names every one of `names` and none of `without`. For what jsdom cannot
   * answer as a computed style: a value read from a variable, a mixed color, a property
   * it does not know.
   */
  const declares = (names: string[], without: string[] = []): string => {
    const sheet = Array.from(document.querySelectorAll('style'))
      .map((style) => style.textContent ?? '')
      .join('\n');
    return Array.from(sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g))
      .filter(([, selector]) => {
        return names.every((name) => selector.includes(name)) && without.every((name) => !selector.includes(name));
      })
      .map(([, , declarations]) => declarations)
      .join('\n');
  };
  const withSelectAndExpand = async (): Promise<void> => {
    const fixture = TestBed.createComponent(SelectAndExpandComponent);
    element = fixture.nativeElement as HTMLElement;
    detect = (): void => fixture.detectChanges();
    await settle(fixture);
  };
  const frame = (): HTMLElement => {
    return element.querySelector('.impulse-grid-scroll') as HTMLElement;
  };
  const table = (): HTMLElement => {
    return element.querySelector('table.impulse-grid-table') as HTMLElement;
  };
  /** The browser lays the frame out this wide and says so. */
  const frameIs = (width: number): void => {
    Object.defineProperty(frame(), 'clientWidth', { configurable: true, value: width });
    const reports = watched.filter((entry) => entry.frame === frame());
    expect(reports.length).toBeGreaterThan(0);
    reports.forEach((entry) => entry.report());
    detect();
  };
  const headers = (): HTMLElement[] => {
    return Array.from(element.querySelectorAll<HTMLElement>('thead th'));
  };
  const cells = (row: number): HTMLElement[] => {
    return Array.from(element.querySelectorAll<HTMLElement>('tr.impulse-row')[row].querySelectorAll('td'));
  };
  const held = (): boolean => {
    return frame().classList.contains('holds-first-column');
  };
  /** A person drags the column's edge from where it is to the right, by so many pixels. */
  const drag = (column: number, by: number): void => {
    const handle = element.querySelectorAll<HTMLElement>('thead th.data-col')[column].querySelector('.resize-handle');
    handle?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, clientX: 400 }));
    document.dispatchEvent(new MouseEvent('mousemove', { clientX: 400 + by }));
    document.dispatchEvent(new MouseEvent('mouseup'));
    detect();
  };

  describe('the narrowest a table is drawn', () => {
    it("is every column at its narrowest: a column of text at the library's width, a declared width as declared, a button column its own", async () => {
      await plain();
      expect(text).toBe(160);
      expect(table().style.minWidth).toBe(`${columnsAtTheirNarrowest}px`);
    });

    it('counts the select and expand columns, which keep their widths', async () => {
      await withSelectAndExpand();
      expect(table().style.minWidth).toBe(`${48 + 40 + columnsAtTheirNarrowest}px`);
      expect(headers().map((th) => th.style.width)).toEqual(['48px', '40px', '', '', '64px', '', '66px']);
    });

    it("follows a column a person dragged: the width they gave it is kept, over the library's and through a new page of rows", async () => {
      const grid = await plain();
      // jsdom measures the header as nothing wide, so the drag starts from nothing.
      drag(1, 220);
      expect(grid.widthFor(columns[1])).toBe(220);
      expect(headers()[1].style.width).toBe('220px');
      expect(table().style.minWidth).toBe(`${columnsAtTheirNarrowest - text + 220}px`);

      // Narrower than the library's width is the person's to choose too, down to the least a column goes.
      drag(1, -500);
      expect(headers()[1].style.width).toBe('48px');
      expect(table().style.minWidth).toBe(`${columnsAtTheirNarrowest - text + 48}px`);

      drag(1, 172);
      give('rowData', [rows[2]]);
      detect();
      frameIs(phone);
      frameIs(wide);
      expect(headers()[1].style.width).toBe('220px');
      expect(cells(0)[1].style.width).toBe('220px');
    });
  });

  describe('a list whose columns fit', () => {
    it('is drawn as it was: the table fills the frame, no column is given a width the page did not give it, nothing is held', async () => {
      await plain();
      const before = headers().map((th) => th.style.width);
      frameIs(wide);

      expect(headers().map((th) => th.style.width)).toEqual(before);
      expect(before).toEqual(['', '', '64px', '', '66px']);
      expect(held()).toBe(false);
      expect(getComputedStyle(table()).width).toBe('100%');
      expect(getComputedStyle(table()).borderCollapse).toBe('collapse');
      expect(getComputedStyle(cells(0)[0]).position).not.toBe('sticky');
    });

    it('fits to the pixel: a frame exactly as wide as the columns at their narrowest holds nothing, and one pixel less does', async () => {
      await plain();
      frameIs(columnsAtTheirNarrowest);
      expect(held()).toBe(false);
      frameIs(columnsAtTheirNarrowest - 1);
      expect(held()).toBe(true);
      frameIs(columnsAtTheirNarrowest);
      expect(held()).toBe(false);
    });

    it('holds nothing in a frame that is not laid out, which measures as nothing wide', async () => {
      await plain([]);
      frameIs(phone);
      expect(held()).toBe(true);
      // The list is inside something hidden: the browser reports no width at all.
      frameIs(0);
      expect(held()).toBe(false);
      expect((element.querySelector('.impulse-grid-across') as HTMLElement).style.width).toBe('');
    });

    it('holds nothing where nothing is measured, as in a spec of an application', async () => {
      delete (globalThis as { ResizeObserver?: unknown }).ResizeObserver;
      await plain();
      expect(watched).toEqual([]);
      expect(held()).toBe(false);
      expect(table().style.minWidth).toBe(`${columnsAtTheirNarrowest}px`);
    });
  });

  describe('a list drawn with its rows from the start, as a list in a dialog is', () => {
    it('is drawn for the width of its frame at its first drawing, before the browser reports any size', async () => {
      // The browser lays the frame out as wide as a phone leaves it, and reports a frame's
      // size only once it has drawn it: what is drawn before that report is what a person
      // sees for a frame.
      const laid = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
      Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
        configurable: true,
        get(this: HTMLElement): number {
          return this.classList.contains('impulse-grid-scroll') ? phone : 0;
        },
      });
      try {
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', rows);
        fixture.componentRef.setInput('columnDefs', columns);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        fixture.detectChanges();

        // Its first drawing: the frame is too narrow for the columns, so the first is held
        // and each column that would cut a value is at its narrowest.
        const first = headers().map((th) => th.style.width);
        expect(held()).toBe(true);
        expect(first).toEqual([`${text}px`, `${text}px`, '64px', `${text}px`, '66px']);

        // The browser's report of the size it drew changes nothing.
        frameIs(phone);
        expect(headers().map((th) => th.style.width)).toEqual(first);
        expect(held()).toBe(true);
      } finally {
        if (laid) {
          Object.defineProperty(HTMLElement.prototype, 'clientWidth', laid);
        } else {
          delete (HTMLElement.prototype as { clientWidth?: number }).clientWidth;
        }
      }
    });
  });

  describe('a frame narrower than the columns', () => {
    it('is what scrolls sideways: the table keeps its width inside the frame, which scrolls whatever does not fit', async () => {
      await plain();
      frameIs(phone);

      // The table is the frame's own child, wider than it, and the frame scrolls its overflow.
      expect(table().parentElement).toBe(frame());
      expect(parseFloat(table().style.minWidth)).toBeGreaterThan(phone);
      expect(getComputedStyle(frame()).overflow).toBe('auto');
      expect(declares(['.impulse-grid-host'])).toContain('overflow: clip');
    });

    it('holds the first column at the left edge, in the header and in every row, and no other', async () => {
      await plain();
      frameIs(phone);

      expect(held()).toBe(true);
      expect(frame().style.getPropertyValue('--impulse-grid-first-left')).toBe('0px');
      expect(headers().map((th) => th.classList.contains('first-col'))).toEqual([true, false, false, false, false]);
      for (const row of [0, 1, 2]) {
        expect(cells(row).map((td) => td.classList.contains('first-col'))).toEqual([true, false, false, false, false]);
      }
      expect(getComputedStyle(headers()[0]).position).toBe('sticky');
      expect(getComputedStyle(cells(0)[0]).position).toBe('sticky');
      expect(getComputedStyle(cells(0)[1]).position).not.toBe('sticky');
      // The header's held cell lies over the header, which lies over the held cells of the rows.
      const layer = (cell: HTMLElement): number => Number(getComputedStyle(cell).zIndex);
      expect(layer(headers()[0])).toBeGreaterThan(layer(headers()[1]));
      expect(layer(headers()[1])).toBeGreaterThan(layer(cells(0)[0]));
      expect(layer(cells(0)[0])).toBeGreaterThan(3);
      // Each cell carries its own lines while a column is held, so the held column keeps its edge.
      expect(getComputedStyle(table()).borderCollapse).toBe('separate');
      // Where it stays is the length the frame carries.
      expect(declares(['holds-first-column', '.first-col'])).toMatch(/left:\s*var\(--impulse-grid-first-left/);
    });

    it('draws every held cell whole, on the color the list sits on, with the tints the rest of its row is drawn in', async () => {
      await plain();
      for (const column of ['.select-col', '.expand-col', '.first-col']) {
        const cell = ['holds-first-column', 'impulse-row', column];
        expect(declares(cell, [':hover', '.selected', 'nth-child'])).toMatch(
          /background-color:\s*var\(--impulse-grid-ground,\s*var\(--mat-sys-surface,/,
        );
        expect(declares([...cell, ':nth-child(even)'])).toMatch(
          /background-color:\s*var\(--mat-sys-surface-container-low,/,
        );
        expect(declares([...cell, ':hover'])).toMatch(
          /background-color:\s*color-mix\(\s*in srgb,\s*var\(--mat-sys-on-surface,[^)]*\)\s*4%,\s*var\(--impulse-grid-ground,/,
        );
        expect(declares([...cell, '.selected'])).toMatch(
          /background-color:\s*color-mix\(\s*in srgb,\s*var\(--mat-sys-primary,[^)]*\)\s*8%,\s*var\(--impulse-grid-ground,/,
        );
      }
    });

    it('stacks what is in the frame among itself, never over the page around the list', async () => {
      await plain();
      expect(declares(['.impulse-grid-scroll'], ['holds-first-column', 'ng-deep'])).toMatch(/isolation:\s*isolate/);
    });

    it('holds the select and expand columns with it, each after the one before', async () => {
      await withSelectAndExpand();
      frameIs(holdsWithSelectAndExpand);

      expect(held()).toBe(true);
      expect(frame().style.getPropertyValue('--impulse-grid-expand-left')).toBe('48px');
      expect(frame().style.getPropertyValue('--impulse-grid-first-left')).toBe('88px');
      const classes = (cell: HTMLElement): string => {
        return ['select-col', 'expand-col', 'first-col'].filter((name) => cell.classList.contains(name)).join();
      };
      expect(headers().map(classes)).toEqual(['select-col', 'expand-col', 'first-col', '', '', '', '']);
      expect(cells(1).map(classes)).toEqual(['select-col', 'expand-col', 'first-col', '', '', '', '']);
      for (const cell of cells(1).slice(0, 3)) {
        expect(getComputedStyle(cell).position).toBe('sticky');
      }
      expect(getComputedStyle(cells(1)[3]).position).not.toBe('sticky');
      expect(declares(['holds-first-column', '.select-col'], ['.expand-col'])).toMatch(/left:\s*0/);
      expect(declares(['holds-first-column', '.expand-col'], ['.select-col'])).toMatch(
        /left:\s*var\(--impulse-grid-expand-left/,
      );
    });

    it('brings what the keyboard lands on into view beside the held columns, not under them', async () => {
      await plain();
      frameIs(phone);
      expect(held()).toBe(true);
      expect(frame().style.getPropertyValue('--impulse-grid-held-width')).toBe(`${text}px`);

      await withSelectAndExpand();
      frameIs(holdsWithSelectAndExpand);
      expect(held()).toBe(true);
      expect(frame().style.getPropertyValue('--impulse-grid-held-width')).toBe(`${48 + 40 + text}px`);
      expect(declares(['.holds-first-column'], ['impulse-grid-table'])).toMatch(
        /scroll-padding-left:\s*var\(--impulse-grid-held-width/,
      );
    });

    it('brings a control the keyboard lands on out from under the held column, and moves nothing for the pointer', async () => {
      await plain();
      frameIs(phone);
      // The frame starts 20 from the window's edge and is scrolled 300; the second header's
      // button lies 60 into the frame, under the held column, which is 160 wide.
      const at = (target: Element, left: number): void => {
        (target as HTMLElement).getBoundingClientRect = (): DOMRect => {
          return { left } as DOMRect;
        };
      };
      at(frame(), 20);
      frame().scrollLeft = 300;
      const second = headers()[1].querySelector('button') as HTMLButtonElement;
      at(second, 80);

      second.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      expect(frame().scrollLeft).toBe(200);

      // Beside the held column already: nothing moves.
      at(second, 190);
      second.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      expect(frame().scrollLeft).toBe(200);

      // A control of the held column itself is under nothing.
      const first = headers()[0].querySelector('button') as HTMLButtonElement;
      at(first, 28);
      first.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      expect(frame().scrollLeft).toBe(200);

      // A press gives the focus too, and the list stays under the pointer until the click lands.
      at(second, 80);
      second.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
      second.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      expect(frame().scrollLeft).toBe(200);
      await new Promise((resolve) => setTimeout(resolve, 0));
      second.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      expect(frame().scrollLeft).toBe(100);
    });

    it('lets the first column go where its widest other column could not be seen whole beside it, to the pixel: nothing is held then, and every column scrolls', async () => {
      await plain();
      // The first column and the widest of the others are 160 each: 320 of the 336 a list
      // has on a phone 390 wide, and more than the 306 it has on one 360 wide.
      frameIs(phone);
      expect(held()).toBe(true);
      frameIs(smallPhone);
      expect(held()).toBe(false);
      expect(parseFloat(table().style.minWidth)).toBeGreaterThan(smallPhone);
      // The title row stays at the top of the frame as ever; nothing stays at its left.
      expect(getComputedStyle(cells(0)[0]).position).not.toBe('sticky');
      expect(getComputedStyle(table()).borderCollapse).toBe('collapse');
      frameIs(2 * text);
      expect(held()).toBe(true);
      frameIs(2 * text - 1);
      expect(held()).toBe(false);

      // Dragged to 176 on the phone, the held column leaves the 160 the widest other needs.
      frameIs(phone);
      drag(0, 176);
      expect(held()).toBe(true);
      expect(frame().style.getPropertyValue('--impulse-grid-held-width')).toBe('176px');
      drag(0, 1);
      expect(held()).toBe(false);
      expect(parseFloat(table().style.minWidth)).toBeGreaterThan(phone);
    });

    it('counts the select and expand columns in what is held: the list lets all three go a pixel under the room they and the widest other column need', async () => {
      await withSelectAndExpand();
      frameIs(holdsWithSelectAndExpand - 1);

      expect(held()).toBe(false);
      for (const cell of cells(1).slice(0, 3)) {
        expect(getComputedStyle(cell).position).not.toBe('sticky');
      }
      // On a phone the two columns and a first column at the library's width leave 88.
      frameIs(phone);
      expect(held()).toBe(false);
    });

    it('goes by the widest column that scrolls as it is drawn: one a person dragged wider or narrower, and a column of buttons with the rest', async () => {
      await plain();
      // The second column dragged to 170: the first, at 160, needs 330 to hold.
      drag(1, 170);
      frameIs(330);
      expect(held()).toBe(true);
      frameIs(329);
      expect(held()).toBe(false);

      // Dragged down to 100, it is no longer the widest: the fourth is, at 160.
      drag(1, -70);
      expect(headers()[1].style.width).toBe('100px');
      frameIs(2 * text - 1);
      expect(held()).toBe(false);
      frameIs(2 * text);
      expect(held()).toBe(true);

      // A first column wider than every other is not counted twice: 200 and 160.
      drag(0, 200);
      frameIs(200 + text);
      expect(held()).toBe(true);
      frameIs(200 + text - 1);
      expect(held()).toBe(false);

      // Of a first column, a column 64 wide and a column of buttons, the buttons' 66 is
      // the widest that scrolls.
      give('columnDefs', [columns[0], columns[2], columns[4]]);
      detect();
      frameIs(200 + 66);
      expect(held()).toBe(true);
      frameIs(200 + 66 - 1);
      expect(held()).toBe(false);
    });

    it('holds nothing in a list of one column or of two: what scrolls would not be whole beside the first', async () => {
      await plain();
      give('columnDefs', [columns[0]]);
      detect();
      frameIs(text - 1);
      expect(parseFloat(table().style.minWidth)).toBeGreaterThan(text - 1);
      expect(held()).toBe(false);

      // Two columns scroll only in a frame narrower than both, where the second is not whole beside the first.
      give('columnDefs', [columns[0], columns[1]]);
      detect();
      frameIs(2 * text - 1);
      expect(held()).toBe(false);
      frameIs(phone);
      expect(held()).toBe(false);
    });

    it('draws a held cell on the color the list sits on, read off the page: a part-clear color nearer by is passed over', async () => {
      document.body.style.backgroundColor = 'rgb(242, 243, 252)';
      await plain();
      element.style.backgroundColor = 'rgba(0, 0, 0, 0.04)';
      frameIs(phone);
      expect(frame().style.getPropertyValue('--impulse-grid-ground')).toBe('rgb(242, 243, 252)');
    });

    it('passes over a color that lets what is under it show, in each form a browser reports one', async () => {
      await plain();
      const reported = new Map<Element, string>([[document.body, 'rgb(1, 2, 3)']]);
      const view = document.defaultView as Window & typeof globalThis;
      const real = view.getComputedStyle.bind(view);
      vi.spyOn(view, 'getComputedStyle').mockImplementation((asked: Element, pseudo?: string | null) => {
        const color = reported.get(asked);
        return color === undefined ? real(asked, pseudo) : ({ backgroundColor: color } as CSSStyleDeclaration);
      });
      const cases: [string, boolean][] = [
        ['rgba(0, 0, 0, 0)', false],
        ['transparent', false],
        ['', false],
        ['rgba(10, 20, 30, 0.5)', false],
        ['color(srgb 0.1 0.1 0.1 / 0.04)', false],
        ['rgb(10 20 30 / 50%)', false],
        ['oklch(0.5 0.1 200 / 0.3)', false],
        ['rgb(10, 20, 30)', true],
        ['rgba(10, 20, 30, 1)', true],
        ['color(srgb 0.9 0.9 0.9)', true],
        ['color(srgb 0.9 0.9 0.9 / 1)', true],
        ['rgb(10 20 30 / 100%)', true],
      ];
      for (const [color, covers] of cases) {
        reported.set(element, color);
        frameIs(phone);
        expect([color, frame().style.getPropertyValue('--impulse-grid-ground')]).toEqual([
          color,
          covers ? color : 'rgb(1, 2, 3)',
        ]);
      }
    });

    it("reads the color again when the page's root or its body is given another attribute, so a held cell follows a change from light to dark", async () => {
      document.body.style.backgroundColor = 'rgb(242, 243, 252)';
      await plain();
      frameIs(phone);
      expect(frame().style.getPropertyValue('--impulse-grid-ground')).toBe('rgb(242, 243, 252)');

      // The theme service sets the look on the page's root, and what the list sits on follows
      // it. (The color is changed here on the list's own element, which nothing watches, so
      // the root's attribute alone is what says to read again.)
      element.style.backgroundColor = 'rgb(25, 28, 34)';
      document.documentElement.setAttribute('data-palette', 'night');
      await new Promise((resolve) => setTimeout(resolve, 0));
      detect();
      expect(frame().style.getPropertyValue('--impulse-grid-ground')).toBe('rgb(25, 28, 34)');

      // An application that sets its look on the body is followed too.
      element.style.backgroundColor = 'rgb(9, 9, 9)';
      document.body.setAttribute('data-look', 'dusk');
      await new Promise((resolve) => setTimeout(resolve, 0));
      detect();
      expect(frame().style.getPropertyValue('--impulse-grid-ground')).toBe('rgb(9, 9, 9)');
    });

    it('stops watching the frame and the page when the list leaves it', async () => {
      document.body.style.backgroundColor = 'rgb(242, 243, 252)';
      const grid = await plain();
      frameIs(phone);
      expect(watched).toHaveLength(1);
      expect(grid.ground()).toBe('rgb(242, 243, 252)');

      leave();
      expect(watched).toEqual([]);
      document.body.style.backgroundColor = 'rgb(25, 28, 34)';
      document.documentElement.setAttribute('data-palette', 'night');
      document.body.setAttribute('data-look', 'dusk');
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(grid.ground()).toBe('rgb(242, 243, 252)');
    });

    it('reads nothing while the list is scrolled down and not sideways', async () => {
      document.body.style.backgroundColor = 'rgb(242, 243, 252)';
      await plain();
      frameIs(phone);
      element.style.backgroundColor = 'rgb(17, 19, 25)';
      Object.defineProperty(frame(), 'scrollTop', { configurable: true, value: 240 });
      frame().dispatchEvent(new Event('scroll'));
      detect();
      expect(frame().style.getPropertyValue('--impulse-grid-ground')).toBe('rgb(242, 243, 252)');
    });

    it('reads the color again as the list is scrolled sideways', async () => {
      document.body.style.backgroundColor = 'rgb(242, 243, 252)';
      await plain();
      frameIs(phone);
      // Changed with nothing said to the page's root: the next scroll sideways finds it.
      element.style.backgroundColor = 'rgb(17, 19, 25)';
      Object.defineProperty(frame(), 'scrollLeft', { configurable: true, value: 120 });
      frame().dispatchEvent(new Event('scroll'));
      detect();
      expect(frame().style.getPropertyValue('--impulse-grid-ground')).toBe('rgb(17, 19, 25)');
    });
  });

  describe('a row that is one cell across the table', () => {
    it("says an empty list's message as wide as the frame, where a person sees it, while the table is wider", async () => {
      await plain([]);
      const words = (): HTMLElement => {
        return element.querySelector('tr.no-records-row td .impulse-grid-across') as HTMLElement;
      };
      expect(words().textContent?.trim()).toBe('No records found');
      expect(words().style.width).toBe('');

      frameIs(phone);
      expect(words().style.width).toBe(`${phone}px`);
      expect(getComputedStyle(words()).position).toBe('sticky');
      expect(parseFloat(getComputedStyle(words()).left)).toBe(0);
      // A cell that clips would be what its words stay in place against.
      expect(getComputedStyle(words().parentElement as HTMLElement).overflow).toBe('visible');

      frameIs(wide);
      expect(words().style.width).toBe('');
    });

    it("draws an expanded row's detail as wide as the frame while the table is wider", async () => {
      await withSelectAndExpand();
      element.querySelector<HTMLButtonElement>('tr.impulse-row button.expand-toggle')?.click();
      detect();
      const detail = (): HTMLElement => {
        return element.querySelector('tr.detail-row td .impulse-grid-across') as HTMLElement;
      };
      expect(detail().querySelector('.detail-words')?.textContent).toBe('More about Commendations');
      expect(detail().style.width).toBe('');

      frameIs(phone);
      expect(detail().style.width).toBe(`${phone}px`);
      expect(getComputedStyle(detail()).position).toBe('sticky');
      expect(getComputedStyle(detail().parentElement as HTMLElement).overflow).toBe('visible');
    });
  });

  describe('the loading overlay', () => {
    it('is laid over the frame, not inside what scrolls, so it covers what is in view however far the list is scrolled', async () => {
      await plain();
      give('loading', true);
      detect();
      const overlay = element.querySelector('.impulse-grid-loading-overlay') as HTMLElement;
      const view = element.querySelector('.impulse-grid-view') as HTMLElement;

      expect(overlay.parentElement).toBe(view);
      expect(frame().parentElement).toBe(view);
      expect(frame().contains(overlay)).toBe(false);
      expect(getComputedStyle(view).position).toBe('relative');
      expect(getComputedStyle(overlay).position).toBe('absolute');
      expect(Number(getComputedStyle(overlay).zIndex)).toBeGreaterThan(0);
      expect(declares(['.impulse-grid-view'])).toMatch(/isolation:\s*isolate/);
    });
  });

  describe('a list inside an expanded row', () => {
    /** A list whose rows expand to another list, as a row's own page drawn in place draws the lists under the row. */
    @Component({
      standalone: true,
      imports: [AppGridComponent],
      template: `
        <impulse-grid
          class="outer"
          [rowData]="rows"
          [columnDefs]="columns"
          [rowKey]="key"
          [enableRowExpansion]="true"
          [detailTemplate]="detail" />
        <ng-template #detail let-row>
          <button type="button" class="detail-button">Rename {{ row.name }}</button>
          <impulse-grid class="inner" [rowData]="[row]" [columnDefs]="inside" />
        </ng-template>
      `,
    })
    class ListInARowComponent {
      rows = rows;
      columns = columns;
      inside = [{ id: 'name' }, { id: 'changedBy' }] as unknown as ColumnConfig[];
      key = (row: RecordData): unknown => {
        return row['name'];
      };
    }

    const frames = (): HTMLElement[] => {
      return Array.from(element.querySelectorAll<HTMLElement>('.impulse-grid-scroll'));
    };
    // The outer list's expand column, its first column and the widest of its others, whole.
    const outerHolds = 40 + text + text;
    /**
     * The browser lays the two frames out: the outer one too narrow for its columns and just
     * wide enough to hold its first, the inner one wide enough for its two columns.
     */
    const laidOut = (): void => {
      const [outer, inner] = frames();
      Object.defineProperty(outer, 'clientWidth', { configurable: true, value: outerHolds });
      Object.defineProperty(inner, 'clientWidth', { configurable: true, value: 2 * text + 4 });
      watched.forEach((entry) => entry.report());
      detect();
    };

    beforeEach(async () => {
      TestBed.resetTestingModule();
      await TestBed.configureTestingModule({
        imports: [ListInARowComponent],
        providers: [provideResourceTesting()],
      }).compileComponents();
      const fixture = TestBed.createComponent(ListInARowComponent);
      element = fixture.nativeElement as HTMLElement;
      detect = (): void => fixture.detectChanges();
      await settle(fixture);
      element.querySelector<HTMLButtonElement>('tr.impulse-row button.expand-toggle')?.click();
      await settle(fixture);
      laidOut();
    });

    it('is held or not by its own frame: the list around it holding its first column changes nothing in it', () => {
      const [outer, inner] = frames();
      expect(outer.classList.contains('holds-first-column')).toBe(true);
      expect(inner.classList.contains('holds-first-column')).toBe(false);

      const innerTable = inner.querySelector('table') as HTMLElement;
      const innerFirst = inner.querySelector('tr.impulse-row td.first-col') as HTMLElement;
      expect(getComputedStyle(innerFirst).position).not.toBe('sticky');
      expect(getComputedStyle(innerTable).borderCollapse).toBe('collapse');
      // Its cells keep their own padding and clipping: the expanded row's cell gives up its own, and no other's.
      expect(getComputedStyle(innerFirst).paddingLeft).toBe('12px');
      expect(getComputedStyle(innerFirst).overflow).toBe('hidden');
    });

    it('moves nothing for a control of the inner list, or of the detail, that takes the focus: neither is under the held column', () => {
      const [outer, inner] = frames();
      const at = (target: Element, left: number): void => {
        (target as HTMLElement).getBoundingClientRect = (): DOMRect => {
          return { left } as DOMRect;
        };
      };
      at(outer, 20);
      outer.scrollLeft = 300;
      // The inner list's second header: its first is in a column that could itself be held.
      const innerSecond = inner.querySelectorAll('thead th')[1].querySelector('button');
      for (const control of [innerSecond, element.querySelector('.detail-button')]) {
        expect(control).not.toBeNull();
        at(control as Element, 60);
        control?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
        expect(outer.scrollLeft).toBe(300);
      }
    });
  });

  // A column is no wider than what it holds needs. The narrowest a column of text goes
  // is the widest of its heading, with the marks beside it, and the values the list is
  // showing, with the cell's padding and line and two pixels to spare, and never more
  // than the library's one width; it is read off what is drawn. A need only grows while
  // the list keeps its columns. A dragged width and a declared one are kept as they are.
  // A heading a person can sort by keeps the room of the sort's arrow whether or not the
  // arrow is drawn, so its column does not grow when they first sort by it.
  // jsdom draws nothing, so each case says how wide a text is drawn (eight pixels a
  // character), how wide an icon, and what a cell's padding and lines are, as the list's
  // stylesheet gives them to a browser: 25 around a cell's content, 13 around a heading's.
  describe('what a column needs', () => {
    const ranges = Range.prototype as { getBoundingClientRect?: () => DOMRect };
    const own = ranges.getBoundingClientRect;
    const character = 8;
    const aroundCell = 25;
    const aroundHeading = 13;
    const spare = 2;
    /** The room a heading a person can sort by keeps for the sort's arrow: the arrow's 14 pixels and the gap of 2 before it. */
    const arrow = 16;
    /**
     * What a text of so many characters needs in a cell; in a heading a person can sort
     * by, which every heading of these lists is unless a case says not; and in a heading
     * that is words alone.
     */
    const cellOf = (characters: number): number => characters * character + aroundCell + spare;
    const headingOf = (characters: number): number => characters * character + arrow + aroundHeading + spare;
    const wordsOf = (characters: number): number => characters * character + aroundHeading + spare;
    // Name: "Commendations", thirteen characters. Description: longer than the library's
    // width holds. Changed By: its heading, ten characters, over "ada".
    const name = cellOf(13);
    const changedBy = headingOf(10);
    const narrowest = name + text + 64 + changedBy + 66;
    /** How many times a drawn width was read. */
    let reads: number;
    /** Where an element is laid out, for the few a case places: every other is nowhere, as jsdom has it. */
    let placed: Map<Element, { left: number; right: number }>;
    /**
     * The page's set of fonts: whether a font is on its way, as it says it, what it tells
     * whoever listens when the fonts have arrived or failed, and whether a list still
     * listens (the wait the lists of a page share listens too, once).
     */
    let fonts: { arrived: () => void; listening: boolean; status: 'loading' | 'loaded' };
    /** Who is told when the fonts have arrived, and whether only the once. */
    let told: Map<() => void, boolean>;
    /** How much wider than the font's the letters are drawn: wider while a font has not arrived. */
    let wider: number;
    /** Whether laying a text out starts the load of its font, as the first text drawn in a font does. */
    let startsFont: boolean;
    /**
     * Whether an icon is laid out as the letters of its name, as it is where the icons'
     * font is not there: the letters run past the icon's box, which clips them and keeps
     * its width.
     */
    let iconsAsLetters: boolean;
    /** How wide the box of a line is laid out in a list drawn a line each: as wide as its cell leaves it, whatever the line says. */
    let lineBox: number;
    /**
     * How much what is around the list draws it smaller or larger than it is laid out: 1
     * unless a case says, as on a page. A dialog that grows open draws what it holds
     * at nine tenths of its size and more until it has opened.
     */
    let scale: number;
    /** The length the list's stylesheet gives its rule, which a browser lays out and jsdom does not. */
    const ruleLength = 100;
    /** How wide the box of an icon is laid out, whatever is drawn in it: a check's, any other icon's, and nothing for what is no icon. */
    const iconBox = (drawn: Element): number => {
      return drawn.tagName !== 'MAT-ICON' ? 0 : drawn.classList.contains('cell-yes') ? 30 : 24;
    };
    /**
     * A length as a browser hands back what it draws: scaled, and no finer than the
     * numbers it keeps a drawing in, so a length brought back from it is a hair off.
     */
    const asDrawn = (length: number): number => {
      return scale === 1 ? length : Math.fround(length * scale);
    };

    beforeEach(() => {
      reads = 0;
      wider = 1;
      scale = 1;
      startsFont = false;
      iconsAsLetters = false;
      lineBox = 0;
      placed = new Map();
      told = new Map();
      fonts = {
        status: 'loaded',
        get listening(): boolean {
          return Array.from(told.values()).some((once) => !once);
        },
        arrived: (): void => {
          for (const [listener, once] of Array.from(told)) {
            if (once) {
              told.delete(listener);
            }
            listener();
          }
        },
      };
      ranges.getBoundingClientRect = function (this: Range): DOMRect {
        reads++;
        if (startsFont) {
          fonts.status = 'loading';
        }
        // What a range over what an element holds takes in, as a browser has it: the box
        // of each element in it and the line of each text in it, an icon's letters with
        // its box and the box of a line set on its own with the line's text.
        const drawn = this.startContainer as HTMLElement;
        const icon = drawn.querySelector('mat-icon');
        const letters = (drawn.textContent ?? '').trim().length * character * wider;
        const width = icon
          ? Math.max(iconBox(icon), iconsAsLetters ? letters : 0)
          : drawn.querySelector('.cell-line')
            ? Math.max(lineBox, letters)
            : letters;
        return { width: asDrawn(width) } as DOMRect;
      };
      const view = document.defaultView as Window;
      const real = view.getComputedStyle.bind(view);
      vi.spyOn(view, 'getComputedStyle').mockImplementation((asked: Element, pseudo?: string | null) => {
        const style = real(asked, pseudo);
        const lengths: Record<string, string> | undefined =
          asked.tagName === 'TD'
            ? { paddingLeft: '12px', paddingRight: '12px', borderLeftWidth: '0px', borderRightWidth: '1px' }
            : asked.tagName === 'TH'
              ? { paddingLeft: '8px', paddingRight: '4px', borderLeftWidth: '0px', borderRightWidth: '1px' }
              : undefined;
        if (lengths === undefined) {
          return style;
        }
        return new Proxy(style, {
          get: (target, key): unknown => {
            const value: unknown = typeof key === 'string' && key in lengths ? lengths[key] : Reflect.get(target, key);
            return typeof value === 'function' ? (value as (...args: unknown[]) => unknown).bind(target) : value;
          },
        });
      });
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (
        this: HTMLElement,
      ): DOMRect {
        const laid = this.classList.contains('scale-rule')
          ? { left: 0, right: ruleLength }
          : (placed.get(this) ?? { left: 0, right: this.closest('td') ? iconBox(this) : 0 });
        const at = { left: asDrawn(laid.left), right: asDrawn(laid.right) };
        return { ...at, width: at.right - at.left, top: 0, bottom: 0, height: 0 } as DOMRect;
      });
      vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement): number {
        return this.classList.contains('scale-rule') ? ruleLength : 0;
      });
      Object.defineProperty(document, 'fonts', {
        configurable: true,
        value: {
          get status(): string {
            return fonts.status;
          },
          addEventListener: (_: string, listener: () => void, options?: { once?: boolean }): void => {
            told.set(listener, options?.once === true);
          },
          removeEventListener: (_: string, listener: () => void): void => {
            told.delete(listener);
          },
        },
      });
    });

    afterEach(() => {
      if (own) {
        ranges.getBoundingClientRect = own;
      } else {
        delete ranges.getBoundingClientRect;
      }
      delete (document as { fonts?: unknown }).fonts;
      vi.useRealTimers();
    });

    /** New rows, columns or sorts, drawn and read. */
    const hand = async (name: string, value: unknown): Promise<void> => {
      give(name, value);
      detect();
      await new Promise((resolve) => setTimeout(resolve, 0));
      detect();
    };
    const widths = (): string[] => {
      return headers().map((th) => th.style.width);
    };
    /** Whether the list is drawn: not held back until it has loaded for the first time, or until a font has arrived. */
    const drawn = (): boolean => {
      return !(frame().closest('.impulse-grid-host') as HTMLElement).classList.contains('held-back');
    };
    /** Whether the loading cover, with its spinner, is over the list. */
    const covered = (): boolean => {
      return element.querySelector('.impulse-grid-loading-overlay') !== null;
    };
    const longer = { name: 'Commendations and citations', description: 'None.', enabled: false, changedBy: 'ada' };
    const shorter = { name: 'Tugs', description: 'None.', enabled: false, changedBy: 'mo' };

    describe('the narrowest a column of text goes', () => {
      it("is what its heading and the longest value it shows need, with the cell's padding and line and two pixels to spare", async () => {
        await plain();
        frameIs(phone);

        expect([name, changedBy]).toEqual([131, 111]);
        expect(table().style.minWidth).toBe(`${narrowest}px`);
        expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);
      });

      it("is never more than the library's one width: a column whose values need more stays at it and cuts them short, as before", async () => {
        await plain();
        frameIs(phone);

        // "Cites a pilot for a sortie flown well." needs more than twice the width.
        expect(cellOf(38)).toBeGreaterThan(2 * text);
        expect(widths()[1]).toBe(`${text}px`);
      });

      it('is never less than the least a column goes, whatever little it holds', async () => {
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', [{ mark: '' }]);
        fixture.componentRef.setInput('columnDefs', [
          { id: 'mark', hideHeader: true },
          { id: 'more' },
          { id: 'again' },
        ]);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        await settle(fixture);
        frameIs(100);

        expect(widths()[0]).toBe('48px');
      });

      it("counts the marks beside a heading's words, the sort's arrow in the room kept for it and the filter's button after it, so they stay whole", async () => {
        await plain();
        await hand('sorts', [{ field: 'changedBy', direction: 'asc' }]);
        await hand('filterability', { changedBy: 'always' });
        const heading = headers()[3];
        const words = heading.querySelector('.col-header') as HTMLElement;
        const mark = heading.querySelector('.sort-icon') as HTMLElement;
        const label = heading.querySelector('.header-label') as HTMLElement;
        const filter = heading.querySelector('.header-cell > .filter-trigger') as HTMLElement;
        expect([mark, filter].map((drawn) => drawn !== null)).toEqual([true, true]);
        // The words end at 80; the arrow, after a gap of 2, ends at 96, where the label
        // ends; the filter's button is 22 wide after it.
        placed.set(words, { left: 0, right: 80 });
        placed.set(mark, { left: 82, right: 96 });
        placed.set(label, { left: 0, right: 96 });
        placed.set(filter, { left: 96, right: 118 });
        frameIs(phone);

        expect(widths()[3]).toBe(`${wordsOf(10) + 16 + 22}px`);
        expect(widths()[3]).toBe(`${changedBy + 22}px`);
      });

      it("keeps the room of a sort's arrow in every heading a person can sort by, from the start: a column as wide as its heading needs does not grow when they first sort by it", async () => {
        await plain();
        frameIs(phone);
        expect(changedBy).toBe(wordsOf(10) + arrow);
        expect(widths()[3]).toBe(`${changedBy}px`);

        await hand('sorts', [{ field: 'changedBy', direction: 'asc' }]);
        const heading = headers()[3];
        placed.set(heading.querySelector('.col-header') as HTMLElement, { left: 0, right: 80 });
        placed.set(heading.querySelector('.sort-icon') as HTMLElement, { left: 82, right: 96 });
        // The arrow is drawn in the room kept for it: the next read finds nothing more to need.
        await hand('sorts', [{ field: 'changedBy', direction: 'desc' }]);
        expect(widths()[3]).toBe(`${changedBy}px`);

        // The sort moves to another column: the arrow goes, and the column keeps its width.
        await hand('sorts', [{ field: 'name', direction: 'asc' }]);
        expect(widths()[3]).toBe(`${changedBy}px`);
      });

      it("keeps for the arrow what the list's stylesheet draws it at: the arrow's 14 pixels and the gap of 2 before it", async () => {
        await plain();
        await hand('sortable', false);
        frameIs(phone);
        // What the heading needs as words alone, and what it needs once a person can sort
        // by it: the difference is the room the list keeps for the arrow.
        const asWords = parseFloat(widths()[3]);
        await hand('sortable', true);
        const kept = parseFloat(widths()[3]) - asWords;
        expect(asWords).toBe(wordsOf(10));

        /** How many pixels a rule of the stylesheet gives a property. */
        const drawnAt = (rule: string, property: string): number => {
          const lengths = Array.from(rule.matchAll(/(?:^|[\s;])([a-z-]+):\s*(\d+)px/g));
          return Number(lengths.find(([, name]) => name === property)?.[2]);
        };
        const arrowIs = drawnAt(declares(['.sort-icon']), 'width');
        const gapIs = drawnAt(declares(['.header-label'], ['.header-words']), 'gap');
        expect([arrowIs, gapIs]).toEqual([14, 2]);
        expect(kept).toBe(arrowIs + gapIs);
        expect(arrow).toBe(kept);
      });

      it('grows by what a second sort draws past the room kept, and by no more: the number that says which sort the column is, the arrow being in the room kept for it', async () => {
        await plain();
        frameIs(phone);
        const unsorted = parseFloat(widths()[3]);
        await hand('sorts', [
          { field: 'name', direction: 'asc' },
          { field: 'changedBy', direction: 'asc' },
        ]);
        const heading = headers()[3];
        const number = heading.querySelector('.sort-priority') as HTMLElement;
        expect(number.textContent).toBe('2');
        // The number is drawn after the arrow and ends 6 pixels past it.
        placed.set(heading.querySelector('.col-header') as HTMLElement, { left: 0, right: 80 });
        placed.set(heading.querySelector('.sort-icon') as HTMLElement, { left: 82, right: 96 });
        placed.set(number, { left: 96, right: 102 });
        await hand('sorts', [
          { field: 'name', direction: 'desc' },
          { field: 'changedBy', direction: 'asc' },
        ]);

        expect(widths()[3]).toBe(`${changedBy + 6}px`);
        // The arrow and the gap before it are in the room the heading kept from the start:
        // the column grows by the number alone, not by the arrow and the number.
        expect(parseFloat(widths()[3]) - unsorted).toBe(6);
      });

      it('keeps no such room in a heading with nothing to press: a list told its rows have one order', async () => {
        await plain();
        await hand('sortable', false);
        frameIs(phone);

        expect(headers()[3].querySelector('button.header-label')).toBeNull();
        expect(widths()[3]).toBe(`${wordsOf(10)}px`);
      });

      it('keeps no such room in the heading of a column that opts out of sorting, and keeps it in the others', async () => {
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', [{ flag: 'x', changedBy: 'mo' }]);
        fixture.componentRef.setInput('columnDefs', [
          { id: 'flag', header: 'Commendation' },
          { id: 'changedBy', sortable: false },
          { id: 'more' },
        ]);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        await settle(fixture);
        frameIs(100);

        expect(widths().slice(0, 2)).toEqual([`${headingOf(12)}px`, `${wordsOf(10)}px`]);
      });

      it('measures what a cell draws, not what it is written with: a check is as wide as its icon', async () => {
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', [{ name: 'Commendations', enabled: true }]);
        fixture.componentRef.setInput('columnDefs', [{ id: 'name' }, { id: 'enabled', header: 'On' }, { id: 'more' }]);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        await settle(fixture);
        frameIs(100);

        // The icon's 30 pixels, not the five letters it is written with, and more than the
        // least a column goes.
        expect(cells(0)[1].querySelector('mat-icon.cell-yes')?.textContent).toBe('check');
        expect(widths()[1]).toBe(`${30 + aroundCell + spare}px`);
        expect(30 + aroundCell + spare).toBeGreaterThan(48);
        expect(5 * character + aroundCell + spare).toBeGreaterThan(30 + aroundCell + spare);
      });

      it('is the same for the first column, which is held at it: the held width and the room kept beside it follow', async () => {
        await plain();
        frameIs(phone);

        expect(held()).toBe(true);
        expect(frame().style.getPropertyValue('--impulse-grid-held-width')).toBe(`${name}px`);
      });

      it("stays at the library's width where nothing is laid out to be read", async () => {
        await plain();
        frameIs(0);
        expect(reads).toBe(0);
        expect(table().style.minWidth).toBe(`${columnsAtTheirNarrowest}px`);
      });
    });

    // A dialog grows open: Material draws its surface, and the list in it, at nine tenths of
    // its size and more until it has opened, and a browser hands back what it draws.
    describe('a list that something around it draws smaller or larger than it is laid out', () => {
      it('lays a rule out with the list, a hundred pixels long, that takes no room and is not seen', async () => {
        await plain();
        const rule = element.querySelector('.impulse-grid-host > .scale-rule');
        expect(rule).not.toBeNull();
        expect(rule?.textContent).toBe('');
        // What jsdom lays out for it, the spec does (`ruleLength`): the stylesheet is what a browser goes by.
        const drawnAs = declares(['.scale-rule']);
        expect(drawnAs).toMatch(new RegExp(`width:\\s*${ruleLength}px`));
        expect(drawnAs).toMatch(/position:\s*absolute/);
        expect(drawnAs).toMatch(/height:\s*0/);
        expect(drawnAs).toMatch(/visibility:\s*hidden/);
      });

      it('needs what it is laid out at, not what it is drawn at: a list read while its dialog grows open keeps its longest value whole once the dialog has opened', async () => {
        for (const drawnBy of [0.8, 0.907, 0.937, 0.94, 1.1, 1.25]) {
          scale = drawnBy;
          await plain();
          frameIs(phone);

          expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);
          expect(table().style.minWidth).toBe(`${narrowest}px`);
        }
        // What the same reads come to taken as drawn: seven pixels short for "Commendations" at 0.937.
        expect(Math.ceil(Math.fround(13 * character * 0.937) + aroundCell) + spare).toBe(name - 6);
      });

      it('brings a length back to the pixel it was laid out at: what the division leaves over does not carry a whole number of pixels on to the next', async () => {
        // "Commendations" is laid out 104 wide, and drawn at 0.937 a browser hands back a
        // length that comes back as a hair more than 104.
        scale = 0.937;
        expect(asDrawn(13 * character) / (asDrawn(ruleLength) / ruleLength)).toBeGreaterThan(13 * character);
        await plain();
        frameIs(phone);

        expect(widths()[0]).toBe(`${name}px`);
      });

      it("reads the marks beside a heading's words as they are laid out too", async () => {
        scale = 0.907;
        await plain();
        await hand('sorts', [{ field: 'changedBy', direction: 'asc' }]);
        await hand('filterability', { changedBy: 'always' });
        const heading = headers()[3];
        const words = heading.querySelector('.col-header') as HTMLElement;
        const mark = heading.querySelector('.sort-icon') as HTMLElement;
        const label = heading.querySelector('.header-label') as HTMLElement;
        const filter = heading.querySelector('.header-cell > .filter-trigger') as HTMLElement;
        // Laid out as in the case of the marks above: the words to 80, the arrow to 96,
        // where the label ends, and the filter's button 22 wide after it.
        placed.set(words, { left: 0, right: 80 });
        placed.set(mark, { left: 82, right: 96 });
        placed.set(label, { left: 0, right: 96 });
        placed.set(filter, { left: 96, right: 118 });
        frameIs(phone);

        expect(widths()[3]).toBe(`${changedBy + 22}px`);
      });

      it('reads a second sort past the room kept for the arrow as it is laid out', async () => {
        scale = 0.8;
        await plain();
        await hand('sorts', [
          { field: 'name', direction: 'asc' },
          { field: 'changedBy', direction: 'asc' },
        ]);
        const heading = headers()[3];
        const words = heading.querySelector('.col-header') as HTMLElement;
        const label = heading.querySelector('.header-label') as HTMLElement;
        const last = label.lastElementChild as HTMLElement;
        expect(last).not.toBe(words);
        // The arrow and the number after it end 26 past the words: 10 past the room kept.
        placed.set(words, { left: 0, right: 80 });
        placed.set(last, { left: 98, right: 106 });
        placed.set(label, { left: 0, right: 106 });
        frameIs(phone);

        expect(widths()[3]).toBe(`${changedBy + 10}px`);
      });

      it("reads an icon's box and the longest line of a list drawn a line each as they are laid out too", async () => {
        scale = 0.8;
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', [
          {
            name: 'Ada',
            standing: 'Cleared',
            [iconCellKey('standing')]: 'verified_user',
            roles: ['Reader', 'Deck warden'],
          },
        ]);
        fixture.componentRef.setInput('columnDefs', [
          { id: 'name' },
          { id: 'standing', header: 'Is', iconConfig: { icons: { Cleared: 'verified_user' } } },
          { id: 'roles', lines: true },
          { id: 'more' },
        ]);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        await settle(fixture);
        frameIs(100);

        // An icon's box is 24 and "Deck warden" eleven characters, each drawn at eight tenths of that.
        expect(asDrawn(24)).toBeLessThan(20);
        expect(widths().slice(1, 3)).toEqual([`${24 + aroundCell + spare}px`, `${cellOf(11)}px`]);
      });

      describe('drawn at no size at all', () => {
        const view = document.defaultView as Window;
        const own = ['requestAnimationFrame', 'cancelAnimationFrame'].map((name): [string, unknown] => {
          return [name, Reflect.get(view, name)];
        });
        /** The frames the list has asked the page for, which a case draws when it says. */
        let asked: Map<number, FrameRequestCallback>;
        const nextFrame = (): void => {
          const due = Array.from(asked.values());
          asked.clear();
          due.forEach((draw) => draw(0));
          detect();
        };

        beforeEach(() => {
          asked = new Map();
          let last = 0;
          view.requestAnimationFrame = (draw: FrameRequestCallback): number => {
            asked.set(++last, draw);
            return last;
          };
          view.cancelAnimationFrame = (id: number): void => {
            asked.delete(id);
          };
        });

        afterEach(() => {
          own.forEach(([name, was]) => Reflect.set(view, name, was));
        });

        it('reads nothing off it, keeps nothing and draws nothing of a list it has never read, and reads and draws it at the first frame that draws it at some size', async () => {
          scale = 0;
          await plain();
          frameIs(phone);
          expect(reads).toBe(0);
          expect(widths()[0]).toBe(`${text}px`);
          expect(drawn()).toBe(false);
          expect(asked.size).toBe(1);

          // A frame later it is still drawn at no size: it asks for the next, and for one only.
          nextFrame();
          await hand('rowData', [rows[0], shorter]);
          expect(reads).toBe(0);
          expect(drawn()).toBe(false);
          expect(asked.size).toBe(1);

          scale = 0.5;
          nextFrame();
          expect(drawn()).toBe(true);
          expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);
          expect(asked.size).toBe(0);
        });

        it('keeps a list a person has seen as it is while it is drawn at no size, and reads it again at the first frame that draws it at some size', async () => {
          await plain([shorter]);
          frameIs(phone);
          expect(drawn()).toBe(true);
          expect(widths()[0]).toBe(`${headingOf(4)}px`);
          const read = reads;

          // Something around the list draws it at no size, and a longer value arrives meanwhile.
          scale = 0;
          await hand('rowData', rows);
          expect(reads).toBe(read);
          expect(drawn()).toBe(true);
          expect(widths()[0]).toBe(`${headingOf(4)}px`);
          expect(asked.size).toBe(1);

          scale = 1;
          nextFrame();
          expect(drawn()).toBe(true);
          expect(widths()[0]).toBe(`${name}px`);
          expect(asked.size).toBe(0);
        });

        it('gives up the frame it asked for once it has left', async () => {
          scale = 0;
          await plain();
          frameIs(phone);
          expect(asked.size).toBe(1);
          const [its] = asked.keys();

          // The page may ask for a frame of its own as the list leaves: the list's is the one given up.
          leave();
          expect(asked.has(its)).toBe(false);
        });
      });
    });

    describe('a need only grows while the list keeps its columns', () => {
      it('grows when a longer value arrives', async () => {
        await plain();
        frameIs(phone);
        await hand('rowData', [longer]);

        // Twenty-seven characters need more than the library's width, which it stays at.
        expect(widths()[0]).toBe(`${text}px`);
        await hand('rowData', [shorter]);
        expect(widths()[0]).toBe(`${text}px`);
      });

      it('does not shrink when a page of shorter values arrives, when the rows are sorted, or when a filter leaves none', async () => {
        await plain();
        frameIs(phone);
        const before = widths();

        await hand('rowData', [shorter]);
        expect(widths()).toEqual(before);
        await hand('sorts', [{ field: 'name', direction: 'desc' }]);
        expect(widths()).toEqual(before);
        await hand('rowData', []);
        expect(widths()).toEqual(before);
        expect(table().style.minWidth).toBe(`${narrowest}px`);
      });

      it('starts again when the columns change, a heading among them: the same rows are then measured anew', async () => {
        await plain();
        frameIs(phone);
        await hand('rowData', [shorter]);
        expect(widths()[0]).toBe(`${name}px`);

        // The same columns, the first under another heading.
        await hand(
          'columnDefs',
          columns.map((col) => (col.id === 'name' ? { ...col, header: 'Flag' } : col)),
        );
        // "Tugs" and "Flag", four characters each: the heading, with the room of its arrow, needs the more.
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
        expect(headingOf(4)).toBeGreaterThan(cellOf(4));
        expect(headingOf(4)).toBeLessThan(name);
      });

      it('starts again when the list is opened again', async () => {
        await plain();
        frameIs(phone);
        leave();

        await plain([shorter]);
        frameIs(phone);
        // "Tugs" under "Name": the heading, with the room of its arrow, needs the more.
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
      });
    });

    describe("a width that is not the library's to choose", () => {
      it('a width a person dragged wins over what the column needs, and stays through other rows', async () => {
        await plain();
        frameIs(phone);
        // The drag starts from the width the column is drawn at.
        placed.set(headers()[0], { left: 0, right: name });
        drag(0, 60);
        expect(headers()[0].style.width).toBe(`${name + 60}px`);

        await hand('rowData', [shorter]);
        expect(headers()[0].style.width).toBe(`${name + 60}px`);
        await hand('rowData', [longer]);
        expect(headers()[0].style.width).toBe(`${name + 60}px`);
        expect(table().style.minWidth).toBe(`${narrowest + 60}px`);
      });

      it('a width a person dragged narrower than the column needs is theirs too', async () => {
        await plain();
        frameIs(phone);
        drag(0, -500);

        expect(headers()[0].style.width).toBe('48px');
        await hand('rowData', [longer]);
        expect(headers()[0].style.width).toBe('48px');
      });

      it('a width the page declared wins over what the column needs: more or less, it is drawn as declared and never read', async () => {
        await plain();
        frameIs(phone);

        // "Enabled" needs more than the 64 its column declares.
        expect(headingOf(7)).toBeGreaterThan(64);
        expect(widths()[2]).toBe('64px');
        expect(table().style.minWidth).toBe(`${narrowest}px`);
      });
    });

    describe('when a list scrolls', () => {
      it("a list that fits at the library's width is drawn as it always was: no column of text is given a width", async () => {
        await plain();
        frameIs(wide);

        expect(widths()).toEqual(['', '', '64px', '', '66px']);
        expect(held()).toBe(false);
        frameIs(columnsAtTheirNarrowest);
        expect(widths()).toEqual(['', '', '64px', '', '66px']);
      });

      it('a list that fits once its columns are as narrow as they need does not scroll: a column that needs more than an equal share of the frame keeps what it needs, and the rest share what is left', async () => {
        await plain();
        // Ten more than the columns need. A third of what the declared and the button
        // column leave is under what Description needs, and half of the rest under what
        // Name needs: both keep theirs, and Changed By takes what is left.
        frameIs(narrowest + 10);
        expect(held()).toBe(false);
        expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', '', '66px']);

        // Sixty more: Description still needs more than a third, and Name fits in half of the rest.
        frameIs(narrowest + 60);
        expect(held()).toBe(false);
        expect(widths()).toEqual(['', `${text}px`, '64px', '', '66px']);

        // Seventy-eight more: a third is the library's width to the pixel, which every column fits in.
        frameIs(narrowest + 78);
        expect(held()).toBe(false);
        expect(widths()).toEqual(['', '', '64px', '', '66px']);
        frameIs(narrowest + 77);
        expect(widths()).toEqual(['', `${text}px`, '64px', '', '66px']);
      });

      it('scrolls only in a frame narrower than its columns at their narrowest, to the pixel, and is in the same state at the same width from either side', async () => {
        await plain();
        const at = (width: number): [boolean, string[]] => {
          frameIs(width);
          return [held(), widths()];
        };
        const fits: [boolean, string[]] = [false, [`${name}px`, `${text}px`, '64px', '', '66px']];
        const scrolls: [boolean, string[]] = [true, [`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']];

        expect(at(narrowest)).toEqual(fits);
        expect(at(narrowest - 1)).toEqual(scrolls);
        expect(at(narrowest)).toEqual(fits);
        expect(at(narrowest + 1)).toEqual(fits);
        expect(at(narrowest)).toEqual(fits);
        expect(at(narrowest - 1)).toEqual(scrolls);
        // What it needs does not move with the frame.
        expect(table().style.minWidth).toBe(`${narrowest}px`);
      });

      it('holds its first column while the widest of the others, as narrow as that one goes, is whole beside it, and lets it go a pixel under that', async () => {
        await plain();
        // Name is held at the 131 it needs; Description, at the library's width, is the
        // widest of those that scroll.
        frameIs(name + text);
        expect(held()).toBe(true);
        frameIs(name + text - 1);
        expect(held()).toBe(false);
        expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);

        // Without Description the widest is Changed By, at the 111 its heading needs.
        await hand('columnDefs', [columns[0], columns[2], columns[3], columns[4]]);
        expect(widths()).toEqual([`${name}px`, '64px', `${changedBy}px`, '66px']);
        frameIs(name + changedBy);
        expect(held()).toBe(true);
        frameIs(name + changedBy - 1);
        expect(held()).toBe(false);
      });
    });

    describe('reading what is drawn', () => {
      it('reads once when the list is first laid out, and nothing more for a frame that only changes width', async () => {
        await plain();
        expect(reads).toBe(0);
        frameIs(phone);
        const first = reads;
        expect(first).toBeGreaterThan(0);

        frameIs(wide);
        frameIs(phone);
        detect();
        expect(reads).toBe(first);
      });

      it('reads once where the frame has its width from the first, as in a browser: the report of its size that follows reads nothing more', async () => {
        const laid = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
        Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
          configurable: true,
          get(this: HTMLElement): number {
            return this.classList.contains('impulse-grid-scroll') ? phone : 0;
          },
        });
        try {
          await plain();
          const first = reads;
          expect(first).toBeGreaterThan(0);

          frameIs(phone);
          expect(reads).toBe(first);
        } finally {
          if (laid) {
            Object.defineProperty(HTMLElement.prototype, 'clientWidth', laid);
          } else {
            delete (HTMLElement.prototype as { clientWidth?: number }).clientWidth;
          }
        }
      });

      it('works out no width again after a read that found nothing new, so the list is not drawn again for it', async () => {
        const list = await plain();
        frameIs(phone);
        // Counts each time the width the first column is drawn at is worked out.
        let workedOut = 0;
        const watch = TestBed.runInInjectionContext(() =>
          effect(() => {
            list.drawnWidthFor(columns[0]);
            workedOut++;
          }),
        );
        TestBed.tick();
        expect(workedOut).toBe(1);
        const first = reads;

        // Another page of values the list has shown: read, and nothing found that the columns did not have.
        await hand('rowData', [rows[1], rows[0]]);
        TestBed.tick();
        expect(reads).toBeGreaterThan(first);
        expect(workedOut).toBe(1);

        // A longer value: its column needs more, and the widths are worked out again.
        await hand('rowData', [longer]);
        TestBed.tick();
        expect(workedOut).toBe(2);
        watch.destroy();
      });

      it("reads a value once for each column it is shown in, no value of a column already at the library's width, and no column with a width of its own", async () => {
        await plain();
        frameIs(phone);
        // Three headings; Name's three values; Description's first, which takes it to the
        // library's width; Changed By's two, "ada" twice being one. Nothing of the
        // declared column and nothing of the button column.
        expect(reads).toBe(3 + 3 + 1 + 2);

        await hand('rowData', [rows[0], rows[0], shorter]);
        // Name's heading and its two values; Changed By's heading and its two. Description is not read again.
        expect(reads).toBe(9 + 3 + 3);
      });

      it('reads once for a change of the rows, and nothing for a turn in which nothing the list draws has changed', async () => {
        await plain();
        frameIs(phone);
        const first = reads;

        detect();
        await hand('rowData', rows);
        expect(reads).toBe(first);
        // What the list would say were it empty is drawn in no column.
        await hand('emptyMessage', 'No flags are declared.');
        expect(reads).toBe(first);
        await hand('rowData', [shorter]);
        expect(reads).toBeGreaterThan(first);
      });

      it("reads nothing while the list is loading, and what it shows once it has loaded: a related record's key, drawn until the record is read, does not hold the column wide", async () => {
        await plain([{ ...shorter, name: 'Sector 9f3c0e2a-5b7d' }]);
        await hand('loading', true);
        frameIs(phone);
        expect(reads).toBe(0);
        expect(widths()[0]).toBe(`${text}px`);

        // The record is read: the row is drawn with its name, and the list has loaded.
        give('rowData', [shorter]);
        await hand('loading', false);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
        expect(cellOf(20)).toBeGreaterThan(text);
      });

      it('reads once the list has loaded, though nothing it draws has changed', async () => {
        await plain([shorter]);
        await hand('loading', true);
        frameIs(phone);
        expect(widths()[0]).toBe(`${text}px`);

        await hand('loading', false);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
      });

      it("draws nothing of a list on its first load, not its headings at the library's width, its pager or a loading cover, and draws it whole once it has loaded, each column at what it needs", async () => {
        await plain([]);
        give('page', { offset: 0, hasPrev: false, hasNext: false });
        await hand('loading', true);
        frameIs(phone);
        // Nothing is read, and nothing of the list is drawn: its frame and its pager are
        // hidden where they will be, and no cover with a spinner stands in for them.
        expect(reads).toBe(0);
        expect(drawn()).toBe(false);
        expect(covered()).toBe(false);
        expect(declares(['.held-back', '.impulse-grid-scroll'])).toMatch(/visibility:\s*hidden/);
        expect(declares(['.held-back', '.impulse-grid-pager'])).toMatch(/visibility:\s*hidden/);
        expect(element.querySelector('.held-back > .impulse-grid-pager')).not.toBeNull();
        expect(element.querySelector('.held-back > .impulse-grid-view > .impulse-grid-scroll')).toBe(frame());

        // The rows arrive: the list is read with them and drawn, no column at the library's width first.
        give('rowData', rows);
        await hand('loading', false);
        expect(drawn()).toBe(true);
        expect(covered()).toBe(false);
        expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);
      });

      it('draws a list whose first load brings no rows, or fails, once it has: its headings, each at what it needs, over what the list says', async () => {
        await plain([]);
        await hand('loading', true);
        frameIs(phone);
        expect(drawn()).toBe(false);

        give('emptyMessage', 'This list could not be loaded.');
        await hand('loading', false);
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
        expect(element.querySelector('.no-records-row')?.textContent).toContain('This list could not be loaded.');
      });

      it('keeps a list a person has seen on the screen, under the loading cover, while it is read again for a page turn, a sort or a filter, and narrows nothing when the rows arrive', async () => {
        await plain();
        frameIs(phone);
        expect(drawn()).toBe(true);
        const seen = widths();

        await hand('loading', true);
        expect(drawn()).toBe(true);
        expect(covered()).toBe(true);
        expect(widths()).toEqual(seen);

        give('rowData', [shorter]);
        await hand('loading', false);
        expect(drawn()).toBe(true);
        expect(covered()).toBe(false);
        expect(widths()).toEqual(seen);
      });

      it('holds a list handed other columns back while it loads with them: those have not been read, and it is drawn whole with their rows', async () => {
        await plain();
        frameIs(phone);
        expect(drawn()).toBe(true);

        // The same columns, the first under another heading, and a load for them.
        give(
          'columnDefs',
          columns.map((col) => (col.id === 'name' ? { ...col, header: 'Flag' } : col)),
        );
        await hand('loading', true);
        expect(drawn()).toBe(false);
        expect(covered()).toBe(false);

        // "Tugs" under "Flag": read anew with the rows the load brought, not kept at what "Commendations" needed.
        give('rowData', [shorter]);
        await hand('loading', false);
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
        expect(headingOf(4)).toBeLessThan(name);
      });

      it('holds no list back that is not laid out, though it is loading: nothing can be read to end it, and a list that cannot be seen keeps its cover for when it can', async () => {
        await plain([]);
        await hand('loading', true);
        expect(drawn()).toBe(true);
        expect(covered()).toBe(true);
      });

      it('reads again when a font arrives, since every text is then drawn another width, and listens no more once the list has left', async () => {
        await plain();
        frameIs(phone);
        const first = reads;
        expect(fonts.listening).toBe(true);

        fonts.arrived();
        expect(reads).toBeGreaterThan(first);
        leave();
        expect(fonts.listening).toBe(false);
      });

      it('looks again for the cells the Tab key stops on once a font has arrived, since a text is cut or whole by its letters', async () => {
        await plain();
        frameIs(phone);
        const text = element.querySelector('tr.impulse-row td.data-col') as HTMLElement;
        let looks = 0;
        Object.defineProperty(text, 'scrollWidth', {
          configurable: true,
          get: (): number => {
            looks++;
            return 620;
          },
        });
        Object.defineProperty(text, 'clientWidth', { configurable: true, value: 180 });
        const tab = (): boolean =>
          document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
        tab();
        tab();
        expect(looks).toBe(1);
        expect(text.getAttribute('tabindex')).toBe('0');

        fonts.arrived();
        tab();
        expect(looks).toBe(2);
      });

      it('grows a column whose text needs more in the letters of a font that arrives, and narrows none: the width a column grew to on a page a person has left stays', async () => {
        await plain();
        frameIs(phone);
        // The person turns to a page of shorter values, and a font the page asked for arrives.
        await hand('rowData', [shorter]);
        fonts.arrived();
        detect();
        expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);

        // A font whose letters are a quarter wider: "Changed By" needs more, and "Tugs" still less than Name has.
        wider = 1.25;
        fonts.arrived();
        detect();
        expect(widths()[3]).toBe(`${changedBy + 20}px`);
        expect(widths()[0]).toBe(`${name}px`);

        // And one whose letters are narrower again narrows nothing.
        wider = 1;
        fonts.arrived();
        detect();
        expect(widths()[3]).toBe(`${changedBy + 20}px`);
        expect(widths()[0]).toBe(`${name}px`);
      });

      it("does not draw a list it has not read while a font is on its way, and draws it whole once the font has arrived, each column at what it needs: no column is seen at the library's width and then narrowed", async () => {
        wider = 1.25;
        fonts.status = 'loading';
        await plain();
        frameIs(phone);
        // Nothing is read in the letters drawn in the font's place, and nothing of the
        // list is drawn: its headings and rows are hidden, with the frame they scroll in.
        expect(reads).toBe(0);
        expect(drawn()).toBe(false);
        const heldBack = declares(['.held-back', '.impulse-grid-scroll']);
        expect(heldBack).toMatch(/visibility:\s*hidden/);
        expect(heldBack).toMatch(/overflow:\s*hidden/);

        // New rows while the font is still on its way are not read either.
        await hand('rowData', [rows[0], shorter]);
        expect(reads).toBe(0);
        expect(drawn()).toBe(false);

        wider = 1;
        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(drawn()).toBe(true);
        expect(widths()).toEqual([`${name}px`, `${text}px`, '64px', `${changedBy}px`, '66px']);
      });

      it("keeps nothing of a pass of reads that started a font on its way, and does not draw the list it read so: what it read was drawn in the font's place", async () => {
        // The first text laid out in a font is what asks the page for it.
        wider = 1.25;
        startsFont = true;
        await plain();
        frameIs(phone);
        expect(reads).toBeGreaterThan(0);
        expect(fonts.status).toBe('loading');
        expect(drawn()).toBe(false);
        expect(Math.ceil(13 * character * 1.25) + aroundCell + spare).toBeLessThan(text);

        startsFont = false;
        wider = 1;
        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${name}px`);
      });

      it('waits three seconds for a font and no longer: the list is then read in the letters the screen shows and drawn, and a font that arrives after all grows a column and narrows none', async () => {
        await plain();
        vi.useFakeTimers({ shouldAdvanceTime: true });
        wider = 1.25;
        fonts.status = 'loading';
        frameIs(phone);
        expect(drawn()).toBe(false);

        vi.advanceTimersByTime(2999);
        detect();
        expect(reads).toBe(0);
        expect(drawn()).toBe(false);

        vi.advanceTimersByTime(1);
        detect();
        expect(drawn()).toBe(true);
        // "Commendations" and "Changed By" in letters a quarter wider than the font's.
        const inItsPlace = [
          `${Math.ceil(13 * character * 1.25) + aroundCell + spare}px`,
          `${Math.ceil(10 * character * 1.25) + arrow + aroundHeading + spare}px`,
        ];
        expect([widths()[0], widths()[3]]).toEqual(inItsPlace);
        expect(Math.ceil(13 * character * 1.25) + aroundCell + spare).toBeGreaterThan(name);

        // The font arrives after all, and its letters are narrower: nothing a person has seen narrows.
        wider = 1;
        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(drawn()).toBe(true);
        expect([widths()[0], widths()[3]]).toEqual(inItsPlace);
      });

      it("reads an icon by its box, not by the letters of its name: a column of icons read once the wait for the icons' font has run out is as wide as the icon needs", async () => {
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', [
          { name: 'Commendations', enabled: true, standing: 'Cleared', [iconCellKey('standing')]: 'verified_user' },
        ]);
        fixture.componentRef.setInput('columnDefs', [
          { id: 'name' },
          { id: 'enabled', header: 'On' },
          { id: 'standing', header: 'Is', iconConfig: { icons: { Cleared: 'verified_user' } } },
          { id: 'more' },
        ]);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        await settle(fixture);
        vi.useFakeTimers({ shouldAdvanceTime: true });
        // The icons' font is on its way and does not arrive: each icon is laid out as the
        // letters of its name, which its box clips.
        iconsAsLetters = true;
        fonts.status = 'loading';
        frameIs(100);
        expect(drawn()).toBe(false);
        vi.advanceTimersByTime(3000);
        detect();
        expect(drawn()).toBe(true);

        // A check's box is 30 where "check" is five letters, and the other icon's 24 where
        // "verified_user" is thirteen: each column is as wide as the box needs.
        expect(cells(0)[1].querySelector('mat-icon.cell-yes')?.textContent).toBe('check');
        expect(cells(0)[2].querySelector('mat-icon.cell-icon')?.textContent?.trim()).toBe('verified_user');
        const boxes = [`${30 + aroundCell + spare}px`, `${24 + aroundCell + spare}px`];
        expect(widths().slice(1, 3)).toEqual(boxes);
        expect(5 * character).toBeGreaterThan(30);
        expect(13 * character).toBeGreaterThan(24);

        // The font arrives after all: the icons are drawn, and nothing moves.
        iconsAsLetters = false;
        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(widths().slice(1, 3)).toEqual(boxes);
      });

      it('reads a list drawn a line each by its longest line, not by the box of a line, which is as wide as its cell whatever the line says', async () => {
        const fixture = TestBed.createComponent(AppGridComponent);
        fixture.componentRef.setInput('rowData', [
          { name: 'Ada', roles: ['Reader', 'Deck warden'] },
          { name: 'Mo', roles: ['Reader'] },
        ]);
        fixture.componentRef.setInput('columnDefs', [{ id: 'name' }, { id: 'roles', lines: true }, { id: 'more' }]);
        element = fixture.nativeElement as HTMLElement;
        detect = (): void => fixture.detectChanges();
        await settle(fixture);
        // The column is drawn at the library's width until it is read: its lines' boxes
        // are what that leaves inside the cell's padding and line.
        lineBox = text - aroundCell;
        frameIs(100);

        expect(Array.from(cells(0)[1].querySelectorAll('.cell-line')).map((line) => line.textContent)).toEqual([
          'Reader',
          'Deck warden',
        ]);
        // "Deck warden" is eleven characters, more than the heading needs.
        expect(widths()[1]).toBe(`${cellOf(11)}px`);
        expect(cellOf(11)).toBeGreaterThan(headingOf(5));
        expect(cellOf(11)).toBeLessThan(text);
      });

      it('counts that wait once for the page, not again for each list: a list opened while it runs waits what is left of it, and one opened after it has run out is drawn at once', async () => {
        await plain();
        vi.useFakeTimers({ shouldAdvanceTime: true });
        fonts.status = 'loading';
        frameIs(phone);
        expect(drawn()).toBe(false);
        vi.advanceTimersByTime(2000);
        leave();

        // A second list, two seconds into the wait: it is held back for the third.
        await plain([shorter]);
        frameIs(phone);
        expect(drawn()).toBe(false);
        vi.advanceTimersByTime(1000);
        detect();
        expect(drawn()).toBe(true);
        leave();

        // A third, after the wait has run out, the font still on its way.
        await plain([shorter]);
        frameIs(phone);
        expect(fonts.status).toBe('loading');
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
      });

      it('ends that wait when the fonts arrive, though no list is on the screen then: the next font to be on its way is waited for from its own start', async () => {
        await plain();
        vi.useFakeTimers({ shouldAdvanceTime: true });
        fonts.status = 'loading';
        frameIs(phone);
        expect(drawn()).toBe(false);
        vi.advanceTimersByTime(1000);
        leave();

        // The fonts arrive with no list to hear of it, and long after another is on its way.
        fonts.status = 'loaded';
        fonts.arrived();
        vi.advanceTimersByTime(5000);
        fonts.status = 'loading';

        await plain([shorter]);
        frameIs(phone);
        expect(drawn()).toBe(false);
        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
      });

      it('holds a list back from when it first knows of the font, though it is loading then, and counts the wait from then: its headings are not drawn under the loading cover and taken away when its rows arrive', async () => {
        await plain([shorter]);
        vi.useFakeTimers({ shouldAdvanceTime: true });
        fonts.status = 'loading';
        give('loading', true);
        frameIs(phone);
        expect(drawn()).toBe(false);
        vi.advanceTimersByTime(2000);

        // The list has loaded, two seconds into the wait: it is held back for the third.
        await hand('loading', false);
        expect(reads).toBe(0);
        expect(drawn()).toBe(false);
        vi.advanceTimersByTime(1000);
        detect();
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
      });

      it('keeps a list it held back for a font held back until it is read: one still loading when the wait runs out is drawn whole once it has loaded', async () => {
        await plain([shorter]);
        vi.useFakeTimers({ shouldAdvanceTime: true });
        fonts.status = 'loading';
        give('loading', true);
        frameIs(phone);
        expect(drawn()).toBe(false);

        // The wait runs out, and the font arrives after all, while the list is loading.
        vi.advanceTimersByTime(3000);
        detect();
        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(reads).toBe(0);
        expect(drawn()).toBe(false);

        await hand('loading', false);
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${headingOf(4)}px`);
      });

      it('reads nothing once it has left, though its wait for a font runs out after', async () => {
        await plain();
        vi.useFakeTimers({ shouldAdvanceTime: true });
        fonts.status = 'loading';
        frameIs(phone);
        expect(drawn()).toBe(false);

        leave();
        vi.advanceTimersByTime(3000);
        expect(reads).toBe(0);
      });

      it('never takes a list a person has seen off the screen for a font: it keeps the widths it was drawn at, is not read while the font is on its way, and grows when the font has arrived', async () => {
        await plain();
        frameIs(phone);
        const before = widths();
        const first = reads;
        expect(drawn()).toBe(true);

        // A font is asked for, and a longer value arrives while it is on its way.
        fonts.status = 'loading';
        await hand('rowData', [longer]);
        expect(reads).toBe(first);
        expect(drawn()).toBe(true);
        expect(widths()).toEqual(before);

        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(drawn()).toBe(true);
        expect(widths()[0]).toBe(`${text}px`);
      });

      it('holds back a list handed other columns while a font is on its way: those have not been read, and what they were drawn at now would narrow', async () => {
        await plain();
        frameIs(phone);
        expect(drawn()).toBe(true);

        fonts.status = 'loading';
        await hand(
          'columnDefs',
          columns.map((col) => (col.id === 'name' ? { ...col, header: 'Flag' } : col)),
        );
        expect(drawn()).toBe(false);

        fonts.status = 'loaded';
        fonts.arrived();
        detect();
        expect(drawn()).toBe(true);
        // "Commendations" under "Flag".
        expect(widths()[0]).toBe(`${name}px`);
      });
    });
  });
});

// The pager under the list: the rows the page spans and the buttons that turn it. On one
// line while they fit; in a frame too narrow for both, the buttons go under the words, at
// the far end, and wrap among themselves, so nothing of the pager is cut off by the
// list's edge, which clips whatever leaves it.
describe("the list's pager", () => {
  let element: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    const fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [{ name: 'Commendations' }]);
    fixture.componentRef.setInput('columnDefs', [{ id: 'name' }]);
    fixture.componentRef.setInput('page', { offset: 0, total: 34, hasPrev: false, hasNext: true });
    fixture.detectChanges();
    element = fixture.nativeElement as HTMLElement;
  });

  /** What the list's stylesheet declares in the rules whose selector names `name`. */
  const declares = (name: string): string => {
    const sheet = Array.from(document.querySelectorAll('style'))
      .map((style) => style.textContent ?? '')
      .join('\n');
    return Array.from(sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g))
      .filter(([, selector]) => selector.includes(name))
      .map(([, , declarations]) => declarations)
      .join('\n');
  };

  it('says the rows and offers the three buttons, each with its icon and its words', () => {
    const pager = element.querySelector('.impulse-grid-pager') as HTMLElement;

    expect(pager.querySelector('.pager-rows')?.textContent).toBe('1–1 of 34');
    expect(
      Array.from(pager.querySelectorAll('.pager-buttons button')).map((b) =>
        b.textContent?.replace(/\s+/g, ' ').trim(),
      ),
    ).toEqual(['first_page First', 'chevron_left Previous', 'Next chevron_right']);
  });

  it('wraps in a frame too narrow for the words and the buttons on one line: the buttons go under the words, at the far end, and wrap among themselves', () => {
    const pager = element.querySelector('.impulse-grid-pager') as HTMLElement;
    const buttons = pager.querySelector('.pager-buttons') as HTMLElement;

    expect(getComputedStyle(pager).flexWrap).toBe('wrap');
    expect(getComputedStyle(buttons).flexWrap).toBe('wrap');
    expect(declares('.pager-buttons')).toContain('margin-left: auto');
    expect(declares('.pager-buttons')).toContain('justify-content: flex-end');
    // What is past the list's edge is not drawn, so the pager must not reach past it.
    expect(declares('.impulse-grid-host')).toContain('overflow: clip');
  });
});

describe('a list on paper', () => {
  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppGridComponent],
      providers: [provideResourceTesting()],
    }).compileComponents();
    const fixture = TestBed.createComponent(AppGridComponent);
    fixture.componentRef.setInput('rowData', [{ name: 'Commendations' }]);
    fixture.componentRef.setInput('columnDefs', [{ id: 'name' }]);
    fixture.componentRef.setInput('page', { offset: 0, total: 34, hasPrev: false, hasNext: true });
    fixture.detectChanges();
  });

  /**
   * What the list's stylesheet declares for print in the rules whose selector, with what
   * the framework adds to it taken away, is `selector`. jsdom draws no page for paper, so
   * the rules are read as the page is given them.
   */
  const onPaper = (selector: string): string => {
    const sheet = Array.from(document.querySelectorAll('style'))
      .map((style) => style.textContent ?? '')
      .filter((text) => text.includes('.impulse-grid-host'))
      .join('\n');
    return Array.from(sheet.matchAll(/@media print\s*\{((?:[^{}]*\{[^{}]*\})*)\s*\}/g))
      .flatMap(([, held]) => Array.from(held.matchAll(/([^{}]+)\{([^{}]*)\}/g)))
      .filter(([, named]) => {
        const plain = named
          .replace(/\[_nghost[^\]]*\]/g, ':host')
          .replace(/\[_ngcontent[^\]]*\]/g, '')
          .replace(/\s+/g, ' ')
          .trim();
        return plain === selector;
      })
      .map(([, , declarations]) => declarations)
      .join('\n');
  };

  it('is as long as its rows, laid out as blocks one under the other: the pager comes after the last row of the last page', () => {
    expect(onPaper(':host')).toMatch(/(^|[;\s])height:\s*auto/);
    expect(onPaper('.impulse-grid-host')).toMatch(/(^|[;\s])display:\s*block/);
    expect(onPaper('.impulse-grid-host')).toMatch(/(^|[;\s])height:\s*auto/);
    expect(onPaper('.impulse-grid-view')).toMatch(/(^|[;\s])display:\s*block/);
  });

  it('scrolls nothing and cuts nothing off', () => {
    expect(onPaper('.impulse-grid-host')).toMatch(/(^|[;\s])overflow:\s*visible/);
    expect(onPaper('.impulse-grid-scroll')).toMatch(/(^|[;\s])overflow:\s*visible/);
  });

  it("draws a heading's button as its words alone, which a browser repeats whole on every page", () => {
    expect(onPaper('.header-cell .header-label')).toMatch(/(^|[;\s])display:\s*contents/);
  });
});
