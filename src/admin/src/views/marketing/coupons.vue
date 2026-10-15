<template>
  <n-card title="优惠券管理">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/marketing/coupons/create')">创建优惠券</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'CouponList' })
import { ref, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCouponList, deleteCoupon, updateCoupon } from '@/api/marketing'
import type { Coupon } from '@/api/types'

/**
 * 优惠券管理页面
 * 状态机：publish_status（草稿/已发布/已下架）+ grant_status（发放中/停止发放）
 * 编辑边界（决策点⑧）：草稿全字段可编辑；已发布/已下架仅可补充数量/停发恢复/规则文案
 */
const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const list = ref<Coupon[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const typeMap: Record<number, string> = {
  1: '满减券',
  2: '折扣券',
}

const publishMap: Record<number, { text: string; type: 'default' | 'success' | 'warning' }> = {
  0: { text: '草稿', type: 'default' },
  1: { text: '已发布', type: 'success' },
  2: { text: '已下架', type: 'warning' },
}

/** 商品范围列渲染 */
function renderScopes(row: Coupon) {
  const scopes = row.scopes || []
  if (!scopes.length) return '全场通用'
  const parts = scopes.map((s) => {
    const prefix = s.scope_type === 4 || s.scope_type === 5 ? '排除:' : ''
    const name = s.scope_type === 4 || s.scope_type === 2
      ? s.category_name || `分类#${s.category_id}`
      : s.product_name || `商品#${s.product_id}`
    return `${prefix}${name}`
  })
  return parts.join('、')
}

const columns: DataTableColumns<Coupon> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '名称', key: 'name', width: 140, ellipsis: { tooltip: true } },
  { title: '类型', key: 'type', width: 80, render(row) { return typeMap[row.type] || '未知' } },
  { title: '门槛', key: 'threshold', width: 70 },
  { title: '面值/折扣', key: 'discount_value', width: 90 },
  { title: '总量/已领/已用', key: 'count', width: 120, render: (row) => `${row.total_count} / ${row.received_count} / ${row.used_count}` },
  { title: '商品范围', key: 'scopes', ellipsis: { tooltip: true }, render: renderScopes },
  {
    title: '发布状态',
    key: 'publish_status',
    width: 90,
    render(row) {
      const p = publishMap[row.publish_status] || { text: '未知', type: 'default' as const }
      return h(NTag, { type: p.type }, { default: () => p.text })
    },
  },
  {
    title: '发放',
    key: 'grant_status',
    width: 90,
    render(row) {
      const stopped = row.publish_status !== 0 && row.grant_status !== 1
      return h(NTag, { type: stopped ? 'warning' : 'success' }, { default: () => (stopped ? '已停发' : '发放中') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 260,
    render(row) {
      const actions = [
        h(NButton, { size: 'small', onClick: () => router.push(`/marketing/coupons/${row.id}/edit`) }, { default: () => '编辑' }),
      ]
      if (row.publish_status === 0) {
        actions.push(
          h(NButton, { size: 'small', type: 'success', onClick: () => publish(row) }, { default: () => '发布' })
        )
      } else {
        // 已发布/已下架都要有发放开关，否则下架后无法停止发放（运营死角）
        actions.push(
          h(
            NButton,
            { size: 'small', type: row.grant_status === 1 ? 'warning' : 'success', onClick: () => toggleGrant(row) },
            { default: () => (row.grant_status === 1 ? '停止发放' : '恢复发放') }
          )
        )
        if (row.publish_status === 1) {
          actions.push(
            h(NButton, { size: 'small', type: 'warning', onClick: () => unpublish(row) }, { default: () => '下架' })
          )
        } else {
          actions.push(
            h(NButton, { size: 'small', type: 'success', onClick: () => publish(row) }, { default: () => '重新上架' })
          )
        }
      }
      actions.push(
        h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' })
      )
      return h(NSpace, { size: 'small' }, { default: () => actions })
    },
  },
]

async function loadData() {
  loading.value = true
  const res = await getCouponList({ page: pagination.value.page, page_size: pagination.value.pageSize })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

/** 发布（草稿 → 已发布；已下架 → 重新上架） */
async function publish(row: Coupon) {
  const res = await updateCoupon(row.id, { publish_status: 1 } as Partial<Coupon>)
  if (res.data.code === 0) {
    message.success('已发布')
    loadData()
  } else {
    message.error(res.data.message || '发布失败')
  }
}

/** 下架（已发布 → 已下架；已领取的券仍可使用，但不再可领） */
function unpublish(row: Coupon) {
  dialog.warning({
    title: '下架优惠券',
    content: `确认下架「${row.name}」？下架后用户不可再领取，已领取的券仍可使用。`,
    positiveText: '确认下架',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await updateCoupon(row.id, { publish_status: 2 } as Partial<Coupon>)
      if (res.data.code === 0) {
        message.success('已下架')
        loadData()
      } else {
        message.error(res.data.message || '下架失败')
      }
    },
  })
}

/** 停止/恢复发放（停止后已领的券仍可用，只是不再发） */
async function toggleGrant(row: Coupon) {
  const stopping = row.grant_status === 1
  const res = await updateCoupon(row.id, { grant_status: stopping ? 0 : 1 } as Partial<Coupon>)
  if (res.data.code === 0) {
    message.success(stopping ? '已停止发放' : '已恢复发放')
    loadData()
  } else {
    message.error(res.data.message || '操作失败')
  }
}

function remove(row: Coupon) {
  dialog.warning({
    title: '删除优惠券',
    content: `确认删除「${row.name}」？已有人领取的券无法删除，只能停止发放。`,
    positiveText: '确认删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await deleteCoupon(row.id)
      if (res.data.code === 0) {
        message.success('已删除')
        loadData()
      } else {
        message.error(res.data.message || '删除失败')
      }
    },
  })
}

onMounted(loadData)
</script>
