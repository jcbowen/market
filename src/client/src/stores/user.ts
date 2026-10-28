/**
 * 用户状态管理
 * 维护 token、userInfo、communityInfo、addressInfo，提供登录登出与社区切换能力
 */

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getUserInfo, updateCommunity, getCommunityById } from '@/api/user'
import { logout as logoutApi } from '@/api/auth'
import { registerUnauthorizedHandler } from '@/api/request'
import { isH5 } from '@/utils/platform'
import type { UserInfo, Community, Address } from '@/api/types'

const TOKEN_KEY = 'access_token'
const REFRESH_TOKEN_KEY = 'refresh_token'
const USER_KEY = 'user_info'
// v2：旧版本曾把只含 id/name/address/longitude/latitude 的残缺社区对象写进缓存，
// 而 Community 全字段均为 proto omitempty，无法靠字段存在性判断完整性，
// 故直接换 key 让历史缓存失效，启动时重新拉取完整对象
const COMMUNITY_KEY = 'community_info_v2'
const ADDRESS_KEY = 'address_info'

export const useUserStore = defineStore('user', () => {
  /** 访问令牌 */
  const token = ref<string>(uni.getStorageSync(TOKEN_KEY) || '')
  /** 刷新令牌，401 时用于静默续期 */
  const refreshToken = ref<string>(uni.getStorageSync(REFRESH_TOKEN_KEY) || '')
  /** 用户信息 */
  const userInfo = ref<UserInfo | null>(
    uni.getStorageSync(USER_KEY) ? (uni.getStorageSync(USER_KEY) as UserInfo) : null,
  )
  /** 当前社区 */
  const communityInfo = ref<Community | null>(
    uni.getStorageSync(COMMUNITY_KEY)
      ? (uni.getStorageSync(COMMUNITY_KEY) as Community)
      : null,
  )
  /** 当前选中地址 */
  const addressInfo = ref<Address | null>(
    uni.getStorageSync(ADDRESS_KEY)
      ? (uni.getStorageSync(ADDRESS_KEY) as Address)
      : null,
  )

  /** 是否已登录 */
  const isLogin = computed(() => !!token.value)

  /**
   * 设置 token 并持久化
   * @param value token 字符串
   */
  function setToken(value: string) {
    token.value = value
    uni.setStorageSync(TOKEN_KEY, value)
  }

  /**
   * 设置刷新令牌并持久化
   * @param value 刷新令牌字符串，空值时清除本地缓存
   */
  function setRefreshToken(value: string) {
    refreshToken.value = value
    if (value) {
      uni.setStorageSync(REFRESH_TOKEN_KEY, value)
    } else {
      uni.removeStorageSync(REFRESH_TOKEN_KEY)
    }
  }

  /**
   * 设置用户信息并持久化
   * @param info 用户信息
   */
  function setUserInfo(info: UserInfo | null) {
    userInfo.value = info
    if (info) {
      uni.setStorageSync(USER_KEY, info)
    } else {
      uni.removeStorageSync(USER_KEY)
    }
  }

  /**
   * 设置当前社区并持久化
   * @param community 社区信息
   */
  function setCommunity(community: Community | null) {
    communityInfo.value = community
    if (community) {
      uni.setStorageSync(COMMUNITY_KEY, community)
    } else {
      uni.removeStorageSync(COMMUNITY_KEY)
    }
  }

  /**
   * 设置当前地址并持久化
   * @param address 地址信息
   */
  function setAddress(address: Address | null) {
    addressInfo.value = address
    if (address) {
      uni.setStorageSync(ADDRESS_KEY, address)
    } else {
      uni.removeStorageSync(ADDRESS_KEY)
    }
  }

  /**
   * 登录成功后统一写入 token 与用户信息
   * 不在此处写 communityInfo：登录响应只带 community_id/community_name，
   * 缺 delivery_fee/min_order_amount，写残缺对象会被持久化并导致结算页运费恒为 0，
   * 社区信息由调用方随后 await syncBoundCommunity() 拉取完整对象写入
   * @param accessToken 访问令牌
   * @param info 用户信息
   * @param refreshTokenValue 刷新令牌，用于 401 静默续期；缺省时保留原值
   */
  function login(accessToken: string, info: UserInfo, refreshTokenValue?: string) {
    setToken(accessToken)
    if (refreshTokenValue) {
      setRefreshToken(refreshTokenValue)
    }
    setUserInfo(info)
  }

  /**
   * 按用户绑定的社区 ID 拉取完整社区信息并写入 store
   * 拉取失败时保留原有 communityInfo 不覆盖（安全降级）
   * @returns 是否成功写入完整社区对象
   */
  async function syncBoundCommunity(): Promise<boolean> {
    const id = userInfo.value?.community_id
    if (!id) return false
    const community = await getCommunityById(id)
    if (!community) return false
    setCommunity(community)
    return true
  }

  /**
   * 退出登录，清空本地状态与缓存
   * H5 还需请后端清除 refresh_token 的 httpOnly cookie（JS 删不掉，只能后端下发过期值）；
   * 失败不阻塞本地登出，cookie 最长 7 天后自然过期
   */
  function logout() {
    token.value = ''
    refreshToken.value = ''
    userInfo.value = null
    communityInfo.value = null
    addressInfo.value = null
    uni.removeStorageSync(TOKEN_KEY)
    uni.removeStorageSync(REFRESH_TOKEN_KEY)
    uni.removeStorageSync(USER_KEY)
    uni.removeStorageSync(COMMUNITY_KEY)
    uni.removeStorageSync(ADDRESS_KEY)
    if (isH5()) {
      void logoutApi().catch(() => {})
    }
  }

  /**
   * 从服务端拉取最新用户信息
   * 失败时由请求拦截器统一提示，并保留本地缓存（不抛出）：
   * 页面多在 onShow 里直接调用，未捕获的 rejection 会成为全局未处理错误
   */
  async function fetchUserInfo() {
    if (!isLogin.value) return
    try {
      const res = await getUserInfo()
      if (res.data) {
        setUserInfo(res.data)
      }
    } catch {
      // 错误提示由请求拦截器统一弹出，本地用户信息维持原值
    }
  }

  /**
   * 切换当前社区
   * 已登录时同步到服务端，未登录时仅更新本地。
   * 同时刷新本地 userInfo 的社区字段：服务端绑定已变更，若本地 userInfo 仍是旧值，
   * 下次启动 syncBoundCommunity 会按旧 ID 把用户切回原社区
   * @param community 社区信息
   */
  async function switchCommunity(community: Community) {
    if (isLogin.value) {
      await updateCommunity(community.id)
      if (userInfo.value) {
        setUserInfo({
          ...userInfo.value,
          community_id: community.id,
          community_name: community.name,
        })
      }
    }
    setCommunity(community)
  }

  return {
    token,
    refreshToken,
    userInfo,
    communityInfo,
    addressInfo,
    isLogin,
    setToken,
    setRefreshToken,
    setUserInfo,
    setCommunity,
    setAddress,
    login,
    logout,
    fetchUserInfo,
    syncBoundCommunity,
    switchCommunity,
  }
})

// 向请求层注册 401 登出实现（见 api/request.ts registerUnauthorizedHandler）：
// 请求层不反向依赖本模块，避免 request → stores/user → api/auth → request 循环依赖。
// 本模块被 App.vue 静态引用，注册必先于任何业务请求发生；
// 回调在事件触发时才调用 useUserStore()，Pinia 实例彼时必然已就绪
registerUnauthorizedHandler(() => useUserStore().logout())
