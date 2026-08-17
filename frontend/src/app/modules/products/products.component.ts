import { Component, DestroyRef, OnInit, WritableSignal, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { Subject, catchError, debounceTime, distinctUntilChanged, of, startWith, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { BULK_MAX_ITEMS, BulkResponse } from 'src/app/core/models/bulk.model';
import { NewProduct, Product } from 'src/app/core/models/product.model';
import { ProductFilters, ProductService, StockAdjustment } from 'src/app/core/services/product.service';
import { BulkResultComponent } from 'src/app/shared/components/bulk-result/bulk-result.component';
import { ProductFormComponent } from './product-form.component';

/** How long the screen waits after a keystroke before searching. */
const SEARCH_DEBOUNCE_MS = 300;

/**
 * Product catalogue: lists what is in stock and lets the operator register a
 * product or fix its description and balance.
 */
@Component({
  selector: 'app-products',
  imports: [ReactiveFormsModule, AngularSvgIconModule, ProductFormComponent, BulkResultComponent],
  templateUrl: './products.component.html',
})
export class ProductsComponent implements OnInit {
  private readonly products = inject(ProductService);
  private readonly destroyRef = inject(DestroyRef);

  readonly searchControl = new FormControl('', { nonNullable: true });

  /** Only what is out of stock, which is what a replenishment run looks for. */
  readonly outOfStockOnly = signal(false);
  /** Lowest balance first, to see what needs attention. */
  readonly lowestBalanceFirst = signal(false);

  readonly items = signal<Product[]>([]);
  readonly loading = signal(false);
  readonly loadingMore = signal(false);
  readonly loadFailure = signal<ApiError | null>(null);

  /** Cursor of the next page, empty when the whole catalogue was read. */
  private readonly nextCursor = signal('');
  readonly hasMore = computed(() => this.nextCursor() !== '');

  readonly formOpen = signal(false);
  readonly editing = signal<Product | undefined>(undefined);
  readonly saving = signal(false);
  readonly saveFailure = signal<ApiError | null>(null);

  /**
   * Products picked for a stock adjustment. The selection holds ids, so it
   * survives reading another page.
   */
  readonly selected = signal<ReadonlySet<string>>(new Set());
  readonly selectedCount = computed(() => this.selected().size);
  readonly maxSelectable = BULK_MAX_ITEMS;
  readonly selectionFull = computed(() => this.selectedCount() >= BULK_MAX_ITEMS);

  /** One line per selected product while the adjustment is being written. */
  readonly adjustmentOpen = signal(false);
  readonly drafts = signal<AdjustmentDraft[]>([]);
  readonly reasonControl = new FormControl('', { nonNullable: true });
  readonly applying = signal(false);
  readonly adjustmentResult = signal<BulkResponse | null>(null);

  /**
   * The lines actually worth sending: a blank delta means the operator picked
   * the product but is not moving it, which is not an error.
   */
  readonly pendingAdjustments = computed(() =>
    this.drafts().flatMap((draft) => {
      const raw = draft.delta().trim();
      const delta = Number(raw);
      if (raw === '' || !Number.isInteger(delta) || delta === 0) {
        return [];
      }
      return [{ product: draft.product, delta }];
    }),
  );

  readonly canApply = computed(() => this.pendingAdjustments().length > 0 && !this.applying());

  /**
   * The key that makes a retry safe.
   *
   * It stays the same while the movements do, so resending after a lost answer
   * cannot apply the delivery note twice, and it is replaced as soon as the
   * operator changes a line: reusing it for different movements would be
   * refused by the service as a key reused with another payload.
   */
  private readonly idempotency = signal<{ key: string; fingerprint: string } | null>(null);

  /** Emits whenever the list has to be read again. */
  private readonly reload = new Subject<void>();

  ngOnInit(): void {
    // Typing filters the list, but only after the operator pauses, and a
    // repeated term never triggers a second request. switchMap drops the
    // answer of a search that was already replaced by a newer one.
    this.searchControl.valueChanges
      .pipe(
        debounceTime(SEARCH_DEBOUNCE_MS),
        distinctUntilChanged(),
        startWith(this.searchControl.value),
        switchMap((term) => this.fetch(term)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();

    this.reload
      .pipe(
        switchMap(() => this.fetch(this.searchControl.value)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();
  }

  /** The filters currently applied to the listing. */
  private currentFilters(search = this.searchControl.value): ProductFilters {
    // Defaults are left out of the request, so the URL only carries what was
    // actually asked for.
    return {
      search,
      maxBalance: this.outOfStockOnly() ? 0 : undefined,
      sort: this.lowestBalanceFirst() ? 'balance' : undefined,
    };
  }

  /** Reads the first page of the catalogue for the current filters. */
  private fetch(search: string) {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.products.list(this.currentFilters(search)).pipe(
      tap((page) => {
        this.items.set(page.items);
        this.nextCursor.set(page.nextCursor);
        this.loading.set(false);
      }),
      catchError((error: ApiError) => {
        this.loading.set(false);
        this.loadFailure.set(error);
        return of({ items: [], nextCursor: '' });
      }),
    );
  }

  /** Appends the next page to what is already on screen. */
  loadMore(): void {
    const cursor = this.nextCursor();
    if (!cursor || this.loadingMore()) {
      return;
    }

    this.loadingMore.set(true);

    this.products
      .list(this.currentFilters(), cursor)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.items.update((products) => [...products, ...page.items]);
          this.nextCursor.set(page.nextCursor);
          this.loadingMore.set(false);
        },
        error: (error: ApiError) => {
          this.loadingMore.set(false);
          this.loadFailure.set(error);
        },
      });
  }

  /** Turns the out of stock filter on or off and reads the listing again. */
  toggleOutOfStock(): void {
    this.outOfStockOnly.update((only) => !only);
    this.reload.next();
  }

  /** Switches between catalogue order and lowest balance first. */
  toggleLowestBalanceFirst(): void {
    this.lowestBalanceFirst.update((lowest) => !lowest);
    this.reload.next();
  }

  openCreateForm(): void {
    this.editing.set(undefined);
    this.saveFailure.set(null);
    this.formOpen.set(true);
  }

  openEditForm(product: Product): void {
    this.editing.set(product);
    this.saveFailure.set(null);
    this.formOpen.set(true);
  }

  closeForm(): void {
    this.formOpen.set(false);
    this.editing.set(undefined);
    this.saveFailure.set(null);
  }

  onSave(input: NewProduct): void {
    const editing = this.editing();
    this.saving.set(true);
    this.saveFailure.set(null);

    const request = editing
      ? this.products.update(editing.id, {
          description: input.description,
          balance: input.balance,
          // The version the form was opened with: if an invoice debited this
          // product in the meantime, the service refuses the write instead of
          // putting the sold stock back.
          version: editing.version,
        })
      : this.products.create(input);

    request.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: (product) => {
        this.saving.set(false);
        this.closeForm();
        toast.success(editing ? `Product ${product.code} updated.` : `Product ${product.code} registered.`, {
          position: 'bottom-right',
        });
        this.reload.next();
      },
      error: (error: ApiError) => {
        this.saving.set(false);
        this.saveFailure.set(error);
        // Field level messages are shown by the form; this is the summary.
        toast.error(error.message, { position: 'bottom-right' });
      },
    });
  }

  retry(): void {
    this.reload.next();
  }

  isSelected(id: string): boolean {
    return this.selected().has(id);
  }

  /** Adds or removes one product from the adjustment. */
  toggleSelection(id: string): void {
    this.selected.update((current) => {
      const next = new Set(current);
      if (!next.delete(id) && next.size < BULK_MAX_ITEMS) {
        next.add(id);
      }
      return next;
    });
  }

  clearSelection(): void {
    this.selected.set(new Set());
  }

  /** Opens the adjustment with one line per selected product. */
  openAdjustments(): void {
    const chosen = this.items().filter((product) => this.selected().has(product.id));

    this.drafts.set(chosen.map((product) => ({ product, delta: signal('') })));
    this.reasonControl.setValue('');
    this.adjustmentResult.set(null);
    this.adjustmentOpen.set(true);
  }

  closeAdjustments(): void {
    this.adjustmentOpen.set(false);
    this.drafts.set([]);
    this.adjustmentResult.set(null);
  }

  /** Reads what was typed for one line. */
  onDeltaInput(draft: AdjustmentDraft, value: string): void {
    draft.delta.set(value);
  }

  dismissAdjustmentResult(): void {
    this.adjustmentResult.set(null);
  }

  /**
   * Applies every movement at once.
   *
   * The lines belong to one document, so the service applies all of them or
   * none. When it refuses, the panel stays open with what was typed: the
   * operator fixes the offending line and sends again.
   */
  applyAdjustments(): void {
    const lines = this.pendingAdjustments();
    if (lines.length === 0 || this.applying()) {
      return;
    }

    const reason = this.reasonControl.value.trim();
    const adjustments: StockAdjustment[] = lines.map((line) => ({
      productId: line.product.id,
      delta: line.delta,
      reason: reason || undefined,
    }));

    this.applying.set(true);
    this.adjustmentResult.set(null);

    this.products
      .adjustBalances(adjustments, this.idempotencyKeyFor(adjustments))
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (response) => {
          this.applying.set(false);

          if (response.summary.failed === 0) {
            // Applied: the key must not be reused for the next document.
            this.idempotency.set(null);
            this.closeAdjustments();
            this.clearSelection();
            toast.success(`${response.summary.succeeded} balance(s) adjusted.`, { position: 'bottom-right' });
            this.reload.next();
            return;
          }

          // Nothing was applied. What was typed stays on screen next to the
          // reason it was refused.
          this.adjustmentResult.set(this.withProductCodes(response));
        },
        error: (error: ApiError) => {
          this.applying.set(false);
          toast.error(error.message, { position: 'bottom-right' });
        },
      });
  }

  /**
   * Names each result by its product code.
   *
   * The movements are sent by id, so that is what the service can report back
   * for a line it could not apply. The operator reads codes, not identifiers.
   */
  private withProductCodes(response: BulkResponse): BulkResponse {
    const codes = new Map(this.drafts().map((draft) => [draft.product.id, draft.product.code]));

    return {
      ...response,
      results: response.results.map((result) => {
        const code = codes.get(result.id || result.reference || '');
        return code === undefined ? result : { ...result, reference: code };
      }),
    };
  }

  /**
   * The idempotency key for these movements: kept while they are unchanged so
   * a retry is safe, replaced as soon as they change.
   */
  private idempotencyKeyFor(adjustments: StockAdjustment[]): string {
    const fingerprint = JSON.stringify(adjustments);
    const current = this.idempotency();
    if (current?.fingerprint === fingerprint) {
      return current.key;
    }

    const key = crypto.randomUUID();
    this.idempotency.set({ key, fingerprint });
    return key;
  }
}

/** One product being moved while the adjustment is written. */
export interface AdjustmentDraft {
  product: Product;
  /** What is typed, kept as text so an empty field means "not moving this one". */
  delta: WritableSignal<string>;
}
