import { Component } from '@angular/core';
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
    const refusal = 'This list is not available to you: the sector keeps its roster to its own marshal.';
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

// A column keeps a readable width: the table is never narrower than its columns at the
// narrowest each is drawn, so a frame too narrow for them scrolls sideways, the frame and
// never the page, and the first column is then held at the left edge with the select and
// expand columns. A list whose columns fit is drawn as it always was. jsdom lays nothing
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
  // What a wide window leaves the list, and a phone held upright.
  const wide = 1226;
  const phone = 336;

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

  describe('a frame narrower than the columns', () => {
    it('is what scrolls sideways: the table keeps its width inside the frame, which scrolls whatever does not fit', async () => {
      await plain();
      frameIs(phone);

      // The table is the frame's own child, wider than it, and the frame scrolls its overflow.
      expect(table().parentElement).toBe(frame());
      expect(parseFloat(table().style.minWidth)).toBeGreaterThan(phone);
      expect(getComputedStyle(frame()).overflow).toBe('auto');
      expect(getComputedStyle(element.querySelector('.impulse-grid-host') as HTMLElement).overflow).toBe('hidden');
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
      frameIs(phone);

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
      frameIs(phone);
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

    it('lets the first column go where holding it would leave less than the narrowest column beside it', async () => {
      await plain();
      frameIs(phone);
      // Dragged to 288, the held column leaves 48 beside it, the least a column goes.
      drag(0, 288);
      expect(held()).toBe(true);
      expect(frame().style.getPropertyValue('--impulse-grid-held-width')).toBe('288px');
      drag(0, 1);
      expect(held()).toBe(false);
      expect(parseFloat(table().style.minWidth)).toBeGreaterThan(phone);
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
    /** The browser lays the two frames out: the outer one a phone wide, the inner one wide enough for its two columns. */
    const laidOut = (): void => {
      const [outer, inner] = frames();
      Object.defineProperty(outer, 'clientWidth', { configurable: true, value: phone });
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
});
