<template>
  <n-card :title="isEdit ? '编辑社区' : '新增社区'">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="社区名称">
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item label="地址">
        <n-input v-model:value="form.address" />
      </n-form-item>
      <n-form-item label="联系电话">
        <n-input v-model:value="form.manager_phone" />
      </n-form-item>
      <n-form-item label="负责人">
        <n-input v-model:value="form.manager_name" />
      </n-form-item>
      <n-form-item label="状态">
        <n-select v-model:value="form.status" :options="statusOptions" />
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" @click="submit">保存</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { createCommunity, updateCommunity, getCommunityList } from '@/api/user'
import type { Community } from '@/api/types'

/**
 * 社区新增/编辑页面
 */
const route = useRoute()
const router = useRouter()
const isEdit = ref(false)
const form = ref<Partial<Community>>({
  name: '',
  address: '',
  manager_phone: '',
  manager_name: '',
  status: 1,
})
const statusOptions = [
  { label: '营业', value: 1 },
  { label: '停业', value: 0 },
]

async function loadDetail() {
  const id = Number(route.params.id)
  if (!id) return
  isEdit.value = true
  const res = await getCommunityList({ page: 1, page_size: 100 })
  if (res.data.code === 0) {
    const item = res.data.data.list.find((c: Community) => c.id === id)
    if (item) form.value = { ...item }
  }
}

async function submit() {
  const data = { ...form.value } as Community
  const res = isEdit.value ? await updateCommunity(data.id, data) : await createCommunity(data)
  if (res.data.code === 0) {
    router.back()
  }
}

onMounted(loadDetail)
</script>
