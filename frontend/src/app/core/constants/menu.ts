import { MenuItem } from '../models/menu.model';

export class Menu {
  public static pages: MenuItem[] = [
    {
      group: 'Invoicing',
      separator: false,
      items: [
        {
          icon: 'assets/icons/heroicons/outline/chart-pie.svg',
          label: 'Home',
          route: '/home',
        },
        {
          icon: 'assets/icons/heroicons/outline/cube.svg',
          label: 'Products',
          route: '/products',
        },
        {
          icon: 'assets/icons/heroicons/outline/folder.svg',
          label: 'Invoices',
          route: '/invoices',
        },
      ],
    },
    {
      group: 'Account',
      separator: true,
      items: [
        {
          icon: 'assets/icons/heroicons/outline/lock-closed.svg',
          label: 'Auth',
          route: '/auth',
          children: [
            { label: 'Sign in', route: '/auth/sign-in' },
            { label: 'Sign up', route: '/auth/sign-up' },
          ],
        },
        {
          icon: 'assets/icons/heroicons/outline/exclamation-triangle.svg',
          label: 'Errors',
          route: '/errors',
          children: [
            { label: '404', route: '/errors/404' },
            { label: '500', route: '/errors/500' },
          ],
        },
      ],
    },
  ];
}
