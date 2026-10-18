<template>
  <n-card title="SKU 选择器方案对比（临时演示页）">
    <template #header-extra>
      <n-text depth="3">敲定保留方案后此页将删除</n-text>
    </template>
    <n-grid :cols="2" :x-gap="16" item-responsive responsive="screen">
      <n-gi span="0:2">
        <n-card title="方案A：单框下拉（SkuPicker）" size="small">
          <n-space vertical size="medium">
            <n-text depth="3">输入即搜；下拉选项两行详情；回显单行省略，不撑高明细行</n-text>
            <n-table :bordered="false" :single-line="true" size="small">
              <thead>
                <tr>
                  <th style="width: 400px">商品 / SKU</th>
                  <th style="width: 120px">数量</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>
                    <n-space vertical size="small">
                      <SkuPicker v-model="aSkuId" @change="(sku) => (aSku = sku)" />
                      <n-text v-if="aSku" depth="3" style="font-size: 12px">
                        当前库存 {{ aSku.stock }} · 成本 ¥{{ Number(aSku.cost_price || 0).toFixed(2) }}
                      </n-text>
                    </n-space>
                  </td>
                  <td><n-input-number v-model:value="aQty" :min="1" :precision="0" style="width: 100px" /></td>
                </tr>
              </tbody>
            </n-table>
            <n-text depth="3" style="font-size: 12px">
              已选：{{ aSku ? `${aSku.product_name || aSku.sku_code}${aSku.spec_name ? ' · ' + aSku.spec_name : ''}（${aSku.sku_code}｜库存 ${aSku.stock}）` : '未选择' }}
            </n-text>
          </n-space>
        </n-card>
      </n-gi>
      <n-gi span="0:2">
        <n-card title="方案B：弹窗选货（SkuPickerModal）" size="small">
          <n-space vertical size="medium">
            <n-text depth="3">按钮触发弹窗；弹窗内多列表格展示完整信息；回显同样单行省略</n-text>
            <n-table :bordered="false" :single-line="true" size="small">
              <thead>
                <tr>
                  <th style="width: 400px">商品 / SKU</th>
                  <th style="width: 120px">数量</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>
                    <n-space vertical size="small">
                      <SkuPickerModal v-model="bSkuId" @change="(sku) => (bSku = sku)" />
                      <n-text v-if="bSku" depth="3" style="font-size: 12px">
                        当前库存 {{ bSku.stock }} · 成本 ¥{{ Number(bSku.cost_price || 0).toFixed(2) }}
                      </n-text>
                    </n-space>
                  </td>
                  <td><n-input-number v-model:value="bQty" :min="1" :precision="0" style="width: 100px" /></td>
                </tr>
              </tbody>
            </n-table>
            <n-text depth="3" style="font-size: 12px">
              已选：{{ bSku ? `${bSku.product_name || bSku.sku_code}${bSku.spec_name ? ' · ' + bSku.spec_name : ''}（${bSku.sku_code}｜库存 ${bSku.stock}）` : '未选择' }}
            </n-text>
          </n-space>
        </n-card>
      </n-gi>
    </n-grid>
  </n-card>
</template>

<script setup lang="ts">
/**
 * SKU 选择器方案对比演示页（临时）：
 * 左/右卡片分别还原采购单明细行的表格内场景，验证方案A（SkuPicker 单框下拉）
 * 与方案B（SkuPickerModal 弹窗选货）的回显不撑高行高、下拉/弹窗内容完整展示。
 * 用户敲定保留方案后本页与临时路由一并删除。
 */
import { ref } from 'vue'
import SkuPicker from './components/SkuPicker.vue'
import SkuPickerModal from './components/SkuPickerModal.vue'
import type { Sku } from '@/api/types'

defineOptions({ name: 'StockPickerDemo' })

const aSkuId = ref<number>()
const aSku = ref<Sku | null>(null)
const aQty = ref(1)
const bSkuId = ref<number>()
const bSku = ref<Sku | null>(null)
const bQty = ref(1)
</script>
