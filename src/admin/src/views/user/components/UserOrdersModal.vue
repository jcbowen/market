<template>
  <n-modal
    :show="show"
    title="会员订单"
    preset="card"
    style="width: 860px; max-width: 92vw"
    scrollable
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <n-space vertical>
      <!-- 会员信息卡：打开时现拉详情（从订单详情「查看会员」进入时仅有 id） -->
      <n-spin :show="detailLoading" size="small">
        <div class="member-card">
          <template v-if="detail">
            <template v-if="!editing">
              <div class="member-base">
                <n-avatar round :size="44" :src="detail.avatar || undefined">{{ (detail.nickname || detail.phone || '?').slice(0, 1) }}</n-avatar>
                <div class="member-name">
                  <span class="nickname">{{ detail.nickname || '未设置昵称' }}</span>
                  <span class="phone">{{ detail.phone }}</span>
                </div>
                <n-tag :type="detail.status === 1 ? 'success' : 'error'" size="small">{{ detail.status === 1 ? '正常' : '已拉黑' }}</n-tag>
                <n-button
                  v-if="canManage"
                  size="small"
                  type="primary"
                  secondary
                  style="margin-left: auto"
                  @click="startEdit"
                >编辑资料</n-button>
              </div>
              <n-descriptions label-placement="left" :column="3" size="small" style="margin-top: 10px">
                <n-descriptions-item label="性别">{{ genderText(detail.gender) }}</n-descriptions-item>
                <n-descriptions-item label="生日">{{ detail.birthday || '-' }}</n-descriptions-item>
                <n-descriptions-item label="所属社区">{{ detail.community_name || '-' }}</n-descriptions-item>
                <n-descriptions-item label="注册时间">{{ formatDateTime(detail.created_at) }}</n-descriptions-item>
                <n-descriptions-item label="最后登录">{{ formatDateTime(detail.last_login_at) }}</n-descriptions-item>
              </n-descriptions>
            </template>
            <!-- 基础编辑：昵称/性别/生日（手机号只读；birthday 空串=清空，全量提交） -->
            <n-form v-else label-placement="left" label-width="64" style="max-width: 420px">
              <n-form-item label="手机号">
                <n-text depth="3">{{ detail.phone }}（不可修改）</n-text>
              </n-form-item>
              <n-form-item label="昵称">
                <n-input v-model:value="editForm.nickname" placeholder="会员昵称" maxlength="30" />
              </n-form-item>
              <n-form-item label="性别">
                <n-select v-model:value="editForm.gender" :options="genderOptions" style="width: 140px" />
              </n-form-item>
              <n-form-item label="生日">
                <n-date-picker
                  v-model:formatted-value="editForm.birthday"
                  type="date"
                  value-format="yyyy-MM-dd"
                  clearable
                  placeholder="选填"
                  style="width: 160px"
                  :is-date-disabled="isFutureDate"
                />
              </n-form-item>
              <n-space>
                <n-button size="small" type="primary" :loading="saving" @click="saveEdit">保存</n-button>
                <n-button size="small" @click="editing = false">取消</n-button>
              </n-space>
            </n-form>
          </template>
          <n-text v-else-if="!detailLoading" depth="3">会员详情加载失败（可能不在当前社区）</n-text>
        </div>
      </n-spin>

      <n-space wrap align="center">
        <!-- 跨社区查询：仅超管可见，传 community_id=0 查该会员全部社区订单 -->
        <n-checkbox v-if="isSuperAdmin" v-model:checked="crossCommunity">跨社区查询（全部社区）</n-checkbox>
        <n-button size="small" type="primary" secondary @click="loadOrders">刷新</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="orders" :loading="loading" scroll-x="760" size="small" />
      <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadOrders" />

      <!-- 常买商品排行：按该会员有效订单（已支付及之后）统计，用户维度跨全部社区 -->
      <n-divider style="margin: 8px 0" />
      <n-space align="center" justify="space-between">
        <n-text strong>常买商品排行</n-text>
        <n-text depth="3" style="font-size: 12px">按购买次数排序 · 含全部社区有效订单</n-text>
      </n-space>
      <n-data-table :columns="productColumns" :data="products" :loading="productsLoading" size="small" />
      <n-pagination v-model:page="productPage" :page-count="productPageCount" @update:page="loadFrequentProducts" />
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { h, reactive, ref, watch } from 'vue'
import { formatAmount } from '@/utils/format'
import { NButton, NImage, NTag, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderList } from '@/api/order'
import { getUser, getUserFrequentProducts, updateUser } from '@/api/user'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
import type { FrequentProduct, Order, User } from '@/api/types'

/** 弹窗入参：仅需会员 ID，昵称/手机号可选（订单列表场景仅有 id 与姓名快照） */
type ModalUser = Pick<User, 'id'> & Partial<Pick<User, 'nickname' | 'phone'>>

const props = defineProps<{
  show: boolean
  user: ModalUser | null
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  /** 点击订单号：请求页面层打开订单详情弹窗（弹窗间不互相嵌套，避免组件递归） */
  (e: 'open-order', id: number): void
  /** 会员资料编辑成功：通知页面层刷新列表 */
  (e: 'updated'): void
}>()

const authStore = useAuthStore()
const isSuperAdmin = authStore.isSuperAdmin
const message = useMessage()
/** 基础编辑与列表页拉黑/恢复同权限口径 */
const canManage = authStore.hasPermission('user:update')

const pageSize = 10
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const orders = ref<Order[]>([])
const crossCommunity = ref(false)

// --- 会员信息卡与基础编辑 ---
const detailLoading = ref(false)
const detail = ref<User | null>(null)
const editing = ref(false)
const saving = ref(false)
const editForm = reactive<{ nickname: string; gender: number; birthday: string | null }>({
  nickname: '',
  gender: 0,
  birthday: null,
})

const genderOptions = [
  { label: '未知', value: 0 },
  { label: '男', value: 1 },
  { label: '女', value: 2 },
]

/**
 * 性别取值转文案
 * @param gender 0未知 1男 2女
 */
function genderText(gender?: number) {
  return gender === 1 ? '男' : gender === 2 ? '女' : '未知'
}

/** 生日不允许选择未来日期 */
function isFutureDate(ts: number) {
  return ts > Date.now()
}

/**
 * 进入编辑态：用当前详情回填编辑表单
 */
function startEdit() {
  if (!detail.value) return
  editForm.nickname = detail.value.nickname
  editForm.gender = detail.value.gender ?? 0
  editForm.birthday = detail.value.birthday || null
  editing.value = true
}

/**
 * 保存基础编辑（全量提交：gender/birthday 覆盖，birthday 空串=清空；nickname 空串不修改）
 * 成功后更新信息卡展示并通知页面层刷新列表
 */
async function saveEdit() {
  if (!detail.value) return
  saving.value = true
  try {
    const res = await updateUser(detail.value.id, {
      nickname: editForm.nickname.trim(),
      gender: editForm.gender,
      birthday: editForm.birthday || '',
    })
    detail.value = { ...detail.value, ...res.data.data }
    editing.value = false
    message.success('会员资料已更新')
    emit('updated')
  } finally {
    saving.value = false
  }
}

/**
 * 拉取会员详情（信息卡数据源；失败时降级提示，不阻塞订单列表）
 */
async function loadDetail() {
  if (!props.user) return
  detailLoading.value = true
  editing.value = false
  detail.value = null
  try {
    const res = await getUser(props.user.id)
    detail.value = res.data.data
  } catch {
    // 跨社区会员对普通管理员不可见：保持 detail 为空走降级文案
  } finally {
    detailLoading.value = false
  }
}

// --- 常买商品排行（每页 5 条） ---
const PRODUCT_PAGE_SIZE = 5
const productsLoading = ref(false)
const productPage = ref(1)
const productPageCount = ref(1)
const products = ref<FrequentProduct[]>([])

/** 状态标签配色（与订单列表页口径一致） */
function statusTagType(status: number): 'default' | 'success' | 'warning' | 'error' | 'info' {
  switch (status) {
    case 1:
      return 'warning' // 待支付
    case 2:
    case 3:
      return 'info' // 已支付/备货中
    case 4:
      return 'info' // 配送中
    case 5:
      return 'success' // 已完成
    case 6:
      return 'default' // 已取消
    default:
      return 'error' // 退款中/已退款
  }
}

const columns: DataTableColumns<Order> = [
  {
    title: '订单号',
    key: 'order_no',
    width: 200,
    ellipsis: { tooltip: true },
    render: (row) =>
      h(
        NButton,
        { size: 'small', text: true, type: 'primary', onClick: () => emit('open-order', row.id) },
        { default: () => row.order_no },
      ),
  },
  { title: '社区', key: 'community_name', width: 110, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status_text', width: 90, render: (row) => h(NTag, { type: statusTagType(row.status), size: 'small' }, { default: () => row.status_text }) },
  { title: '实付金额', key: 'pay_amount', width: 100, render: (row) => `¥${formatAmount(row.pay_amount)}` },
  { title: '配送方式', key: 'delivery_type', width: 90, render: (row) => (row.delivery_type === 1 ? '自提' : '配送') },
  { title: '下单时间', key: 'created_at', width: 160, render: (row) => formatDateTime(row.created_at) },
]

/** 常买商品排行列（排名按跨页累计序号） */
const productColumns: DataTableColumns<FrequentProduct> = [
  {
    title: '#',
    key: 'rank',
    width: 50,
    render: (_row, index) => `${(productPage.value - 1) * PRODUCT_PAGE_SIZE + index + 1}`,
  },
  {
    title: '商品',
    key: 'product_name',
    render: (row) =>
      h('div', { style: 'display:flex;align-items:center;gap:8px;min-width:0' }, [
        row.product_image
          ? h(NImage, {
              src: row.product_image,
              width: 32,
              height: 32,
              objectFit: 'cover',
              style: 'border-radius:4px;flex:none',
              imgProps: { referrerpolicy: 'no-referrer' },
            })
          : h('div', { style: 'width:32px;height:32px;border-radius:4px;background:var(--n-action-color);flex:none' }),
        h('span', { style: 'overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, row.product_name),
      ]),
  },
  { title: '购买次数', key: 'purchase_count', width: 90, render: (row) => `${row.purchase_count} 次` },
  { title: '累计件数', key: 'total_quantity', width: 90, render: (row) => `${row.total_quantity} 件` },
]

/**
 * 加载该会员的订单列表
 */
async function loadOrders() {
  if (!props.user) return
  loading.value = true
  try {
    const res = await getOrderList({
      page: page.value,
      page_size: pageSize,
      user_id: props.user.id,
      // 仅跨社区查询时显式传 0；默认不传走当前社区语境
      ...(crossCommunity.value && isSuperAdmin ? { community_id: 0 } : {}),
    })
    orders.value = res.data.data.list || []
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 加载该会员的常买商品排行（分页）
 */
async function loadFrequentProducts() {
  if (!props.user) return
  productsLoading.value = true
  try {
    const res = await getUserFrequentProducts(props.user.id, {
      page: productPage.value,
      page_size: PRODUCT_PAGE_SIZE,
    })
    products.value = res.data.data.list || []
    productPageCount.value = Math.ceil(res.data.data.total / PRODUCT_PAGE_SIZE) || 1
  } finally {
    productsLoading.value = false
  }
}

watch(
  () => props.show,
  (show) => {
    if (show && props.user) {
      page.value = 1
      productPage.value = 1
      loadDetail()
      loadOrders()
      loadFrequentProducts()
    }
  },
)

watch(crossCommunity, () => {
  page.value = 1
  loadOrders()
})
</script>

<style lang="scss" scoped>
.member-card {
  padding: 12px 14px;
  border: 1px solid #efeff5;
  border-radius: 8px;
  background: var(--n-action-color, #fafafc);

  .member-base {
    display: flex;
    align-items: center;
    gap: 10px;

    .member-name {
      display: flex;
      flex-direction: column;
      min-width: 0;

      .nickname {
        font-weight: 600;
      }

      .phone {
        font-size: 12px;
        color: #999;
      }
    }
  }
}
</style>
