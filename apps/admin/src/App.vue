<script setup lang="ts">
import { computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ShieldCheck,
  LogOut,
  ArrowUpRight,
  UserRound,
  LayoutDashboard,
  Users,
  Map,
  ScrollText,
  Activity,
  DatabaseBackup,
} from '@lucide/vue'
import { ElMessage } from 'element-plus'
import { identity, logout } from './api'
import { adminBasePath } from './config'

const route = useRoute()
const router = useRouter()
const titles: Record<string, string> = {
  overview: '平台概览',
  users: '用户管理',
  account: '账号与安全',
  trips: '旅行管理',
  trip: '旅行概况',
  audits: '审计中心',
  runtime: '运行状态',
  backups: '数据库备份',
}
const pageTitle = computed(() => titles[String(route.name)] || '管理后台')
watch(identity, (value) => {
  if (!value && route.name !== 'login' && route.name !== 'setup')
    void router.replace({ name: 'login' })
})
async function signOut() {
  try {
    await logout()
    await router.replace({ name: 'login' })
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '退出失败，请重试。')
  }
}
</script>

<template>
  <RouterView v-if="route.name === 'login' || route.name === 'setup'" />
  <div v-else-if="identity" class="admin-layout">
    <a :href="router.resolve(route.fullPath).href + '#main-content'" class="skip-link"
      >跳到主要内容</a
    >
    <aside class="sidebar">
      <a class="brand" :href="adminBasePath" aria-label="Tripfolio 管理后台首页">
        <ShieldCheck :size="28" aria-hidden="true" />
        <span>Tripfolio<small>管理后台</small></span>
      </a>
      <nav aria-label="后台导航">
        <span class="nav-label">管理空间</span>
        <RouterLink to="/overview" class="nav-item"
          ><LayoutDashboard :size="18" aria-hidden="true" />平台概览</RouterLink
        >
        <RouterLink to="/users" class="nav-item"
          ><Users :size="18" aria-hidden="true" />用户管理</RouterLink
        >
        <RouterLink
          to="/trips"
          class="nav-item"
          :class="{ 'router-link-active': route.name === 'trip' }"
          ><Map :size="18" aria-hidden="true" />旅行管理</RouterLink
        >
        <RouterLink to="/account" class="nav-item"
          ><UserRound :size="18" aria-hidden="true" />账号与安全</RouterLink
        >
        <RouterLink to="/audits" class="nav-item"
          ><ScrollText :size="18" aria-hidden="true" />审计中心</RouterLink
        >
        <RouterLink to="/runtime" class="nav-item"
          ><Activity :size="18" aria-hidden="true" />运行状态</RouterLink
        >
        <RouterLink to="/backups" class="nav-item"
          ><DatabaseBackup :size="18" aria-hidden="true" />数据库备份</RouterLink
        >
      </nav>
      <div class="sidebar-bottom">
        <a href="/" target="_blank" rel="noopener"
          >访问用户端<ArrowUpRight :size="16" aria-hidden="true"
        /></a>
        <p>Tripfolio · Administration</p>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <span>管理后台 <span class="breadcrumb-divider">/</span> {{ pageTitle }}</span>
        <div class="account-actions">
          <span class="account-name">{{ identity.nickname }}</span>
          <span class="role-badge">超级管理员</span>
          <el-button text @click="signOut"
            ><LogOut :size="16" aria-hidden="true" /><span>退出</span></el-button
          >
        </div>
      </header>
      <main id="main-content" tabindex="-1"><RouterView /></main>
    </div>
  </div>
</template>
