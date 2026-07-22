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
        <n-input v-model:value="form.contact_phone" />
      </n-form-item>
      <n-form-item label="负责人">
        <n-input v-model:value="form.manager_name" />
      </n-form-item>
      <n-form-item label="负责人电话">
        <n-input v-model:value="form.manager_phone" />
      </n-form-item>
      <n-form-item label="自提点地址">
        <n-input v-model:value="form.pickup_address" />
      </n-form-item>
      <n-form-item label="经纬度">
        <n-space>
          <n-input v-model:value="longitudeText" placeholder="经度" style="width: 140px" />
          <n-input v-model:value="latitudeText" placeholder="纬度" style="width: 140px" />
          <n-button @click="openMapPicker">在地图中选择</n-button>
        </n-space>
      </n-form-item>
      <n-form-item label="配送费">
        <n-input-number v-model:value="form.delivery_fee" :min="0" />
      </n-form-item>
      <n-form-item label="起送金额">
        <n-input-number v-model:value="form.min_order_amount" :min="0" />
      </n-form-item>
      <n-form-item label="配送范围(km)">
        <n-input-number v-model:value="form.delivery_range_km" :min="0" />
      </n-form-item>
      <n-form-item label="营业时间">
        <n-input v-model:value="form.business_hours" placeholder="如 08:00-22:00" />
      </n-form-item>
      <n-form-item label="排序">
        <n-input-number v-model:value="form.sort" :min="0" />
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

    <n-modal v-model:show="mapVisible" title="选择经纬度" style="width: 80%; max-width: 900px" preset="card">
      <iframe
        src="https://lbs.amap.com/console/show/picker"
        style="width: 100%; height: 500px; border: 0"
        allow="geolocation"
      />
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { createCommunity, updateCommunity, getCommunityList } from '@/api/user'
import type { Community } from '@/api/types'

/**
 * 社区新增/编辑页面
 */
const route = useRoute()
const router = useRouter()
const isEdit = ref(false)
const mapVisible = ref(false)
const form = ref<Partial<Community>>({
  name: '',
  address: '',
  contact_phone: '',
  manager_name: '',
  manager_phone: '',
  pickup_address: '',
  longitude: 0,
  latitude: 0,
  delivery_fee: 0,
  min_order_amount: 0,
  delivery_range_km: 0,
  business_hours: '',
  sort: 0,
  status: 1,
})
const statusOptions = [
  { label: '营业', value: 1 },
  { label: '停业', value: 0 },
]

const longitudeText = computed({
  get: () => (form.value.longitude ? String(form.value.longitude) : ''),
  set: (v: string) => {
    form.value.longitude = parseFloat(v) || 0
  },
})

const latitudeText = computed({
  get: () => (form.value.latitude ? String(form.value.latitude) : ''),
  set: (v: string) => {
    form.value.latitude = parseFloat(v) || 0
  },
})

function openMapPicker() {
  mapVisible.value = true
}

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
