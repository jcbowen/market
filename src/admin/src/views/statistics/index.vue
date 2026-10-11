<template>
  <div class="statistics-page">
    <n-grid cols="1 s:2 m:3 l:3 xl:3 xxl:3" :x-gap="16" :y-gap="16" responsive="screen">
      <n-grid-item v-for="item in summaryList" :key="item.label">
        <n-card>
          <n-statistic :label="item.label" :value="item.value" :precision="item.precision">
            <template #prefix v-if="item.prefix">{{ item.prefix }}</template>
          </n-statistic>
        </n-card>
      </n-grid-item>
    </n-grid>

    <n-card title="销售趋势" class="chart-card">
      <n-table :bordered="false" :single-line="false">
        <thead>
          <tr>
            <th>日期</th>
            <th>订单数</th>
            <th>销售额</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in revenueList" :key="item.date">
            <td>{{ item.date }}</td>
            <td>{{ item.order_count }}</td>
            <td>¥{{ item.revenue.toFixed(2) }}</td>
          </tr>
          <tr v-if="!revenueList.length">
            <td colspan="3" style="text-align: center; color: #999;">暂无数据</td>
          </tr>
        </tbody>
      </n-table>
    </n-card>

    <n-card title="热销商品 TOP10" class="table-card">
      <n-table :bordered="false" :single-line="false">
        <thead>
          <tr>
            <th>排名</th>
            <th>商品名称</th>
            <th>销量</th>
            <th>销售额</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(item, index) in hotProducts" :key="item.product_id">
            <td>{{ index + 1 }}</td>
            <td>{{ item.product_name }}</td>
            <td>{{ item.sales_count }}</td>
            <td>¥{{ item.sales_amount.toFixed(2) }}</td>
          </tr>
          <tr v-if="!hotProducts.length">
            <td colspan="4" style="text-align: center; color: #999;">暂无数据</td>
          </tr>
        </tbody>
      </n-table>
    </n-card>
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'Statistics' })
import { onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { getSalesStatistics, getProductRankings, getRevenueReport } from '@/api/statistics'
import type { ProductRankingItem, RevenueReportItem } from '@/api/statistics'

const message = useMessage()

/**
 * 统计摘要数据
 */
const summaryList = reactive([
  { label: '本月销售额', value: 0, prefix: '¥', precision: 2 },
  { label: '本月订单数', value: 0, prefix: '', precision: 0 },
  { label: '客单价', value: 0, prefix: '¥', precision: 2 },
])

/**
 * 热销商品列表
 */
const hotProducts = ref<ProductRankingItem[]>([])

/**
 * 营收报表列表
 */
const revenueList = ref<RevenueReportItem[]>([])

/**
 * 获取本月起始日期字符串（YYYY-MM-DD）
 * @returns 日期字符串
 */
function getMonthStart(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

/**
 * 获取今日日期字符串（YYYY-MM-DD）
 * @returns 日期字符串
 */
function getToday(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/**
 * 加载统计页面数据
 * 包括销售摘要、营收趋势、商品排行
 */
async function loadData() {
  const startDate = getMonthStart()
  const endDate = getToday()
  try {
    const [salesRes, rankingRes, revenueRes] = await Promise.all([
      getSalesStatistics({ start_date: startDate, end_date: endDate }),
      getProductRankings({ start_date: startDate, end_date: endDate, limit: 10 }),
      getRevenueReport({ start_date: startDate, end_date: endDate, group_by: 'day' }),
    ])

    if (salesRes.data.code === 0) {
      const data = salesRes.data.data
      summaryList[0].value = data.pay_amount
      summaryList[1].value = data.total_orders
      summaryList[2].value = data.avg_order_amount
    }

    if (rankingRes.data.code === 0) {
      hotProducts.value = rankingRes.data.data.list || []
    }

    if (revenueRes.data.code === 0) {
      revenueList.value = revenueRes.data.data.list || []
    }
  } catch (error) {
    message.error('加载统计数据失败')
  }
}

onMounted(() => {
  loadData()
})
</script>

<style lang="scss" scoped>
.statistics-page {
  .chart-card,
  .table-card {
    margin-top: 16px;
  }
}
</style>
