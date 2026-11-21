import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { PermissionsCardComponent, PermissionsCardResource, READ_AND_WRITE } from './permissions-card.component';

// The card for what a person may do: per resource, what they hold of each permission, in
// words. It is laid out by its own width: a table, a column per permission, while its
// columns fit the card, and each resource as a block of its own where they do not. jsdom
// lays nothing out, so the specs of the layout say what the browser would report (the
// card's width, and the width its table needs) and read what the card does with it.

/** A marshal's card in one tenant: everything on missions, ships read under terms, nothing on tenants. */
const marshal: PermissionsCardResource[] = [
  {
    name: 'Missions',
    scope: 'Anvil',
    held: { List: 'granted', Read: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  },
  { name: 'Ships', scope: 'Anvil', held: { List: 'conditional', Read: 'conditional' } },
  { name: 'Tenants', held: {} },
];

/** A page that says its own words for a card that lists nothing, and draws a line of its own under the table. */
@Component({
  imports: [PermissionsCardComponent],
  template: `
    <impulse-permissions-card heading="Your permissions" [resources]="[]">
      <span permissionsCardEmpty>No resources yet. Add one and come back.</span>
      <p class="under">Methods you may run</p>
    </impulse-permissions-card>
  `,
})
class OwnWordsPageComponent {}

describe('PermissionsCardComponent', () => {
  let fixture: ComponentFixture<PermissionsCardComponent>;
  let element: HTMLElement;

  const settle = async (drawn: ComponentFixture<unknown>): Promise<void> => {
    drawn.detectChanges();
    await drawn.whenStable();
    await new Promise((resolve) => setTimeout(resolve, 0));
    drawn.detectChanges();
  };
  /** Draws the card over the resources, with the inputs given beside them. */
  const draw = async (
    resources: readonly PermissionsCardResource[],
    inputs: Record<string, unknown> = {},
  ): Promise<void> => {
    fixture = TestBed.createComponent(PermissionsCardComponent);
    fixture.componentRef.setInput('resources', resources);
    for (const [name, value] of Object.entries(inputs)) {
      fixture.componentRef.setInput(name, value);
    }
    element = fixture.nativeElement as HTMLElement;
    await settle(fixture);
  };
  const table = (): HTMLElement => {
    return element.querySelector('table.permissions') as HTMLElement;
  };
  const frame = (): HTMLElement => {
    return element.querySelector('.frame') as HTMLElement;
  };
  /** The mark whose width is the room the card has. */
  const mark = (): HTMLElement => {
    return element.querySelector('.width-mark') as HTMLElement;
  };
  const texts = (selector: string, within: Element = element): string[] => {
    return Array.from(within.querySelectorAll(selector)).map((found) => found.textContent?.trim() ?? '');
  };
  /** The card's rows, each as its name cell's text and the words of its answers. */
  const rows = (): { name: string; answers: string[]; named: string[] }[] => {
    return Array.from(element.querySelectorAll('tbody tr')).map((row) => ({
      name: (row.querySelector('td.resource-name')?.textContent ?? '').replace(/\s+/g, ' ').trim(),
      answers: texts('td.held .pill', row),
      named: texts('td.held .permission', row),
    }));
  };
  /** Whether the selector names the class or the element, whole: `.permission` is not `.permissions`. */
  const names = (selector: string, name: string): boolean => {
    for (let at = selector.indexOf(name); at >= 0; at = selector.indexOf(name, at + 1)) {
      if (!/[\w-]/.test(selector.charAt(at + name.length))) {
        return true;
      }
    }
    return false;
  };
  /**
   * What the card's own stylesheet, as the page is given it, declares in the rules whose
   * selector names every one of `all` and none of `without`: for what jsdom cannot answer
   * as a computed style. The card's rules are the ones that carry the mark Angular gives
   * its elements.
   */
  const declares = (all: string[], without: string[] = []): string => {
    const scoped = frame()
      .getAttributeNames()
      .find((name) => name.startsWith('_ngcontent'));
    expect(scoped).toBeDefined();
    const sheet = Array.from(document.querySelectorAll('style'))
      .map((style) => style.textContent ?? '')
      .join('\n');
    return Array.from(sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g))
      .filter(([, selector]) => {
        return (
          selector.includes(scoped ?? '') &&
          all.every((name) => names(selector, name)) &&
          without.every((name) => !names(selector, name))
        );
      })
      .map(([, , declarations]) => declarations)
      .join('\n')
      .replace(/\s/g, '');
  };

  describe('what it says', () => {
    beforeEach(async () => {
      await TestBed.configureTestingModule({
        imports: [PermissionsCardComponent, OwnWordsPageComponent],
      }).compileComponents();
    });

    it('answers for reading and writing a resource unless a page names its own permissions', async () => {
      await draw(marshal);

      expect(READ_AND_WRITE).toEqual(['List', 'Read', 'Create', 'Update', 'Delete']);
      expect(texts('thead th')).toEqual(['Resource', 'List', 'Read', 'Create', 'Update', 'Delete']);
    });

    it('says what a person holds on each resource in words: granted, terms apply, or a dash', async () => {
      await draw(marshal);

      expect(rows()).toEqual([
        {
          name: 'Missions Anvil',
          answers: ['granted', 'granted', 'granted', 'granted', 'granted'],
          named: ['List', 'Read', 'Create', 'Update', 'Delete'],
        },
        {
          name: 'Ships Anvil',
          answers: ['terms apply', 'terms apply', '—', '—', '—'],
          named: ['List', 'Read', 'Create', 'Update', 'Delete'],
        },
        { name: 'Tenants', answers: ['—', '—', '—', '—', '—'], named: ['List', 'Read', 'Create', 'Update', 'Delete'] },
      ]);
      // Each answer is told apart by more than its words, for a page that colors them.
      expect(texts('td.held .pill.granted')).toHaveLength(5);
      expect(texts('td.held .pill.conditional')).toHaveLength(2);
      expect(texts('td.held .pill.absent')).toHaveLength(8);
    });

    it('writes where an answer holds small beside the name, for a resource that says where', async () => {
      await draw(marshal);

      expect(texts('td.resource-name .scope')).toEqual(['Anvil', 'Anvil']);
    });

    it("answers for a page's own permissions, in the order the page names them", async () => {
      await draw([{ name: 'Dispatch', held: { Execute: 'granted' } }], { permissions: ['Read', 'Execute'] });

      expect(texts('thead th')).toEqual(['Resource', 'Read', 'Execute']);
      expect(rows()).toEqual([{ name: 'Dispatch', answers: ['—', 'granted'], named: ['Read', 'Execute'] }]);
    });

    it("stripes the permissions a session's mask has removed, heading and answers alike", async () => {
      await draw(marshal, { masked: ['Create', 'Delete'] });

      expect(texts('thead th.masked')).toEqual(['Create', 'Delete']);
      expect(texts('tbody tr:first-child td.masked .permission')).toEqual(['Create', 'Delete']);
      expect(declares(['.masked'])).toContain('repeating-linear-gradient');
    });

    it('is titled by the page, with a line under the title where the page gives one', async () => {
      await draw(marshal, { heading: 'Your permissions', subheading: 'What you hold on each resource.' });

      expect(texts('mat-card-title')).toEqual(['Your permissions']);
      expect(texts('mat-card-subtitle')).toEqual(['What you hold on each resource.']);
    });

    it('draws no header for a page that titles the card itself', async () => {
      await draw(marshal);

      expect(element.querySelector('mat-card-header')).toBeNull();
    });

    it('says so when it lists nothing', async () => {
      await draw([]);

      expect(texts('tbody td.nothing-held')).toEqual(['You hold nothing here.']);
      expect(element.querySelector('tbody td.nothing-held')?.getAttribute('colspan')).toBe('6');
    });

    it("says a page's own words when it lists nothing, and draws what else the page writes under the table", async () => {
      const page = TestBed.createComponent(OwnWordsPageComponent);
      await settle(page);
      const drawn = page.nativeElement as HTMLElement;

      expect(drawn.querySelector('tbody td.nothing-held')?.textContent?.trim()).toBe(
        'No resources yet. Add one and come back.',
      );
      expect(drawn.textContent).not.toContain('You hold nothing here.');
      // The page's own line is inside the card, after the table.
      const under = drawn.querySelector('mat-card-content .under');
      expect(under?.textContent).toBe('Methods you may run');
      const position = drawn.querySelector('table.permissions')?.compareDocumentPosition(under as Node) ?? 0;
      expect(position & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    it('is a table where nothing reports a width: a test, a page drawn on the server', async () => {
      await draw(marshal);

      expect(fixture.componentInstance.layout()).toBe('table');
      expect(table().classList.contains('blocks')).toBe(false);
    });
  });

  describe('laid out by its own width', () => {
    /** What a ResizeObserver was asked to watch, each with the call that reports a new size. */
    let watched: { target: Element; report: () => void }[];

    class CardObserver {
      constructor(private readonly report: () => void) {}

      observe(target: Element): void {
        watched.push({ target, report: this.report });
      }

      unobserve(target: Element): void {
        watched = watched.filter((entry) => entry.report !== this.report || entry.target !== target);
      }

      disconnect(): void {
        watched = watched.filter((entry) => entry.report !== this.report);
      }
    }

    beforeEach(async () => {
      watched = [];
      (globalThis as { ResizeObserver?: unknown }).ResizeObserver = CardObserver;
      await TestBed.configureTestingModule({ imports: [PermissionsCardComponent] }).compileComponents();
    });

    afterEach(() => {
      delete (globalThis as { ResizeObserver?: unknown }).ResizeObserver;
    });

    /**
     * The browser lays the card out with this much room, and says so: the report alone,
     * as the browser makes it between laying the page out and painting it.
     */
    const reportRoom = (width: number): void => {
      Object.defineProperty(mark(), 'clientWidth', { configurable: true, value: width });
      const reports = watched.filter((entry) => entry.target === mark());
      expect(reports.length).toBeGreaterThan(0);
      reports.forEach((entry) => entry.report());
    };
    /** The browser lays the card out with this much room, says so, and the page is drawn again. */
    const roomIs = (width: number): void => {
      reportRoom(width);
      fixture.detectChanges();
    };
    /** The table's columns, each cell on one line, take this much: the table is drawn this wide or wider. */
    const tableNeeds = (width: number): void => {
      Object.defineProperty(table(), 'offsetWidth', {
        configurable: true,
        get: () => Math.max(width, mark().clientWidth),
      });
    };
    const layout = (): string => {
      return table().classList.contains('blocks') ? 'blocks' : 'table';
    };

    it('is a table while its columns fit the card', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(900);

      expect(layout()).toBe('table');
      expect(fixture.componentInstance.layout()).toBe('table');
      roomIs(640);
      expect(layout()).toBe('table');
    });

    it('draws each resource as a block where its columns do not fit', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(358);

      expect(layout()).toBe('blocks');
      expect(fixture.componentInstance.layout()).toBe('blocks');
      // The same words as the table's: every resource, and every permission named beside its answer.
      expect(rows().map((row) => row.name)).toEqual(['Missions Anvil', 'Ships Anvil', 'Tenants']);
      expect(rows()[1]).toEqual({
        name: 'Ships Anvil',
        answers: ['terms apply', 'terms apply', '—', '—', '—'],
        named: ['List', 'Read', 'Create', 'Update', 'Delete'],
      });
    });

    it('as blocks, hides the column headings, names each permission beside its answer, and wraps', async () => {
      await draw(marshal);

      // In the table a permission's name is its column heading alone.
      expect(declares(['.permissions', '.permission'], ['.blocks'])).toContain('display:none');
      expect(declares(['.blocks', 'thead'])).toContain('display:none');
      expect(declares(['.blocks', '.permission'])).toContain('display:inline');
      expect(declares(['.blocks', 'tr'])).toContain('flex-wrap:wrap');
      // The name takes a line of its own, and a long one breaks instead of running past the edge.
      const name = declares(['.blocks', '.resource-name']);
      expect(name).toContain('flex:00100%');
      expect(name).toContain('overflow-wrap:anywhere');
    });

    it('stays as blocks until the card is as wide as its table needed, then is a table again', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(358);
      expect(layout()).toBe('blocks');

      roomIs(639);
      expect(layout()).toBe('blocks');
      roomIs(640);
      expect(layout()).toBe('table');
      roomIs(639);
      expect(layout()).toBe('blocks');
    });

    it('changes over inside the report of a new width, so no frame paints the wrong layout', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(900);
      expect(layout()).toBe('table');

      // The browser reports a width after it lays the page out and before it paints. The
      // card is drawn again by the time the report returns, with nothing else run between.
      reportRoom(358);
      expect(layout()).toBe('blocks');
      reportRoom(900);
      expect(layout()).toBe('table');
    });

    it('watches a mark of its own width that no layout resizes, and its table only while it is one', async () => {
      await draw(marshal);
      tableNeeds(640);
      const targets = (): Element[] => watched.map((entry) => entry.target);
      // The mark has no height, as a table or as blocks: a report drawn inside itself
      // resizes nothing the browser was asked to report on, the frame least of all.
      expect(declares(['.width-mark'])).toContain('height:0');
      expect(targets()).toContain(mark());
      expect(targets()).not.toContain(frame());

      roomIs(900);
      expect(targets()).toContain(table());
      // As blocks the table's width says nothing, and the changeover itself resized it.
      reportRoom(358);
      expect(targets()).not.toContain(table());
      expect(targets()).toContain(mark());
      // A table again is watched again.
      reportRoom(900);
      expect(targets().filter((target) => target === table())).toHaveLength(1);
    });

    it("is laid out by the card's width, whatever the window's", async () => {
      await draw(marshal);
      tableNeeds(640);
      const wide = window.innerWidth;
      expect(wide).toBeGreaterThan(640);

      // A wide window whose page sets the card beside something else leaves it little room.
      roomIs(520);
      expect(layout()).toBe('blocks');
      expect(window.innerWidth).toBe(wide);
      // No rule of the card asks how wide the window is.
      const sheet = Array.from(document.querySelectorAll('style'))
        .map((style) => style.textContent ?? '')
        .filter((text) => text.includes('.permissions'))
        .join('\n');
      expect(sheet).toContain('.permissions');
      expect(sheet).not.toContain('@media');
    });

    it('measures the width its own content needs, so a page with more to say changes over sooner', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(700);
      expect(layout()).toBe('table');

      // A sixth permission makes the table need more than the card has.
      tableNeeds(760);
      fixture.componentRef.setInput('permissions', [...READ_AND_WRITE, 'Execute']);
      await settle(fixture);
      expect(layout()).toBe('blocks');

      // Two permissions need less than the card has: a table again, though the card is no wider.
      tableNeeds(300);
      fixture.componentRef.setInput('permissions', ['List', 'Read']);
      await settle(fixture);
      expect(layout()).toBe('table');
    });

    it('measures again when the table itself changes width, as when a font arrives', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(700);
      expect(layout()).toBe('table');

      tableNeeds(720);
      const reports = watched.filter((entry) => entry.target === table());
      expect(reports.length).toBeGreaterThan(0);
      reports.forEach((entry) => entry.report());
      fixture.detectChanges();
      expect(layout()).toBe('blocks');
    });

    it('is left as it is while it has no width: a card not on the screen', async () => {
      await draw(marshal);
      tableNeeds(640);
      roomIs(0);

      expect(layout()).toBe('table');
    });

    it('has no frame that scrolls sideways, and its table never widens the page', async () => {
      await draw(marshal);

      // The frame is as wide as the card gives it, never as wide as the table it holds.
      expect(declares(['.frame'])).toContain('contain:inline-size');
      // Nothing of the card scrolls: no rule of its own sets an overflow.
      expect(declares([])).not.toMatch(/overflow(-x|-y|-inline|-block)?:/);
      // A table cell stays on one line, so the table's width is the width its columns need.
      expect(declares(['.permissions', 'td'], ['.blocks'])).toContain('white-space:nowrap');
    });

    it('stops watching when the card leaves the page', async () => {
      await draw(marshal);
      expect(watched.length).toBeGreaterThan(0);

      fixture.destroy();
      expect(watched).toEqual([]);
    });
  });
});
