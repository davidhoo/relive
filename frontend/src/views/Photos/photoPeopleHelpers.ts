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

export function buildFaceThumbnailUrl(faceId: number, baseUrl: string, version?: string): string {
  const normalizedBase = baseUrl.replace(/\/$/, '')
  const query = version ? `?v=${encodeURIComponent(version)}` : ''
  return `${normalizedBase}/faces/${faceId}/thumbnail${query}`
}
