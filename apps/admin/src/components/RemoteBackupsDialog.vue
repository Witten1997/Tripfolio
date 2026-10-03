<script setup lang="ts">
import { ref, watch } from 'vue'
import type { components } from '@tripfolio/contracts/openapi/admin'
import { request } from '../api'
import RestoreBackupDialog from './RestoreBackupDialog.vue'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
type Page = components['schemas']['RemoteBackupPage']
type Backup = components['schemas']['RemoteBackup']
const directories = ref<string[]>([])
const destination = ref('')
const records = ref<Page | null>(null)
const loading = ref(false)
const error = ref('')
const selected = ref<Backup | null>(null)
const restoreOpen = ref(false)
let generation = 0
const time = (value: string) =>
  new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'short',
    timeStyle: 'medium',
    timeZone: 'Asia/Shanghai',
  }).format(new Date(value))

async function load(page = 1) {
  const current = ++generation
  loading.value = true
  records.value = null
  error.value = ''
  try {
    const query = new URLSearchParams({ page: String(page) })
    if (destination.value) query.set('destination', destination.value)
    const result = await request<Page>(`/backup-remote?${query}`)
    if (current !== generation) return
    if (destination.value) records.value = result
    else directories.value = result.destinations
  } catch (e) {
    if (current === generation) error.value = e instanceof Error ? e.message : '远端备份加载失败'
  } finally {
    if (current === generation) loading.value = false
  }
}
function select(backup: Backup) {
  selected.value = backup
  restoreOpen.value = true
}
function reloadDirectories() {
  destination.value = ''
  directories.value = []
  void load()
}
watch(
  () => props.modelValue,
  (open) => {
    generation++
    destination.value = ''
    directories.value = []
    records.value = null
    selected.value = null
    if (open) void load()
  },
)
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    title="从 WebDAV 恢复"
    width="min(900px, 94vw)"
    :close-on-click-modal="false"
    :close-on-press-escape="!restoreOpen"
    :show-close="!restoreOpen"
    @update:model-value="!restoreOpen && emit('update:modelValue', $event)"
  >
    <p class="remote-note">
      无需本地备份记录。使用已保存的 WebDAV 地址和凭证，选择原系统的备份目录。地址应填写 tripfolio-…
      目录的上一级。
    </p>
    <div class="remote-controls">
      <el-select
        v-model="destination"
        :disabled="loading || restoreOpen"
        placeholder="选择原系统备份目录"
        aria-label="备份来源目录"
        @change="load(1)"
      >
        <el-option v-for="id in directories" :key="id" :value="id" :label="`tripfolio-${id}`" />
      </el-select>
      <el-button :loading="loading" :disabled="restoreOpen" @click="reloadDirectories"
        >重新查找目录</el-button
      >
    </div>
    <p v-if="error" class="form-error" role="alert">{{ error }}</p>
    <p v-if="loading" role="status">正在读取 WebDAV…</p>
    <p v-else-if="!directories.length && !error" class="remote-note">
      未找到备份目录，请检查 WebDAV 地址是否与原系统一致，以及当前账号是否有列出目录和读取权限。
    </p>
    <template v-if="records">
      <p v-if="records.skipped" role="alert">
        本页有 {{ records.skipped }} 份备份信息损坏或不受支持，已跳过。
      </p>
      <el-table :data="records.data" row-key="id" empty-text="此目录暂无完整备份">
        <el-table-column label="数据快照（北京时间）" min-width="175"
          ><template #default="{ row }">{{ time(row.snapshot_at) }}</template></el-table-column
        >
        <el-table-column prop="id" label="备份编号" min-width="280" />
        <el-table-column label="大小" width="100"
          ><template #default="{ row }"
            >{{ (row.size_bytes / 1024 / 1024).toFixed(2) }} MB</template
          ></el-table-column
        >
        <el-table-column label="操作" width="85" fixed="right"
          ><template #default="{ row }"
            ><el-button type="danger" link :disabled="loading || restoreOpen" @click="select(row)"
              >恢复</el-button
            ></template
          ></el-table-column
        >
      </el-table>
      <el-pagination
        v-if="records.total > records.page_size"
        layout="prev, pager, next, total"
        :total="records.total"
        :page-size="records.page_size"
        :current-page="records.page"
        :disabled="loading || restoreOpen"
        @current-change="load"
      />
    </template>
    <RestoreBackupDialog
      v-model="restoreOpen"
      :run="selected"
      :remote="
        selected && records
          ? { destination: selected.destination, settingsVersion: records.settings_version }
          : undefined
      "
    />
  </el-dialog>
</template>

<style scoped>
.remote-note {
  color: var(--el-text-color-secondary);
  line-height: 1.8;
}
.remote-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin: 20px 0;
}
.remote-controls .el-select {
  flex: 1;
  min-width: min(340px, 100%);
}
.el-pagination {
  margin-top: 20px;
}
</style>
