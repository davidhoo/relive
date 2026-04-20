import test from 'node:test'
import assert from 'node:assert/strict'

import {
  getPeopleTaskStatusMeta,
  getPersonAvatarFallback,
  getPersonCategoryLabel,
  sortPeopleForDisplay,
} from '../src/views/People/peopleHelpers.ts'

test('getPersonCategoryLabel 映射固定人物类别文案', () => {
  assert.equal(getPersonCategoryLabel('family'), '家人')
  assert.equal(getPersonCategoryLabel('friend'), '亲友')
  assert.equal(getPersonCategoryLabel('acquaintance'), '熟人')
  assert.equal(getPersonCategoryLabel('stranger'), '路人')
  assert.equal(getPersonCategoryLabel('unknown'), '未知')
})

test('sortPeopleForDisplay 按 家人 > 亲友 > 熟人 > 路人 排序', () => {
  const people = sortPeopleForDisplay([
    { id: 4, category: 'stranger', photo_count: 9, face_count: 9 },
    { id: 2, category: 'friend', photo_count: 2, face_count: 3 },
    { id: 3, category: 'acquaintance', photo_count: 4, face_count: 4 },
    { id: 1, category: 'family', photo_count: 1, face_count: 2 },
  ])

  assert.deepEqual(people.map(person => person.id), [1, 2, 3, 4])
})

test('getPeopleTaskStatusMeta 返回任务状态胶囊文案', () => {
  assert.deepEqual(getPeopleTaskStatusMeta('running'), { label: '运行中', type: 'warning' })
  assert.deepEqual(getPeopleTaskStatusMeta('stopping'), { label: '停止中', type: 'warning' })
  assert.deepEqual(getPeopleTaskStatusMeta('failed'), { label: '失败', type: 'danger' })
  assert.deepEqual(getPeopleTaskStatusMeta(undefined), { label: '未运行', type: 'info' })
})

test('getPersonAvatarFallback 优先使用姓名首字，其次使用类别占位字', () => {
  assert.equal(getPersonAvatarFallback({ name: 'Alice', category: 'family' }), 'A')
  assert.equal(getPersonAvatarFallback({ name: ' 小王 ', category: 'friend' }), '小')
  assert.equal(getPersonAvatarFallback({ category: 'family' }), '家')
  assert.equal(getPersonAvatarFallback({ category: 'friend' }), '友')
  assert.equal(getPersonAvatarFallback({ category: 'acquaintance' }), '熟')
  assert.equal(getPersonAvatarFallback({ category: 'stranger' }), '路')
})
