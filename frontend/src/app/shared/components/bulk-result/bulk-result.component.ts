import { ChangeDetectionStrategy, Component, computed, inject, input, output } from '@angular/core';

import { BulkResponse, bulkFailures } from 'src/app/core/models/bulk.model';
import { translateApiDetails, translateErrorCode } from 'src/app/core/i18n/error-translation';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';

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
  imports: [TranslatePipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './bulk-result.component.html',
})
export class BulkResultComponent {
  private readonly i18n = inject(TranslateService);

  readonly response = input.required<BulkResponse>();

  /**
   * Key of the noun the items are called on the calling screen, under
   * `nouns.*` — `nouns.invoice`, not the word "invoice" itself, so the
   * headline can say "nota"/"notas" in Portuguese and "invoice"/"invoices" in
   * English from the same call site.
   */
  readonly nounKey = input('nouns.product');

  readonly dismiss = output<void>();

  /** Only the refused items are listed; the ones that worked need no words. */
  readonly failures = computed(() => bulkFailures(this.response()));

  /** True when the call was refused as a whole and nothing was applied. */
  readonly rolledBack = computed(() => this.response().atomic && this.response().summary.succeeded === 0);

  private nounForm(count: number): string {
    return this.i18n.plural(this.nounKey(), count);
  }

  readonly headline = computed(() => {
    const summary = this.response().summary;
    // The noun in the headline agrees with the request size, the same
    // grammatical number the original English always used here.
    const noun = this.nounForm(summary.requested);

    if (this.rolledBack()) {
      return this.i18n.t('bulkResult.nothingApplied');
    }
    if (summary.failed === 0) {
      return this.i18n.plural('bulkResult.allWentThrough', summary.requested, { noun });
    }
    return this.i18n.plural('bulkResult.someWentThrough', summary.requested, { succeeded: summary.succeeded, noun });
  });

  readonly explanation = computed(() => {
    // The explanation always talks about the category, not a specific count,
    // so it always reads in the plural — "these invoices", never "this 1 invoice".
    const noun = this.nounForm(2);

    if (this.rolledBack()) {
      return this.i18n.t('bulkResult.rolledBackExplanation', { noun });
    }
    if (this.response().summary.failed > 0) {
      return this.i18n.t('bulkResult.refusedExplanation', { noun });
    }
    return '';
  });

  /** Whether the panel reports a problem, which decides how it is coloured. */
  readonly hasProblem = computed(() => this.response().summary.failed > 0);

  /** "Item 3" for a refused line the service could not name. */
  itemFallback(index: number): string {
    return this.i18n.t('bulkResult.itemFallback', { index: index + 1 });
  }

  /** The friendly message for one refused item. */
  failureMessage(code: string | undefined): string {
    return code ? translateErrorCode(this.i18n, code) : '';
  }

  /** The "field: reason" lines under a refused item, both sides translated. */
  failureDetails(details: Record<string, string> | undefined): { key: string; value: string }[] {
    return translateApiDetails(this.i18n, details);
  }
}
