<template>
  <div class="user-page">
    <n-card title="社区列表">
      <n-space vertical>
        <n-space wrap>
          <n-input v-model:value="searchKeyword" placeholder="搜索社区名称" clearable />
          <n-button type="primary" @click="loadCommunities">查询</n-button>
          <n-button type="primary" @click="openCommunityModal()">新增社区</n-button>
        </n-space>
        <n-data-table :columns="communityColumns" :data="communities" :loading="loading" scroll-x="720" />
        <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadCommunities" />
      </n-space>
    </n-card>

    <n-card title="用户列表" class="user-card">
      <n-space vertical>
        <n-space wrap>
          <n-input v-model:value="userKeyword" placeholder="搜索昵称/手机号" clearable />
          <n-button type="primary" @click="loadUsers">查询</n-button>
        </n-space>
        <n-data-table :columns="userColumns" :data="users" :loading="userLoading" scroll-x="540" />
        <n-pagination v-model:page="userPage" :page-count="userPageCount" @update:page="loadUsers" />
      </n-space>
    </n-card>

    <!-- 社区编辑弹窗 -->
    <n-modal v-model:show="showModal" title="社区信息" preset="card" style="width: 500px; max-width: 90vw" scrollable>
      <n-form :model="communityForm" label-placement="top">
        <n-form-item label="社区名称">
          <n-input v-model:value="communityForm.name" placeholder="请输入社区名称" />
        </n-form-item>
        <n-form-item label="详细地址">
          <n-input v-model:value="communityForm.address" placeholder="请输入详细地址" />
        </n-form-item>
        <n-form-item label="负责人">
          <n-input v-model:value="communityForm.manager_name" placeholder="请输入负责人姓名" />
        </n-form-item>
        <n-form-item label="联系电话">
          <n-input v-model:value="communityForm.manager_phone" placeholder="请输入联系电话" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="modalLoading" @click="saveCommunity">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCommunityList, createCommunity, updateCommunity } from '@/api/user'
import { getUserList } from '@/api/user'
import type { Community, User } from '@/api/types'

const message = useMessage()

/** 社区表格列 */
const communityColumns: DataTableColumns<Community> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '社区名称', key: 'name', width: 140, ellipsis: { tooltip: true } },
  { title: '地址', key: 'address', width: 180, ellipsis: { tooltip: true } },
  { title: '负责人', key: 'manager_name', width: 100, ellipsis: { tooltip: true } },
  { title: '联系电话', key: 'manager_phone', width: 120 },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h('div', [
        h('a', { onClick: () => openCommunityModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑'),
      ])
    },
  },
]

/** 用户表格列 */
const userColumns: DataTableColumns<User> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '昵称', key: 'nickname', width: 120, ellipsis: { tooltip: true } },
  { title: '手机号', key: 'phone', width: 120 },
  { title: '所属社区', key: 'community_name', width: 140, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status', width: 80 },
]

const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const pageSize = 10
const searchKeyword = ref('')
const communities = ref<Community[]>([])

const userLoading = ref(false)
const userPage = ref(1)
const userPageCount = ref(1)
const userKeyword = ref('')
const users = ref<User[]>([])

const showModal = ref(false)
const modalLoading = ref(false)
const communityForm = reactive<Partial<Community>>({
  id: undefined,
  name: '',
  address: '',
  manager_name: '',
  manager_phone: '',
})

/**
 * 加载社区列表
 */
async function loadCommunities() {
  loading.value = true
  try {
    const res = await getCommunityList({ page: page.value, page_size: pageSize, keyword: searchKeyword.value })
    communities.value = res.data.data.list
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 加载用户列表
 */
async function loadUsers() {
  userLoading.value = true
  try {
    const res = await getUserList({ page: userPage.value, page_size: pageSize, keyword: userKeyword.value })
    users.value = res.data.data.list
    userPageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    userLoading.value = false
  }
}

/**
 * 打开社区编辑弹窗
 * @param row 社区数据，为空则新增
 */
function openCommunityModal(row?: Community) {
  communityForm.id = row?.id
  communityForm.name = row?.name || ''
  communityForm.address = row?.address || ''
  communityForm.manager_name = row?.manager_name || ''
  communityForm.manager_phone = row?.manager_phone || ''
  showModal.value = true
}

/**
 * 保存社区信息
 */
async function saveCommunity() {
  if (!communityForm.name) {
    message.warning('请输入社区名称')
    return
  }
  modalLoading.value = true
  try {
    if (communityForm.id) {
      await updateCommunity(communityForm.id, communityForm)
      message.success('更新成功')
    } else {
      await createCommunity(communityForm)
      message.success('创建成功')
    }
    showModal.value = false
    loadCommunities()
  } finally {
    modalLoading.value = false
  }
}

onMounted(() => {
  loadCommunities()
  loadUsers()
})
</script>

<style lang="scss" scoped>
.user-page {
  .user-card {
    margin-top: 16px;
  }
}
</style>
