import { KeyValuePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input, output } from '@angular/core';

import { BulkResponse, bulkFailures } from 'src/app/core/models/bulk.model';

/**
 * The outcome of a bulk call, written for the operator who is waiting.
 *
 * A toast cannot carry a hundred items, and the part worth reading is never
 * the whole list: it is the summary and the items that did not go through. An
 * atomic call that was refused gets a different headline, because "nothing was
 * applied" and "three of your items failed" ask for different next steps.
 */
@Component({
  selector: 'app-bulk-result',
  imports: [KeyValuePipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './bulk-result.component.html',
})
export class BulkResultComponent {
  readonly response = input.required<BulkResponse>();

  /** What the items are called on the calling screen, e.g. "invoice". */
  readonly noun = input('item');

  readonly dismiss = output<void>();

  /** Only the refused items are listed; the ones that worked need no words. */
  readonly failures = computed(() => bulkFailures(this.response()));

  /** True when the call was refused as a whole and nothing was applied. */
  readonly rolledBack = computed(() => this.response().atomic && this.response().summary.succeeded === 0);

  readonly headline = computed(() => {
    const summary = this.response().summary;

    if (this.rolledBack()) {
      return 'Nothing was applied.';
    }
    if (summary.failed === 0) {
      return `All ${summary.requested} ${this.plural(summary.requested)} went through.`;
    }
    return `${summary.succeeded} of ${summary.requested} ${this.plural(summary.requested)} went through.`;
  });

  readonly explanation = computed(() => {
    if (this.rolledBack()) {
      return `These ${this.noun()}s belong to one document, so a single item that cannot be applied stops all of them. Fix it below and send again.`;
    }
    if (this.response().summary.failed > 0) {
      return `The ${this.noun()}s below were refused and are still selected, so you can correct them and try again.`;
    }
    return '';
  });

  /** Whether the panel reports a problem, which decides how it is coloured. */
  readonly hasProblem = computed(() => this.response().summary.failed > 0);

  private plural(count: number): string {
    return count === 1 ? this.noun() : `${this.noun()}s`;
  }
}
