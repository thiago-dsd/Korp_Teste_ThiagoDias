import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, FormControl, ReactiveFormsModule, Validators } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';

import { ApiError } from 'src/app/core/models/api-error.model';
import { Product } from 'src/app/core/models/product.model';
import { InvoiceService } from 'src/app/core/services/invoice.service';
import { ProductService } from 'src/app/core/services/product.service';

/** Mirrors the limit the service enforces on the sentence. */
export const MAX_DRAFT_TEXT_LENGTH = 500;

/** A line the operator added to the invoice being written. */
interface DraftLine {
  product: Product;
  quantity: number;
}

/**
 * Writes a new invoice: pick a product, say how many, add the line, repeat.
 *
 * The invoice is created open. Balances are only debited when it is printed,
 * so a quantity above the current balance is accepted here and refused later,
 * with a warning shown as soon as the line is added.
 */
@Component({
  selector: 'app-invoice-new',
  imports: [ReactiveFormsModule, RouterLink, AngularSvgIconModule],
  templateUrl: './invoice-new.component.html',
})
export class InvoiceNewComponent implements OnInit {
  private readonly products = inject(ProductService);
  private readonly invoices = inject(InvoiceService);
  private readonly router = inject(Router);
  private readonly formBuilder = inject(FormBuilder);
  private readonly destroyRef = inject(DestroyRef);

  readonly catalogue = signal<Product[]>([]);
  readonly loadingCatalogue = signal(true);
  readonly catalogueFailure = signal<ApiError | null>(null);

  readonly lines = signal<DraftLine[]>([]);
  readonly saving = signal(false);
  readonly saveFailure = signal<ApiError | null>(null);

  readonly lineForm = this.formBuilder.nonNullable.group({
    productId: ['', Validators.required],
    quantity: [1, [Validators.required, Validators.min(1), Validators.max(1_000_000)]],
  });

  /** Free text handed to the assistant. */
  readonly draftControl = new FormControl('', { nonNullable: true });

  readonly assistantAvailable = signal(false);
  readonly drafting = signal(false);
  readonly draftWarnings = signal<string[]>([]);
  readonly draftFailure = signal<ApiError | null>(null);
  readonly maxDraftLength = MAX_DRAFT_TEXT_LENGTH;

  /** Products that are not on the invoice yet. */
  readonly available = computed(() => {
    const used = new Set(this.lines().map((line) => line.product.id));
    return this.catalogue().filter((product) => !used.has(product.id));
  });

  readonly totalQuantity = computed(() => this.lines().reduce((total, line) => total + line.quantity, 0));

  /** Lines asking for more than the product currently has in stock. */
  readonly linesOverBalance = computed(() => this.lines().filter((line) => line.quantity > line.product.balance));

  ngOnInit(): void {
    // The assistant is optional, so the screen asks whether to offer it.
    this.invoices
      .assistantAvailable()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (available) => this.assistantAvailable.set(available),
        error: () => this.assistantAvailable.set(false),
      });

    this.products
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (products) => {
          this.catalogue.set(products);
          this.loadingCatalogue.set(false);
        },
        error: (error: ApiError) => {
          this.loadingCatalogue.set(false);
          this.catalogueFailure.set(error);
        },
      });
  }

  /**
   * Asks the assistant to read the sentence and fill the lines.
   *
   * What comes back is a suggestion: the operator reviews the lines, changes
   * what is wrong and presses the button that creates the invoice.
   */
  askAssistant(): void {
    const text = this.draftControl.value.trim();
    if (!text || this.drafting()) {
      return;
    }

    this.drafting.set(true);
    this.draftFailure.set(null);
    this.draftWarnings.set([]);

    this.invoices
      .draft(text)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (draft) => {
          this.drafting.set(false);
          this.draftWarnings.set(draft.warnings);
          this.applySuggestions(draft.lines);

          if (draft.lines.length > 0) {
            this.draftControl.reset('');
            toast.success(`The assistant added ${draft.lines.length} product(s). Review before creating.`, {
              position: 'bottom-right',
            });
          }
        },
        error: (error: ApiError) => {
          this.drafting.set(false);
          this.draftFailure.set(error);
        },
      });
  }

  /** Merges suggestions into the invoice without dropping what is there. */
  private applySuggestions(suggestions: { productId: string; quantity: number }[]): void {
    for (const suggestion of suggestions) {
      const product = this.catalogue().find((candidate) => candidate.id === suggestion.productId);
      if (!product) {
        continue;
      }

      const existing = this.lines().findIndex((line) => line.product.id === product.id);
      if (existing >= 0) {
        this.lines.update((lines) =>
          lines.map((line, index) =>
            index === existing ? { ...line, quantity: line.quantity + suggestion.quantity } : line,
          ),
        );
        continue;
      }
      this.lines.update((lines) => [...lines, { product, quantity: suggestion.quantity }]);
    }
  }

  addLine(): void {
    if (this.lineForm.invalid) {
      this.lineForm.markAllAsTouched();
      return;
    }

    const { productId, quantity } = this.lineForm.getRawValue();
    const product = this.catalogue().find((candidate) => candidate.id === productId);
    if (!product) {
      return;
    }

    this.lines.update((lines) => [...lines, { product, quantity: Number(quantity) }]);
    this.lineForm.reset({ productId: '', quantity: 1 });
  }

  removeLine(productId: string): void {
    this.lines.update((lines) => lines.filter((line) => line.product.id !== productId));
  }

  save(): void {
    if (this.lines().length === 0) {
      return;
    }

    this.saving.set(true);
    this.saveFailure.set(null);

    const items = this.lines().map((line) => ({ productId: line.product.id, quantity: line.quantity }));

    this.invoices
      .create(items)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (invoice) => {
          this.saving.set(false);
          toast.success(`Invoice #${invoice.number} created.`, { position: 'bottom-right' });
          void this.router.navigate(['/invoices', invoice.id]);
        },
        error: (error: ApiError) => {
          this.saving.set(false);
          this.saveFailure.set(error);
          toast.error(error.message, { position: 'bottom-right' });
        },
      });
  }
}
