import { Component, computed, effect, inject, input, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';

import { ApiError } from 'src/app/core/models/api-error.model';
import { NewProduct, Product } from 'src/app/core/models/product.model';

/** Codes are what identifies a product on a printed invoice and in URLs. */
const CODE_PATTERN = /^[\p{L}\p{N}._-]+$/u;

/**
 * Form used to register a product and to change an existing one.
 *
 * Validation mirrors the rules the stock service enforces, so the operator is
 * told what is wrong before a request is sent; whatever the service rejects
 * anyway is shown on the same fields through {@link ProductFormComponent.failure}.
 */
@Component({
  selector: 'app-product-form',
  imports: [ReactiveFormsModule],
  templateUrl: './product-form.component.html',
})
export class ProductFormComponent {
  /** Product being edited, or undefined when registering a new one. */
  readonly product = input<Product | undefined>();
  /** Whether a request is in flight. */
  readonly saving = input(false);
  /** Error returned by the service for the last attempt. */
  readonly failure = input<ApiError | null>(null);

  readonly save = output<NewProduct>();
  readonly cancelled = output<void>();

  private readonly formBuilder = inject(FormBuilder);
  private readonly submitted = signal(false);

  readonly form = this.formBuilder.nonNullable.group({
    code: ['', [Validators.required, Validators.maxLength(32), Validators.pattern(CODE_PATTERN)]],
    description: ['', [Validators.required, Validators.maxLength(200)]],
    balance: [0, [Validators.required, Validators.min(0), Validators.max(1_000_000_000)]],
  });

  readonly isEditing = computed(() => this.product() !== undefined);

  constructor() {
    // Filling the form is a reaction to the input changing, so it is expressed
    // as an effect rather than as a lifecycle hook.
    effect(() => {
      const product = this.product();
      if (!product) {
        return;
      }
      this.form.setValue({ code: product.code, description: product.description, balance: product.balance });
      this.form.controls.code.disable();
    });

    // Editing the form clears the "submitted" state, so messages only appear
    // again when the operator tries to save.
    this.form.valueChanges.pipe(takeUntilDestroyed()).subscribe(() => this.submitted.set(false));
  }

  /** Message to show under a field, coming from the form or from the service. */
  errorFor(field: 'code' | 'description' | 'balance'): string | null {
    const control = this.form.controls[field];
    const serverMessage = this.failure()?.details[field];

    if (serverMessage) {
      return serverMessage;
    }
    if (!control.touched && !this.submitted()) {
      return null;
    }
    if (control.hasError('required')) {
      return 'This field is required.';
    }
    if (control.hasError('maxlength')) {
      const max = control.getError('maxlength').requiredLength;
      return `Use at most ${max} characters.`;
    }
    if (control.hasError('pattern')) {
      return 'Use only letters, digits, dot, dash or underscore.';
    }
    if (control.hasError('min')) {
      return 'The balance cannot be negative.';
    }
    if (control.hasError('max')) {
      return 'This balance is too large.';
    }
    return null;
  }

  onSubmit(): void {
    this.submitted.set(true);
    if (this.form.invalid) {
      this.form.markAllAsTouched();
      return;
    }

    const value = this.form.getRawValue();
    this.save.emit({
      code: value.code.trim(),
      description: value.description.trim(),
      balance: Number(value.balance),
    });
  }
}
