import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { authGuard } from 'src/app/core/guards/auth.guard';
import { LayoutComponent } from './layout.component';

const routes: Routes = [
  {
    path: 'home',
    component: LayoutComponent,
    canActivate: [authGuard],
    loadChildren: () => import('../home/home.module').then((m) => m.HomeModule),
  },
  {
    path: 'products',
    component: LayoutComponent,
    canActivate: [authGuard],
    loadChildren: () => import('../products/products.module').then((m) => m.ProductsModule),
  },
  {
    path: 'invoices',
    component: LayoutComponent,
    canActivate: [authGuard],
    loadChildren: () => import('../invoices/invoices.module').then((m) => m.InvoicesModule),
  },
  { path: '', redirectTo: 'home', pathMatch: 'full' },
  { path: '**', redirectTo: 'errors/404' },
];

@NgModule({
  imports: [RouterModule.forChild(routes)],
  exports: [RouterModule],
})
export class LayoutRoutingModule {}
