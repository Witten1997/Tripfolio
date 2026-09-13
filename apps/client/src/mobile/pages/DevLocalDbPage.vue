<script setup lang="ts">
import {
  Button as VanButton,
  Cell as VanCell,
  CellGroup as VanCellGroup,
  NoticeBar as VanNoticeBar,
  Tag as VanTag,
} from 'vant'
import { onMounted, ref } from 'vue'

import { platform } from '@/platform'
import {
  bumpPersistenceCounter,
  runLocalDbSelfCheck,
  type SelfCheckReport,
} from '@/platform/localdb/selfcheck'

const opener = platform.localDatabase
const running = ref(false)
const report = ref<SelfCheckReport | null>(null)
const persistRuns = ref<number | null>(null)
const fatal = ref<string | null>(null)

async function run() {
  if (!opener) return
  running.value = true
  fatal.value = null
  try {
    report.value = await runLocalDbSelfCheck(opener)
  } catch (error) {
    fatal.value = error instanceof Error ? error.message : String(error)
  } finally {
    running.value = false
  }
}

onMounted(async () => {
  if (!opener) return
  try {
    persistRuns.value = await bumpPersistenceCounter(opener)
  } catch (error) {
    fatal.value = error instanceof Error ? error.message : String(error)
  }
})
</script>

<template>
  <VanNoticeBar
    v-if="!opener"
    text="当前平台没有本地数据库（网页端在线使用），请在安卓应用内打开本页。"
  />
  <template v-else>
    <VanCellGroup inset title="重启保留验证">
      <VanCell
        title="本页打开次数（持久化）"
        :value="persistRuns === null ? '…' : String(persistRuns)"
        label="完全退出并重新打开应用后再进入本页，计数应继续递增"
      />
    </VanCellGroup>

    <VanCellGroup inset title="数据链路自检">
      <VanCell>
        <VanButton type="primary" block :loading="running" loading-text="正在执行…" @click="run">
          运行自检
        </VanButton>
      </VanCell>
      <VanNoticeBar v-if="fatal" :text="fatal" />
      <template v-if="report">
        <VanCell
          title="结果"
          :value="report.ok ? '全部通过' : '有失败步骤'"
          :label="`${report.startedAt}，耗时 ${report.durationMs} ms`"
        >
          <template #right-icon>
            <VanTag :type="report.ok ? 'success' : 'danger'">
              {{ report.ok ? '通过' : '失败' }}
            </VanTag>
          </template>
        </VanCell>
        <VanCell
          v-for="step in report.steps"
          :key="step.name"
          :title="step.name"
          :label="step.detail"
        >
          <template #right-icon>
            <VanTag :type="step.ok ? 'success' : 'danger'">{{ step.ok ? '通过' : '失败' }}</VanTag>
          </template>
        </VanCell>
      </template>
    </VanCellGroup>
  </template>
</template>
