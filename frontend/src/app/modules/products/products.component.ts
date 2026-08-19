import {
  Component,
  DestroyRef,
  ElementRef,
  HostListener,
  OnInit,
  WritableSignal,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { catchError, debounceTime, distinctUntilChanged, map, of, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { BULK_MAX_ITEMS, BulkResponse } from 'src/app/core/models/bulk.model';
import { NewProduct, Product } from 'src/app/core/models/product.model';
import { AuthService } from 'src/app/core/services/auth.service';
import { ProductFilters, ProductService, StockAdjustment } from 'src/app/core/services/product.service';
import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { translateErrorCode } from 'src/app/core/i18n/error-translation';
import { LocaleNumberPipe } from 'src/app/core/i18n/intl.pipe';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';
import { BulkResultComponent } from 'src/app/shared/components/bulk-result/bulk-result.component';
import { ModalComponent } from 'src/app/shared/components/modal/modal.component';
import { LOW_STOCK_THRESHOLD, StockLevelComponent } from 'src/app/shared/components/stock-level/stock-level.component';
import { csvFilename, downloadCsv, toCsv } from 'src/app/shared/utils/csv';
import { ProductFormComponent } from './product-form.component';
import { ProductImportComponent } from './product-import.component';
import { ProductMovementsComponent } from './product-movements.component';

/** How long the screen waits after a keystroke before searching. */
const SEARCH_DEBOUNCE_MS = 300;

/** Which part of the catalogue is being looked at. */
export type StockFilter = 'all' | 'low' | 'out';

/** Everything that decides which products are on screen. */
interface ListingState {
  search: string;
  stock: StockFilter;
  /** Lowest balance first rather than catalogue order. */
  byBalance: boolean;
}

/**
 * Product catalogue: lists what is in stock and lets an administrator register
 * a product, correct it, import a whole catalogue or move balances.
 *
 * The filters live in the URL rather than in the component. Reloading after a
 * refused adjustment used to drop the operator back at an unfiltered list, and
 * "the ones that are running out" could not be sent to anybody.
 */
@Component({
  selector: 'app-products',
  imports: [
    ReactiveFormsModule,
    AngularSvgIconModule,
    ProductFormComponent,
    ProductImportComponent,
    ProductMovementsComponent,
    BulkResultComponent,
    ModalComponent,
    StockLevelComponent,
    TranslatePipe,
    ApiErrorPipe,
    LocaleNumberPipe,
  ],
  templateUrl: './products.component.html',
})
export class ProductsComponent implements OnInit {
  private readonly products = inject(ProductService);
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);
  /** Public: the template calls `i18n.plural()` directly for count-driven text. */
  readonly i18n = inject(TranslateService);

  /** Managing the catalogue is kept to administrators by the service. */
  readonly canManageCatalogue = this.auth.isAdmin;

  readonly lowStockThreshold = LOW_STOCK_THRESHOLD;

  readonly searchControl = new FormControl('', { nonNullable: true });
  private readonly searchField = viewChild<ElementRef<HTMLInputElement>>('searchField');

  /** Which part of the catalogue the listing is showing. */
  readonly stockFilter = signal<StockFilter>('all');
  /** Lowest balance first, to see what needs attention. */
  readonly lowestBalanceFirst = signal(false);

  readonly hasActiveFilters = computed(
    () => this.stockFilter() !== 'all' || this.lowestBalanceFirst() || this.searchControl.value.trim() !== '',
  );

  readonly items = signal<Product[]>([]);
  readonly loading = signal(false);
  readonly loadingMore = signal(false);
  readonly loadFailure = signal<ApiError | null>(null);

  /** Cursor of the next page, empty when the whole catalogue was read. */
  private readonly nextCursor = signal('');
  readonly hasMore = computed(() => this.nextCursor() !== '');

  /**
   * What the table is showing right now.
   *
   * The listing is cursor-paged, so there is no total to count towards —
   * the number of rows loaded so far is the honest thing to state, and the
   * footer says whether more can be fetched.
   */
  readonly showingLabel = computed(() => {
    const count = this.items().length;
    return this.i18n.t('common.showing', {
      count,
      noun: this.i18n.plural('nouns.product', count),
    });
  });

  readonly formOpen = signal(false);
  readonly editing = signal<Product | undefined>(undefined);
  readonly saving = signal(false);
  readonly saveFailure = signal<ApiError | null>(null);

  readonly importOpen = signal(false);
  /** The product whose stock history is open, if any. */
  readonly historyOf = signal<Product | null>(null);

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

  /**
   * "/" jumps to the search box, the way it does in every tool an operator
   * already uses. It is ignored while something is being typed, so a slash in
   * a description does not teleport the cursor.
   */
  @HostListener('document:keydown', ['$event'])
  onKeydown(event: KeyboardEvent): void {
    if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) {
      return;
    }
    const active = document.activeElement;
    if (
      active instanceof HTMLInputElement ||
      active instanceof HTMLTextAreaElement ||
      active instanceof HTMLSelectElement
    ) {
      return;
    }

    event.preventDefault();
    this.searchField()?.nativeElement.focus();
  }

  ngOnInit(): void {
    // The URL is the single source of truth for what is on screen: the query
    // string is read, the listing follows it, and every control writes back to
    // it. That is what makes a filtered listing survive a reload and be worth
    // sending to somebody.
    this.route.queryParamMap
      .pipe(
        map((params) => readState(params.get('search'), params.get('stock'), params.get('sort'))),
        distinctUntilChanged((a, b) => a.search === b.search && a.stock === b.stock && a.byBalance === b.byBalance),
        tap((state) => this.applyState(state)),
        switchMap(() => this.fetch()),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();

    // Typing filters the list, but only after the operator pauses, and a
    // repeated term never triggers a second navigation.
    this.searchControl.valueChanges
      .pipe(debounceTime(SEARCH_DEBOUNCE_MS), distinctUntilChanged(), takeUntilDestroyed(this.destroyRef))
      .subscribe(() => this.writeState());
  }

  /** Puts the state read from the URL onto the controls, without echoing back. */
  private applyState(state: ListingState): void {
    if (this.searchControl.value !== state.search) {
      this.searchControl.setValue(state.search, { emitEvent: false });
    }
    this.stockFilter.set(state.stock);
    this.lowestBalanceFirst.set(state.byBalance);
  }

  /**
   * Writes the current controls to the URL, which is what triggers the reload.
   *
   * The entry is replaced rather than pushed: every keystroke of a search would
   * otherwise become a step the back button has to walk through.
   */
  private writeState(): void {
    const search = this.searchControl.value.trim();
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: {
        search: search || null,
        stock: this.stockFilter() === 'all' ? null : this.stockFilter(),
        sort: this.lowestBalanceFirst() ? 'balance' : null,
      },
      queryParamsHandling: 'merge',
      replaceUrl: true,
    });
  }

  /** The filters currently applied to the listing. */
  private currentFilters(): ProductFilters {
    // Defaults are left out of the request, so only what was asked for travels.
    return {
      search: this.searchControl.value,
      maxBalance: this.maxBalanceFor(this.stockFilter()),
      sort: this.lowestBalanceFirst() || this.stockFilter() === 'low' ? 'balance' : undefined,
    };
  }

  /**
   * "Running out" is a balance the service can filter on; there is no such
   * field on a product, and inventing one nobody maintains would be worse than
   * a threshold everyone can read.
   */
  private maxBalanceFor(filter: StockFilter): number | undefined {
    switch (filter) {
      case 'out':
        return 0;
      case 'low':
        return LOW_STOCK_THRESHOLD;
      case 'all':
        return undefined;
    }
  }

  /** Reads the first page of the catalogue for the current filters. */
  private fetch() {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.products.list(this.currentFilters()).pipe(
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

  /** Switches which part of the catalogue is listed. */
  selectStockFilter(filter: StockFilter): void {
    this.stockFilter.set(filter);
    this.writeState();
  }

  /** Switches between catalogue order and lowest balance first. */
  toggleLowestBalanceFirst(): void {
    this.lowestBalanceFirst.update((lowest) => !lowest);
    this.writeState();
  }

  clearFilters(): void {
    this.searchControl.setValue('', { emitEvent: false });
    this.stockFilter.set('all');
    this.lowestBalanceFirst.set(false);
    this.writeState();
  }

  /**
   * Hands the listing over as a spreadsheet.
   *
   * What is exported is what is on screen, filters and all: exporting the
   * whole catalogue when the operator is looking at what is running out would
   * be answering a question nobody asked.
   */
  exportCsv(): void {
    const rows = this.items().map((product) => [product.code, product.description, product.balance, product.updatedAt]);

    downloadCsv(
      csvFilename('products'),
      toCsv(
        [
          this.i18n.t('products.tableCode'),
          this.i18n.t('products.tableDescription'),
          this.i18n.t('products.tableBalance'),
          this.i18n.t('products.tableUpdatedAt'),
        ],
        rows,
      ),
    );
    toast.success(this.i18n.plural('toasts.productsExported', rows.length), { position: 'bottom-right' });
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

  openImport(): void {
    this.importOpen.set(true);
  }

  closeImport(): void {
    this.importOpen.set(false);
  }

  onImported(): void {
    this.reload();
  }

  openHistory(product: Product): void {
    this.historyOf.set(product);
  }

  closeHistory(): void {
    this.historyOf.set(null);
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
        toast.success(
          this.i18n.t(editing ? 'toasts.productUpdated' : 'toasts.productRegistered', { code: product.code }),
          { position: 'bottom-right' },
        );
        this.reload();
      },
      error: (error: ApiError) => {
        this.saving.set(false);
        this.saveFailure.set(error);
        // Field level messages are shown by the form; this is the summary.
        toast.error(translateErrorCode(this.i18n, error.code), { position: 'bottom-right' });
      },
    });
  }

  retry(): void {
    this.reload();
  }

  /** Reads the listing again without changing what is being filtered on. */
  private reload(): void {
    this.fetch().pipe(takeUntilDestroyed(this.destroyRef)).subscribe();
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
            toast.success(this.i18n.plural('toasts.balancesAdjusted', response.summary.succeeded), {
              position: 'bottom-right',
            });
            this.reload();
            return;
          }

          // Nothing was applied. What was typed stays on screen next to the
          // reason it was refused.
          this.adjustmentResult.set(this.withProductCodes(response));
        },
        error: (error: ApiError) => {
          this.applying.set(false);
          toast.error(translateErrorCode(this.i18n, error.code), { position: 'bottom-right' });
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

/** Reads the listing state out of the query string, ignoring anything else. */
function readState(search: string | null, stock: string | null, sort: string | null): ListingState {
  const known: StockFilter[] = ['low', 'out'];
  return {
    search: search ?? '',
    stock: known.includes(stock as StockFilter) ? (stock as StockFilter) : 'all',
    byBalance: sort === 'balance',
  };
}

/** One product being moved while the adjustment is written. */
export interface AdjustmentDraft {
  product: Product;
  /** What is typed, kept as text so an empty field means "not moving this one". */
  delta: WritableSignal<string>;
}
