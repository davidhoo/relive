import type { Router } from 'vue-router'

let appRouter: Router | null = null

export const registerRouter = (router: Router) => {
  appRouter = router
}

export const navigateTo = (path: string) => {
  if (!appRouter) {
    return Promise.resolve()
  }
  return appRouter.push(path)
}
