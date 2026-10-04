<script setup lang="ts">
import { ElAlert, ElProgress, ElTag } from 'element-plus'
import type { DeletionJob } from '@/shared/api/deletion'
import {
  deletionJobHint,
  deletionProgress,
  deletionStages,
  deletionStatuses,
} from '@/shared/deletion/presentation'

defineProps<{ job: DeletionJob }>()
</script>

<template>
  <div class="deletion-status" role="status" aria-live="polite">
    <ElTag
      :type="
        job.status === 'completed' ? 'success' : job.status === 'failed' ? 'danger' : 'warning'
      "
      >{{ deletionStatuses[job.status] }}</ElTag
    >
    <p>{{ deletionStages[job.stage] }}</p>
    <ElProgress
      v-if="deletionProgress(job) !== null"
      :percentage="deletionProgress(job)!"
      :status="
        job.status === 'failed' ? 'exception' : job.status === 'completed' ? 'success' : undefined
      "
    />
    <p v-if="job.status !== 'completed'" class="deletion-status__count">
      已处理 {{ job.processed_items }} 项<span v-if="job.total_items !== null">
        / {{ job.total_items }} 项</span
      ><span v-else> · 总量正在核对</span>
    </p>
    <ElAlert
      v-if="job.status === 'failed'"
      type="error"
      :closable="false"
      :title="
        job.scope === 'account'
          ? '清理暂未完成，服务端将自动重试。请稍后刷新查看结果。'
          : '清理暂未完成，旅行仍不可恢复。'
      "
    />
    <p v-if="deletionJobHint(job)" class="deletion-status__count">{{ deletionJobHint(job) }}</p>
    <p v-if="job.status === 'completed'">服务端已确认数据清理完成，无法恢复。</p>
  </div>
</template>

<style scoped>
.deletion-status {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
  min-width: 0;
}
.deletion-status p {
  margin: 0;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.deletion-status :deep(.el-progress) {
  width: 100%;
  min-width: 120px;
}
.deletion-status__count {
  color: var(--tf-text-3);
  font-size: 12px;
}
</style>
