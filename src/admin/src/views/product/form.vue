<template>
  <n-card :title="isEdit ? '编辑商品' : '新增商品'">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="商品名称">
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item label="商品分类">
        <n-select v-model:value="form.category_id" :options="categoryOptions" />
      </n-form-item>
      <n-form-item label="商品单位">
        <n-input v-model:value="form.unit" />
      </n-form-item>
      <n-form-item label="封面图">
        <n-input v-model:value="form.cover_image" placeholder="图片 URL" />
      </n-form-item>
      <n-form-item label="是否多规格">
        <n-switch v-model:value="form.is_multi_spec" />
      </n-form-item>
      <n-form-item label="排序">
        <n-input-number v-model:value="form.sort" />
      </n-form-item>
      <n-form-item label="起售数量">
        <n-input-number v-model:value="form.min_buy" />
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
import { getProductDetail, createProduct, updateProduct, getCategoryList } from '@/api/product'
import type { Product, Category } from '@/api/types'

/**
 * 商品新增/编辑页面
 * 用于创建或修改商品基础信息，SKU 管理可后续扩展
 */
const route = useRoute()
const router = useRouter()
const isEdit = ref(false)
const categoryOptions = ref<Array<{ label: string; value: number }>>([])
const form = ref<Partial<Product>>({
  name: '',
  category_id: undefined,
  unit: '',
  cover_image: '',
  is_multi_spec: false,
  sort: 0,
  min_buy: 1,
})

async function loadCategories() {
  const res = await getCategoryList()
  if (res.data.code === 0) {
    categoryOptions.value = (res.data.data || []).map((item: Category) => ({ label: item.name, value: item.id }))
  }
}

async function loadDetail() {
  const id = Number(route.params.id)
  if (!id) return
  isEdit.value = true
  const res = await getProductDetail(id)
  if (res.data.code === 0) {
    form.value = { ...res.data.data }
  }
}

async function submit() {
  const data = { ...form.value } as Product
  const res = isEdit.value ? await updateProduct(data.id, data) : await createProduct(data)
  if (res.data.code === 0) {
    router.back()
  }
}

onMounted(() => {
  loadCategories()
  loadDetail()
})
</script>
