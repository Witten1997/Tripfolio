<script setup lang="ts">
import { ElAlert, ElButton, ElCard, ElEmpty, ElSkeleton, ElTag } from 'element-plus'
import { computed, ref, shallowRef, watch } from 'vue'

import SlidingSegmented from '@/desktop/components/SlidingSegmented.vue'
import { getTripSettlement, type TripSettlement } from '@/shared/api/settlement'
import { actionError } from '@/shared/api/writes'
import { formatMoney, toNumber } from '@/shared/travel/statisticsView'
import { useTripContext } from '@/shared/travel/tripContext'

/** refreshKey 变化（账目增删改、成员保存）时重新加载。 */
const props = defineProps<{ refreshKey: number }>()
const context = useTripContext()
const settlement = shallowRef<TripSettlement | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const view = ref<'members' | 'transfers'>('members')
const viewOptions = [
  { value: 'members', label: '每人净额' },
  { value: 'transfers', label: '转账方案' },
]
function setView(value: string) {
  if (value === 'members' || value === 'transfers') view.value = value
}
let generation = 0

async function reload() {
  const request = ++generation
  loading.value = true
  error.value = null
  try {
    const loaded = await getTripSettlement(context.tripId)
    if (request === generation) settlement.value = loaded
  } catch (cause) {
    if (request === generation) error.value = actionError(cause, '无法加载成员结算，请重试')
  } finally {
    if (request === generation) loading.value = false
  }
}

const memberName = (id: string) =>
  settlement.value?.members.find((m) => m.member_id === id)?.name ?? '已删除成员'
const balanced = computed(() =>
  (settlement.value?.members ?? []).every((m) => toNumber(m.net_amount) === 0),
)
const soloMember = computed(() => (settlement.value?.members.length ?? 0) <= 1)

watch(
  () => [props.refreshKey, context.membersVersion.value],
  () => {
    void reload()
  },
  { immediate: true },
)
</script>

<template>
  <ElCard shadow="never" class="settlement-card">
    <template #header>
      <div class="chart-header">
        <h2>成员结算</h2>
        <SlidingSegmented
          :model-value="view"
          :options="viewOptions"
          label="结算视图"
          class="settlement-switch"
          @update:model-value="setView"
        />
      </div>
    </template>
    <ElSkeleton v-if="loading && !settlement" :rows="3" animated />
    <ElAlert v-else-if="error" :title="error" type="error" :closable="false" show-icon>
      <ElButton size="small" class="retry-button" @click="reload">重新加载</ElButton>
    </ElAlert>
    <template v-else-if="settlement">
      <p v-if="soloMember" class="settlement-hint">
        只有「我」一位成员。在旅行标题旁的「旅行成员」中添加同行者后，这里会按付款人与分摊显示每人应收应付。
      </p>
      <table v-else-if="view === 'members'" class="settlement-table">
        <thead>
          <tr>
            <th scope="col">成员</th>
            <th scope="col" class="num">已支付</th>
            <th scope="col" class="num">应承担</th>
            <th scope="col" class="num">净额</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in settlement.members" :key="m.member_id">
            <th scope="row">
              {{ m.name }}
              <ElTag v-if="m.is_self" size="small" type="info" class="self-tag">我</ElTag>
            </th>
            <td class="num">{{ formatMoney(m.paid_amount) }}</td>
            <td class="num">{{ formatMoney(m.owed_amount) }}</td>
            <td
              class="num net"
              :class="{
                'net--receive': toNumber(m.net_amount) > 0,
                'net--pay': toNumber(m.net_amount) < 0,
              }"
            >
              <span class="net-label">{{
                toNumber(m.net_amount) > 0 ? '应收' : toNumber(m.net_amount) < 0 ? '应付' : '已平'
              }}</span>
              {{ formatMoney(m.net_amount.replace('-', '')) }}
              <span class="settlement-currency">{{ settlement.currency_code }}</span>
            </td>
          </tr>
        </tbody>
      </table>
      <template v-else>
        <ElEmpty
          v-if="balanced || !settlement.transfers.length"
          description="账目已结清，无需转账"
        />
        <ol v-else class="transfer-list">
          <li v-for="(t, index) in settlement.transfers" :key="index" class="transfer">
            <span class="transfer-from">{{ memberName(t.from_member_id) }}</span>
            <span class="transfer-arrow" aria-hidden="true">→</span>
            <span class="transfer-to">{{ memberName(t.to_member_id) }}</span>
            <strong class="transfer-amount"
              >{{ formatMoney(t.amount) }}
              <span class="settlement-currency">{{ settlement.currency_code }}</span></strong
            >
          </li>
        </ol>
      </template>
    </template>
  </ElCard>
</template>

<style scoped>
.chart-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.chart-header h2 {
  margin: 0;
  font-size: 15px;
  color: var(--tf-text-1);
}
.settlement-switch {
  max-width: 220px;
}
.settlement-hint {
  margin: 0;
  color: var(--tf-text-3);
  line-height: 1.7;
  font-size: 13px;
}
.settlement-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.settlement-table th,
.settlement-table td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--tf-line-soft);
  font-variant-numeric: tabular-nums;
}
.settlement-table thead th {
  font-size: 12px;
  color: var(--tf-text-3);
  font-weight: 500;
}
.settlement-table .num {
  text-align: right;
}
.settlement-table tbody tr:last-child th,
.settlement-table tbody tr:last-child td {
  border-bottom: 0;
}
.self-tag {
  margin-left: 6px;
}
.net {
  font-weight: 600;
}
.net--receive {
  color: var(--tf-success);
}
.net--pay {
  color: var(--tf-danger);
}
.net-label {
  font-size: 11px;
  font-weight: 500;
  margin-right: 4px;
}
.settlement-currency {
  font-size: 11px;
  color: var(--tf-text-3);
  font-weight: 500;
}
.transfer-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.transfer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  font-size: 13px;
}
.transfer-arrow {
  color: var(--tf-text-3);
}
.transfer-amount {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}
.retry-button {
  margin-top: 8px;
}
</style>
