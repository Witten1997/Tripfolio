import { BusinessError } from '@kit.BasicServicesKit';
import { url } from '@kit.ArkTS';

export const PROBE_LAUNCH: string = Date.now().toString();

export class ProbeFailure extends Error {
  code: number;

  constructor(error: BusinessError) {
    super('H03 platform operation failed');
    this.code = error.code ?? -1;
  }
}

export function probeError(error: BusinessError): string {
  return `系统错误码 ${error.code ?? -1}`;
}

export function isProbeHttps(value: string): boolean {
  try {
    const address = new url.URL(value);
    return address.protocol === 'https:' && address.hostname.length > 0 &&
      address.username.length === 0 && address.password.length === 0 &&
      address.search.length === 0 && address.hash.length === 0;
  } catch {
    return false;
  }
}
