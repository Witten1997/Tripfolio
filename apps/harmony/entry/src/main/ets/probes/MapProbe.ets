import { BusinessError } from '@kit.BasicServicesKit';
import { MapComponent, map, mapCommon } from '@kit.MapKit';
import { probeError } from './CapabilityProbe';

@Component
export struct MapProbe {
  @State private opened: boolean = false;
  @State private result: string = '尚未初始化。地图服务开通、应用身份与签名鉴权须在真机核实。';
  private controller?: map.MapComponentController;
  private timer: number = -1;
  private options: mapCommon.MapOptions = {
    position: { target: { latitude: 39.9087, longitude: 116.3975 }, zoom: 13 }
  };
  private loaded = (): void => {
    clearTimeout(this.timer);
    this.result = '收到 mapLoad；仍需目视确认真实底图、标记，以及拖动和缩放。';
  };
  private failed = (error: BusinessError): void => {
    clearTimeout(this.timer);
    this.result = `${probeError(error)}；地图能力未通过，需核服务权限及应用鉴权。`;
  };
  private ready = async (error: BusinessError, controller: map.MapComponentController): Promise<void> => {
    if (!this.opened) {
      return;
    }
    if (error) {
      this.failed(error);
      return;
    }
    try {
      this.controller = controller;
      controller.getEventManager().on('mapLoad', this.loaded);
      controller.getEventManager().on('error', this.failed);
      this.result = '控制器初始化成功，等待地图加载；这不代表底图可用。';
      await controller.addMarker({ position: this.options.position.target, title: 'H03 固定测试点' });
    } catch (failure) {
      this.failed(failure as BusinessError);
    }
  };

  aboutToDisappear(): void {
    this.opened = false;
    clearTimeout(this.timer);
    try {
      this.controller?.getEventManager().off('mapLoad', this.loaded);
      this.controller?.getEventManager().off('error', this.failed);
    } catch (error) {
      this.result = probeError(error as BusinessError);
    }
    this.controller = undefined;
  }

  build() {
    Column({ space: 12 }) {
      Text('原生地图').fontSize(22).fontWeight(FontWeight.Bold)
      Text('只显示固定测试点，不申请定位。点击后由地图服务联网加载；搜索、路线及离线地图不由本探针证明。')
      Button('初始化原生地图').enabled(!this.opened).onClick(() => {
        if (!canIUse('SystemCapability.Map.Core')) {
          this.result = '此设备未声明原生地图能力，不能初始化；未通过。';
          return;
        }
        this.opened = true;
        this.result = '等待地图回调…';
        this.timer = setTimeout(() => {
          this.result = '20 秒内未收到地图加载完成；请核网络、服务权限和应用鉴权。';
        }, 20000);
      })
      if (this.opened) {
        MapComponent({ mapOptions: this.options, mapCallback: this.ready }).width('100%').height(320)
      }
      Text(this.result).width('100%').fontSize(16)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
