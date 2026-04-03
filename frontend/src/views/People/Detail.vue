<template>
  <div class="people-detail-page" v-loading="loading">
    <PageHeader :title="personTitle" subtitle="查看构成人脸样本、关联照片，并执行拆分、移动、合并与头像修正">
      <template #actions>
        <el-button @click="goBack">返回列表</el-button>
        <el-button type="primary" @click="loadData">刷新</el-button>
      </template>
    </PageHeader>

    <template v-if="person">
      <el-row :gutter="20">
        <el-col :xs="24" :lg="10">
          <div class="section-stack">
            <el-card shadow="never" class="section-card">
              <template #header>
                <SectionHeader :icon="User" title="人物信息" />
              </template>

              <div class="summary-card">
                <div class="summary-avatar">
                  <el-avatar :size="88" :src="avatarUrl">
                    {{ getPersonAvatarFallback(person) }}
                  </el-avatar>
                  <div class="summary-avatar-text">
                    <div class="summary-name">{{ personTitle }}</div>
                    <div class="summary-subtitle">
                      {{ getPersonCategoryLabel(person.category) }} · {{ person.face_count }} 张人脸 · {{ person.photo_count }} 张照片
                    </div>
                  </div>
                </div>

                <div class="edit-grid">
                  <div class="edit-field">
                    <span class="edit-label">人物姓名</span>
                    <div class="edit-inline">
                      <el-input v-model="editableName" placeholder="未命名人物" clearable />
                      <el-button type="primary" :loading="nameSaving" @click="saveName">保存</el-button>
                    </div>
                  </div>

                  <div class="edit-field">
                    <span class="edit-label">人物类别</span>
                    <el-select v-model="editableCategory" class="category-select" @change="saveCategory">
                      <el-option v-for="option in categoryOptions" :key="option.value" :label="option.label" :value="option.value" />
                    </el-select>
                  </div>
                </div>

                <el-descriptions :column="1" border class="summary-descriptions">
                  <el-descriptions-item label="人物 ID">{{ `#${person.id}` }}</el-descriptions-item>
                  <el-descriptions-item label="代表头像">
                    {{ person.representative_face_id ? `Face #${person.representative_face_id}` : '未设置' }}
                  </el-descriptions-item>
                  <el-descriptions-item label="创建时间">{{ formatTime(person.created_at) }}</el-descriptions-item>
                  <el-descriptions-item label="更新时间">{{ formatTime(person.updated_at) }}</el-descriptions-item>
                </el-descriptions>
              </div>
            </el-card>

            <el-card shadow="never" class="section-card animate-delay-1">
              <template #header>
                <SectionHeader :icon="Operation" title="纠错操作" />
              </template>

              <div class="operation-list">
                <div class="operation-item">
                  <div>
                    <div class="operation-title">拆分选中人脸</div>
                    <div class="operation-desc">把当前选中的人脸拆成一个新人物，适合把误聚类的人脸拆出去。</div>
                  </div>
                  <el-button type="warning" plain :disabled="selectedFaceIds.length === 0" :loading="splitting" @click="splitSelectedFaces">
                    拆分
                  </el-button>
                </div>

                <div class="operation-item">
                  <div>
                    <div class="operation-title">移动到其他人物</div>
                    <div class="operation-desc">把当前选中的人脸移动到已有人物，适合做误归属修正。</div>
                  </div>
                  <el-button plain :disabled="selectedFaceIds.length === 0 || candidatePeople.length === 0" @click="showMoveDialog = true">
                    选择目标
                  </el-button>
                </div>

                <div class="operation-item">
                  <div>
                    <div class="operation-title">合并其他人物到当前人物</div>
                    <div class="operation-desc">从其他人物中选择若干个，并把它们全部并入当前人物。</div>
                  </div>
                  <el-button plain :disabled="candidatePeople.length === 0" @click="showMergeDialog = true">
                    发起合并
                  </el-button>
                </div>
              </div>
            </el-card>
          </div>
        </el-col>

        <el-col :xs="24" :lg="14">
          <div class="section-stack">
            <el-card shadow="never" class="section-card">
              <template #header>
                <SectionHeader :icon="Crop" :title="`人脸样本（${faces.length}）`">
                  <template #actions>
                    <el-tag size="small" effect="plain">
                      已选择 {{ selectedFaceIds.length }} 张
                    </el-tag>
                  </template>
                </SectionHeader>
              </template>

              <el-empty v-if="faces.length === 0" description="暂无人脸样本" />

              <div v-else class="face-grid">
                <div v-for="face in faces" :key="face.id" class="face-card" :class="{ 'is-selected': selectedFaceIds.includes(face.id) }">
                  <div class="face-card-toolbar">
                    <el-checkbox :model-value="selectedFaceIds.includes(face.id)" @change="toggleFace(face.id, $event as boolean)" />
                    <el-tag v-if="person.representative_face_id === face.id" type="success" size="small">当前头像</el-tag>
                  </div>

                  <img :src="faceThumbnail(face.id)" alt="face" class="face-image" />

                  <div class="face-meta">
                    <span>{{ `Face #${face.id}` }}</span>
                    <span>{{ `Photo #${face.photo_id}` }}</span>
                  </div>

                  <div class="face-meta-sub">
                    <span>{{ `质量 ${(face.quality_score || 0).toFixed(2)}` }}</span>
                    <span v-if="face.manual_locked">人工锁定</span>
                  </div>

                  <div class="face-actions">
                    <el-button size="small" plain :disabled="person.representative_face_id === face.id" :loading="avatarSavingFaceId === face.id" @click="setAvatar(face.id)">
                      设为头像
                    </el-button>
                    <el-button size="small" link @click="goToPhoto(face.photo_id)">查看照片</el-button>
                  </div>
                </div>
              </div>
            </el-card>

            <el-card shadow="never" class="section-card animate-delay-1">
              <template #header>
                <SectionHeader :icon="Picture" :title="`关联照片（${photos.length}）`" />
              </template>

              <el-empty v-if="photos.length === 0" description="暂无关联照片" />

              <div v-else class="photo-grid">
                <button v-for="photo in photos" :key="photo.id" type="button" class="photo-card" @click="goToPhoto(photo.id)">
                  <img :src="photoThumbnail(photo.id)" :alt="photo.file_name || `photo-${photo.id}`" class="photo-image" />
                  <div class="photo-card-main">
                    <div class="photo-title">{{ photo.caption || photo.file_name || `Photo #${photo.id}` }}</div>
                    <div class="photo-subtitle">{{ formatTime(photo.taken_at || photo.created_at) }}</div>
                  </div>
                </button>
              </div>
            </el-card>
          </div>
        </el-col>
      </el-row>
    </template>

    <el-dialog v-model="showMoveDialog" title="移动到其他人物" width="480px">
      <el-select v-model="moveTargetPersonId" filterable class="dialog-select" placeholder="选择目标人物">
        <el-option v-for="candidate in candidatePeople" :key="candidate.id" :label="candidateLabel(candidate)" :value="candidate.id" />
      </el-select>
      <template #footer>
        <el-button @click="showMoveDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!moveTargetPersonId" :loading="moving" @click="confirmMoveFaces">确认移动</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showMergeDialog" title="合并其他人物到当前人物" width="560px">
      <el-select v-model="mergeSourceIds" multiple filterable class="dialog-select" placeholder="选择要并入当前人物的对象">
        <el-option v-for="candidate in candidatePeople" :key="candidate.id" :label="candidateLabel(candidate)" :value="candidate.id" />
      </el-select>
      <template #footer>
        <el-button @click="showMergeDialog = false">取消</el-button>
        <el-button type="primary" :disabled="mergeSourceIds.length === 0" :loading="merging" @click="confirmMerge">确认合并</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Crop, Operation, Picture, User } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

import PageHeader from '@/components/PageHeader.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { peopleApi } from '@/api/people'
import type { Face, Person, PersonCategory } from '@/types/people'
import type { Photo } from '@/types/photo'
import { getPersonAvatarFallback, getPersonCategoryLabel, sortPeopleForDisplay } from './peopleHelpers'

const route = useRoute()
const router = useRouter()
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const loading = ref(false)
const person = ref<Person | null>(null)
const faces = ref<Face[]>([])
const photos = ref<Photo[]>([])
const allPeople = ref<Person[]>([])
const editableName = ref('')
const editableCategory = ref<PersonCategory>('stranger')
const selectedFaceIds = ref<number[]>([])
const avatarSavingFaceId = ref<number | null>(null)
const nameSaving = ref(false)
const categorySaving = ref(false)
const splitting = ref(false)
const moving = ref(false)
const merging = ref(false)
const showMoveDialog = ref(false)
const showMergeDialog = ref(false)
const moveTargetPersonId = ref<number>()
const mergeSourceIds = ref<number[]>([])

const categoryOptions = [
  { label: '家人', value: 'family' },
  { label: '亲友', value: 'friend' },
  { label: '熟人', value: 'acquaintance' },
  { label: '路人', value: 'stranger' },
] satisfies Array<{ label: string; value: PersonCategory }>

const personTitle = computed(() => {
  if (!person.value) return '人物详情'
  return person.value.name?.trim() || `未命名人物 #${person.value.id}`
})

const avatarUrl = computed(() => {
  if (!person.value?.representative_face_id) return ''
  return `${apiBaseUrl}/faces/${person.value.representative_face_id}/thumbnail?v=${person.value.representative_face_id}`
})

const candidatePeople = computed(() => {
  if (!person.value) return []
  return sortPeopleForDisplay(allPeople.value.filter(item => item.id !== person.value?.id && item.has_avatar))
})

const formatTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN')
}

const photoThumbnail = (photoId: number) => `${apiBaseUrl}/photos/${photoId}/thumbnail?v=${photoId}`
const faceThumbnail = (faceId: number) => `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`

const candidateLabel = (item: Person) => `${item.name?.trim() || `未命名人物 #${item.id}`} · ${getPersonCategoryLabel(item.category)}`

const resetSelections = () => {
  selectedFaceIds.value = []
  moveTargetPersonId.value = undefined
  mergeSourceIds.value = []
  showMoveDialog.value = false
  showMergeDialog.value = false
}

const loadCandidatePeople = async () => {
  try {
    const res = await peopleApi.getList({ page: 1, page_size: 200 })
    allPeople.value = res.data?.data?.items || []
  } catch (error) {
    console.error('Failed to load candidate people:', error)
  }
}

const loadData = async () => {
  const personId = Number(route.params.id)
  if (!personId) return

  loading.value = true
  try {
    const [personRes, facesRes, photosRes] = await Promise.all([
      peopleApi.getById(personId),
      peopleApi.getFaces(personId),
      peopleApi.getPhotos(personId),
    ])

    person.value = personRes.data?.data || null
    faces.value = facesRes.data?.data || []
    photos.value = photosRes.data?.data || []
    editableName.value = person.value?.name || ''
    editableCategory.value = person.value?.category || 'stranger'
    resetSelections()
    await loadCandidatePeople()
  } catch (error: any) {
    ElMessage.error(error.message || '加载人物详情失败')
  } finally {
    loading.value = false
  }
}

const saveName = async () => {
  if (!person.value) return
  try {
    nameSaving.value = true
    await peopleApi.updateName(person.value.id, editableName.value.trim())
    ElMessage.success('人物姓名已更新')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '更新人物姓名失败')
  } finally {
    nameSaving.value = false
  }
}

const saveCategory = async (category: PersonCategory) => {
  if (!person.value) return
  try {
    categorySaving.value = true
    await peopleApi.updateCategory(person.value.id, category)
    ElMessage.success('人物类别已更新')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '更新人物类别失败')
  } finally {
    categorySaving.value = false
  }
}

const setAvatar = async (faceId: number) => {
  if (!person.value) return
  try {
    avatarSavingFaceId.value = faceId
    await peopleApi.updateAvatar(person.value.id, faceId)
    ElMessage.success('代表头像已更新')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '更新人物头像失败')
  } finally {
    avatarSavingFaceId.value = null
  }
}

const toggleFace = (faceId: number, checked: boolean) => {
  if (checked) {
    selectedFaceIds.value = [...selectedFaceIds.value, faceId]
    return
  }
  selectedFaceIds.value = selectedFaceIds.value.filter(id => id !== faceId)
}

const splitSelectedFaces = async () => {
  if (selectedFaceIds.value.length === 0) return
  try {
    splitting.value = true
    const res = await peopleApi.split(selectedFaceIds.value)
    const newPerson = res.data?.data
    ElMessage.success('已拆分为新人物')
    if (newPerson?.id) {
      router.push(`/people/${newPerson.id}`)
      return
    }
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '拆分人物失败')
  } finally {
    splitting.value = false
  }
}

const confirmMoveFaces = async () => {
  if (!moveTargetPersonId.value || selectedFaceIds.value.length === 0) return
  try {
    moving.value = true
    await peopleApi.moveFaces(selectedFaceIds.value, moveTargetPersonId.value)
    ElMessage.success('人脸已移动到目标人物')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '移动人脸失败')
  } finally {
    moving.value = false
  }
}

const confirmMerge = async () => {
  if (!person.value || mergeSourceIds.value.length === 0) return
  try {
    merging.value = true
    await peopleApi.merge(person.value.id, mergeSourceIds.value)
    ElMessage.success('人物已合并')
    await loadData()
  } catch (error: any) {
    ElMessage.error(error.message || '合并人物失败')
  } finally {
    merging.value = false
  }
}

const goToPhoto = (photoId: number) => {
  router.push(`/photos/${photoId}`)
}

const goBack = () => {
  router.push('/people')
}

watch(() => route.params.id, async () => {
  await loadData()
})

onMounted(async () => {
  await loadData()
})
</script>

<style scoped>
.people-detail-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: var(--spacing-xl);
}

.section-card {
  border-radius: 18px;
}

.section-card :deep(.el-card__header) {
  padding: 22px 28px;
}

.section-card :deep(.el-card__body) {
  padding: 24px 28px;
}

.summary-card {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.summary-avatar {
  display: flex;
  gap: 16px;
  align-items: center;
}

.summary-name {
  font-size: 22px;
  font-weight: 700;
  color: var(--color-text-primary);
}

.summary-subtitle {
  margin-top: 6px;
  color: var(--color-text-secondary);
}

.edit-grid {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.edit-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.edit-label {
  font-size: 13px;
  color: var(--color-text-secondary);
}

.edit-inline {
  display: flex;
  gap: 12px;
}

.category-select,
.dialog-select {
  width: 100%;
}

.summary-descriptions {
  margin-top: 4px;
}

.operation-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.operation-item {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 18px;
  border-radius: 14px;
  background: var(--color-bg-soft);
  border: 1px solid var(--color-border);
}

.operation-title {
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 4px;
}

.operation-desc {
  color: var(--color-text-secondary);
  font-size: 13px;
  line-height: 1.7;
}

.face-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 12px;
}

.face-card {
  border: 1px solid var(--color-border);
  border-radius: 16px;
  padding: 10px;
  background: #fff;
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.face-card.is-selected {
  border-color: var(--color-primary);
  box-shadow: 0 8px 18px rgba(84, 112, 198, 0.12);
  transform: translateY(-2px);
}

.face-card-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.face-image {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  border-radius: 12px;
  background: var(--color-bg-soft);
}

.face-meta,
.face-meta-sub {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
}

.face-meta {
  color: var(--color-text-primary);
  font-weight: 600;
}

.face-meta-sub {
  color: var(--color-text-secondary);
}

.face-actions {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.photo-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
}

.photo-card {
  border: 1px solid var(--color-border);
  border-radius: 16px;
  padding: 0;
  background: #fff;
  cursor: pointer;
  overflow: hidden;
  text-align: left;
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.photo-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 10px 20px rgba(15, 23, 42, 0.08);
}

.photo-image {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  background: var(--color-bg-soft);
}

.photo-card-main {
  padding: 12px;
}

.photo-title {
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.5;
}

.photo-subtitle {
  margin-top: 4px;
  color: var(--color-text-secondary);
  font-size: 12px;
}

@media (max-width: 1200px) {
  .face-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .photo-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .people-detail-page {
    padding: 16px;
  }

  .section-card :deep(.el-card__header),
  .section-card :deep(.el-card__body) {
    padding-left: 18px;
    padding-right: 18px;
  }

  .summary-avatar,
  .edit-inline,
  .operation-item {
    flex-direction: column;
    align-items: stretch;
  }

  .face-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .photo-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .face-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .face-grid {
    grid-template-columns: 1fr;
  }
}
</style>
