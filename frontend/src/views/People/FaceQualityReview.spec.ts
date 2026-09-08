import { describe, it, expect } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import FaceQualityReview from './FaceQualityReview.vue'

describe('FaceQualityReview.vue - 已下线页', () => {
  it('展示下线提示，不含质检任务控件', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'Home', component: { template: '<div />' } },
        { path: '/people', name: 'People', component: { template: '<div />' } },
        { path: '/photos', name: 'Photos', component: { template: '<div />' } },
        { path: '/face-quality-review', name: 'FaceQualityReview', component: FaceQualityReview },
      ],
    })
    await router.push('/face-quality-review')
    const wrapper = mount(FaceQualityReview, {
      global: {
        plugins: [router],
        stubs: {
          'el-result': {
            props: ['title', 'subTitle', 'icon'],
            template: '<div class="stub-result">{{ title }} {{ subTitle }}<slot name="extra" /></div>',
          },
          'el-button': { template: '<button><slot /></button>' },
        },
      },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('人脸质检已下线')
    expect(wrapper.text()).toContain('前往人物管理')
    expect(wrapper.text()).not.toContain('自动隔离')
    expect(wrapper.text()).not.toContain('按规则版本恢复')
  })
})
