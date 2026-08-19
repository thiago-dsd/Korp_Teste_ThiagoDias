import { Component, DestroyRef, OnInit, computed, inject, input, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';

import { ApiError } from 'src/app/core/models/api-error.model';
import { Product, StockMovement } from 'src/app/core/models/product.model';
import { ProductService } from 'src/app/core/services/product.service';
import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { LocaleDatePipe, LocaleNumberPipe } from 'src/app/core/i18n/intl.pipe';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';

/**
 * Why the balance of a product is what it is.
 *
 * The balance on the listing answers "how much is there" and stops at that.
 * The first question after a stock count that does not match is "who took it",
 * and until now the only honest answer was that nobody knew.
 */
@Component({
  selector: 'app-product-movements',
  imports: [TranslatePipe, ApiErrorPipe, LocaleDatePipe, LocaleNumberPipe],
  templateUrl: './product-movements.component.html',
})
export class ProductMovementsComponent implements OnInit {
  private readonly products = inject(ProductService);
  private readonly destroyRef = inject(DestroyRef);
  private readonly i18n = inject(TranslateService);

  readonly product = input.required<Product>();
  readonly closed = output<void>();

  readonly items = signal<StockMovement[]>([]);
  readonly loading = signal(false);
  readonly loadingMore = signal(false);
  readonly failure = signal<ApiError | null>(null);

  private readonly nextCursor = signal('');
  readonly hasMore = computed(() => this.nextCursor() !== '');

  ngOnInit(): void {
    // The panel is created when it is opened, so loading on init is loading
    // when it is shown. It cannot be the constructor: a required input has no
    // value yet at that point.
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.failure.set(null);

    this.products
      .listMovements(this.product().id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.items.set(page.items);
          this.nextCursor.set(page.nextCursor);
          this.loading.set(false);
        },
        error: (error: ApiError) => {
          this.loading.set(false);
          this.failure.set(error);
        },
      });
  }

  loadMore(): void {
    const cursor = this.nextCursor();
    if (!cursor || this.loadingMore()) {
      return;
    }

    this.loadingMore.set(true);

    this.products
      .listMovements(this.product().id, cursor)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.items.update((current) => [...current, ...page.items]);
          this.nextCursor.set(page.nextCursor);
          this.loadingMore.set(false);
        },
        error: (error: ApiError) => {
          this.loadingMore.set(false);
          this.failure.set(error);
        },
      });
  }

  /** What caused the movement, in the words an operator uses. */
  labelFor(movement: StockMovement): string {
    switch (movement.source) {
      case 'registration':
        return this.i18n.t('productMovements.sources.registration');
      case 'edit':
        return this.i18n.t('productMovements.sources.edit');
      case 'adjustment':
        return this.i18n.t(
          movement.delta > 0
            ? 'productMovements.sources.adjustmentReceived'
            : 'productMovements.sources.adjustmentRemoved',
        );
      case 'invoice':
        return this.i18n.t('productMovements.sources.invoice');
    }
  }

  /** Who to credit it to: a person, or the invoice that caused it. */
  actorFor(movement: StockMovement): string {
    if (movement.actorEmail) {
      return movement.actorEmail;
    }
    return this.i18n.t(
      movement.source === 'invoice' ? 'productMovements.actorInvoice' : 'productMovements.actorSystem',
    );
  }

  close(): void {
    this.closed.emit();
  }
}
