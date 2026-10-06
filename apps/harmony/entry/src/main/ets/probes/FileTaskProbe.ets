import { common } from '@kit.AbilityKit';
import { BusinessError, request } from '@kit.BasicServicesKit';
import { preferences } from '@kit.ArkData';
import { fileIo, hash } from '@kit.CoreFileKit';
import { isProbeHttps, ProbeFailure, probeError } from './CapabilityProbe';

let fileTaskBusy: boolean = false;

@Component
export struct FileTaskProbe {
  @State private endpoint: string = '';
  @State private expectedHash: string = '';
  @State private upload: boolean = true;
  @State private consent: boolean = false;
  @State private busy: boolean = false;
  @State private initialized: boolean = false;
  @State private intent: string = '';
  @State private taskId: string = '';
  @State private state: number = -1;
  @State private result: string = '正在读取本探针任务记录…';
  private task?: request.agent.Task;
  private saved?: preferences.Preferences;
  private leaving: boolean = false;
  private progress = (value: request.agent.Progress): void => {
    this.state = value.state;
    this.result = `任务 ${this.taskId}，状态 ${value.state}，已处理 ${value.processed} 字节。` +
      (value.state === request.agent.State.COMPLETED ?
        '系统报告完成；上传须核服务端收据，下载须校验 SHA-256。' : '未判定成功。');
  };

  aboutToAppear(): void {
    this.leaving = false;
    this.run('恢复');
  }

  aboutToDisappear(): void {
    this.leaving = true;
    this.detach();
  }

  private context(): common.UIAbilityContext {
    return this.getUIContext().getHostContext() as common.UIAbilityContext;
  }

  private async storage(): Promise<preferences.Preferences> {
    if (!this.saved) {
      this.saved = await preferences.getPreferences(this.context(), { name: 'h03_file_task' });
    }
    return this.saved;
  }

  private detach(): void {
    try {
      this.task?.off('progress', this.progress);
      this.task?.off('completed', this.progress);
      this.task?.off('failed', this.progress);
      this.task?.off('pause', this.progress);
      this.task?.off('resume', this.progress);
    } catch (error) {
      this.result = probeError(error as BusinessError);
    }
    this.task = undefined;
  }

  private attach(task: request.agent.Task): void {
    this.detach();
    if (this.leaving) {
      return;
    }
    this.task = task;
    task.on('progress', this.progress);
    task.on('completed', this.progress);
    task.on('failed', this.progress);
    task.on('pause', this.progress);
    task.on('resume', this.progress);
  }

  private filePath(): string {
    if (!/^h03-\d+$/.test(this.intent)) {
      throw new Error('Invalid probe intent');
    }
    return `${this.context().filesDir}/${this.intent}.bin`;
  }

  private async recover(): Promise<void> {
    this.detach();
    this.state = -1;
    const saved = await this.storage();
    this.intent = await saved.get('intent', '') as string;
    this.taskId = await saved.get('taskId', '') as string;
    if (this.intent.length === 0) {
      this.initialized = true;
      this.result = '尚无任务。配置受控测试地址后手动创建。';
      return;
    }
    this.filePath();
    this.upload = await saved.get('upload', true) as boolean;
    this.expectedHash = await saved.get('expectedHash', '') as string;
    if (this.taskId.length === 0) {
      const ids = await this.findTasks();
      const matched: string[] = [];
      for (const id of ids) {
        const info = await this.taskInfo(id);
        if (info?.title === this.intent) {
          matched.push(id);
        }
      }
      if (matched.length !== 1) {
        this.initialized = true;
        this.result = `未完成创建的意图 ${this.intent}，匹配任务 ${matched.length}。不会自动新建；可结束本次记录后重新配置。`;
        return;
      }
      this.taskId = matched[0];
      await saved.put('taskId', this.taskId);
      await saved.flush();
    }
    const info = await this.taskInfo(this.taskId);
    if (!info) {
      this.initialized = true;
      this.state = request.agent.State.REMOVED;
      this.result = '原系统任务已移除或不存在；记录保留，不自动重新上传。可结束本次记录。';
      return;
    }
    if (info.title !== this.intent) {
      throw new Error('Task identity mismatch');
    }
    this.progress(info.progress);
    if (info.progress.state !== request.agent.State.COMPLETED && info.progress.state !== request.agent.State.REMOVED) {
      this.attach(await request.agent.getTask(this.context(), this.taskId));
    }
    this.initialized = true;
    this.result += ` 重读系统原任务，重试次数 ${info.tries}，故障类别 ${info.faults}；未自动启动。`;
  }

  private async taskInfo(id: string): Promise<request.agent.TaskInfo | undefined> {
    try {
      return await request.agent.show(id);
    } catch (error) {
      if ((error as BusinessError).code === 21900006) {
        return undefined;
      }
      throw new ProbeFailure(error as BusinessError);
    }
  }

  private async findTasks(): Promise<string[]> {
    this.filePath();
    return await request.agent.search({ after: Number(this.intent.substring(4)) - 1000, before: Date.now() + 1000 });
  }

  private async createTask(): Promise<void> {
    if (!this.initialized || this.intent.length > 0 || !this.consent) {
      return;
    }
    if (!isProbeHttps(this.endpoint.trim()) || (!this.upload && !/^[a-fA-F0-9]{64}$/.test(this.expectedHash.trim()))) {
      this.result = '需要不带凭据和查询参数的 HTTPS 地址；下载还需预先提供测试文件 SHA-256。';
      return;
    }
    const saved = await this.storage();
    this.intent = `h03-${Date.now()}`;
    this.taskId = '';
    await saved.put('intent', this.intent);
    await saved.put('taskId', '');
    await saved.put('upload', this.upload);
    await saved.put('expectedHash', this.expectedHash.trim().toLowerCase());
    await saved.flush();
    const path = this.filePath();
    if (this.upload) {
      const file = await fileIo.open(path, fileIo.OpenMode.CREATE | fileIo.OpenMode.READ_WRITE | fileIo.OpenMode.TRUNC);
      try {
        const block = 'H'.repeat(65536);
        for (let index = 0; index < 16; index++) {
          if (await fileIo.write(file.fd, block) !== block.length) {
            throw new Error('Incomplete synthetic file');
          }
        }
      } finally {
        await fileIo.close(file);
      }
      this.expectedHash = await hash.hash(path, 'sha256');
      await saved.put('expectedHash', this.expectedHash);
      await saved.flush();
    }
    const config: request.agent.Config = {
      action: this.upload ? request.agent.Action.UPLOAD : request.agent.Action.DOWNLOAD,
      url: this.endpoint.trim(), title: this.intent,
      mode: request.agent.Mode.BACKGROUND, network: request.agent.Network.WIFI,
      metered: false, roaming: false, retry: false, redirect: false,
      method: this.upload ? 'POST' : 'GET',
      headers: { 'X-H03-Probe-ID': this.intent }
    };
    if (this.upload) {
      config.data = [{ name: 'file', value: { path: path, filename: `${this.intent}.bin` } }];
    } else {
      config.saveas = path;
      config.overwrite = false;
      config.ends = 8388607;
    }
    const task = await request.agent.create(this.context(), config);
    this.taskId = task.tid;
    await saved.put('taskId', this.taskId);
    await saved.flush();
    this.attach(task);
    await task.start();
    const info = await request.agent.show(this.taskId);
    this.progress(info.progress);
  }

  private async verify(): Promise<void> {
    const info = await request.agent.show(this.taskId);
    if (info.title !== this.intent || info.progress.state !== request.agent.State.COMPLETED) {
      this.result = '系统任务尚未完成，不能验证成功。';
      return;
    }
    const path = this.filePath();
    const digest = await hash.hash(path, 'sha256');
    const stat = await fileIo.stat(path);
    if (digest.toLowerCase() !== this.expectedHash.toLowerCase()) {
      this.result = 'SHA-256 不匹配，文件校验失败。';
      return;
    }
    this.result = `${stat.size} 字节，SHA-256 ${digest}\n` + (this.upload ?
      '这是本地源文件摘要；必须与服务端接收文件及次数核对，不能据此宣称上传内容通过。' :
      '下载内容校验通过；后台期间进度、断网和进程重启仍需按实际操作分别验证。');
  }

  private async remove(): Promise<void> {
    if (this.taskId.length > 0) {
      const info = await this.taskInfo(this.taskId);
      if (info && info.title !== this.intent) {
        throw new Error('Task identity mismatch');
      }
      if (info) {
        await request.agent.remove(this.taskId);
      }
    } else {
      const ids = await this.findTasks();
      for (const id of ids) {
        const info = await this.taskInfo(id);
        if (info?.title === this.intent) {
          await request.agent.remove(id);
        }
      }
    }
    this.detach();
    const saved = await this.storage();
    await saved.clear();
    await saved.flush();
    this.taskId = '';
    this.intent = '';
    this.state = -1;
    this.initialized = true;
    this.consent = false;
    this.result = '本测试任务与恢复记录已移除；沙箱测试文件保留，不操作其他文件。';
  }

  private async run(action: string): Promise<void> {
    if (fileTaskBusy) {
      this.result = '上一页面的任务操作尚未结束，请稍后查询原任务。';
      return;
    }
    if (!canIUse('SystemCapability.Request.FileTransferAgent')) {
      this.result = '此设备未声明系统文件传输代理能力；未通过。';
      return;
    }
    fileTaskBusy = true;
    this.busy = true;
    try {
      if (action === '创建') {
        await this.createTask();
      } else if (action === '暂停') {
        await this.task?.pause();
        await this.recover();
      } else if (action === '继续') {
        if (this.state === request.agent.State.INITIALIZED || this.state === request.agent.State.STOPPED) {
          await this.task?.start();
        } else {
          await this.task?.resume();
        }
        await this.recover();
      } else if (action === '校验') {
        await this.verify();
      } else if (action === '移除') {
        await this.remove();
      } else {
        await this.recover();
      }
    } catch (error) {
      this.result = `${probeError(error as BusinessError)}；未判通过，原任务记录保留。可重新查询，勿据此重复上传。`;
    } finally {
      this.busy = false;
      fileTaskBusy = false;
      if (this.leaving) {
        this.detach();
      }
    }
  }

  build() {
    Column({ space: 12 }) {
      Text('系统后台文件任务').fontSize(22).fontWeight(FontWeight.Bold)
      Text('仅 Wi-Fi。上传 1 MiB 合成文件到 multipart POST 测试服务；下载最多请求前 8 MiB。不开自动重试，恢复不新建任务。')
      Button(this.upload ? '上传模式 · 切换下载' : '下载模式 · 切换上传')
        .enabled(!this.busy && this.intent.length === 0).onClick(() => { this.upload = !this.upload; this.consent = false; })
      TextInput({ placeholder: '受控 HTTPS 测试地址', text: this.endpoint }).enabled(!this.busy && this.intent.length === 0)
        .onChange((value: string) => { this.endpoint = value; this.consent = false; })
      if (!this.upload) {
        TextInput({ placeholder: '预期文件 SHA-256', text: this.expectedHash }).enabled(!this.busy && this.intent.length === 0)
          .onChange((value: string) => { this.expectedHash = value; this.consent = false; })
      }
      Row() {
        Checkbox().select(this.consent).enabled(!this.busy && this.intent.length === 0)
          .onChange((value: boolean) => this.consent = value)
        Text('确认使用此受控测试服务传输合成文件').layoutWeight(1)
      }
      Button('创建并启动一次').enabled(this.initialized && this.consent && !this.busy && this.intent.length === 0)
        .onClick(() => this.run('创建'))
      Button('查询 / 恢复原任务').enabled(!this.busy).onClick(() => this.run('恢复'))
      Button('暂停').enabled(!this.busy && this.state === request.agent.State.RUNNING).onClick(() => this.run('暂停'))
      Button('继续原任务').enabled(!this.busy && (this.state === request.agent.State.PAUSED ||
        this.state === request.agent.State.INITIALIZED || this.state === request.agent.State.STOPPED))
        .onClick(() => this.run('继续'))
      Button('核对完成文件').enabled(!this.busy && this.state === request.agent.State.COMPLETED).onClick(() => this.run('校验'))
      Button('结束并移除本测试任务').enabled(!this.busy && this.intent.length > 0).onClick(() => this.run('移除'))
      Text(this.result).width('100%').fontSize(16).copyOption(CopyOptions.LocalDevice)
      Text('切后台后回前台查询；进程重启后按原编号恢复。强行停止后不承诺继续传输，上传断点与去重须结合服务端收据验证。').fontSize(14)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
