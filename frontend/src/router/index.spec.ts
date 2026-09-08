import { describe, expect, it } from 'vitest'

import router from './index'

describe('router', () => {
  it('does not register the retired face quality page', () => {
    const routes = router.getRoutes()

    expect(routes.some(route => route.name === 'FaceQualityReview')).toBe(false)
    expect(routes.some(route => route.path === '/face-quality-review')).toBe(false)
    expect(routes.some(route => String(route.meta.title ?? '').includes('人脸质检'))).toBe(false)
  })
})
