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
        </n-form>
      </n-space>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'UserList' })
import { computed, h, onMounted, reactive, ref } from 'vue'
import { NButton, NTag, useDialog, useMessage } from 'naive-ui'
import { getUserList, createUser, setUserStatus } from '@/api/user'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
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

/** 渲染操作列：拉黑/恢复 */
function renderActions(row: User) {
  if (!authStore.hasPermission('user:update')) return null
  if (row.status === 1) {
    return h(
      NButton,
      {
        size: 'small',
        type: 'error',
        secondary: true,
        onClick: () => confirmSetStatus(row, 0),
      },
      { default: () => '拉黑' },
    )
  }
  return h(
    NButton,
    {
      size: 'small',
      type: 'warning',
      secondary: true,
      onClick: () => confirmSetStatus(row, 1),
    },
    { default: () => '恢复' },
  )
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
  { title: '操作', key: 'actions', width: 90, render: (row) => renderActions(row) },
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
const createForm = reactive({ phone: '', nickname: '', gender: 0 })

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
    })
    message.success('会员添加成功')
    createForm.phone = ''
    createForm.nickname = ''
    createForm.gender = 0
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

onMounted(() => {
  loadUsers()
})
</script>
