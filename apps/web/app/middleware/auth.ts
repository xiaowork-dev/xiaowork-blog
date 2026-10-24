// UI-level guard for /account pages: signed-out readers go to the login page and come back afterwards.
// Every /me API call is still authorised by the Go API (API-DD §7).
export default defineNuxtRouteMiddleware(async (to) => {
  const { user, ensureLoaded } = useAuth()
  await ensureLoaded()
  if (!user.value) {
    return navigateTo({ path: '/auth/login', query: { redirect: to.fullPath } })
  }
})
