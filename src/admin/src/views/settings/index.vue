<template>
  <div class="settings-page">
    <n-card title="系统管理">
      <n-tabs v-model:value="activeTab" type="line" animated>
        <n-tab-pane name="info" tab="系统信息">
          <n-descriptions label-placement="top" bordered :columns="2">
            <n-descriptions-item label="系统名称">社区商超后台管理系统</n-descriptions-item>
            <n-descriptions-item label="版本号">v1.0.0</n-descriptions-item>
            <n-descriptions-item label="当前管理员">{{ authStore.admin?.nickname || '-' }}</n-descriptions-item>
            <n-descriptions-item label="角色">{{ authStore.admin?.role_name || '-' }}</n-descriptions-item>
          </n-descriptions>
          <n-space style="margin-top: 16px;">
            <n-button type="primary" @click="openPasswordModal">修改密码</n-button>
          </n-space>
        </n-tab-pane>

        <n-tab-pane name="roles" tab="角色权限">
          <n-space vertical>
            <n-space>
              <n-button type="primary" :disabled="!authStore.hasPermission('role:create')" @click="openRoleModal()">新增角色</n-button>
            </n-space>
            <n-data-table :columns="roleColumns" :data="roles" :loading="roleLoading" scroll-x="600" />
          </n-space>
        </n-tab-pane>
      </n-tabs>
    </n-card>

    <!-- 角色编辑弹窗 -->
    <n-modal v-model:show="showRoleModal" title="角色信息" preset="card" style="width: 560px; max-width: 90vw" scrollable>
      <n-form :model="roleForm" label-placement="top">
        <n-form-item label="角色名称">
          <n-input v-model:value="roleForm.name" placeholder="请输入角色名称" />
        </n-form-item>
        <n-form-item label="角色编码">
          <n-input v-model:value="roleForm.code" placeholder="请输入角色编码，如 admin" />
        </n-form-item>
        <n-form-item label="权限">
          <n-checkbox-group v-model:value="roleForm.permissions">
            <n-space vertical>
              <n-checkbox v-for="perm in permissionOptions" :key="perm.value" :value="perm.value" :label="perm.label" />
            </n-space>
          </n-checkbox-group>
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="roleForm.description" type="textarea" placeholder="角色描述" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showRoleModal = false">取消</n-button>
          <n-button type="primary" :loading="roleModalLoading" @click="saveRole">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 修改密码弹窗 -->
    <n-modal v-model:show="showPasswordModal" title="修改密码" preset="card" style="width: 420px; max-width: 90vw" scrollable>
      <n-form :model="passwordForm" label-placement="top">
        <n-form-item label="旧密码">
          <n-input v-model:value="passwordForm.old_password" type="password" placeholder="请输入旧密码" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="passwordForm.new_password" type="password" placeholder="请输入新密码，至少 6 位" />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input v-model:value="passwordForm.confirm_password" type="password" placeholder="请再次输入新密码" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showPasswordModal = false">取消</n-button>
          <n-button type="primary" :loading="passwordLoading" @click="savePassword">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRoleList, createRole, updateRole, deleteRole, changeAdminPassword } from '@/api/admin'
import type { Role } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const authStore = useAuthStore()

const activeTab = ref('info')
const roleLoading = ref(false)
const roles = ref<Role[]>([])

const showRoleModal = ref(false)
const roleModalLoading = ref(false)
const roleForm = reactive<Partial<Role>>({
  id: undefined,
  name: '',
  code: '',
  permissions: [],
  description: '',
})

const showPasswordModal = ref(false)
const passwordLoading = ref(false)
const passwordForm = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})

/** 权限可选项 */
const permissionOptions = [
  { label: '仪表盘查看', value: 'dashboard:view' },
  { label: '管理员查看', value: 'admin:view' },
  { label: '管理员创建', value: 'admin:create' },
  { label: '管理员更新', value: 'admin:update' },
  { label: '管理员删除', value: 'admin:delete' },
  { label: '角色查看', value: 'role:view' },
  { label: '角色创建', value: 'role:create' },
  { label: '角色更新', value: 'role:update' },
  { label: '角色删除', value: 'role:delete' },
  { label: '商品查看', value: 'product:view' },
  { label: '商品创建', value: 'product:create' },
  { label: '商品更新', value: 'product:update' },
  { label: '商品删除', value: 'product:delete' },
  { label: '订单查看', value: 'order:view' },
  { label: '订单更新', value: 'order:update' },
  { label: '库存查看', value: 'inventory:view' },
  { label: '库存创建', value: 'inventory:create' },
  { label: '营销查看', value: 'marketing:view' },
  { label: '营销创建', value: 'marketing:create' },
  { label: '营销更新', value: 'marketing:update' },
  { label: '营销删除', value: 'marketing:delete' },
  { label: '社区查看', value: 'community:view' },
  { label: '用户查看', value: 'user:view' },
  { label: '退款查看', value: 'refund:view' },
  { label: '退款更新', value: 'refund:update' },
  { label: '数据统计', value: 'statistics:view' },
  { label: '系统管理', value: 'settings:view' },
]

/** 角色表格列 */
const roleColumns: DataTableColumns<Role> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '角色名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '角色编码', key: 'code', width: 120, ellipsis: { tooltip: true } },
  {
    title: '权限',
    key: 'permissions',
    width: 200,
    ellipsis: { tooltip: true },
    render(row) {
      return (row.permissions || []).join(', ')
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render(row) {
      return h('div', { style: 'display: flex; gap: 12px; flex-wrap: wrap;' }, [
        authStore.hasPermission('role:update')
          ? h('a', { onClick: () => openRoleModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑')
          : null,
        authStore.hasPermission('role:delete')
          ? h('a', { onClick: () => onDeleteRole(row.id), style: 'color: #ff4d4f; cursor: pointer;' }, '删除')
          : null,
      ])
    },
  },
]

/**
 * 加载角色列表
 */
async function loadRoles() {
  roleLoading.value = true
  try {
    const res = await getRoleList()
    roles.value = res.data.data
  } finally {
    roleLoading.value = false
  }
}

/**
 * 打开角色编辑弹窗
 * @param row 角色数据
 */
function openRoleModal(row?: Role) {
  roleForm.id = row?.id
  roleForm.name = row?.name || ''
  roleForm.code = row?.code || ''
  roleForm.permissions = row?.permissions || []
  roleForm.description = row?.description || ''
  showRoleModal.value = true
}

/**
 * 保存角色
 */
async function saveRole() {
  if (!roleForm.name) {
    message.warning('请输入角色名称')
    return
  }
  if (!roleForm.code) {
    message.warning('请输入角色编码')
    return
  }

  roleModalLoading.value = true
  try {
    if (roleForm.id) {
      await updateRole(roleForm.id, roleForm)
    } else {
      await createRole(roleForm)
    }
    message.success('保存成功')
    showRoleModal.value = false
    loadRoles()
  } finally {
    roleModalLoading.value = false
  }
}

/**
 * 删除角色
 * @param id 角色 ID
 */
async function onDeleteRole(id: number) {
  try {
    await deleteRole(id)
    message.success('删除成功')
    loadRoles()
  } catch {
    message.error('删除失败')
  }
}

/**
 * 打开修改密码弹窗
 */
function openPasswordModal() {
  passwordForm.old_password = ''
  passwordForm.new_password = ''
  passwordForm.confirm_password = ''
  showPasswordModal.value = true
}

/**
 * 保存新密码
 */
async function savePassword() {
  if (!passwordForm.old_password) {
    message.warning('请输入旧密码')
    return
  }
  if (!passwordForm.new_password || passwordForm.new_password.length < 6) {
    message.warning('新密码至少 6 位')
    return
  }
  if (passwordForm.new_password !== passwordForm.confirm_password) {
    message.warning('两次输入的新密码不一致')
    return
  }

  const adminId = authStore.admin?.id
  if (!adminId) {
    message.error('未获取到管理员信息')
    return
  }

  passwordLoading.value = true
  try {
    await changeAdminPassword(adminId, {
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password,
    })
    message.success('密码修改成功，请重新登录')
    showPasswordModal.value = false
  } finally {
    passwordLoading.value = false
  }
}

onMounted(() => {
  loadRoles()
})
</script>

<style lang="scss" scoped>
.settings-page {
  max-width: 100%;
}
</style>
