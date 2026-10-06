import { AppShell } from '../components/AppShell';
import { APP_DESTINATIONS, AppDestination, TripfolioTheme } from '../design/TripfolioTheme';

@Preview({ title: 'H05 · 四入口', width: 390, height: 844, colorMode: 'light', deviceType: 'phone' })
@Component
struct H05Preview {
  @State selected: AppDestination = AppDestination.Trips;
  @State keyboardVisible: boolean = false;
  @State previewInput: string = '';

  private selectedLabel(): string {
    const destination = APP_DESTINATIONS.find(item => item.id === this.selected);
    return destination ? destination.label : '';
  }

  @Builder
  previewContent() {
    Column({ space: TripfolioTheme.spaceLarge }) {
      Text('Tripfolio')
        .fontSize(TripfolioTheme.captionSize)
        .fontWeight(FontWeight.Medium)
        .fontColor(TripfolioTheme.secondaryText)
      Text(this.selectedLabel())
        .id('h05-preview-title')
        .fontSize(TripfolioTheme.titleSize)
        .fontWeight(FontWeight.Bold)
        .fontColor(TripfolioTheme.text)
      Text('界面预览')
        .fontSize(TripfolioTheme.bodySize)
        .fontColor(TripfolioTheme.secondaryText)
      TextInput({ text: this.previewInput, placeholder: '输入以检查键盘与字号' })
        .fontSize(TripfolioTheme.bodySize)
        .fontColor(TripfolioTheme.text)
        .backgroundColor(TripfolioTheme.surface)
        .onChange((value: string) => { this.previewInput = value; })
      Toggle({ type: ToggleType.Switch, isOn: this.keyboardVisible })
        .id('h05-preview-keyboard')
        .accessibilityText('预览键盘显隐接口')
        .onChange((visible: boolean) => { this.keyboardVisible = visible; })
      Text(this.keyboardVisible ? '键盘显隐接口：Dock 已隐藏' : '键盘显隐接口：Dock 已显示')
        .fontSize(TripfolioTheme.captionSize)
        .fontColor(TripfolioTheme.secondaryText)
    }
    .width('100%')
    .alignItems(HorizontalAlign.Start)
  }

  build() {
    AppShell({
      selected: this.selected,
      keyboardVisible: this.keyboardVisible,
      onSelect: (destination: AppDestination) => { this.selected = destination; },
      content: this.previewContent
    })
  }
}

@Preview({ title: 'H05 · 深色窄屏空内容', width: 320, height: 640, colorMode: 'dark', deviceType: 'phone' })
@Component
struct H05DarkPreview {
  @State selected: AppDestination = AppDestination.Trips;

  build() {
    AppShell({
      selected: this.selected,
      onSelect: (destination: AppDestination) => { this.selected = destination; }
    })
  }
}
