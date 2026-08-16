import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { Subject, catchError, debounceTime, distinctUntilChanged, of, startWith, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { NewProduct, Product } from 'src/app/core/models/product.model';
import { ProductService } from 'src/app/core/services/product.service';
import { ProductFormComponent } from './product-form.component';

/** How long the screen waits after a keystroke before searching. */
const SEARCH_DEBOUNCE_MS = 300;

/**
 * Product catalogue: lists what is in stock and lets the operator register a
 * product or fix its description and balance.
 */
@Component({
  selector: 'app-products',
  imports: [ReactiveFormsModule, AngularSvgIconModule, ProductFormComponent],
  templateUrl: './products.component.html',
})
export class ProductsComponent implements OnInit {
  private readonly products = inject(ProductService);
  private readonly destroyRef = inject(DestroyRef);

  readonly searchControl = new FormControl('', { nonNullable: true });

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

  /** Reads the first page of the catalogue for a search term. */
  private fetch(search: string) {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.products.list(search).pipe(
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
      .list(this.searchControl.value, cursor)
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
      ? this.products.update(editing.id, { description: input.description, balance: input.balance })
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
}
