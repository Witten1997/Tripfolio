<script setup lang="ts">
import { computed } from 'vue'
import type { AdminContentPage } from '../api'
import { formatTime } from '../format'

const props = defineProps<{ content: AdminContentPage }>()
const labels: Record<string, string> = {
  attraction: '景点',
  transport: '交通',
  lodging: '住宿',
  dining: '餐饮',
  other: '其他',
  pending: '待完成',
  completed: '已完成',
  skipped: '已跳过',
  ready: '已备好',
  packed: '已装包',
  documents: '证件',
  electronics: '电子设备',
  clothing: '衣物',
  daily: '日用品',
  food: '食品',
  medicine: '药品',
}
const label = (value: string) => labels[value] || value
type Field = [string, string | number | null | undefined]
type DisplayItem = { id: string; title: string; subtitle: string; fields: Field[] }
const rows = computed<DisplayItem[]>(() => {
  const c = props.content
  if (c.kind === 'itinerary')
    return c.itinerary.map((v) => ({
      id: v.id,
      title: v.title,
      subtitle: `${v.scheduled_on} · ${label(v.kind)} · ${label(v.status)}`,
      fields: [
        ['地点', v.place_name],
        ['地址', v.address],
        ['计划开始', v.planned_start_local],
        ['计划结束', v.planned_end_local],
        [
          '计划时长',
          v.planned_duration_minutes == null ? null : `${v.planned_duration_minutes} 分钟`,
        ],
        [
          '预计费用',
          v.estimated_amount == null ? null : `${v.estimated_amount} ${v.currency_code}`,
        ],
        ['坐标', v.latitude == null ? null : `${v.latitude}, ${v.longitude}`],
        ['地点编号', v.poi_id],
        ['足迹统计', v.footprint_excluded ? '不参与' : '参与'],
        ['备注', v.notes],
        ['实际开始', v.actual_start_local],
        ['实际结束', v.actual_end_local],
        ['实际记录', v.actual_notes],
      ],
    }))
  if (c.kind === 'packing')
    return c.packing.map((v) => ({
      id: v.id,
      title: v.name,
      subtitle: `${label(v.category)} · ${label(v.status)}`,
      fields: [
        ['数量', v.quantity],
        ['备注', v.notes],
      ],
    }))
  if (c.kind === 'todos')
    return c.todos.map((v) => ({
      id: v.id,
      title: v.title,
      subtitle: v.completed ? '已完成' : '待完成',
      fields: [
        ['截止日期', v.due_on],
        ['完成时间', v.completed_at ? formatTime(v.completed_at) : null],
        ['备注', v.notes],
      ],
    }))
  return c.members.map((v) => ({
    id: v.id,
    title: v.name,
    subtitle: v.is_self ? '用户本人' : '同行成员',
    fields: [['分摊比例', `${v.share_percent}%`]],
  }))
})
</script>

<template>
  <p v-if="!rows.length" class="empty-state">暂无此类内容</p>
  <ul v-else class="content-list">
    <li v-for="item in rows" :key="item.id" class="content-item">
      <details>
        <summary class="content-summary">
          <div>
            <h2>{{ item.title }}</h2>
            <p class="muted">{{ item.subtitle }}</p>
          </div>
          <span class="text-link">查看详情</span>
        </summary>
        <dl class="user-facts">
          <template v-for="[name, value] in item.fields" :key="name"
            ><dt>{{ name }}</dt>
            <dd class="preserve-text">
              {{ value === null || value === undefined || value === '' ? '未填写' : value }}
            </dd></template
          >
        </dl>
      </details>
    </li>
  </ul>
</template>
