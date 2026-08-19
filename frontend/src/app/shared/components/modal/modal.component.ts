import { DOCUMENT } from '@angular/common';
import { AfterViewInit, Component, ElementRef, OnDestroy, inject, input, output, viewChild } from '@angular/core';

/**
 * The shell every dialog in the application sits in.
 *
 * Each screen used to draw its own overlay, which meant each screen also had
 * to remember Escape, the focus and the page scrolling underneath — and none
 * of them did. Putting it here makes those behaviours a property of dialogs
 * rather than something to get right four times.
 */
@Component({
  selector: 'app-modal',
  template: `
    <!--
      The backdrop is a convenience for the mouse, not the only way out: Escape
      closes the dialog from the panel below, which is focused on open. Making
      the backdrop itself focusable would put a tab stop on a decorative
      overlay, which is worse for a keyboard than what the rule is guarding.
    -->
    <!-- eslint-disable-next-line @angular-eslint/template/click-events-have-key-events, @angular-eslint/template/interactive-supports-focus -->
    <div class="fixed inset-0 z-30 flex items-center justify-center bg-black/40 p-4" (click)="onBackdrop($event)">
      <div
        #panel
        role="dialog"
        aria-modal="true"
        [attr.aria-labelledby]="labelledBy()"
        tabindex="-1"
        (keydown.escape)="dismiss()"
        class="surface flex max-h-[90vh] w-full flex-col overflow-y-auto p-6 shadow-lg"
        [class]="width()">
        <ng-content></ng-content>
      </div>
    </div>
  `,
})
export class ModalComponent implements AfterViewInit, OnDestroy {
  private readonly document = inject(DOCUMENT);

  /** Id of the heading inside, so assistive technology announces the dialog. */
  readonly labelledBy = input<string | null>(null);
  /** Tailwind width class, because a form and a history are not the same size. */
  readonly width = input('max-w-md');
  /**
   * Whether clicking the backdrop closes the dialog. It does not when there is
   * unsaved typing inside, where a stray click would throw the work away.
   */
  readonly dismissOnBackdrop = input(true);

  readonly closed = output<void>();

  private readonly panel = viewChild.required<ElementRef<HTMLElement>>('panel');
  private previouslyFocused: HTMLElement | null = null;

  ngAfterViewInit(): void {
    // Where focus was, so it can go back there on close: reopening the page
    // with focus on the body loses the operator's place entirely.
    this.previouslyFocused = this.document.activeElement as HTMLElement | null;

    const firstField = this.panel().nativeElement.querySelector<HTMLElement>(
      'input:not([type=hidden]), textarea, select, button',
    );
    (firstField ?? this.panel().nativeElement).focus();

    // The page behind must not scroll under the dialog.
    this.document.body.style.overflow = 'hidden';
  }

  ngOnDestroy(): void {
    this.document.body.style.overflow = '';
    this.previouslyFocused?.focus();
  }

  onBackdrop(event: MouseEvent): void {
    if (this.dismissOnBackdrop() && event.target === event.currentTarget) {
      this.dismiss();
    }
  }

  dismiss(): void {
    this.closed.emit();
  }
}
