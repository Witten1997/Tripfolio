@Component
export struct GlassProbe {
  @State private material: boolean = true;
  @State private dark: boolean = false;
  private bands: string[] = ['#B5DACD', '#E8AD7A', '#8EBED1', '#DCC5DD', '#F2CF71', '#B5DACD'];

  build() {
    Column({ space: 12 }) {
      Text('原生玻璃与降级').fontSize(22).fontWeight(FontWeight.Bold)
      Text('滑动色带观察背景模糊；分别检查深浅色、纯色降级和系统大字号。这是材质探针。')
      Button(this.material ? '切换纯色降级' : '切换原生材质')
        .onClick(() => this.material = !this.material)
      Button(this.dark ? '切换浅色样本' : '切换深色样本').onClick(() => this.dark = !this.dark)
      Stack({ alignContent: Alignment.Bottom }) {
        Scroll() {
          Column() {
            ForEach(this.bands, (color: string, index: number) => {
              Text(`背景 ${index + 1}`).width('100%').height(100).backgroundColor(color).fontColor('#182321')
            }, (_color: string, index: number) => index.toString())
          }
        }.height(280).width('100%')
        Text(this.material ? '原生玻璃 · 拖动背景观察' : '纯色降级 · 保持可读')
          .fontSize(18).fontWeight(FontWeight.Medium).fontColor(this.dark ? '#FFFFFF' : '#172321')
          .padding(20).width('90%').margin({ bottom: 20 }).borderRadius(24)
          .backgroundColor(this.material ? Color.Transparent : this.dark ? '#243532' : '#EDF4F1')
          .backgroundBlurStyle(this.material ? BlurStyle.COMPONENT_THICK : BlurStyle.NONE, {
            colorMode: this.dark ? ThemeColorMode.DARK : ThemeColorMode.LIGHT
          })
      }.width('100%').clip(true).borderRadius(12)
      Text('是否透色、边缘及文字是否清晰需真机目视确认；显示此页面不自动判定通过。').fontSize(14)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
