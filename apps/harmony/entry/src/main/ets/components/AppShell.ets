import { AppDestination, SolidDockSurface, TripfolioTheme } from '../design/TripfolioTheme';
import { AppDock } from './AppDock';

@Component
export struct AppShell {
  @Prop selected: AppDestination = AppDestination.Trips;
  @Prop keyboardVisible: boolean = false;
  // Additional unconsumed system insets in vp; leave zero in a system-safe viewport.
  @Prop topInset: number = 0;
  @Prop bottomInset: number = 0;
  @Prop leftInset: number = 0;
  @Prop rightInset: number = 0;
  onSelect: (destination: AppDestination) => void = () => {};
  @BuilderParam content: () => void = this.emptyContent;
  @BuilderParam dockSurface: () => void = SolidDockSurface;
  @State private dockHeight: number = TripfolioTheme.dockInitialHeight;

  @Builder
  emptyContent() {}

  private contentBottomSpace(): number {
    return this.keyboardVisible ? TripfolioTheme.pageInset :
      this.dockHeight + TripfolioTheme.dockBottomGap + TripfolioTheme.pageInset;
  }

  build() {
    Stack({ alignContent: Alignment.Bottom }) {
      Scroll() {
        Column() {
          this.content()
        }
        .width('100%')
        .alignItems(HorizontalAlign.Start)
        .padding({
          top: TripfolioTheme.pageInset,
          left: TripfolioTheme.pageInset,
          right: TripfolioTheme.pageInset,
          bottom: this.contentBottomSpace()
        })
      }
      .id('h05-content-scroll')
      .width('100%')
      .height('100%')
      .align(Alignment.TopStart)
      .scrollBar(BarState.Auto)

      if (!this.keyboardVisible) {
        Row() {
          AppDock({ selected: this.selected, onSelect: this.onSelect, dockSurface: this.dockSurface })
            .onAreaChange((_oldArea: Area, newArea: Area) => {
              const height = Number(newArea.height);
              if (Number.isFinite(height) && height > 0 && this.dockHeight !== height) {
                this.dockHeight = height;
              }
            })
        }
        .width('100%')
        .constraintSize({ maxWidth: TripfolioTheme.dockMaxWidth })
        .padding({ left: TripfolioTheme.spaceLarge, right: TripfolioTheme.spaceLarge })
        .margin({ bottom: TripfolioTheme.dockBottomGap })
      }
    }
    .width('100%')
    .height('100%')
    .padding({
      top: Math.max(0, this.topInset),
      bottom: Math.max(0, this.bottomInset),
      left: Math.max(0, this.leftInset),
      right: Math.max(0, this.rightInset)
    })
    .backgroundColor(TripfolioTheme.canvas)
  }
}
