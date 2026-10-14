<template>
  <n-card title="角色管理">
    <n-space vertical>
      <n-space align="center">
        <!-- 超管可切换查看全局角色 / 当前社区角色；普通管理员固定为当前社区 -->
        <n-radio-group v-if="isSuper" v-model:value="scope">
          <n-radio-button value="community">当前社区角色</n-radio-button>
          <n-radio-button value="global">全局角色</n-radio-button>
        </n-radio-group>
        <n-button type="primary" @click="openCreate">新增角色</n-button>
      </n-space>
      <n-alert v-if="!isSuper" type="info" :show-icon="false">
        以下为当前社区的私有角色；可分配的权限点不超出你自身的权限范围
      </n-alert>
      <n-data-table :columns="columns" :data="list" :loading="loading" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showModal" title="角色" preset="card" style="width: 520px; max-width: 90vw">
    <n-space vertical>
      <n-input v-model:value="form.name" placeholder="角色名称" />
      <n-input v-model:value="form.code" placeholder="角色编码（如 staff，社区内唯一）" :disabled="isBuiltinRow" />
      <n-input v-model:value="form.description" placeholder="描述" />
      <n-select
        v-model:value="form.permissions"
        multiple
        :options="permissionOptions"
        placeholder="权限"
        :disabled="isBuiltinRow && !isSuper"
      />
      <n-alert v-if="isBuiltinRow" type="warning" :show-icon="false">
        内置社区管理员角色：不可删除{{ isSuper ? '，仅超管可修改' : '，仅超管可修改' }}
      </n-alert>
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RoleList' })
import { ref, onMounted, computed, h, watch } from 'vue'
import { NButton, NSpace, NTag } from 'naive-ui'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRoleList, createRole, updateRole, deleteRole } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import type { Role } from '@/api/types'

/**
 * 角色管理页面（多社区模型）
 * - 普通管理员：管理当前社区的私有角色，权限点不超出自身权限
 * - 超管：可切换查看/管理当前社区角色与全局角色；内置社区管理员角色仅超管可改
 */
const message = useMessage()
const authStore = useAuthStore()
const isSuper = computed(() => authStore.isSuperAdmin)

/** 查看范围：community=当前社区私有角色，global=全局角色（仅超管） */
const scope = ref<'community' | 'global'>('community')
const list = ref<Role[]>([])
const loading = ref(false)
const showModal = ref(false)
const isEdit = ref(false)
/** 当前编辑行（用于内置角色判定） */
const editingRow = ref<Role | null>(null)
const form = ref<Partial<Role>>({
  name: '',
  code: '',
  description: '',
  permissions: [],
})

/** 是否内置社区管理员角色 */
const isBuiltinRow = computed(() => !!editingRow.value?.code && editingRow.value.code === 'community_admin' && (editingRow.value.community_id ?? 0) > 0)

/** 全量权限点清单 */
const allPermissionOptions = [
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
  { label: '区划查看', value: 'region:view' },
  { label: '区划更新', value: 'region:update' },
  { label: '员工查看', value: 'admin:view' },
  { label: '员工创建', value: 'admin:create' },
  { label: '员工更新', value: 'admin:update' },
  { label: '员工删除', value: 'admin:delete' },
  { label: '角色查看', value: 'role:view' },
  { label: '角色创建', value: 'role:create' },
  { label: '角色更新', value: 'role:update' },
  { label: '角色删除', value: 'role:delete' },
]

/**
 * 可勾选的权限点：
 * 超管全量；普通管理员仅自身当前社区的权限（含 "*" 通配时为全量），防止越权放大
 */
const permissionOptions = computed(() => {
  if (isSuper.value) return allPermissionOptions
  const perms = authStore.permissions
  if (perms.includes('*')) return allPermissionOptions
  const owned = new Set(perms)
  return allPermissionOptions.filter((o) => owned.has(o.value))
})

const columns = computed<DataTableColumns<Role>>(() => [
  { title: 'ID', key: 'id', width: 80 },
  { title: '角色名称', key: 'name' },
  { title: '角色编码', key: 'code' },
  {
    title: '类型',
    key: 'kind',
    width: 110,
    render: (row) =>
      h(
        NTag,
        { type: row.code === 'community_admin' && (row.community_id ?? 0) > 0 ? 'warning' : 'default', size: 'small' },
        { default: () => (row.code === 'community_admin' && (row.community_id ?? 0) > 0 ? '内置角色' : scope.value === 'global' ? '全局' : '社区角色') },
      ),
  },
  { title: '描述', key: 'description', ellipsis: { tooltip: true } },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      const builtin = row.code === 'community_admin' && (row.community_id ?? 0) > 0
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
          !builtin &&
            h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ].filter(Boolean),
      })
    },
  },
])

async function loadData() {
  loading.value = true
  const res = await getRoleList(scope.value === 'global' ? { scope: 'global' } : undefined)
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data
  }
}

function openCreate() {
  isEdit.value = false
  editingRow.value = null
  form.value = { name: '', code: '', description: '', permissions: [] }
  showModal.value = true
}

function edit(row: Role) {
  isEdit.value = true
  editingRow.value = row
  form.value = { ...row }
  showModal.value = true
}

async function submit() {
  // 角色归属：全局页签下超管创建的是全局角色（community_id=0）；
  // 社区页签下按当前社区归属（后端对普通管理员会强制锁定为其当前社区）
  const data = {
    ...form.value,
    community_id: scope.value === 'global' ? 0 : authStore.currentCommunityId || 0,
  }
  const res = isEdit.value && form.value.id ? await updateRole(form.value.id, data) : await createRole(data)
  if (res.data.code === 0) {
    message.success('保存成功')
    showModal.value = false
    loadData()
  }
}

async function remove(row: Role) {
  const res = await deleteRole(row.id)
  if (res.data.code === 0) {
    message.success('已删除')
    loadData()
  }
}

// 超管切换查看范围时重新加载
watch(scope, () => loadData())

onMounted(loadData)
</script>
