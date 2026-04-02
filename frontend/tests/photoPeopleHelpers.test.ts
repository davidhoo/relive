import test from 'node:test'
import assert from 'node:assert/strict'

import {
  buildFaceThumbnailUrl,
  getPhotoPeopleSummaryLabel,
  groupPhotoPeopleByCategory,
} from '../src/views/Photos/photoPeopleHelpers.js'

test('groupPhotoPeopleByCategory 按人物类别聚合照片人物响应', () => {
  const groups = groupPhotoPeopleByCategory({
    photo_id: 12,
    face_process_status: 'ready',
    face_count: 3,
    top_person_category: 'family',
    people: [
      {
        id: 1,
        category: 'family',
        face_count: 2,
        photo_count: 1,
        created_at: '',
        updated_at: '',
        faces: [{ id: 11, photo_id: 12 }, { id: 12, photo_id: 12 }],
      },
      {
        id: 2,
        category: 'stranger',
        face_count: 1,
        photo_count: 1,
        created_at: '',
        updated_at: '',
        faces: [{ id: 21, photo_id: 12 }],
      },
    ],
  })

  assert.equal(groups.length, 2)
  assert.equal(groups[0].category, 'family')
  assert.equal(groups[0].label, '家人')
  assert.equal(groups[0].face_count, 2)
  assert.equal(groups[1].category, 'stranger')
  assert.equal(groups[1].face_count, 1)
})

test('getPhotoPeopleSummaryLabel 返回照片人物状态文案', () => {
  assert.equal(getPhotoPeopleSummaryLabel({ face_process_status: 'no_face', face_count: 0 }), '未检测到人脸')
  assert.equal(getPhotoPeopleSummaryLabel({ face_process_status: 'ready', face_count: 1, top_person_category: 'stranger' }), '路人')
  assert.equal(getPhotoPeopleSummaryLabel({ face_process_status: 'ready', face_count: 2, top_person_category: 'family' }), '家人')
})

test('buildFaceThumbnailUrl 生成带版本参数的人脸缩略图地址', () => {
  assert.equal(
    buildFaceThumbnailUrl(8, 'http://localhost:8080/api/v1', 'abc123'),
    'http://localhost:8080/api/v1/faces/8/thumbnail?v=abc123',
  )
  assert.equal(
    buildFaceThumbnailUrl(8, 'http://localhost:8080/api/v1'),
    'http://localhost:8080/api/v1/faces/8/thumbnail',
  )
})
