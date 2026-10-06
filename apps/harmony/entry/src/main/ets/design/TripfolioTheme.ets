export enum AppDestination {
  AI = 'ai',
  Trips = 'trips',
  Dashboard = 'dashboard',
  Profile = 'profile'
}

export interface DockDestination {
  id: AppDestination;
  label: string;
  symbol: Resource;
}

export const APP_DESTINATIONS: DockDestination[] = [
  { id: AppDestination.AI, label: 'AI', symbol: $r('sys.symbol.wand_and_stars') },
  { id: AppDestination.Trips, label: '旅行', symbol: $r('sys.symbol.airplane') },
  { id: AppDestination.Dashboard, label: '看板', symbol: $r('sys.symbol.square_grid_2x2') },
  { id: AppDestination.Profile, label: '我的', symbol: $r('sys.symbol.person') }
];

export class TripfolioTheme {
  static readonly canvas: Resource = $r('app.color.h05_canvas');
  static readonly surface: Resource = $r('app.color.h05_surface');
  static readonly text: Resource = $r('app.color.h05_text');
  static readonly secondaryText: Resource = $r('app.color.h05_secondary_text');
  static readonly accent: Resource = $r('app.color.h05_accent');
  static readonly selectedSurface: Resource = $r('app.color.h05_selected_surface');
  static readonly outline: Resource = $r('app.color.h05_outline');
  static readonly spaceSmall: number = 4;
  static readonly spaceMedium: number = 8;
  static readonly spaceLarge: number = 16;
  static readonly pageInset: number = 20;
  static readonly titleSize: number = 30;
  static readonly bodySize: number = 16;
  static readonly captionSize: number = 12;
  static readonly dockIconSize: string = '24vp';
  static readonly dockRadius: number = 28;
  static readonly dockItemRadius: number = 20;
  static readonly dockMinItemHeight: number = 56;
  static readonly dockInitialHeight: number = 74;
  static readonly dockMaxWidth: number = 520;
  static readonly dockBottomGap: number = 12;
}

// The host may replace this builder after H03 establishes the material capability.
@Builder
export function SolidDockSurface() {
  Column()
    .width('100%')
    .height('100%')
    .backgroundColor(TripfolioTheme.surface)
    .hitTestBehavior(HitTestMode.None)
}
