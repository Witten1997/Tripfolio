import { BusinessError, deviceInfo } from '@kit.BasicServicesKit';
import { http } from '@kit.NetworkKit';
import { util } from '@kit.ArkTS';
import { isProbeHttps, probeError } from './CapabilityProbe';

interface ProbeMessage {
  role: string;
  content: string;
}

interface ProbeRequest {
  model: string;
  stream: boolean;
  max_tokens: number;
  messages: ProbeMessage[];
}

interface ProbeDelta {
  content?: string;
}

interface ProbeChoice {
  delta?: ProbeDelta;
}

interface ProbeChunk {
  choices?: ProbeChoice[];
  error?: Object;
}

@Component
export struct ModelProbe {
  @State private endpoint: string = '';
  @State private model: string = '';
  @State private apiToken: string = '';
  @State private consent: boolean = false;
  @State private busy: boolean = false;
  @State private result: string = '未配置、未调用。仅验证兼容 chat/completions 的文本 SSE。';
  @State private output: string = '';
  private request?: http.HttpRequest;
  private generation: number = 0;
  private timer: number = -1;
  private decoder?: util.TextDecoder;
  private buffer: string = '';
  private bytes: number = 0;
  private done: boolean = false;
  private ended: boolean = false;
  private code: number = 0;
  private eventStream: boolean = false;
  private protocolError: boolean = false;

  aboutToDisappear(): void {
    this.cancel('页面已离开，请求已取消；不自动重试。');
    this.apiToken = '';
    this.consent = false;
  }

  private cancel(message: string): void {
    this.generation++;
    clearTimeout(this.timer);
    const request = this.request;
    this.request = undefined;
    this.busy = false;
    try {
      request?.destroy();
      this.result = message;
    } catch (error) {
      this.result = `${message}；释放请求时 ${probeError(error as BusinessError)}`;
    }
  }

  private consume(text: string): void {
    this.buffer += text;
    let separator = this.buffer.search(/\r?\n\r?\n/);
    while (separator >= 0) {
      if (separator > 8192) {
        this.cancel('单个 SSE 事件超出 8 KiB 限制，已取消；结果不完整。');
        return;
      }
      const block = this.buffer.substring(0, separator);
      const width = this.buffer.substring(separator).startsWith('\r\n\r\n') ? 4 :
        this.buffer.substring(separator).startsWith('\n\n') ? 2 : 3;
      this.buffer = this.buffer.substring(separator + width);
      const lines = block.split(/\r?\n/);
      const data: string[] = [];
      lines.forEach((line: string) => {
        if (line.startsWith('data:')) {
          const field = line.substring(5);
          data.push(field.startsWith(' ') ? field.substring(1) : field);
        }
      });
      if (data.length > 0) {
        const payload = data.join('\n');
        if (payload === '[DONE]') {
          this.done = true;
        } else if (this.done) {
          this.protocolError = true;
        } else {
          try {
            const chunk = JSON.parse(payload) as ProbeChunk;
            if (chunk === null || chunk.error || !Array.isArray(chunk.choices)) {
              this.protocolError = true;
            } else {
              for (const choice of chunk.choices) {
                const content = choice?.delta?.content;
                if (content !== undefined && content !== null) {
                  if (typeof content !== 'string') {
                    this.protocolError = true;
                  } else {
                    if (this.output.length + content.length > 4096) {
                      this.cancel('文本超出 4096 字符限制，已取消；结果不完整。');
                      return;
                    }
                    this.output += content;
                  }
                }
              }
            }
          } catch {
            this.protocolError = true;
          }
        }
      }
      separator = this.buffer.search(/\r?\n\r?\n/);
    }
    if (this.buffer.length > 8192) {
      this.cancel('单个 SSE 事件超出 8 KiB 限制，已取消；结果不完整。');
    }
  }

  private finish(): void {
    if (!this.ended || this.code === 0 || !this.busy) {
      return;
    }
    if (this.code < 200 || this.code >= 300) {
      this.cancel(`HTTP ${this.code}；请求未通过，不显示服务端错误正文。`);
    } else if (!this.eventStream || this.protocolError || !this.done || this.buffer.trim().length > 0 ||
      this.output.length === 0) {
      this.cancel(`HTTP ${this.code}；SSE 类型、帧或结束标记校验未通过，保留片段，不判成功。`);
    } else {
      this.cancel(`HTTP ${this.code}，收到完整 SSE 结束标记，共 ${this.bytes} 字节。取消、断网及错误场景需分别实测。`);
    }
  }

  private async start(): Promise<void> {
    if (this.busy || !this.consent) {
      return;
    }
    if (!isProbeHttps(this.endpoint.trim()) || this.model.trim().length === 0) {
      this.result = '请填写完整 HTTPS chat/completions 地址和模型名；地址不能含账号、查询参数或片段。';
      return;
    }
    if (deviceInfo.sdkApiVersion < 23) {
      this.result = '此探针要求 API 23+ 以禁用重定向；API 12 模型直连尚未验证。';
      return;
    }
    const generation = ++this.generation;
    this.busy = true;
    this.output = '';
    this.buffer = '';
    this.bytes = 0;
    this.done = false;
    this.ended = false;
    this.code = 0;
    this.eventStream = false;
    this.protocolError = false;
    this.result = '请求进行中，可随时取消；无自动重试。';
    this.timer = setTimeout(() => this.cancel('达到 60 秒总时限，已取消；结果不完整。'), 60000);
    try {
      this.decoder = util.TextDecoder.create('utf-8', { fatal: true });
      const request = http.createHttp();
      this.request = request;
      request.on('headersReceive', (headers: Object) => {
        if (generation !== this.generation) {
          return;
        }
        const values = headers as Record<string, string>;
        const type = values['content-type'] ?? values['Content-Type'] ?? '';
        this.eventStream = typeof type === 'string' && type.toLowerCase().includes('text/event-stream');
      });
      request.on('dataReceive', (chunk: ArrayBuffer) => {
        if (generation !== this.generation) {
          return;
        }
        this.bytes += chunk.byteLength;
        if (this.bytes > 65536) {
          this.cancel('响应超出 64 KiB 限制，已取消；结果不完整。');
          return;
        }
        try {
          this.consume(this.decoder!.decodeToString(new Uint8Array(chunk), { stream: true }));
        } catch {
          this.cancel('UTF-8 流解码失败；保留片段，不判成功。');
        }
      });
      request.on('dataEnd', () => {
        if (generation !== this.generation) {
          return;
        }
        try {
          this.consume(this.decoder!.decodeToString(new Uint8Array(0), { stream: false }));
          this.ended = true;
          this.finish();
        } catch {
          this.cancel('流结束时 UTF-8 数据不完整，不判成功。');
        }
      });
      const headers: Record<string, string> = { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' };
      if (this.apiToken.length > 0) {
        headers['Authorization'] = `Bearer ${this.apiToken}`;
      }
      const body: ProbeRequest = {
        model: this.model.trim(), stream: true, max_tokens: 32,
        messages: [{ role: 'user', content: '这是连接测试，请只回复：连接成功。' }]
      };
      const code = await request.requestInStream(this.endpoint.trim(), {
        method: http.RequestMethod.POST, header: headers, extraData: JSON.stringify(body),
        usingCache: false, connectTimeout: 10000, readTimeout: 15000, maxRedirects: 0
      });
      if (generation === this.generation) {
        this.code = code;
        if (code < 200 || code >= 300) {
          this.cancel(`HTTP ${code}；请求未通过，不显示服务端错误正文。`);
        } else {
          this.finish();
        }
      }
    } catch (error) {
      if (generation === this.generation) {
        this.cancel(`${probeError(error as BusinessError)}；保留片段，不判成功，不自动重试。`);
      }
    }
  }

  build() {
    Column({ space: 12 }) {
      Text('模型 HTTPS 直连').fontSize(22).fontWeight(FontWeight.Bold)
      Text('仅发送固定连接测试语句，最多请求 32 tokens。密钥仅暂存内存，离开页面清除；不会发送旅行数据。')
      TextInput({ placeholder: '完整 HTTPS chat/completions 地址', text: this.endpoint }).enabled(!this.busy)
        .onChange((value: string) => { this.endpoint = value; this.consent = false; })
      TextInput({ placeholder: '模型名称', text: this.model }).enabled(!this.busy)
        .onChange((value: string) => { this.model = value; this.consent = false; })
      TextInput({ placeholder: '测试密钥（无鉴权服务可留空）', text: this.apiToken }).type(InputType.Password).enabled(!this.busy)
        .onChange((value: string) => { this.apiToken = value; this.consent = false; })
      Row() {
        Checkbox().select(this.consent).enabled(!this.busy).onChange((value: boolean) => this.consent = value)
        Text('确认使用此测试服务，调用可能计费').layoutWeight(1)
      }
      Button('发送一次测试').enabled(this.consent && !this.busy).onClick(() => this.start())
      Button('取消请求').enabled(this.busy).onClick(() => this.cancel('用户已取消；保留片段，不判成功。'))
      Text(this.result).width('100%').fontSize(16)
      Text(this.output).width('100%').fontSize(16)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
