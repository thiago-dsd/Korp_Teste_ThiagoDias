import { ChangeDetectionStrategy, Component } from '@angular/core';
import { AngularSvgIconModule } from 'angular-svg-icon';

/**
 * Landing page of the application. The product and invoice screens are
 * reached from the sidebar.
 */
@Component({
  selector: 'app-home',
  imports: [AngularSvgIconModule],
  templateUrl: './home.component.html',
  changeDetection: ChangeDetectionStrategy.Eager,
})
export class HomeComponent {
  readonly steps = [
    {
      icon: 'assets/icons/heroicons/outline/cube.svg',
      title: 'Register the products',
      description: 'Every product has a code, a description and the balance available in stock.',
    },
    {
      icon: 'assets/icons/heroicons/outline/folder.svg',
      title: 'Create an invoice',
      description: 'Invoices are numbered in sequence and start open, holding one line per product.',
    },
    {
      icon: 'assets/icons/heroicons/outline/download.svg',
      title: 'Print it',
      description: 'Printing closes the invoice and takes the quantities out of the stock balances.',
    },
  ];
}
