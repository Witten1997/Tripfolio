import { defineStore } from 'pinia'
import { ref } from 'vue'

import type { components } from '@tripfolio/contracts/openapi/v1'

import { api } from '@/shared/api/client'

export type Metadata = components['schemas']['Metadata']
export type LoadStatus = 'idle' | 'loading' | 'ready' | 'error'

/** 服务端固定枚举与币种精度；随程序版本变化，登录前即可加载。 */
export const useMetadataStore = defineStore('metadata', () => {
  const metadata = ref<Metadata | null>(null)
  const status = ref<LoadStatus>('idle')
  const error = ref<string | null>(null)

  async function load(): Promise<void> {
    status.value = 'loading'
    error.value = null
    try {
      const { data, error: problem, response } = await api.GET('/metadata')
      if (problem || !data) {
        status.value = 'error'
        error.value = problem?.detail || problem?.title || `请求失败（${response.status}）`
        return
      }
      metadata.value = data.data
      status.value = 'ready'
    } catch (cause) {
      status.value = 'error'
      error.value = cause instanceof Error ? cause.message : '网络错误'
    }
  }

  /** 币种小数位；未加载或未知币种时按 2 位处理，只用于展示，不用于提交。 */
  function minorUnits(code: string): number {
    return metadata.value?.currencies.find((c) => c.code === code)?.minor_units ?? 2
  }

  return { metadata, status, error, load, minorUnits }
})
