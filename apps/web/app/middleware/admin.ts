// UI-level guard for /admin pages. It only decides what to render; every admin API call is still
// authorised by the Go API (API-DD §7: never rely on Nuxt route guards alone).
export default defineNuxtRouteMiddleware(async (to) => {
  const { user, ensureLoaded } = useAuth()
  await ensureLoaded()
  if (!user.value) {
    return navigateTo({ path: '/auth/login', query: { redirect: to.fullPath } })
  }
  if (user.value.role !== 'ADMIN') {
    throw createError({ statusCode: 403, statusMessage: '没有权限访问管理后台', fatal: true })
  }
})
