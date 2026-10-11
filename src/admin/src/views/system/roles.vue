<template>
  <n-card title="角色管理">
    <n-space vertical>
      <n-space>
        <n-button @click="showModal = true">新增角色</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showModal" title="角色" preset="card" style="width: 500px; max-width: 90vw">
    <n-space vertical>
      <n-input v-model:value="form.name" placeholder="角色名称" />
      <n-input v-model:value="form.code" placeholder="角色编码" />
      <n-input v-model:value="form.description" placeholder="描述" />
      <n-select v-model:value="form.permissions" multiple :options="permissionOptions" placeholder="权限" />
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRoleList, createRole, updateRole, deleteRole } from '@/api/admin'
import type { Role } from '@/api/types'

/**
 * 角色管理页面
 * 支持角色的增删改查
 */
const list = ref<Role[]>([])
const loading = ref(false)
const showModal = ref(false)
const isEdit = ref(false)
const form = ref<Partial<Role>>({
  name: '',
  code: '',
  description: '',
  permissions: [],
})

const permissionOptions = [
  { label: '工作台', value: 'dashboard:view' },
  { label: '商品查看', value: 'product:view' },
  { label: '商品创建', value: 'product:create' },
  { label: '商品更新', value: 'product:update' },
  { label: '商品删除', value: 'product:delete' },
  { label: '库存查看', value: 'inventory:view' },
  { label: '库存创建', value: 'inventory:create' },
  { label: '库存更新', value: 'inventory:update' },
  { label: '订单查看', value: 'order:view' },
  { label: '订单更新', value: 'order:update' },
  { label: '退款查看', value: 'refund:view' },
  { label: '退款更新', value: 'refund:update' },
  { label: '会员查看', value: 'user:view' },
  { label: '营销查看', value: 'marketing:view' },
  { label: '营销创建', value: 'marketing:create' },
  { label: '营销更新', value: 'marketing:update' },
  { label: '营销删除', value: 'marketing:delete' },
  { label: '统计查看', value: 'statistics:view' },
  { label: '社区查看', value: 'community:view' },
  { label: '社区创建', value: 'community:create' },
  { label: '社区更新', value: 'community:update' },
  { label: '社区删除', value: 'community:delete' },
  { label: '管理员查看', value: 'admin:view' },
  { label: '管理员创建', value: 'admin:create' },
  { label: '管理员更新', value: 'admin:update' },
  { label: '管理员删除', value: 'admin:delete' },
  { label: '角色查看', value: 'role:view' },
  { label: '角色创建', value: 'role:create' },
  { label: '角色更新', value: 'role:update' },
  { label: '角色删除', value: 'role:delete' },
]

const columns: DataTableColumns<Role> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '角色名称', key: 'name' },
  { title: '角色编码', key: 'code' },
  { title: '描述', key: 'description' },
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

async function loadData() {
  loading.value = true
  const res = await getRoleList()
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data
  }
}

function edit(row: Role) {
  isEdit.value = true
  form.value = { ...row }
  showModal.value = true
}

function reset() {
  isEdit.value = false
  form.value = { name: '', code: '', description: '', permissions: [] }
}

async function submit() {
  const data = { ...form.value } as Role
  const res = isEdit.value ? await updateRole(data.id, data) : await createRole(data)
  if (res.data.code === 0) {
    reset()
    showModal.value = false
    loadData()
  }
}

async function remove(row: Role) {
  const res = await deleteRole(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(loadData)
</script>
