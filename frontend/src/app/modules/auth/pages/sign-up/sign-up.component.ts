import { Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';

import { ApiError } from 'src/app/core/models/api-error.model';
import { AuthService } from 'src/app/core/services/auth.service';

/** The identity service refuses anything shorter than this. */
export const MIN_PASSWORD_LENGTH = 12;

/** Account creation screen. */
@Component({
  selector: 'app-sign-up',
  templateUrl: './sign-up.component.html',
  styleUrls: ['./sign-up.component.css'],
  imports: [ReactiveFormsModule, RouterLink, AngularSvgIconModule],
})
export class SignUpComponent {
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly formBuilder = inject(FormBuilder);
  private readonly destroyRef = inject(DestroyRef);

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
      return serverMessage;
    }

    const control = this.form.controls[field];
    if (!control.touched || control.valid) {
      return null;
    }
    if (control.hasError('required')) {
      return 'This field is required.';
    }
    if (control.hasError('email')) {
      return 'Enter a valid email address.';
    }
    if (control.hasError('minlength')) {
      return `Use at least ${MIN_PASSWORD_LENGTH} characters. A passphrase is easier to remember and harder to guess.`;
    }
    if (control.hasError('maxlength')) {
      return 'This value is too long.';
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
