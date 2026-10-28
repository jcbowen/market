<template>
  <n-card title="优惠券管理">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/marketing/coupons/create')">创建优惠券</n-button>
        <n-button @click="router.push('/marketing/coupon-records')">领取/发放记录</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>

    <!-- 手动发放：会员多选弹窗（公共 UserPicker，收银台/发券共用） -->
    <UserPicker
      v-model:show="pickerShow"
      multiple
      :title="pickerTitle"
      @confirm="onGrantConfirm"
    />
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'CouponList' })
import { ref, onActivated, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCouponList, deleteCoupon, updateCoupon, grantCoupon } from '@/api/marketing'
import type { Coupon, User } from '@/api/types'
import UserPicker from '@/components/UserPicker.vue'

/**
 * 优惠券管理页面
 * 状态机：publish_status（草稿/已发布/已下架）+ grant_status（发放中/停止发放）
 * 编辑边界（决策点⑧）：草稿全字段可编辑；已发布/已下架仅可补充数量/停发恢复/规则文案
 * 手动发放（M②期）：仅已发布 + 发放中的券可批量发放给指定会员，逐人独立校验
 */
const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const list = ref<Coupon[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

/** 手动发放弹窗状态 */
const pickerShow = ref(false)
const pickerTitle = ref('选择会员')
const grantingCoupon = ref<Coupon | null>(null)
const granting = ref(false)

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
    width: 320,
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
        // 手动发放：仅发放中的已发布券可发（停发/下架后端也会拦截）
        if (row.grant_status === 1) {
          actions.push(
            h(NButton, { size: 'small', type: 'info', loading: granting.value, onClick: () => openGrant(row) }, { default: () => '手动发放' })
          )
        }
      }
      actions.push(
        h(NButton, { size: 'small', onClick: () => router.push({ path: '/marketing/coupon-records', query: { coupon_id: String(row.id) } }) }, { default: () => '发放记录' })
      )
      actions.push(
        h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' })
      )
      return h(NSpace, { size: 'small' }, { default: () => actions })
    },
  },
]

// 请求序号守卫：快速翻页/切换筛选时旧响应可能后到并覆盖新结果（与 UserPicker 同范式）
let loadSeq = 0

async function loadData() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await getCouponList({ page: pagination.value.page, page_size: pagination.value.pageSize })
    if (seq !== loadSeq) return
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈；过期批次不得掐掉新一轮的 loading
    if (seq === loadSeq) loading.value = false
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
      // 失败时保持弹窗打开（错误提示由请求拦截器统一弹出），避免误以为已下架
      try {
        await updateCoupon(row.id, { publish_status: 2 } as Partial<Coupon>)
        message.success('已下架')
        loadData()
      } catch {
        return false
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

/** 打开手动发放弹窗（提示余量，避免超发后批量失败） */
function openGrant(row: Coupon) {
  grantingCoupon.value = row
  pickerTitle.value = `发放「${row.name}」给会员`
  pickerShow.value = true
}

/** 确认发放：批量提交，展示成功人数与逐人失败原因 */
async function onGrantConfirm(users: User[]) {
  const coupon = grantingCoupon.value
  if (!coupon || !users.length) return
  const remaining = coupon.total_count - coupon.received_count
  if (users.length > remaining) {
    message.warning(`当前仅剩 ${remaining} 个发放名额，已选 ${users.length} 人，请分批发放`)
    return
  }
  granting.value = true
  try {
    const res = await grantCoupon(coupon.id, users.map((u) => u.id))
    if (res.data.code === 0) {
      const { granted_count, failures } = res.data.data
      if (!failures.length) {
        message.success(`已成功发放 ${granted_count} 人`)
      } else {
        // 部分失败：逐人原因弹窗展示（超限/领完等）
        const lines = failures.map((f) => {
          const u = users.find((x) => x.id === f.user_id)
          return `· ${u ? u.nickname || `会员#${f.user_id}` : `会员#${f.user_id}`}：${f.reason}`
        })
        dialog.warning({
          title: '发放结果',
          content: `成功 ${granted_count} 人，失败 ${failures.length} 人：` + lines.join(''),
          positiveText: '知道了',
        })
      }
      pickerShow.value = false
      loadData()
    } else {
      message.error(res.data.message || '发放失败')
    }
  } finally {
    granting.value = false
  }
}

function remove(row: Coupon) {
  dialog.warning({
    title: '删除优惠券',
    content: `确认删除「${row.name}」？已有人领取的券无法删除，只能停止发放。`,
    positiveText: '确认删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      // 失败时保持弹窗打开（错误提示由请求拦截器统一弹出），避免误以为已删除
      try {
        await deleteCoupon(row.id)
        message.success('已删除')
        loadData()
      } catch {
        return false
      }
    },
  })
}

// keep-alive 缓存下从内页（新建/编辑）返回时 onMounted 不会触发，
// onActivated 重新拉取，保证优惠券新增/编辑后列表展示最新数据
onActivated(loadData)
</script>
