<template>
  <n-card :title="isGlobalPage ? '全局角色' : '角色管理'">
    <n-space vertical>
      <n-alert v-if="isGlobalPage" type="info" :show-icon="false">
        全局角色为系统级角色（community_id = 0），仅超级管理员可管理；社区级角色请在「门店管理 → 角色管理」中维护
      </n-alert>
      <n-alert v-else type="info" :show-icon="false">
        以下为当前社区的私有角色；可分配的权限点不超出你自身的权限范围（并受社区权限上限约束）。
        内置「社区管理员」角色仅展示不可编辑，权限上限由超级管理员在「系统管理 → 社区权限」中分配
      </n-alert>
      <n-space align="center">
        <n-button type="primary" @click="openCreate">{{ isGlobalPage ? '新增全局角色' : '新增角色' }}</n-button>
      </n-space>
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
        内置社区管理员角色：不可删除，仅超管可修改；其权限即本社区权限上限，也可在「系统管理 → 社区权限」中调整
      </n-alert>
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RoleList' })
import { ref, onMounted, computed, h } from 'vue'
import { useRoute } from 'vue-router'
import { NButton, NSpace, NTag } from 'naive-ui'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRoleList, createRole, updateRole, deleteRole } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import { allPermissionOptions } from '@/constants/permissions'
import type { Role } from '@/api/types'

/**
 * 角色管理页面（多社区模型）
 * - 同一组件承载两个入口：员工与角色 → 角色管理（当前社区角色）；系统管理 → 全局角色（仅超管）
 * - 普通管理员：管理当前社区的私有角色，权限点不超出自身权限（并受社区权限上限约束）
 * - 超管：社区语境下经顶栏社区切换可管理任意社区角色；内置社区管理员角色仅超管可改
 */
const message = useMessage()
const route = useRoute()
const authStore = useAuthStore()
const isSuper = computed(() => authStore.isSuperAdmin)

/** 全局角色页（系统管理入口）固定查看全局角色；否则固定当前社区 */
const isGlobalPage = computed(() => route.name === 'RoleListGlobal')
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

/** 全量权限点清单（共享常量） */
const allOptions = allPermissionOptions

/**
 * 可勾选的权限点：
 * 超管全量；普通管理员仅自身当前社区的权限（含 "*" 通配时为全量），防止越权放大
 */
const permissionOptions = computed(() => {
  if (isSuper.value) return allOptions
  const perms = authStore.permissions
  if (perms.includes('*')) return allOptions
  const owned = new Set(perms)
  return allOptions.filter((o) => owned.has(o.value))
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
        { default: () => (row.code === 'community_admin' && (row.community_id ?? 0) > 0 ? '内置角色' : isGlobalPage.value ? '全局' : '社区角色') },
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
          // 内置社区管理员角色在业务页仅展示禁编辑：其权限由超管在「系统管理 → 社区权限」调整
          h(NButton, { size: 'small', disabled: builtin, onClick: () => edit(row) }, { default: () => '编辑' }),
          !builtin &&
            h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ].filter(Boolean),
      })
    },
  },
])

async function loadData() {
  loading.value = true
  try {
    const res = await getRoleList(isGlobalPage.value ? { scope: 'global' } : undefined)
    if (res.data.code === 0) {
      list.value = res.data.data
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
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
    community_id: isGlobalPage.value ? 0 : authStore.currentCommunityId || 0,
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

onMounted(loadData)
</script>
