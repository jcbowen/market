<template>
  <div class="user-page">
    <n-card title="用户列表">
      <n-space vertical>
        <n-space wrap>
          <n-input v-model:value="userKeyword" placeholder="搜索昵称/手机号" clearable />
          <n-button type="primary" @click="loadUsers">查询</n-button>
        </n-space>
        <n-data-table :columns="userColumns" :data="users" :loading="userLoading" scroll-x="540" />
        <n-pagination v-model:page="userPage" :page-count="userPageCount" @update:page="loadUsers" />
      </n-space>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getUserList } from '@/api/user'
import type { DataTableColumns } from 'naive-ui'
import type { User } from '@/api/types'

const pageSize = 10

/** 用户表格列 */
const userColumns: DataTableColumns<User> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '昵称', key: 'nickname', width: 120, ellipsis: { tooltip: true } },
  { title: '手机号', key: 'phone', width: 120 },
  { title: '所属社区', key: 'community_name', width: 140, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status', width: 80 },
]

const userLoading = ref(false)
const userPage = ref(1)
const userPageCount = ref(1)
const userKeyword = ref('')
const users = ref<User[]>([])

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

onMounted(() => {
  loadUsers()
})
</script>
