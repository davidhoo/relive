import test from 'node:test'
import assert from 'node:assert/strict'

import {
  getMergeSuggestionTaskStatusMeta,
  getMergeSuggestionVisibility,
  sortMergeSuggestionCandidates,
} from '../src/views/People/peopleHelpers.ts'

test('getMergeSuggestionVisibility 在没有待审核建议时隐藏区块', () => {
  assert.equal(getMergeSuggestionVisibility(0, false), false)
  assert.equal(getMergeSuggestionVisibility(0, true), true)
  assert.equal(getMergeSuggestionVisibility(2, false), true)
})

test('sortMergeSuggestionCandidates 按相似度从高到低排序', () => {
  const items = sortMergeSuggestionCandidates([
    { candidate_person_id: 3, similarity_score: 0.81 },
    { candidate_person_id: 1, similarity_score: 0.93 },
    { candidate_person_id: 2, similarity_score: 0.93 },
  ])

  assert.deepEqual(items.map(item => item.candidate_person_id), [1, 2, 3])
})

test('getMergeSuggestionTaskStatusMeta 映射 paused 与 running 状态', () => {
  assert.deepEqual(getMergeSuggestionTaskStatusMeta('running'), { label: '巡检中', type: 'warning' })
  assert.deepEqual(getMergeSuggestionTaskStatusMeta('stopped'), { label: '已暂停', type: 'info' })
  assert.deepEqual(getMergeSuggestionTaskStatusMeta('idle'), { label: '等待巡检', type: 'info' })
})
