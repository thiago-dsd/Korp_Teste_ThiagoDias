import { AfterViewInit, Directive, ElementRef, EventEmitter, OnDestroy, Output, DOCUMENT, inject } from '@angular/core';
import { filter, fromEvent, Subscription } from 'rxjs';

@Directive({
  selector: '[appClickOutside]',
  standalone: true,
})
export class ClickOutsideDirective implements AfterViewInit, OnDestroy {
  private element = inject(ElementRef);
  private document = inject<Document>(DOCUMENT);

  @Output() appClickOutside = new EventEmitter<void>();

  documentClickSubscription: Subscription | undefined;

  ngAfterViewInit(): void {
    this.documentClickSubscription = fromEvent(this.document, 'click')
      .pipe(
        filter((event) => {
          return !this.isInside(event.target as HTMLElement);
        }),
      )
      .subscribe(() => {
        this.appClickOutside.emit();
      });
  }

  ngOnDestroy(): void {
    this.documentClickSubscription?.unsubscribe();
  }

  isInside(elementToCheck: HTMLElement): boolean {
    return elementToCheck === this.element.nativeElement || this.element.nativeElement.contains(elementToCheck);
  }
}
