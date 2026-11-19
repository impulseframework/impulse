import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { iconCellKey, RecordData } from '@impulseframework/resource-angular/types';

import { AppGridComponent } from './grid.component';
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
    await settle();
    expect(tooltip()).toBe(notesHeader);

    button?.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    await settle();
    expect(tooltip()).toBeNull();

    // A header that fits says nothing more when its button takes the focus.
    measured(header(0), 60, 96);
    header(0)
      .closest('button')
      ?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    await settle();
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
