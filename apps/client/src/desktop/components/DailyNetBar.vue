<script setup lang="ts">
import { ElEmpty } from 'element-plus'
import { computed } from 'vue'
import VChart from 'vue-echarts'

import '@/shared/echarts'
import { useChartTokens } from '@/shared/theme/chartTokens'
import { formatMoney, type DailyBar } from '@/shared/travel/statisticsView'

const props = defineProps<{ bars: DailyBar[]; currency: string }>()
const emit = defineEmits<{ select: [date: string] }>()

const tokens = useChartTokens()
const hasNegative = computed(() => props.bars.some((bar) => bar.net < 0))

const option = computed(() => {
  const t = tokens.value
  return {
    grid: { left: 8, right: 16, top: 24, bottom: 8, containLabel: true },
    tooltip: {
      trigger: 'axis' as const,
      axisPointer: { type: 'shadow' as const },
      backgroundColor: t.surface,
      borderColor: t.line,
      textStyle: { color: t.text, fontSize: 12 },
      formatter: (params: Array<{ dataIndex: number }>) => {
        const bar = props.bars[params[0]?.dataIndex ?? 0]
        if (!bar) return ''
        return `${bar.date}<br/>净支出 ${formatMoney(bar.amount)} ${props.currency}`
      },
    },
    xAxis: {
      type: 'category' as const,
      data: props.bars.map((bar) => bar.date.slice(5)),
      // 轴线与刻度保持克制
      axisLine: { lineStyle: { color: t.line } },
      axisTick: { show: false },
      axisLabel: { color: t.textMuted, fontSize: 11 },
    },
    yAxis: {
      type: 'value' as const,
      axisLine: { show: false },
      splitLine: { lineStyle: { color: t.line } },
      axisLabel: { color: t.textMuted, fontSize: 11 },
    },
    series: [
      {
        type: 'bar' as const,
        // 单系列不需要图例；退款日为负值，用语义色区分方向而非身份
        data: props.bars.map((bar) => ({
          value: bar.net,
          itemStyle: {
            color: bar.net < 0 ? t.danger : t.series[0],
            borderRadius: bar.net < 0 ? [0, 0, 4, 4] : [4, 4, 0, 0],
          },
        })),
        barMaxWidth: 28,
        // 相邻柱之间留出表面色缝隙
        barCategoryGap: '32%',
      },
    ],
  }
})

function onClick(params: unknown) {
  const index = (params as { dataIndex?: number }).dataIndex
  const bar = props.bars[index ?? -1]
  if (bar) emit('select', bar.date)
}
</script>

<template>
  <div class="daily-wrap">
    <VChart
      v-if="bars.length"
      class="daily"
      :option="option"
      autoresize
      role="img"
      aria-label="每日净支出"
      @click="onClick"
    />
    <ElEmpty v-else description="筛选范围内还没有账目" :image-size="72" />
    <p v-if="bars.length && hasNegative" class="daily-note">低于零的柱表示当天退款多于支出。</p>
  </div>
</template>

<style scoped>
.daily-wrap {
  min-height: 260px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}
.daily {
  width: 100%;
  height: 240px;
}
.daily-note {
  margin: 0;
  font-size: 12px;
  color: var(--tf-text-3);
}
</style>
