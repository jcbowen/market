<template>
  <n-card title="管理员列表">
    <n-space vertical>
      <n-space>
        <n-input v-model:value="keyword" placeholder="用户名/手机号" clearable />
        <n-button type="primary" @click="loadData">查询</n-button>
        <n-button @click="showModal = true">新增管理员</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showModal" title="新增管理员" preset="card" style="width: 500px">
    <n-space vertical>
      <n-input v-model:value="form.username" placeholder="用户名" />
      <n-input v-model:value="form.password" type="password" placeholder="密码" />
      <n-input v-model:value="form.real_name" placeholder="真实姓名" />
      <n-input v-model:value="form.phone" placeholder="手机号" />
      <n-select v-model:value="form.role_id" :options="roleOptions" placeholder="角色" />
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getAdminList, createAdmin, updateAdmin, deleteAdmin, getRoleList } from '@/api/admin'
import type { AdminInfo, Role } from '@/api/types'

/**
 * 管理员列表页面
 * 支持管理员的增删改查
 */
const keyword = ref('')
const list = ref<AdminInfo[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })
const showModal = ref(false)
const roleOptions = ref<Array<{ label: string; value: number }>>([])
const form = ref<Partial<AdminInfo & { password: string }>>({
  username: '',
  password: '',
  real_name: '',
  phone: '',
  role_id: undefined,
  status: 1,
})

const columns: DataTableColumns<AdminInfo> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '用户名', key: 'username' },
  { title: '真实姓名', key: 'real_name' },
  { title: '手机号', key: 'phone' },
  { title: '角色', key: 'role_name' },
  {
    title: '状态',
    key: 'status',
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '正常' : '禁用') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadRoles() {
  const res = await getRoleList()
  if (res.data.code === 0) {
    roleOptions.value = (res.data.data || []).map((r: Role) => ({ label: r.name, value: r.id }))
  }
}

async function loadData() {
  loading.value = true
  const res = await getAdminList({ page: pagination.value.page, page_size: pagination.value.pageSize, keyword: keyword.value })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

function edit(row: AdminInfo) {
  form.value = { ...row, password: '' }
  showModal.value = true
}

async function submit() {
  const data = { ...form.value }
  const id = data.id
  const res = id ? await updateAdmin(id, data) : await createAdmin(data as any)
  if (res.data.code === 0) {
    showModal.value = false
    loadData()
  }
}

async function remove(row: AdminInfo) {
  const res = await deleteAdmin(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(() => {
  loadRoles()
  loadData()
})
</script>
