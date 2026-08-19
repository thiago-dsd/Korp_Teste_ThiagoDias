import { animate, state, style, transition, trigger } from '@angular/animations';
import { NgClass } from '@angular/common';
import { Component, DestroyRef, ChangeDetectionStrategy, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { ApiError } from '../../../../../core/models/api-error.model';
import { ApiErrorPipe } from '../../../../../core/i18n/api-error.pipe';
import { TranslatePipe } from '../../../../../core/i18n/translate.pipe';
import { TranslateService } from '../../../../../core/i18n/translate.service';
import { AuthService } from '../../../../../core/services/auth.service';
import { ThemeService } from '../../../../../core/services/theme.service';
import { ClickOutsideDirective } from '../../../../../shared/directives/click-outside.directive';
import { LanguageSwitcherComponent } from '../../../../../shared/components/language-switcher/language-switcher.component';

@Component({
  selector: 'app-profile-menu',
  templateUrl: './profile-menu.component.html',
  styleUrls: ['./profile-menu.component.css'],
  imports: [
    ClickOutsideDirective,
    NgClass,
    AngularSvgIconModule,
    FormsModule,
    TranslatePipe,
    ApiErrorPipe,
    LanguageSwitcherComponent,
  ],
  changeDetection: ChangeDetectionStrategy.Eager,
  animations: [
    trigger('openClose', [
      state(
        'open',
        style({
          opacity: 1,
          transform: 'translateY(0)',
          visibility: 'visible',
        }),
      ),
      state(
        'closed',
        style({
          opacity: 0,
          transform: 'translateY(-20px)',
          visibility: 'hidden',
        }),
      ),
      transition('open => closed', [animate('0.2s')]),
      transition('closed => open', [animate('0.2s')]),
    ]),
  ],
})
export class ProfileMenuComponent {
  themeService = inject(ThemeService);

  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);
  private readonly destroyRef = inject(DestroyRef);
  private readonly i18n = inject(TranslateService);

  /** Who is signed in, straight from the session. */
  readonly user = this.auth.currentUser;

  readonly confirmingDeletion = signal(false);
  readonly deleting = signal(false);
  readonly deleteFailure = signal<ApiError | null>(null);
  deletePassword = '';

  public isOpen = false;

  /**
   * `code` is the key under `profileMenu.themeColor` in the dictionaries, not
   * a colour name to translate word for word: "base" reads as "Rose" in
   * English and "Rosa" in Portuguese, which a literal translation of the
   * identifier would not give.
   */
  public themeColors = [
    { name: 'base', code: '#e11d48' },
    { name: 'yellow', code: '#f59e0b' },
    { name: 'green', code: '#22c55e' },
    { name: 'blue', code: '#3b82f6' },
    { name: 'orange', code: '#ea580c' },
    { name: 'red', code: '#cc0022' },
    { name: 'violet', code: '#6d28d9' },
  ];

  public themeMode = ['light', 'dark'];
  public themeDirection = ['ltr', 'rtl'];

  /** Ends the session and goes back to the sign in screen. */
  signOut(): void {
    this.auth
      .logout()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.isOpen = false;
        void this.router.navigate(['/auth/sign-in']);
      });
  }

  startAccountDeletion(): void {
    this.confirmingDeletion.set(true);
    this.deleteFailure.set(null);
    this.deletePassword = '';
  }

  cancelAccountDeletion(): void {
    this.confirmingDeletion.set(false);
    this.deletePassword = '';
    this.deleteFailure.set(null);
  }

  /** Deletes the account for good, after confirming the password. */
  confirmAccountDeletion(): void {
    if (!this.deletePassword) {
      return;
    }

    this.deleting.set(true);
    this.deleteFailure.set(null);

    this.auth
      .deleteAccount(this.deletePassword)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.deleting.set(false);
          this.confirmingDeletion.set(false);
          this.isOpen = false;
          toast.success(this.i18n.t('toasts.accountDeleted'), { position: 'bottom-right' });
          void this.router.navigate(['/auth/sign-in']);
        },
        error: (error: ApiError) => {
          this.deleting.set(false);
          this.deleteFailure.set(error);
        },
      });
  }

  public toggleMenu(): void {
    this.isOpen = !this.isOpen;
  }

  toggleThemeMode() {
    this.themeService.theme.update((theme) => {
      const mode = !this.themeService.isDark ? 'dark' : 'light';
      return { ...theme, mode: mode };
    });
  }

  toggleThemeColor(color: string) {
    this.themeService.theme.update((theme) => {
      return { ...theme, color: color };
    });
  }

  setDirection(value: string) {
    this.themeService.theme.update((theme) => {
      return { ...theme, direction: value };
    });
  }
}
