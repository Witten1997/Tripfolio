<script setup lang="ts">
import { ElEmpty } from 'element-plus'
import { computed } from 'vue'
import VChart from 'vue-echarts'

import '@/shared/echarts'
import { useChartTokens } from '@/shared/theme/chartTokens'
import { formatMoney, formatShare, type PieSlice } from '@/shared/travel/statisticsView'

const props = defineProps<{
  slices: PieSlice[]
  currency: string
  /** 服务端判定可给出占比；为 false 时页面改用金额明细。 */
  ratioAvailable: boolean
}>()
const emit = defineEmits<{ select: [categoryId: string] }>()

const tokens = useChartTokens()

const option = computed(() => {
  const t = tokens.value
  return {
    // 提示层的文字用文字令牌，颜色只由色块承担身份
    tooltip: {
      trigger: 'item' as const,
      backgroundColor: t.surface,
      borderColor: t.line,
      textStyle: { color: t.text, fontSize: 12 },
      formatter: (params: { data: { name: string; amount: string; share: number | null } }) => {
        const share = formatShare(params.data.share)
        return `${params.data.name}<br/>${formatMoney(params.data.amount)} ${props.currency}${
          share ? `　占比 ${share}` : ''
        }`
      },
    },
    legend: {
      bottom: 0,
      icon: 'circle',
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: t.textMuted, fontSize: 12 },
    },
    series: [
      {
        type: 'pie' as const,
        radius: ['46%', '68%'],
        center: ['50%', '44%'],
        avoidLabelOverlap: true,
        // 切片之间留 2px 表面色缝隙，色块边界不靠颜色对比
        itemStyle: { borderColor: t.surface, borderWidth: 2, borderRadius: 4 },
        label: {
          color: t.text,
          fontSize: 12,
          formatter: (params: { data: { name: string; share: number | null } }) => {
            const share = formatShare(params.data.share)
            return share ? `${params.data.name} ${share}` : params.data.name
          },
        },
        labelLine: { lineStyle: { color: t.line } },
        emphasis: { itemStyle: { borderWidth: 3 } },
        data: props.slices.map((slice) => ({
          name: slice.name,
          value: slice.value,
          amount: slice.amount,
          share: slice.share,
          categoryId: slice.key,
          // colorIndex 0 是折叠出来的“其他分类”，用中性色而不是再取一个分类色
          itemStyle: {
            color: slice.colorIndex === 0 ? t.neutral : t.series[slice.colorIndex - 1],
          },
        })),
      },
    ],
  }
})

function onClick(params: unknown) {
  const data = (params as { data?: { categoryId?: string } }).data
  const id = data?.categoryId
  if (id && id !== '__other__') emit('select', id)
}
</script>

<template>
  <div class="pie-wrap">
    <VChart
      v-if="slices.length && ratioAvailable"
      class="pie"
      :option="option"
      autoresize
      role="img"
      aria-label="分类净支出占比"
      @click="onClick"
    />
    <ElEmpty
      v-else-if="!slices.length"
      description="筛选范围内没有净支出，暂无占比"
      :image-size="72"
    />
    <p v-else class="pie-note">净支出不大于零或有分类为负，占比无意义；请看下方金额明细。</p>
  </div>
</template>

<style scoped>
.pie-wrap {
  min-height: 260px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.pie {
  width: 100%;
  height: 260px;
}
.pie-note {
  margin: 0;
  padding: 24px;
  color: var(--tf-text-3);
  font-size: 13px;
  line-height: 1.7;
  text-align: center;
}
</style>
