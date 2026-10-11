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

    <n-modal v-model:show="mapVisible" title="选择经纬度（点击地图选点后自动回填）" style="width: 80%; max-width: 900px" preset="card">
      <iframe
        src="https://lbs.amap.com/tools/picker"
        style="width: 100%; height: 600px; border: 0"
        allow="geolocation"
      />
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
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

// 高德坐标拾取器 iframe 的合法来源，postMessage 消息必须来自该域名才被处理
const AMAP_PICKER_ORIGIN = 'https://lbs.amap.com'

/**
 * 处理高德坐标拾取器回传的选点消息
 * 高德官方拾取器（lbs.amap.com/tools/picker）通过 postMessage 向父窗口回传结果，
 * data 为 JSON 字符串或对象，坐标字段为 lnglat（[经度, 纬度]）或 lnglat 字符串
 * 仅处理 event.origin 为高德官方域名的消息，防止其他来源伪造选点数据
 * @param event message 事件
 */
function onMapMessage(event: MessageEvent) {
  if (event.origin !== AMAP_PICKER_ORIGIN || !mapVisible.value || !event.data) {
    return
  }

  let payload: { lnglat?: string | [string | number, string | number] } | null = null
  try {
    payload = typeof event.data === 'string' ? JSON.parse(event.data) : event.data
  } catch {
    // 非坐标类消息（如高德页面自身的日志）直接忽略
    return
  }

  const lnglat = payload?.lnglat
  if (!lnglat) {
    return
  }

  const [lng, lat] = Array.isArray(lnglat) ? lnglat : String(lnglat).split(',')
  const lngValue = parseFloat(String(lng))
  const latValue = parseFloat(String(lat))
  if (Number.isNaN(lngValue) || Number.isNaN(latValue)) {
    return
  }

  form.value.longitude = lngValue
  form.value.latitude = latValue
  mapVisible.value = false
}

onMounted(() => {
  window.addEventListener('message', onMapMessage)
})

onUnmounted(() => {
  window.removeEventListener('message', onMapMessage)
})

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
