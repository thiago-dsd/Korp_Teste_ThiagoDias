import { Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';

import { ApiError } from 'src/app/core/models/api-error.model';
import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { translateFieldMessage } from 'src/app/core/i18n/error-translation';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';
import { AuthService } from 'src/app/core/services/auth.service';

/** The identity service refuses anything shorter than this. */
export const MIN_PASSWORD_LENGTH = 12;

/** Account creation screen. */
@Component({
  selector: 'app-sign-up',
  templateUrl: './sign-up.component.html',
  styleUrls: ['./sign-up.component.css'],
  imports: [ReactiveFormsModule, RouterLink, AngularSvgIconModule, TranslatePipe, ApiErrorPipe],
})
export class SignUpComponent {
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly formBuilder = inject(FormBuilder);
  private readonly destroyRef = inject(DestroyRef);
  private readonly i18n = inject(TranslateService);

  readonly minPasswordLength = MIN_PASSWORD_LENGTH;
  readonly submitting = signal(false);
  readonly failure = signal<ApiError | null>(null);
  readonly showPassword = signal(false);

  readonly form = this.formBuilder.nonNullable.group({
    name: ['', [Validators.required, Validators.maxLength(120)]],
    email: ['', [Validators.required, Validators.email, Validators.maxLength(254)]],
    password: ['', [Validators.required, Validators.minLength(MIN_PASSWORD_LENGTH), Validators.maxLength(128)]],
  });

  togglePasswordVisibility(): void {
    this.showPassword.update((visible) => !visible);
  }

  /** Message for a field, preferring what the service said about it. */
  errorFor(field: 'name' | 'email' | 'password'): string | null {
    const serverMessage = this.failure()?.details[field];
    if (serverMessage) {
      return translateFieldMessage(this.i18n, serverMessage);
    }

    const control = this.form.controls[field];
    if (!control.touched || control.valid) {
      return null;
    }
    if (control.hasError('required')) {
      return this.i18n.t('validation.required');
    }
    if (control.hasError('email')) {
      return this.i18n.t('validation.invalidEmail');
    }
    if (control.hasError('minlength')) {
      return this.i18n.t('validation.minLength', { min: MIN_PASSWORD_LENGTH });
    }
    if (control.hasError('maxlength')) {
      return this.i18n.t('validation.tooLong');
    }
    return null;
  }

  onSubmit(): void {
    if (this.form.invalid) {
      this.form.markAllAsTouched();
      return;
    }

    this.submitting.set(true);
    this.failure.set(null);

    const { name, email, password } = this.form.getRawValue();

    this.auth
      .register(email, name, password)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.submitting.set(false);
          void this.router.navigate(['/home']);
        },
        error: (error: ApiError) => {
          this.submitting.set(false);
          this.failure.set(error);
        },
      });
  }
}
