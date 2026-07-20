<template>
  <div class="dashboard-page">
    <n-grid :cols="{ xs: 1, s: 2, m: 2, l: 4, xl: 4 }" :x-gap="16" :y-gap="16" responsive="screen">
      <n-grid-item>
        <n-card>
          <n-statistic class="dashboard-statistic" label="今日订单数" :value="statistics.order_count">
            <template #prefix>
              <n-icon :component="CartOutline" />
            </template>
          </n-statistic>
        </n-card>
      </n-grid-item>
      <n-grid-item>
        <n-card>
          <n-statistic class="dashboard-statistic" label="今日销售额" :value="statistics.sale_amount" precision="2">
            <template #prefix>¥</template>
          </n-statistic>
        </n-card>
      </n-grid-item>
      <n-grid-item>
        <n-card>
          <n-statistic class="dashboard-statistic" label="注册用户" :value="statistics.user_count">
            <template #prefix>
              <n-icon :component="PeopleOutline" />
            </template>
          </n-statistic>
        </n-card>
      </n-grid-item>
      <n-grid-item>
        <n-card>
          <n-statistic class="dashboard-statistic" label="在售商品" :value="statistics.product_count">
            <template #prefix>
              <n-icon :component="CubeOutline" />
            </template>
          </n-statistic>
        </n-card>
      </n-grid-item>
    </n-grid>

    <n-card title="待处理事项" class="todo-card">
      <n-list>
        <n-list-item>
          <n-thing :title="`待发货订单`" :description="`当前有 ${statistics.pending_ship} 笔订单待发货`" />
        </n-list-item>
        <n-list-item>
          <n-thing :title="`待处理退款`" :description="`当前有 ${statistics.refunding} 笔退款待处理`" />
        </n-list-item>
      </n-list>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive } from 'vue'
import { useMessage } from 'naive-ui'
import { CartOutline, PeopleOutline, CubeOutline } from '@vicons/ionicons5'
import { getOrderStatistics, getSalesStatistics } from '@/api/statistics'

const message = useMessage()

/**
 * 工作台统计卡片数据
 */
const statistics = reactive({
  order_count: 0,
  sale_amount: 0,
  user_count: 0,
  product_count: 0,
  pending_ship: 0,
  refunding: 0,
})

/**
 * 获取今日日期字符串（YYYY-MM-DD）
 * @returns 日期字符串
 */
function getToday(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/**
 * 加载仪表盘统计数据
 * 组合订单统计与销售统计的今日数据
 */
async function loadStatistics() {
  const today = getToday()
  try {
    const [orderRes, salesRes] = await Promise.all([
      getOrderStatistics({ start_date: today, end_date: today }),
      getSalesStatistics({ start_date: today, end_date: today }),
    ])
    if (orderRes.data.code === 0) {
      const orderData = orderRes.data.data
      statistics.order_count = orderData.total_orders
      statistics.pending_ship = orderData.pending_ship
      statistics.refunding = orderData.refunding
    }
    if (salesRes.data.code === 0) {
      const salesData = salesRes.data.data
      statistics.sale_amount = salesData.pay_amount
    }
  } catch (error) {
    message.error('加载统计数据失败')
  }
}

onMounted(() => {
  loadStatistics()
})
</script>

<style lang="scss" scoped>
.dashboard-page {
  .todo-card {
    margin-top: 16px;
  }
}
.dashboard-statistic {
  :deep(.n-statistic__value) {
    white-space: nowrap;
  }
}
</style>
