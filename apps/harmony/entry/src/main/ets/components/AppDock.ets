import { APP_DESTINATIONS, AppDestination, DockDestination, SolidDockSurface,
  TripfolioTheme } from '../design/TripfolioTheme';

@Component
export struct AppDock {
  @Prop selected: AppDestination = AppDestination.Trips;
  onSelect: (destination: AppDestination) => void = () => {};
  @BuilderParam dockSurface: () => void = SolidDockSurface;

  @Builder
  destinationButton(destination: DockDestination) {
    Button({ type: ButtonType.Normal, stateEffect: true }) {
      Column({ space: TripfolioTheme.spaceSmall }) {
        SymbolGlyph(destination.symbol)
          .fontSize(TripfolioTheme.dockIconSize)
          .fontColor([this.selected === destination.id ? TripfolioTheme.accent : TripfolioTheme.secondaryText])
          .accessibilityLevel('no')
        Text(destination.label)
          .fontSize(TripfolioTheme.captionSize)
          .fontWeight(this.selected === destination.id ? FontWeight.Bold : FontWeight.Medium)
          .fontColor(this.selected === destination.id ? TripfolioTheme.accent : TripfolioTheme.secondaryText)
          .textAlign(TextAlign.Center)
          .width('100%')
      }
      .width('100%')
      .padding({ top: TripfolioTheme.spaceMedium, bottom: TripfolioTheme.spaceMedium })
    }
    .id(`h05-dock-${destination.id}`)
    .layoutWeight(1)
    .constraintSize({ minHeight: TripfolioTheme.dockMinItemHeight })
    .padding({ left: TripfolioTheme.spaceSmall, right: TripfolioTheme.spaceSmall })
    .borderRadius(TripfolioTheme.dockItemRadius)
    .backgroundColor(this.selected === destination.id ? TripfolioTheme.selectedSurface : Color.Transparent)
    .accessibilityGroup(true)
    .accessibilityText(this.selected === destination.id ? `${destination.label}，已选中` : destination.label)
    .onClick(() => {
      if (this.selected !== destination.id) {
        this.onSelect(destination.id);
      }
    })
  }

  build() {
    Row({ space: TripfolioTheme.spaceSmall }) {
      ForEach(APP_DESTINATIONS, (destination: DockDestination) => {
        this.destinationButton(destination)
      }, (destination: DockDestination) => destination.id)
    }
    .width('100%')
    .padding(TripfolioTheme.spaceMedium)
    .background(this.dockSurface)
    .border({ width: 1, color: TripfolioTheme.outline })
    .borderRadius(TripfolioTheme.dockRadius)
    .clip(true)
  }
}
