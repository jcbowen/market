<template>
  <n-card title="账号管理">
    <n-space vertical>
      <n-alert type="info" :show-icon="false">
        管理所有后台账号本身（新增/停用/重置密码）。账号在各社区的任职与角色请在对应社区的「门店管理 → 员工管理」中维护。
      </n-alert>
      <n-space>
        <n-input v-model:value="keyword" placeholder="用户名/姓名/手机号" clearable @keyup.enter="onSearch" />
        <n-button type="primary" @click="onSearch">查询</n-button>
        <n-button type="primary" secondary @click="openCreate">新增账号</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" remote />
    </n-space>
  </n-card>

  <!-- 新增账号弹窗：仅创建账号本身，不在任何社区任职 -->
  <n-modal v-model:show="showModal" title="新增账号" preset="card" style="width: 520px; max-width: 90vw">
    <n-space vertical>
      <n-input v-model:value="form.username" placeholder="用户名（登录账号，全局唯一）" />
      <n-input v-model:value="form.password" type="password" show-password-on="click" placeholder="初始密码（至少 6 位）" />
      <n-input v-model:value="form.real_name" placeholder="真实姓名" />
      <n-input v-model:value="form.phone" placeholder="手机号" />
      <n-button type="primary" @click="submitCreate">保存</n-button>
    </n-space>
  </n-modal>

  <!-- 重置密码弹窗 -->
  <n-modal v-model:show="showResetModal" title="重置密码" preset="card" style="width: 420px; max-width: 90vw">
    <n-space vertical>
      <n-text>为「{{ resetForm.username }}」设置新密码：</n-text>
      <n-input v-model:value="resetForm.password" type="password" show-password-on="click" placeholder="新密码（至少 6 位）" />
      <n-button type="primary" @click="submitResetPassword">确定</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'AccountList' })
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getAccountList, createAccount, updateAccountStatus, resetAccountPassword } from '@/api/user'
import type { Account } from '@/api/types'

/**
 * 账号管理页面（系统管理空间，超管专用）
 * 仅管理账号本身：列表/新增/停用启用/重置密码；
 * 社区任职不在此维护（由各社区员工管理页按社区视角操作）
 */
const message = useMessage()
const dialog = useDialog()

const keyword = ref('')
const list = ref<Account[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })
const showModal = ref(false)
const form = ref({ username: '', password: '', real_name: '', phone: '' })
const showResetModal = ref(false)
const resetForm = ref({ id: 0, username: '', password: '' })

const columns: DataTableColumns<Account> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '用户名', key: 'username', width: 140 },
  { title: '真实姓名', key: 'real_name', width: 120 },
  { title: '手机号', key: 'phone', width: 130 },
  {
    title: '类型',
    key: 'is_super',
    width: 90,
    render: (row) =>
      h(NTag, { type: row.is_super ? 'warning' : 'default', size: 'small' }, { default: () => (row.is_super ? '超级管理员' : '普通账号') }),
  },
  {
    title: '状态',
    key: 'status',
    width: 80,
    render: (row) =>
      h(NTag, { type: row.status === 1 ? 'success' : 'error', size: 'small' }, { default: () => (row.status === 1 ? '正常' : '已停用') }),
  },
  {
    title: '任职社区数',
    key: 'community_ids',
    width: 100,
    render: (row) => (row.is_super ? '全部' : String(row.community_ids?.length ?? 0)),
  },
  { title: '最后登录', key: 'last_login_at', width: 160 },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    render(row) {
      return h(NSpace, null, {
        default: () => [
          row.status === 1
            ? h(NButton, { size: 'small', type: 'error', secondary: true, onClick: () => confirmStatus(row, 0) }, { default: () => '停用' })
            : h(NButton, { size: 'small', type: 'warning', secondary: true, onClick: () => confirmStatus(row, 1) }, { default: () => '启用' }),
          h(NButton, { size: 'small', onClick: () => confirmResetPassword(row) }, { default: () => '重置密码' }),
        ],
      })
    },
  },
]

/**
 * 查询（重置回第一页）
 */
function onSearch() {
  pagination.value.page = 1
  loadData()
}

/**
 * 加载账号列表
 */
async function loadData() {
  loading.value = true
  try {
    const res = await getAccountList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      keyword: keyword.value,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    loading.value = false
  }
}

/**
 * 打开新增账号弹窗
 */
function openCreate() {
  form.value = { username: '', password: '', real_name: '', phone: '' }
  showModal.value = true
}

/**
 * 提交新增账号
 */
async function submitCreate() {
  if (!form.value.username.trim()) {
    message.warning('请填写用户名')
    return
  }
  if (form.value.password.length < 6) {
    message.warning('初始密码长度至少 6 位')
    return
  }
  await createAccount({
    username: form.value.username.trim(),
    password: form.value.password,
    real_name: form.value.real_name.trim(),
    phone: form.value.phone.trim(),
  })
  message.success('账号创建成功；请在各社区「员工管理」中分配任职')
  showModal.value = false
  loadData()
}

/**
 * 停用/启用账号（二次确认；不能停用自己）
 * @param row 目标账号
 * @param status 0 停用 1 启用
 */
function confirmStatus(row: Account, status: number) {
  const action = status === 0 ? '停用' : '启用'
  dialog.warning({
    title: `${action}确认`,
    content:
      status === 0
        ? `停用后「${row.real_name || row.username}」将无法登录后台，确定停用？`
        : `确定启用账号「${row.real_name || row.username}」？`,
    positiveText: '确定',
    negativeText: '取消',
    onPositiveClick: async () => {
      await updateAccountStatus(row.id, status)
      message.success(`${action}成功`)
      loadData()
    },
  })
}

/**
 * 重置账号密码：打开弹窗
 * @param row 目标账号
 */
function confirmResetPassword(row: Account) {
  resetForm.value = { id: row.id, username: row.real_name || row.username, password: '' }
  showResetModal.value = true
}

/**
 * 提交重置密码
 */
async function submitResetPassword() {
  if (resetForm.value.password.length < 6) {
    message.warning('密码长度至少 6 位')
    return
  }
  await resetAccountPassword(resetForm.value.id, resetForm.value.password)
  message.success('密码已重置')
  showResetModal.value = false
}

onMounted(loadData)
</script>
