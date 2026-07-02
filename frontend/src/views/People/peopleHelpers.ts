import type { PeopleTask, Person, PersonCategory, PersonMergeSuggestionItem, PersonMergeSuggestionTask } from '../../types/people.js'

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

export function getPeopleTaskStatusMeta(task?: Pick<PeopleTask, 'status' | 'current_phase'> | string | null): TaskStatusMeta {
  const status = typeof task === 'string' ? task : task?.status
  const phase = typeof task === 'string' ? undefined : task?.current_phase

  if (status === 'stopping') {
    return { label: '停止中', type: 'warning' }
  }
  if (status === 'failed') {
    return { label: '失败', type: 'danger' }
  }
  if (status === 'running' && phase === 'clustering') {
    return { label: '聚类处理中', type: 'warning' }
  }
  if (status === 'running') {
    return { label: phase ? '检测处理中' : '运行中', type: 'warning' }
  }
  if (status === 'idle') {
    return { label: '空闲等待', type: 'info' }
  }
  return { label: '未运行', type: 'info' }
}

export function getPersonAvatarFallback(person: Pick<Person, 'name' | 'category'>): string {
  const normalizedName = person.name?.trim()
  if (normalizedName) {
    return normalizedName.charAt(0).toUpperCase()
  }
  return CATEGORY_FALLBACKS[person.category] || '人'
}

export function getMergeSuggestionVisibility(totalPending: number, _loading = false): boolean {
  // 只根据是否有待审核建议决定显示，加载状态不影响（避免刷新时闪烁）
  return totalPending > 0
}

export function sortMergeSuggestionCandidates<T extends Pick<PersonMergeSuggestionItem, 'similarity_score' | 'candidate_person_id'>>(items: T[]): T[] {
  return [...items].sort((left, right) => {
    if (left.similarity_score !== right.similarity_score) {
      return right.similarity_score - left.similarity_score
    }
    return left.candidate_person_id - right.candidate_person_id
  })
}

// 同照片共现警告文案：人工审核提示，不阻断建议。
export const MERGE_SUGGESTION_SAME_PHOTO_WARNING = '两个人物曾出现在同一张照片中，请确认是否属于拼图、反射或屏幕内容。'

// isMergeSuggestionWarning 判断候选是否携带人工审核警告，避免在 Vue 模板中散落判断规则。
export function isMergeSuggestionWarning(item: Pick<PersonMergeSuggestionItem, 'warning'>): boolean {
  return item.warning === 'same_photo_cooccurrence'
}

export function getMergeSuggestionTaskStatusMeta(task?: Pick<PersonMergeSuggestionTask, 'status'> | string | null): TaskStatusMeta {
  const status = typeof task === 'string' ? task : task?.status
  if (status === 'running') {
    return { label: '巡检中', type: 'warning' }
  }
  if (status === 'paused') {
    return { label: '已暂停', type: 'info' }
  }
  if (status === 'idle') {
    return { label: '等待巡检', type: 'info' }
  }
  return { label: '未运行', type: 'info' }
}
