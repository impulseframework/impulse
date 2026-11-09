import { Directive } from '@angular/core';
import { ResourceStore } from './resource-store.service';

/**
 * A store of its own for a row view outside the compound page's primary slot. A
 * `impulse-resource-view` injects the nearest ResourceStore and provides none itself: the
 * view over the page's own row shares the compound page's store, so what the form
 * writes is what every other child of the page reads. A view over a different row (the
 * compound's related-row branch, a view an application places by hand) sits on an
 * element carrying this directive, which provides the store that view reads and writes.
 * A view with no store in scope fails at construction naming ResourceStore.
 *
 *     <impulse-resource-view impulseRowStore [uuid]="wingId" [config]="wingView" [relatedData]="row" />
 */
@Directive({
  selector: '[impulseRowStore]',
  providers: [ResourceStore],
})
export class RowStoreDirective {}
