import { DatabaseProbe } from '../probes/DatabaseProbe';
import { GlassProbe } from '../probes/GlassProbe';
import { MapProbe } from '../probes/MapProbe';
import { ModelProbe } from '../probes/ModelProbe';
import { FileTaskProbe } from '../probes/FileTaskProbe';

@Entry
@Component
struct Index {
  @State private selected: string = '数据库';
  @State private selectedIndex: number = 0;
  private probes: SelectOption[] = [
    { value: '数据库' }, { value: '玻璃' }, { value: '地图' }, { value: '模型' }, { value: '文件' }
  ];

  build() {
    Column({ space: 12 }) {
      Text('Tripfolio · H03 能力验证').fontSize(24).fontWeight(FontWeight.Bold)
      Select(this.probes)
        .selected(this.selectedIndex).value(this.selected).onSelect((index: number, value: string) => {
          this.selectedIndex = index;
          this.selected = value;
        })
      Scroll() {
        Column() {
          if (this.selected === '数据库') {
            DatabaseProbe()
          } else if (this.selected === '玻璃') {
            GlassProbe()
          } else if (this.selected === '地图') {
            MapProbe()
          } else if (this.selected === '模型') {
            ModelProbe()
          } else {
            FileTaskProbe()
          }
        }.width('100%').padding({ bottom: 32 })
      }.layoutWeight(1).width('100%')
    }.height('100%').width('100%').padding(20).alignItems(HorizontalAlign.Start)
  }
}
