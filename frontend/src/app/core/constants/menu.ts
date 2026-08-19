import { MenuItem } from '../models/menu.model';

/**
 * What the sidebar offers.
 *
 * The template this project started from also listed "Sign in", "Sign up" and
 * the error pages here. They were navigation for a demo, not for a system: a
 * signed in operator has no use for a link to the sign in screen, and nobody
 * navigates to a 404 on purpose. Both are still reachable — one by signing
 * out, the other by getting an address wrong — they are just not offered.
 *
 * `group` and `label` hold translation keys, not literal text: the menu
 * components read them through `TranslatePipe`, the same as every other
 * screen, so the sidebar switches language with the rest of the app instead
 * of being the one place still in whatever language it was hardcoded in.
 */
export class Menu {
  public static pages: MenuItem[] = [
    {
      group: 'nav.groupInvoicing',
      separator: false,
      items: [
        {
          icon: 'assets/icons/heroicons/outline/chart-pie.svg',
          label: 'nav.today',
          route: '/home',
        },
        {
          icon: 'assets/icons/heroicons/outline/cube.svg',
          label: 'nav.products',
          route: '/products',
        },
        {
          icon: 'assets/icons/heroicons/outline/folder.svg',
          label: 'nav.invoices',
          route: '/invoices',
        },
      ],
    },
  ];
}
