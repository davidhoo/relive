import type { Person, PhotoPeopleResponse } from '../../types/people.js'
import { getPersonCategoryLabel } from '../People/peopleHelpers.js'

export interface PhotoPeopleCategoryGroup {
  category: Person['category']
  label: string
  face_count: number
  people: Person[]
}

const CATEGORY_ORDER: Record<Person['category'], number> = {
  family: 0,
  friend: 1,
  acquaintance: 2,
  stranger: 3,
}

export function groupPhotoPeopleByCategory(payload?: PhotoPeopleResponse | null): PhotoPeopleCategoryGroup[] {
  if (!payload?.people?.length) return []

  const groups = new Map<Person['category'], PhotoPeopleCategoryGroup>()
  for (const person of payload.people) {
    const category = person.category || 'stranger'
    const current = groups.get(category)
    if (current) {
      current.people.push(person)
      current.face_count += person.faces?.length || 0
      continue
    }
    groups.set(category, {
      category,
      label: getPersonCategoryLabel(category),
      face_count: person.faces?.length || 0,
      people: [person],
    })
  }

  return [...groups.values()].sort((left, right) => CATEGORY_ORDER[left.category] - CATEGORY_ORDER[right.category])
}

export function getPhotoPeopleSummaryLabel(payload?: Pick<PhotoPeopleResponse, 'face_process_status' | 'face_count' | 'top_person_category'> | null): string {
  if (!payload) return '未检测'
  if (payload.face_process_status === 'no_face' || payload.face_count === 0) return '未检测到人脸'
  if (payload.top_person_category) return getPersonCategoryLabel(payload.top_person_category)
  switch (payload.face_process_status) {
    case 'pending':
      return '待处理'
    case 'processing':
      return '识别中'
    case 'failed':
      return '识别失败'
    default:
      return '已检测到人物'
  }
}

/**
 * 照片详情页人物信息统计 tag 文案。与列表/管理页不同，这里只反映“识别状态/人数”，
 * 不展示 top_person_category（详情页条目已直接显示每个人脸的分类）。
 */
export function getPhotoPeopleCountTag(payload?: Pick<PhotoPeopleResponse, 'face_process_status' | 'face_count' | 'people'> | null): string {
  if (!payload) return '未检测'
  switch (payload.face_process_status) {
    case 'none':
      return '未检测'
    case 'pending':
      return '识别中'
    case 'processing':
      return '识别中'
    case 'failed':
      return '识别失败'
    case 'no_face':
      return '未检测到人脸'
    default:
      break
  }
  const peopleCount = payload.people?.length ?? 0
  if (peopleCount === 0) return '未检测到人脸'
  return `${peopleCount} 人`
}

/**
 * 照片详情页人脸级条目：把按人物分组的数据展开成“每张 face 一项”，
 * 便于在单张照片中直接检查/编辑每张人脸的归属。
 */
export interface PhotoFaceEntry {
  faceId: number
  person: Person
}

export function flattenPhotoPeopleFaces(payload?: PhotoPeopleResponse | null): PhotoFaceEntry[] {
  if (!payload?.people?.length) return []
  const entries: PhotoFaceEntry[] = []
  for (const person of payload.people) {
    const faces = person.faces || []
    if (faces.length === 0) {
      // 人物在本照片中没有人脸记录（理论上不应发生），用代表头像兜底。
      if (person.representative_face_id) {
        entries.push({ faceId: person.representative_face_id, person })
      }
      continue
    }
    for (const face of faces) {
      entries.push({ faceId: face.id, person })
    }
  }
  return entries
}

export function buildFaceThumbnailUrl(faceId: number, baseUrl: string, version?: string): string {
  const normalizedBase = baseUrl.replace(/\/$/, '')
  const query = version ? `?v=${encodeURIComponent(version)}` : ''
  return `${normalizedBase}/faces/${faceId}/thumbnail${query}`
}
