import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { InvoiceDetailComponent } from './invoice-detail.component';
import { InvoiceNewComponent } from './invoice-new.component';
import { InvoicesComponent } from './invoices.component';

const routes: Routes = [
  { path: '', component: InvoicesComponent },
  { path: 'new', component: InvoiceNewComponent },
  { path: ':id', component: InvoiceDetailComponent },
  { path: '**', redirectTo: '' },
];

@NgModule({
  imports: [RouterModule.forChild(routes)],
})
export class InvoicesModule {}
