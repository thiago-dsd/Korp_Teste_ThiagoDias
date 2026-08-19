import {
  Component,
  DestroyRef,
  ElementRef,
  HostListener,
  OnInit,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { ReactiveFormsModule, FormControl } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { catchError, distinctUntilChanged, map, of, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { BULK_MAX_ITEMS, BulkResponse, bulkSucceededIds } from 'src/app/core/models/bulk.model';
import { Invoice, InvoiceStatus } from 'src/app/core/models/invoice.model';
import { InvoiceFilters, InvoiceService } from 'src/app/core/services/invoice.service';
import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { translateErrorCode } from 'src/app/core/i18n/error-translation';
import { LocaleDatePipe } from 'src/app/core/i18n/intl.pipe';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';
import { BulkResultComponent } from 'src/app/shared/components/bulk-result/bulk-result.component';
import { csvFilename, downloadCsv, toCsv } from 'src/app/shared/utils/csv';
import { InvoiceStatusComponent } from './invoice-status.component';

/** Filters offered above the list. */
const STATUS_FILTERS: readonly (InvoiceStatus | '')[] = ['', 'OPEN', 'PRINTING', 'CLOSED'];

/** Lists the invoices and their current state. */
@Component({
  selector: 'app-invoices',
  imports: [
    RouterLink,
    ReactiveFormsModule,
    AngularSvgIconModule,
    InvoiceStatusComponent,
    BulkResultComponent,
    TranslatePipe,
    ApiErrorPipe,
    LocaleDatePipe,
  ],
  templateUrl: './invoices.component.html',
})
export class InvoicesComponent implements OnInit {
  private readonly invoices = inject(InvoiceService);
  readonly i18n = inject(TranslateService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);

  readonly filters = STATUS_FILTERS;
  readonly activeFilter = signal<InvoiceStatus | ''>('');

  /** Free filters offered above the listing. */
  readonly numberControl = new FormControl('', { nonNullable: true });
  private readonly numberField = viewChild<ElementRef<HTMLInputElement>>('numberField');
  readonly fromControl = new FormControl('', { nonNullable: true });
  readonly toControl = new FormControl('', { nonNullable: true });
  readonly productCodeControl = new FormControl('', { nonNullable: true });

  /**
   * The question asked in plain words, which the assistant turns into the
   * filters above.
   *
   * It is a way of setting the filters, not a second way of listing: what comes
   * back lands in the same controls and the same URL, so the operator can see
   * what was understood and correct it by hand.
   */
  readonly questionControl = new FormControl('', { nonNullable: true });
  readonly assistantAvailable = signal(false);
  readonly asking = signal(false);
  readonly askWarnings = signal<string[]>([]);
  readonly askFailure = signal<ApiError | null>(null);
  readonly needsAttentionOnly = signal(false);

  // Reading the controls as signals keeps the "clear" button in step with what
  // is typed, without the template asking for it on every change detection.
  private readonly typedFilters = [
    toSignal(this.numberControl.valueChanges, { initialValue: '' }),
    toSignal(this.fromControl.valueChanges, { initialValue: '' }),
    toSignal(this.toControl.valueChanges, { initialValue: '' }),
    toSignal(this.productCodeControl.valueChanges, { initialValue: '' }),
  ];

  /** True when anything is filtering the listing. */
  readonly hasActiveFilters = computed(
    () => this.needsAttentionOnly() || this.activeFilter() !== '' || this.typedFilters.some((value) => value() !== ''),
  );

  readonly items = signal<Invoice[]>([]);
  readonly loading = signal(false);
  readonly loadingMore = signal(false);
  readonly loadFailure = signal<ApiError | null>(null);

  /** Cursor of the next page, empty when the listing ended. */
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
      noun: this.i18n.plural('nouns.invoice', count),
    });
  });

  /** True while at least one invoice is waiting for the stock service. */
  readonly hasPrinting = computed(() => this.items().some((invoice) => invoice.status === 'PRINTING'));

  /**
   * Ids picked for printing in bulk. Only open invoices can be picked, and the
   * selection survives paging because it holds ids rather than row positions.
   */
  readonly selected = signal<ReadonlySet<string>>(new Set());
  readonly selectedCount = computed(() => this.selected().size);

  /** The cap the service enforces, applied here so a batch is never refused whole. */
  readonly maxSelectable = BULK_MAX_ITEMS;
  readonly selectionFull = computed(() => this.selectedCount() >= BULK_MAX_ITEMS);

  readonly printingBatch = signal(false);
  readonly batchResult = signal<BulkResponse | null>(null);

  /** Open invoices on screen that are not selected yet. */
  private readonly selectableIds = computed(() =>
    this.items()
      .filter((invoice) => invoice.status === 'OPEN')
      .map((invoice) => invoice.id),
  );

  readonly hasSelectable = computed(() => this.selectableIds().length > 0);

  /**
   * "/" jumps to the invoice number box, which is the field this screen is
   * opened for: somebody is looking for one document. Ignored while something
   * is being typed, so a slash inside a product code stays a slash.
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
    this.numberField()?.nativeElement.focus();
  }

  ngOnInit(): void {
    // The screen only offers the question box when a model is configured, the
    // same way the new-invoice screen offers the drafting assistant.
    this.invoices
      .assistantAvailable()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (available) => this.assistantAvailable.set(available),
        error: () => this.assistantAvailable.set(false),
      });

    // The URL decides what is listed. That is what lets an operator reload
    // after a batch, walk back with the browser, or send a colleague a link to
    // exactly the invoices that failed to print.
    this.route.queryParamMap
      .pipe(
        map((params) => ({
          status: readStatus(params.get('status')),
          number: params.get('number') ?? '',
          from: params.get('from') ?? '',
          to: params.get('to') ?? '',
          product: params.get('product') ?? '',
          attention: params.get('attention') === 'true',
        })),
        distinctUntilChanged((a, b) => JSON.stringify(a) === JSON.stringify(b)),
        tap((state) => {
          this.activeFilter.set(state.status);
          this.needsAttentionOnly.set(state.attention);
          this.numberControl.setValue(state.number, { emitEvent: false });
          this.fromControl.setValue(state.from, { emitEvent: false });
          this.toControl.setValue(state.to, { emitEvent: false });
          this.productCodeControl.setValue(state.product, { emitEvent: false });
        }),
        switchMap(() => this.fetch()),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();
  }

  /**
   * Writes the current filters to the URL, which is what reloads the listing.
   *
   * The entry is replaced rather than pushed: narrowing a filter three times
   * should not cost three presses of the back button to undo.
   */
  private writeState(): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: {
        status: this.activeFilter() || null,
        number: this.numberControl.value.trim() || null,
        from: this.fromControl.value || null,
        to: this.toControl.value || null,
        product: this.productCodeControl.value.trim() || null,
        attention: this.needsAttentionOnly() ? 'true' : null,
      },
      replaceUrl: true,
    });
  }

  /** The filters currently applied to the listing. */
  private currentFilters(): InvoiceFilters {
    const number = Number(this.numberControl.value.trim());

    return {
      statuses: this.activeFilter() ? [this.activeFilter() as InvoiceStatus] : undefined,
      number: this.numberControl.value.trim() && Number.isFinite(number) ? number : undefined,
      createdFrom: this.fromControl.value || undefined,
      createdTo: this.toControl.value || undefined,
      productCode: this.productCodeControl.value || undefined,
      hasFailure: this.needsAttentionOnly() ? true : undefined,
    };
  }

  private fetch() {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.invoices.list(this.currentFilters()).pipe(
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

    this.invoices
      .list(this.currentFilters(), cursor)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.items.update((invoices) => [...invoices, ...page.items]);
          this.nextCursor.set(page.nextCursor);
          this.loadingMore.set(false);
        },
        error: (error: ApiError) => {
          this.loadingMore.set(false);
          this.loadFailure.set(error);
        },
      });
  }

  selectFilter(status: InvoiceStatus | ''): void {
    this.activeFilter.set(status);
    this.writeState();
  }

  /** Applies the free filters. */
  /**
   * Turns the question into filters and applies them.
   *
   * The answer is written into the controls and then into the URL, exactly as
   * if the filters had been set by hand: the listing reloads through the same
   * path, and what the assistant understood is visible on screen rather than
   * hidden behind a result.
   */
  askAssistant(): void {
    const question = this.questionControl.value.trim();
    if (question === '' || this.asking()) {
      return;
    }

    this.asking.set(true);
    this.askFailure.set(null);
    this.askWarnings.set([]);

    this.invoices
      .searchByText(question)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (search) => {
          this.asking.set(false);
          this.askWarnings.set(search.warnings);
          this.activeFilter.set(readStatus(search.filters.status));
          this.needsAttentionOnly.set(search.filters.attention);
          this.numberControl.setValue(search.filters.number, { emitEvent: false });
          this.fromControl.setValue(search.filters.from, { emitEvent: false });
          this.toControl.setValue(search.filters.to, { emitEvent: false });
          this.productCodeControl.setValue(search.filters.product, { emitEvent: false });
          this.writeState();
        },
        error: (error: ApiError) => {
          this.asking.set(false);
          this.askFailure.set(error);
        },
      });
  }

  /** Clears the question and whatever the assistant had to say about it. */
  clearQuestion(): void {
    this.questionControl.setValue('');
    this.askWarnings.set([]);
    this.askFailure.set(null);
  }

  applyFilters(): void {
    this.writeState();
  }

  /** Shows only the invoices whose last print attempt did not go through. */
  toggleNeedsAttention(): void {
    this.needsAttentionOnly.update((only) => !only);
    this.writeState();
  }

  /** Clears every filter and reads the listing from the top. */
  clearFilters(): void {
    this.activeFilter.set('');
    this.needsAttentionOnly.set(false);
    this.numberControl.setValue('');
    this.fromControl.setValue('');
    this.toControl.setValue('');
    this.productCodeControl.setValue('');
    this.writeState();
  }

  refresh(): void {
    this.reload();
  }

  /** Reads the listing again without changing what is being filtered on. */
  private reload(): void {
    this.fetch().pipe(takeUntilDestroyed(this.destroyRef)).subscribe();
  }

  /**
   * Hands the listing over as a spreadsheet, filters and all.
   *
   * One row per invoice with its lines collapsed into a single cell: an
   * invoice with three products is still one document, and splitting it into
   * three rows makes every total in the spreadsheet wrong.
   */
  exportCsv(): void {
    const rows = this.items().map((invoice) => [
      invoice.number,
      this.i18n.t(`invoiceStatus.${invoice.status.toLowerCase()}`),
      invoice.items.map((item) => `${item.productCode} x${item.quantity}`).join('; '),
      invoice.items.reduce((total, item) => total + item.quantity, 0),
      invoice.createdAt,
      invoice.printedAt ?? '',
      invoice.issuedBy?.email ?? '',
      invoice.printedBy?.email ?? '',
      invoice.failure ? translateErrorCode(this.i18n, invoice.failure.code) : '',
    ]);

    downloadCsv(
      csvFilename('invoices'),
      toCsv(
        [
          this.i18n.t('invoices.csv.number'),
          this.i18n.t('invoices.csv.status'),
          this.i18n.t('invoices.csv.products'),
          this.i18n.t('invoices.csv.totalQuantity'),
          this.i18n.t('invoices.csv.createdAt'),
          this.i18n.t('invoices.csv.printedAt'),
          this.i18n.t('invoices.csv.issuedBy'),
          this.i18n.t('invoices.csv.printedBy'),
          this.i18n.t('invoices.csv.failure'),
        ],
        rows,
      ),
    );
    toast.success(this.i18n.plural('toasts.invoicesExported', rows.length), { position: 'bottom-right' });
  }

  /** Whether this invoice can be picked for a batch. */
  canSelect(invoice: Invoice): boolean {
    return invoice.status === 'OPEN';
  }

  isSelected(id: string): boolean {
    return this.selected().has(id);
  }

  /** Adds or removes one invoice from the batch. */
  toggleSelection(id: string): void {
    this.selected.update((current) => {
      const next = new Set(current);
      if (!next.delete(id) && next.size < BULK_MAX_ITEMS) {
        next.add(id);
      }
      return next;
    });
  }

  /** Picks every open invoice on screen, up to the limit one call may carry. */
  selectAllOpen(): void {
    this.selected.update((current) => {
      const next = new Set(current);
      for (const id of this.selectableIds()) {
        if (next.size >= BULK_MAX_ITEMS) {
          break;
        }
        next.add(id);
      }
      return next;
    });
  }

  clearSelection(): void {
    this.selected.set(new Set());
  }

  dismissBatchResult(): void {
    this.batchResult.set(null);
  }

  /**
   * Prints everything selected.
   *
   * The invoices that were accepted leave the selection and the refused ones
   * stay, so the operator can read why and send just those again.
   */
  printSelected(): void {
    const ids = [...this.selected()];
    if (ids.length === 0 || this.printingBatch()) {
      return;
    }

    this.printingBatch.set(true);
    this.batchResult.set(null);

    this.invoices
      .printMany(ids)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (response) => {
          this.printingBatch.set(false);
          this.batchResult.set(this.withInvoiceNumbers(response));

          const printed = new Set(bulkSucceededIds(response));
          this.selected.update((current) => new Set([...current].filter((id) => !printed.has(id))));

          if (response.summary.succeeded > 0) {
            toast.success(this.i18n.plural('toasts.invoicesSentToPrint', response.summary.succeeded), {
              position: 'bottom-right',
            });
          }
          // Printing is asynchronous, so the listing is read again to pick up
          // the invoices that already moved to PRINTING.
          this.reload();
        },
        error: (error: ApiError) => {
          this.printingBatch.set(false);
          this.loadFailure.set(error);
          toast.error(translateErrorCode(this.i18n, error.code), { position: 'bottom-right' });
        },
      });
  }

  /**
   * Names each result by its invoice number.
   *
   * An invoice that could not start printing has no number to report yet, so
   * the service falls back to its id. The screen already knows the number, and
   * "#7" is what the operator recognises, not a UUID.
   */
  private withInvoiceNumbers(response: BulkResponse): BulkResponse {
    const numbers = new Map(this.items().map((invoice) => [invoice.id, invoice.number]));

    return {
      ...response,
      results: response.results.map((result) => {
        const number = numbers.get(result.id || result.reference || '');
        return number === undefined ? result : { ...result, reference: `#${number}` };
      }),
    };
  }

  labelFor(status: InvoiceStatus | ''): string {
    return this.i18n.t(status === '' ? 'invoiceStatus.all' : `invoiceStatus.${status.toLowerCase()}`);
  }
}

/** Reads a status out of the query string, ignoring anything unknown. */
function readStatus(raw: string | null): InvoiceStatus | '' {
  const known: InvoiceStatus[] = ['OPEN', 'PRINTING', 'CLOSED'];
  return known.includes(raw as InvoiceStatus) ? (raw as InvoiceStatus) : '';
}
