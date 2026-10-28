<template>
  <div class="user-page">
    <n-card title="会员列表">
      <n-space vertical>
        <n-space wrap>
          <n-input v-model:value="userKeyword" placeholder="搜索昵称/手机号" clearable @keyup.enter="onSearch" />
          <n-select
            v-model:value="statusFilter"
            :options="statusOptions"
            style="width: 120px"
            @update:value="onSearch"
          />
          <n-button type="primary" @click="onSearch">查询</n-button>
          <n-button v-if="canManage" type="primary" secondary @click="showCreateModal = true">添加会员</n-button>
        </n-space>
        <n-data-table :columns="userColumns" :data="users" :loading="userLoading" scroll-x="860" />
        <n-pagination v-model:page="userPage" :page-count="userPageCount" @update:page="loadUsers" />
      </n-space>
    </n-card>

    <!-- 添加会员弹窗：手机号注册到当前社区 -->
    <n-modal
      v-model:show="showCreateModal"
      preset="dialog"
      title="添加会员"
      positive-text="确定"
      negative-text="取消"
      :loading="createLoading"
      @positive-click="handleCreate"
    >
      <n-space vertical>
        <n-text depth="3">会员将注册到当前社区：{{ currentCommunityName }}</n-text>
        <n-form label-placement="left" label-width="70">
          <n-form-item label="手机号" required>
            <n-input v-model:value="createForm.phone" placeholder="11 位手机号" maxlength="11" />
          </n-form-item>
          <n-form-item label="昵称">
            <n-input v-model:value="createForm.nickname" placeholder="留空时自动生成" />
          </n-form-item>
          <n-form-item label="性别">
            <n-select v-model:value="createForm.gender" :options="genderOptions" style="width: 140px" />
          </n-form-item>
          <n-form-item label="生日">
            <n-date-picker
              v-model:formatted-value="createForm.birthday"
              type="date"
              value-format="yyyy-MM-dd"
              clearable
              placeholder="选填"
              style="width: 160px"
              :is-date-disabled="isFutureDate"
            />
          </n-form-item>
        </n-form>
      </n-space>
    </n-modal>

    <!-- 会员订单弹窗：查看该会员在当前社区（或超管跨社区）的订单（订单号点击经 open-order 转发到详情弹窗） -->
    <UserOrdersModal v-model:show="showOrdersModal" :user="ordersUser" @open-order="openOrderDetail" @updated="loadUsers" />

    <!-- 订单详情弹窗（会员订单内订单号点击打开；用户点击经 view-user 回到会员订单弹窗） -->
    <order-detail-modal v-model:show="detailVisible" :order-id="detailId" @view-user="openOrdersOf" />
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'UserList' })
import { computed, h, onMounted, reactive, ref } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { NButton, NTag, useDialog, useMessage } from 'naive-ui'
import { getUserList, createUser, setUserStatus } from '@/api/user'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
import UserOrdersModal from './components/UserOrdersModal.vue'
import OrderDetailModal from '@/views/order/detail.vue'
import { reopenModal } from '@/utils/modal'
import type { DataTableColumns } from 'naive-ui'
import type { User } from '@/api/types'

const pageSize = 10
const authStore = useAuthStore()
const message = useMessage()
const dialog = useDialog()

/** 是否具备会员管理能力（添加/拉黑） */
const canManage = computed(() => authStore.hasPermission('user:create') || authStore.hasPermission('user:update'))
/** 当前社区名称（取列表返回值中的社区名） */
const currentCommunityName = computed(() => users.value[0]?.community_name || '当前社区')

/** 状态筛选选项：-1 全部 1 正常 0 拉黑 */
const statusOptions = [
  { label: '全部状态', value: -1 },
  { label: '正常', value: 1 },
  { label: '已拉黑', value: 0 },
]

/** 性别选项 */
const genderOptions = [
  { label: '未知', value: 0 },
  { label: '男', value: 1 },
  { label: '女', value: 2 },
]

/** 渲染状态标签 */
function renderStatus(status: number) {
  return status === 1
    ? h(NTag, { type: 'success', size: 'small' }, { default: () => '正常' })
    : h(NTag, { type: 'error', size: 'small' }, { default: () => '已拉黑' })
}

/** 渲染操作列：查看订单 + 拉黑/恢复 */
function renderActions(row: User) {
  const buttons = [
    h(
      NButton,
      {
        size: 'small',
        type: 'primary',
        secondary: true,
        onClick: () => openOrders(row),
      },
      { default: () => '订单' },
    ),
  ]
  if (authStore.hasPermission('user:update')) {
    buttons.push(
      row.status === 1
        ? h(
            NButton,
            { size: 'small', type: 'error', secondary: true, onClick: () => confirmSetStatus(row, 0) },
            { default: () => '拉黑' },
          )
        : h(
            NButton,
            { size: 'small', type: 'warning', secondary: true, onClick: () => confirmSetStatus(row, 1) },
            { default: () => '恢复' },
          ),
    )
  }
  return buttons.length > 1 ? h('div', { style: 'display:flex;gap:8px' }, buttons) : buttons[0]
}

/** 会员表格列 */
const userColumns: DataTableColumns<User> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '昵称', key: 'nickname', width: 130, ellipsis: { tooltip: true } },
  { title: '手机号', key: 'phone', width: 120 },
  { title: '所属社区', key: 'community_name', width: 150, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status', width: 80, render: (row) => renderStatus(row.status) },
  { title: '注册时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
  { title: '最后登录', key: 'last_login_at', width: 170, render: (row) => formatDateTime(row.last_login_at) },
  { title: '操作', key: 'actions', width: 200, render: (row) => renderActions(row) },
]

const userLoading = ref(false)
const userPage = ref(1)
const userPageCount = ref(1)
const userKeyword = ref('')
const statusFilter = ref(-1)
const users = ref<User[]>([])

// 添加会员弹窗状态
const showCreateModal = ref(false)
const createLoading = ref(false)
const createForm = reactive<{ phone: string; nickname: string; gender: number; birthday: string | null }>({
  phone: '',
  nickname: '',
  gender: 0,
  birthday: null,
})

/** 生日不允许选择未来日期 */
function isFutureDate(ts: number) {
  return ts > Date.now()
}

// 会员订单弹窗状态（行数据或详情弹窗 view-user 转发的轻量用户对象）
const showOrdersModal = ref(false)
const ordersUser = ref<User | { id: number; nickname?: string } | null>(null)

// 订单详情弹窗状态（会员订单弹窗内订单号点击打开）
const detailVisible = ref(false)
const detailId = ref<number | null>(null)

/**
 * 打开会员订单弹窗
 * @param row 目标会员
 */
function openOrders(row: User) {
  ordersUser.value = row
  showOrdersModal.value = true
}

/**
 * 打开会员订单弹窗（轻量用户对象，含订单详情弹窗 view-user 事件转发；已开时重挂载置顶）
 * @param user 会员 ID 与昵称
 */
function openOrdersOf(user: { id: number; nickname?: string }) {
  ordersUser.value = { id: user.id, nickname: user.nickname }
  reopenModal(showOrdersModal)
}

/**
 * 打开订单详情弹窗（含会员订单弹窗 open-order 事件转发；已开时重挂载置顶并重载）
 * @param id 订单 ID
 */
function openOrderDetail(id: number) {
  detailId.value = id
  reopenModal(detailVisible)
}

/**
 * 查询（重置回第一页）
 */
function onSearch() {
  userPage.value = 1
  loadUsers()
}

/**
 * 加载会员列表
 */
async function loadUsers() {
  userLoading.value = true
  try {
    const res = await getUserList({
      page: userPage.value,
      page_size: pageSize,
      keyword: userKeyword.value,
      status_filter: statusFilter.value,
    })
    users.value = res.data.data.list
    userPageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    userLoading.value = false
  }
}

/**
 * 提交添加会员
 * @returns 关闭弹窗（校验失败或请求失败时阻止关闭）
 */
async function handleCreate() {
  if (!/^1[3-9]\d{9}$/.test(createForm.phone)) {
    message.error('请输入正确的 11 位手机号')
    return false
  }
  createLoading.value = true
  try {
    await createUser({
      phone: createForm.phone,
      nickname: createForm.nickname.trim(),
      gender: createForm.gender,
      birthday: createForm.birthday || undefined,
    })
    message.success('会员添加成功')
    createForm.phone = ''
    createForm.nickname = ''
    createForm.gender = 0
    createForm.birthday = null
    onSearch()
    return true
  } finally {
    createLoading.value = false
  }
}

/**
 * 二次确认后拉黑/恢复会员
 * @param row 目标会员
 * @param status 0 拉黑 1 恢复
 */
function confirmSetStatus(row: User, status: number) {
  const action = status === 0 ? '拉黑' : '恢复'
  dialog.warning({
    title: `${action}确认`,
    content:
      status === 0
        ? `拉黑后「${row.nickname || row.phone}」将无法访问 C 端（提示系统错误），确定拉黑？`
        : `确定恢复「${row.nickname || row.phone}」的正常访问？`,
    positiveText: '确定',
    negativeText: '取消',
    onPositiveClick: async () => {
      await setUserStatus(row.id, status)
      message.success(`${action}成功`)
      loadUsers()
    },
  })
}

// 会员列表：keep-alive 缓存下切回本页不重新挂载，需在激活时刷新（后台拉黑/新增会员后同步）
onMounted(loadUsers)
refreshOnActivated(loadUsers)
</script>
