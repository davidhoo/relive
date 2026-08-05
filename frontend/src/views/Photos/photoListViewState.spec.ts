import { describe, it, expect, beforeEach } from 'vitest'
import {
  savePhotoListSnapshot,
  consumePhotoListSnapshot,
  clearPhotoListSnapshot,
  hasPhotoListSnapshot,
  filtersEqual,
  type PhotoListSnapshot,
  type PhotoListFilterFingerprint,
} from './photoListViewState'

const baseFilter: PhotoListFilterFingerprint = {
  search: '',
  category: '',
  tag: '',
  analyzed: '',
  has_thumbnail: '',
  has_gps: '',
  status: '',
}

const makeSnapshot = (overrides: Partial<PhotoListSnapshot> = {}): PhotoListSnapshot => ({
  photos: [],
  nextCursor: 'cursor-1',
  hasMore: true,
  finished: false,
  filter: { ...baseFilter },
  total: 100,
  scrollTop: 500,
  firstVisiblePhotoId: 42,
  ...overrides,
})

describe('photoListViewState', () => {
  beforeEach(() => {
    clearPhotoListSnapshot()
  })

  describe('save / consume / clear', () => {
    it('保存后 hasPhotoListSnapshot=true', () => {
      savePhotoListSnapshot(makeSnapshot())
      expect(hasPhotoListSnapshot()).toBe(true)
    })

    it('consume 取出快照并清除', () => {
      const snap = makeSnapshot({ scrollTop: 1234, nextCursor: 'abc' })
      savePhotoListSnapshot(snap)
      const got = consumePhotoListSnapshot(baseFilter)
      expect(got).not.toBeNull()
      expect(got!.scrollTop).toBe(1234)
      expect(got!.nextCursor).toBe('abc')
      // 取出后快照已清除
      expect(hasPhotoListSnapshot()).toBe(false)
    })

    it('无快照时 consume 返回 null', () => {
      expect(consumePhotoListSnapshot(baseFilter)).toBeNull()
    })

    it('clear 清除快照', () => {
      savePhotoListSnapshot(makeSnapshot())
      clearPhotoListSnapshot()
      expect(hasPhotoListSnapshot()).toBe(false)
    })

    it('consume 后再次 consume 返回 null', () => {
      savePhotoListSnapshot(makeSnapshot())
      expect(consumePhotoListSnapshot(baseFilter)).not.toBeNull()
      expect(consumePhotoListSnapshot(baseFilter)).toBeNull()
    })
  })

  describe('筛选条件匹配', () => {
    it('筛选完全匹配才恢复', () => {
      savePhotoListSnapshot(makeSnapshot({ filter: { ...baseFilter, search: 'cat' } }))
      const got = consumePhotoListSnapshot({ ...baseFilter, search: 'cat' })
      expect(got).not.toBeNull()
    })

    it('search 不同 → 不恢复（返回 null）', () => {
      savePhotoListSnapshot(makeSnapshot({ filter: { ...baseFilter, search: 'cat' } }))
      const got = consumePhotoListSnapshot({ ...baseFilter, search: 'dog' })
      expect(got).toBeNull()
    })

    it('category 不同 → 不恢复', () => {
      savePhotoListSnapshot(makeSnapshot({ filter: { ...baseFilter, category: 'landscape' } }))
      const got = consumePhotoListSnapshot({ ...baseFilter, category: 'portrait' })
      expect(got).toBeNull()
    })

    it('status 不同 → 不恢复（回收站切换）', () => {
      savePhotoListSnapshot(makeSnapshot({ filter: { ...baseFilter, status: 'excluded' } }))
      const got = consumePhotoListSnapshot({ ...baseFilter, status: '' })
      expect(got).toBeNull()
    })

    it('筛选不匹配时快照被清除（避免下次误命中）', () => {
      savePhotoListSnapshot(makeSnapshot({ filter: { ...baseFilter, tag: 'beach' } }))
      consumePhotoListSnapshot({ ...baseFilter, tag: 'mountain' })
      expect(hasPhotoListSnapshot()).toBe(false)
    })

    it('任一筛选字段不同均不恢复', () => {
      const fields: (keyof PhotoListFilterFingerprint)[] = ['search', 'category', 'tag', 'analyzed', 'has_thumbnail', 'has_gps', 'status']
      for (const f of fields) {
        savePhotoListSnapshot(makeSnapshot({ filter: { ...baseFilter, [f]: 'x' } }))
        const got = consumePhotoListSnapshot({ ...baseFilter })
        expect(got).toBeNull()
        clearPhotoListSnapshot()
      }
    })
  })

  describe('filtersEqual', () => {
    it('完全相同返回 true', () => {
      expect(filtersEqual(baseFilter, { ...baseFilter })).toBe(true)
    })
    it('任一字段不同返回 false', () => {
      expect(filtersEqual(baseFilter, { ...baseFilter, analyzed: 'true' })).toBe(false)
    })
  })

  describe('快照内容保留', () => {
    it('保存 photos 数组与游标、hasMore、finished、total、scrollTop、firstVisiblePhotoId', () => {
      const photos = [
        { id: 1, file_path: '/a', file_hash: 'h', ai_analyzed: false, created_at: '', updated_at: '' },
        { id: 2, file_path: '/b', file_hash: 'h', ai_analyzed: false, created_at: '', updated_at: '' },
      ] as any
      savePhotoListSnapshot(makeSnapshot({
        photos,
        nextCursor: 'nc',
        hasMore: false,
        finished: true,
        total: 2,
        scrollTop: 999,
        firstVisiblePhotoId: 2,
      }))
      const got = consumePhotoListSnapshot(baseFilter)
      expect(got!.photos.length).toBe(2)
      expect(got!.nextCursor).toBe('nc')
      expect(got!.hasMore).toBe(false)
      expect(got!.finished).toBe(true)
      expect(got!.total).toBe(2)
      expect(got!.scrollTop).toBe(999)
      expect(got!.firstVisiblePhotoId).toBe(2)
    })

    it('快照保存 filter 副本，修改原 filter 不影响已保存快照', () => {
      const filter = { ...baseFilter, search: 'cat' }
      savePhotoListSnapshot(makeSnapshot({ filter }))
      filter.search = 'dog'
      const got = consumePhotoListSnapshot({ ...baseFilter, search: 'cat' })
      expect(got).not.toBeNull()
    })
  })
})
