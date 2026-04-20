import http from '@/utils/request'
import type { ApiResponse, PagedResponse } from '@/types/api'
import type { Photo } from '@/types/photo'
import type {
  Face,
  PeopleBackgroundLogsResponse,
  PeopleListParams,
  PeopleStats,
  PeopleTask,
  Person,
  PersonMergeSuggestion,
  PersonMergeSuggestionStats,
  PersonMergeSuggestionTask,
  PhotoPeopleResponse
} from '@/types/people'

export const peopleApi = {
  getList(params?: PeopleListParams) {
    return http.get<ApiResponse<PagedResponse<Person>>>('/people', { params })
  },

  getById(id: number) {
    return http.get<ApiResponse<Person>>(`/people/${id}`)
  },

  getPhotos(id: number) {
    return http.get<ApiResponse<Photo[]>>(`/people/${id}/photos`)
  },

  getFaces(id: number) {
    return http.get<ApiResponse<Face[]>>(`/people/${id}/faces`)
  },

  updateCategory(id: number, category: Person['category']) {
    return http.patch<ApiResponse<void>>(`/people/${id}/category`, { category })
  },

  updateName(id: number, name: string) {
    return http.patch<ApiResponse<void>>(`/people/${id}/name`, { name })
  },

  updateAvatar(id: number, faceId: number) {
    return http.patch<ApiResponse<void>>(`/people/${id}/avatar`, { face_id: faceId })
  },

  merge(targetPersonId: number, sourcePersonIds: number[]) {
    return http.post<ApiResponse<void>>('/people/merge', {
      target_person_id: targetPersonId,
      source_person_ids: sourcePersonIds,
    })
  },

  split(faceIds: number[]) {
    return http.post<ApiResponse<Person>>('/people/split', { face_ids: faceIds })
  },

  moveFaces(faceIds: number[], targetPersonId: number) {
    return http.post<ApiResponse<void>>('/people/move-faces', {
      face_ids: faceIds,
      target_person_id: targetPersonId,
    })
  },

  getTask() {
    return http.get<ApiResponse<PeopleTask | null>>('/people/task')
  },

  getStats() {
    return http.get<ApiResponse<PeopleStats>>('/people/stats')
  },

  getBackgroundLogs() {
    return http.get<ApiResponse<PeopleBackgroundLogsResponse>>('/people/background/logs')
  },

  startBackground() {
    return http.post<ApiResponse<PeopleTask>>('/people/background/start')
  },

  stopBackground() {
    return http.post<ApiResponse<void>>('/people/background/stop')
  },

  resetAllPeople() {
    return http.post<ApiResponse<{ photos_enqueued: number; background_started: boolean }>>('/people/reset')
  },

  dissolvePerson(id: number) {
    return http.post<ApiResponse<{ faces_released: number }>>(`/people/${id}/dissolve`)
  },

  rescanByPath(path: string) {
    return http.post<ApiResponse<{ count: number; background_started?: boolean }>>('/people/rescan-by-path', { path })
  },

  enqueueUnprocessed() {
    return http.post<ApiResponse<{ enqueued: number }>>('/people/enqueue-unprocessed')
  },

  getPhotoPeople(photoId: number) {
    return http.get<ApiResponse<PhotoPeopleResponse>>(`/photos/${photoId}/people`)
  },

  getMergeSuggestionTask() {
    return http.get<ApiResponse<PersonMergeSuggestionTask | null>>('/people/merge-suggestions/task')
  },

  getMergeSuggestionStats() {
    return http.get<ApiResponse<PersonMergeSuggestionStats>>('/people/merge-suggestions/stats')
  },

  getMergeSuggestionLogs() {
    return http.get<ApiResponse<PeopleBackgroundLogsResponse>>('/people/merge-suggestions/background/logs')
  },

  pauseMergeSuggestionTask() {
    return http.post<ApiResponse<void>>('/people/merge-suggestions/background/pause')
  },

  resumeMergeSuggestionTask() {
    return http.post<ApiResponse<void>>('/people/merge-suggestions/background/resume')
  },

  rebuildMergeSuggestionTask() {
    return http.post<ApiResponse<void>>('/people/merge-suggestions/background/rebuild')
  },

  listMergeSuggestions(params?: { page?: number; page_size?: number }) {
    return http.get<ApiResponse<PagedResponse<PersonMergeSuggestion>>>('/people/merge-suggestions', { params })
  },

  getMergeSuggestion(id: number) {
    return http.get<ApiResponse<PersonMergeSuggestion>>(`/people/merge-suggestions/${id}`)
  },

  excludeMergeSuggestionCandidates(id: number, candidatePersonIds: number[]) {
    return http.post<ApiResponse<void>>(`/people/merge-suggestions/${id}/exclude`, {
      candidate_person_ids: candidatePersonIds,
    })
  },

  applyMergeSuggestion(id: number, candidatePersonIds: number[]) {
    return http.post<ApiResponse<void>>(`/people/merge-suggestions/${id}/apply`, {
      candidate_person_ids: candidatePersonIds,
    })
  },
}
