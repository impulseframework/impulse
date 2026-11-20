import { Component, computed, input, output } from '@angular/core';
import { Workflows } from '@app/service/zz_gen_resources';
import { Method, Resource } from '@impulseframework/resource';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { GraphEdge, GraphNode, layoutWorkflow } from './workflow-layout';

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
 * No two labels are drawn on one another: the layout (layoutWorkflow) bows a longer arc
 * further than a shorter one and moves a label along its line until it is clear of the
 * others, since a label is the only way to tell which edge is which.
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

  private workflow = computed(() => Workflows.find((w) => w.root === this.root()));

  // Where everything sits: the states as a route, the edges and their labels, none on another.
  private layout = computed(() => {
    const workflow = this.workflow();
    return workflow ? layoutWorkflow(workflow, this.words()) : undefined;
  });

  readonly width = computed(() => this.layout()?.width ?? 620);
  readonly height = computed(() => this.layout()?.height ?? 170);

  nodes = computed<GraphNode[]>(() => this.layout()?.nodes ?? []);

  edges = computed<Edge[]>(() => {
    const executable = new Set(this.executable());
    const current = this.current();
    return (this.layout()?.edges ?? []).map((edge) => ({
      ...edge,
      live: executable.has(edge.method) && edge.from === current,
    }));
  });

  label(state: string): string {
    return state.replace('_', ' ');
  }

  emit(edge: Edge): void {
    if (edge.live) {
      this.fire.emit(edge.method as Method);
    }
  }
}
