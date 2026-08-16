import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { ReactiveFormsModule, FormControl } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { Subject, catchError, of, startWith, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { BULK_MAX_ITEMS, BulkResponse, bulkSucceededIds } from 'src/app/core/models/bulk.model';
import { Invoice, InvoiceStatus } from 'src/app/core/models/invoice.model';
import { InvoiceFilters, InvoiceService } from 'src/app/core/services/invoice.service';
import { BulkResultComponent } from 'src/app/shared/components/bulk-result/bulk-result.component';
import { InvoiceStatusComponent } from './invoice-status.component';

/** Filters offered above the list. */
const STATUS_FILTERS: readonly (InvoiceStatus | '')[] = ['', 'OPEN', 'PRINTING', 'CLOSED'];

/** Lists the invoices and their current state. */
@Component({
  selector: 'app-invoices',
  imports: [
    RouterLink,
    DatePipe,
    ReactiveFormsModule,
    AngularSvgIconModule,
    InvoiceStatusComponent,
    BulkResultComponent,
  ],
  templateUrl: './invoices.component.html',
})
export class InvoicesComponent implements OnInit {
  private readonly invoices = inject(InvoiceService);
  private readonly destroyRef = inject(DestroyRef);

  readonly filters = STATUS_FILTERS;
  readonly activeFilter = signal<InvoiceStatus | ''>('');

  /** Free filters offered above the listing. */
  readonly numberControl = new FormControl('', { nonNullable: true });
  readonly fromControl = new FormControl('', { nonNullable: true });
  readonly toControl = new FormControl('', { nonNullable: true });
  readonly productCodeControl = new FormControl('', { nonNullable: true });
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

  private readonly reload = new Subject<void>();

  ngOnInit(): void {
    this.reload
      .pipe(
        startWith(undefined),
        switchMap(() => this.fetch()),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();
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
    this.reload.next();
  }

  /** Applies the free filters. */
  applyFilters(): void {
    this.reload.next();
  }

  /** Shows only the invoices whose last print attempt did not go through. */
  toggleNeedsAttention(): void {
    this.needsAttentionOnly.update((only) => !only);
    this.reload.next();
  }

  /** Clears every filter and reads the listing from the top. */
  clearFilters(): void {
    this.activeFilter.set('');
    this.needsAttentionOnly.set(false);
    this.numberControl.setValue('');
    this.fromControl.setValue('');
    this.toControl.setValue('');
    this.productCodeControl.setValue('');
    this.reload.next();
  }

  refresh(): void {
    this.reload.next();
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
            toast.success(`${response.summary.succeeded} invoice(s) sent to print.`, { position: 'bottom-right' });
          }
          // Printing is asynchronous, so the listing is read again to pick up
          // the invoices that already moved to PRINTING.
          this.reload.next();
        },
        error: (error: ApiError) => {
          this.printingBatch.set(false);
          this.loadFailure.set(error);
          toast.error(error.message, { position: 'bottom-right' });
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
    return status === '' ? 'All' : status.charAt(0) + status.slice(1).toLowerCase();
  }
}
