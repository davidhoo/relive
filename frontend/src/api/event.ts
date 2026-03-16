import http from '@/utils/request'
import type { Event } from '@/types/event'
import type { Photo } from '@/types/photo'
import type { ApiResponse, PagedResponse } from '@/types/api'

export const eventApi = {
  // 获取事件列表
  getList(page: number, pageSize: number) {
    return http.get<ApiResponse<PagedResponse<Event>>>('/events', {
      params: { page, page_size: pageSize },
    })
  },

  // 获取事件详情（含照片列表）
  getDetail(id: number, page?: number, pageSize?: number) {
    return http.get<ApiResponse<{ event: Event; photos: PagedResponse<Photo> }>>(
      `/events/${id}`,
      { params: { page: page || 1, page_size: pageSize || 50 } },
    )
  },
}
