// Load the signed-in user once per request (SSR) and reuse the serialized state on the client.
export default defineNuxtPlugin(async () => {
  await useAuth().ensureLoaded()
})
