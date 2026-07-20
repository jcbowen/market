<template>
  <div class="marketing-page">
    <n-card>
      <n-tabs v-model:value="activeTab" type="line">
        <n-tab-pane name="coupon" tab="优惠券">
          <coupon-manager />
        </n-tab-pane>
        <n-tab-pane name="seckill" tab="秒杀活动">
          <seckill-manager />
        </n-tab-pane>
        <n-tab-pane name="fullReduction" tab="满减活动">
          <full-reduction-manager />
        </n-tab-pane>
        <n-tab-pane name="banner" tab="Banner管理">
          <banner-manager />
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import CouponManager from './components/CouponManager.vue'
import SeckillManager from './components/SeckillManager.vue'
import FullReductionManager from './components/FullReductionManager.vue'
import BannerManager from './components/BannerManager.vue'

/**
 * 当前选中的营销标签页
 */
const activeTab = ref('coupon')
</script>

<style lang="scss" scoped>
.marketing-page {
  /* 页面样式 */
}
</style>
