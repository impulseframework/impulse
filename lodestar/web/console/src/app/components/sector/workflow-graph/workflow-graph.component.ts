import { Component, computed, DestroyRef, ElementRef, inject, input, output, signal } from '@angular/core';
import { Workflows } from '@app/service/zz_gen_resources';
import { Method, Resource } from '@impulseframework/resource';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { directionFor, GraphEdge, GraphNode, layoutWorkflow } from './workflow-layout';

/** An edge as the graph draws it: where the layout put it, and whether it is lit for this person now. */
interface Edge extends GraphEdge {
  live: boolean;
}

/**
 * WorkflowGraphComponent draws one workflow — its states as a route and every declared
 * transition as an edge — from the generated Workflows constant, so nothing here is a
 * hand-maintained copy of server truth. The current state is lit; the edges the row's
 * capability envelope says the session user may fire NOW are drawn live and clickable;
 * every other edge is faint but present. That is the DOT file's own rule (facts drawn,
 * policy not) carried into the product: the graph is a fact about the workflow, the
 * live edges are a fact about you.
 *
 * An edge is drawn in words, never as its method's name: the words its page gives it
 * (`words`), and where the page gives none the name in words, as the library's pages
 * show a method ("Begin refit" for BeginRefit).
 *
 * No label is drawn on another, and no line crosses another line's label: the layout
 * (layoutWorkflow) settles every line first and then gives each label a place clear of
 * the rest, since a label is the only way to tell which edge is which.
 *
 * The graph is drawn across where it has the width, and top to bottom where it has not:
 * it measures its own width (the card it sits in, never the window), and turns when that
 * is under the layout's downUnder.
 *
 * Demonstrates: workflow.ts-constant, capability-envelope.
 */
@Component({
  selector: 'app-workflow-graph',
  templateUrl: './workflow-graph.component.html',
  styleUrl: './workflow-graph.component.scss',
})
export class WorkflowGraphComponent {
  /** The workflow root resource (Missions or Refits). */
  root = input.required<Resource>();
  /** The row's current state. */
  current = input.required<string>();
  /** The methods the row's Execute envelope lists — the edges lit for this user. */
  executable = input<readonly string[]>([]);
  /** What the page calls a method; a page that gives no words gets each method's name in words. */
  words = input<(method: string) => string>(nameInWords);
  /** Fires when a live edge is clicked. */
  fire = output<Method>();

  private readonly host = inject<ElementRef<HTMLElement>>(ElementRef);
  /** How many times the graph's own width has changed: each time, the way it is drawn is reckoned again. */
  private readonly resized = signal(0);

  private workflow = computed(() => Workflows.find((w) => w.root === this.root()));

  // Which way the graph goes, from the width it has now. It is read while the graph is
  // first drawn, so the graph is never drawn one way and then the other.
  private direction = computed(() => {
    this.resized();
    return directionFor(this.host.nativeElement.getBoundingClientRect().width);
  });

  // Where everything sits: the states as a route, the edges and their labels, none on another.
  private layout = computed(() => {
    const workflow = this.workflow();
    return workflow ? layoutWorkflow(workflow, this.words(), this.direction()) : undefined;
  });

  readonly width = computed(() => this.layout()?.width ?? 620);
  readonly height = computed(() => this.layout()?.height ?? 170);
  /** Whether the graph is drawn top to bottom. Then it is never drawn larger than its own size. */
  readonly down = computed(() => this.layout()?.direction === 'down');

  nodes = computed<GraphNode[]>(() => this.layout()?.nodes ?? []);

  edges = computed<Edge[]>(() => {
    const executable = new Set(this.executable());
    const current = this.current();
    return (this.layout()?.edges ?? []).map((edge) => ({
      ...edge,
      live: executable.has(edge.method) && edge.from === current,
    }));
  });

  constructor() {
    if (typeof ResizeObserver === 'undefined') {
      return;
    }
    const observer = new ResizeObserver(() => this.resized.update((times) => times + 1));
    observer.observe(this.host.nativeElement);
    inject(DestroyRef).onDestroy(() => observer.disconnect());
  }

  label(state: string): string {
    return state.replace('_', ' ');
  }

  emit(edge: Edge): void {
    if (edge.live) {
      this.fire.emit(edge.method as Method);
    }
  }
}
