/**
 * 用户状态管理
 * 维护 token、userInfo、communityInfo、addressInfo，提供登录登出与社区切换能力
 */

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getUserInfo, updateCommunity } from '@/api/user'
import type { UserInfo, Community, Address } from '@/api/types'

const TOKEN_KEY = 'access_token'
const USER_KEY = 'user_info'
const COMMUNITY_KEY = 'community_info'
const ADDRESS_KEY = 'address_info'

export const useUserStore = defineStore('user', () => {
  /** 访问令牌 */
  const token = ref<string>(uni.getStorageSync(TOKEN_KEY) || '')
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
   * @param accessToken 访问令牌
   * @param info 用户信息
   */
  function login(accessToken: string, info: UserInfo) {
    setToken(accessToken)
    setUserInfo(info)
    if (info.community_id && info.community_name) {
      setCommunity({
        id: info.community_id,
        name: info.community_name,
        address: '',
        longitude: 0,
        latitude: 0,
      })
    }
  }

  /**
   * 退出登录，清空本地状态与缓存
   */
  function logout() {
    token.value = ''
    userInfo.value = null
    communityInfo.value = null
    addressInfo.value = null
    uni.removeStorageSync(TOKEN_KEY)
    uni.removeStorageSync(USER_KEY)
    uni.removeStorageSync(COMMUNITY_KEY)
    uni.removeStorageSync(ADDRESS_KEY)
  }

  /**
   * 从服务端拉取最新用户信息
   */
  async function fetchUserInfo() {
    if (!isLogin.value) return
    const res = await getUserInfo()
    if (res.data) {
      setUserInfo(res.data)
    }
  }

  /**
   * 切换当前社区
   * 已登录时同步到服务端，未登录时仅更新本地
   * @param community 社区信息
   */
  async function switchCommunity(community: Community) {
    if (isLogin.value) {
      await updateCommunity(community.id)
    }
    setCommunity(community)
  }

  return {
    token,
    userInfo,
    communityInfo,
    addressInfo,
    isLogin,
    setToken,
    setUserInfo,
    setCommunity,
    setAddress,
    login,
    logout,
    fetchUserInfo,
    switchCommunity,
  }
})
