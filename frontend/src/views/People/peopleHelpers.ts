import type { Person, PersonCategory } from '../../types/people.js'

export interface TaskStatusMeta {
  label: string
  type: 'success' | 'warning' | 'info' | 'danger'
}

const CATEGORY_LABELS: Record<PersonCategory, string> = {
  family: '家人',
  friend: '亲友',
  acquaintance: '熟人',
  stranger: '路人',
}

const CATEGORY_FALLBACKS: Record<PersonCategory, string> = {
  family: '家',
  friend: '友',
  acquaintance: '熟',
  stranger: '路',
}

const CATEGORY_ORDER: Record<PersonCategory, number> = {
  family: 0,
  friend: 1,
  acquaintance: 2,
  stranger: 3,
}

export function getPersonCategoryLabel(category?: string): string {
  if (!category) return '未知'
  return CATEGORY_LABELS[category as PersonCategory] || '未知'
}

export function sortPeopleForDisplay<T extends Pick<Person, 'category' | 'photo_count' | 'face_count' | 'id'>>(people: T[]): T[] {
  return [...people].sort((left, right) => {
    const leftRank = CATEGORY_ORDER[left.category]
    const rightRank = CATEGORY_ORDER[right.category]
    if (leftRank !== rightRank) return leftRank - rightRank
    if (left.photo_count !== right.photo_count) return right.photo_count - left.photo_count
    if (left.face_count !== right.face_count) return right.face_count - left.face_count
    return left.id - right.id
  })
}

export function getPeopleTaskStatusMeta(status?: string): TaskStatusMeta {
  switch (status) {
    case 'running':
      return { label: '运行中', type: 'warning' }
    case 'stopping':
      return { label: '停止中', type: 'warning' }
    case 'completed':
      return { label: '已完成', type: 'success' }
    case 'failed':
      return { label: '失败', type: 'danger' }
    default:
      return { label: '未运行', type: 'info' }
  }
}

export function getPersonAvatarFallback(person: Pick<Person, 'name' | 'category'>): string {
  const normalizedName = person.name?.trim()
  if (normalizedName) {
    return normalizedName.charAt(0).toUpperCase()
  }
  return CATEGORY_FALLBACKS[person.category] || '人'
}
