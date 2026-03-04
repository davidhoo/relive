<template>
  <div class="config-page">
    <!-- Scan Paths Card -->
    <el-card shadow="never" class="scan-paths-card">
      <template #header>
        <div class="card-header">
          <div>
            <el-icon class="header-icon"><FolderOpened /></el-icon>
            <span class="header-title">扫描路径配置</span>
          </div>
          <el-button type="primary" @click="handleAddPath">
            <el-icon><Plus /></el-icon>
            添加路径
          </el-button>
        </div>
      </template>

      <el-empty v-if="!scanPaths.length && !loading" description="暂无扫描路径配置">
        <el-button type="primary" @click="handleAddPath">添加第一个路径</el-button>
      </el-empty>

      <div v-else class="paths-list" v-loading="loading">
        <div
          v-for="path in scanPaths"
          :key="path.id"
          class="path-item"
          :class="{ disabled: !path.enabled }"
        >
          <div class="path-info">
            <div class="path-header">
              <el-checkbox v-model="path.enabled" @change="handleToggleEnabled(path)">
                {{ path.name }}
              </el-checkbox>
              <el-tag v-if="path.is_default" type="success" size="small">默认</el-tag>
            </div>
            <div class="path-location">{{ path.path }}</div>
            <div class="path-meta">
              <span v-if="path.last_scanned_at">
                <el-icon><Clock /></el-icon>
                上次扫描: {{ formatTime(path.last_scanned_at) }}
              </span>
              <span v-else class="never-scanned">从未扫描</span>
            </div>
          </div>
          <div class="path-actions">
            <el-button
              v-if="!path.is_default"
              link
              @click="handleSetDefault(path)"
              style="color: var(--color-primary)"
            >
              设为默认
            </el-button>
            <el-button link @click="handleEditPath(path)" style="color: var(--color-primary)">
              编辑
            </el-button>
            <el-button link @click="handleDeletePath(path)" style="color: var(--color-error)">
              删除
            </el-button>
          </div>
        </div>
      </div>
    </el-card>

    <!-- Geocode Configuration Card -->
    <el-card shadow="never" class="geocode-card">
      <template #header>
        <div class="card-header">
          <div>
            <el-icon class="header-icon"><Location /></el-icon>
            <span class="header-title">GPS 逆地理编码配置</span>
          </div>
          <el-button type="primary" @click="handleSaveGeocodeConfig" :loading="savingGeocode">
            <el-icon><Check /></el-icon>
            保存配置
          </el-button>
        </div>
      </template>

      <div v-loading="loadingGeocode">
        <el-form :model="geocodeConfig" label-width="140px" class="geocode-form">
          <!-- Provider Selection -->
          <el-form-item label="主要提供商">
            <el-select v-model="geocodeConfig.provider" placeholder="选择主要提供商" style="width: 100%">
              <el-option value="offline" label="离线数据库 (Offline)">
                <div class="provider-option">
                  <span>离线数据库 (Offline)</span>
                  <el-tag size="small" type="success">最快</el-tag>
                </div>
              </el-option>
              <el-option value="amap" label="高德地图 (AMap)">
                <div class="provider-option">
                  <span>高德地图 (AMap)</span>
                  <el-tag size="small">中国优选</el-tag>
                </div>
              </el-option>
              <el-option value="nominatim" label="OpenStreetMap (Nominatim)">
                <div class="provider-option">
                  <span>OpenStreetMap (Nominatim)</span>
                  <el-tag size="small" type="info">全球覆盖</el-tag>
                </div>
              </el-option>
            </el-select>
            <div class="form-hint">
              当前使用的地理编码服务提供商，优先级最高
            </div>
          </el-form-item>

          <!-- Fallback Provider -->
          <el-form-item label="备用提供商">
            <el-select v-model="geocodeConfig.fallback" placeholder="选择备用提供商" style="width: 100%">
              <el-option value="" label="无备用"></el-option>
              <el-option value="offline" label="离线数据库 (Offline)"></el-option>
              <el-option value="amap" label="高德地图 (AMap)"></el-option>
              <el-option value="nominatim" label="OpenStreetMap (Nominatim)"></el-option>
            </el-select>
            <div class="form-hint">
              主提供商失败时自动切换到备用提供商
            </div>
          </el-form-item>

          <!-- Cache Settings -->
          <el-divider content-position="left">缓存设置</el-divider>

          <el-form-item label="启用缓存">
            <el-switch v-model="geocodeConfig.cache_enabled" />
            <div class="form-hint">
              缓存可大幅提升性能，相同坐标不会重复查询
            </div>
          </el-form-item>

          <el-form-item label="缓存有效期" v-if="geocodeConfig.cache_enabled">
            <el-input-number
              v-model="geocodeConfig.cache_ttl"
              :min="3600"
              :max="604800"
              :step="3600"
              style="width: 200px"
            />
            <span style="margin-left: 12px">秒 ({{ Math.floor(geocodeConfig.cache_ttl / 3600) }} 小时)</span>
            <div class="form-hint">
              缓存数据保留时长，默认 24 小时
            </div>
          </el-form-item>

          <!-- AMap Configuration -->
          <el-divider content-position="left">
            <el-icon><Location /></el-icon>
            高德地图 (AMap) 配置
          </el-divider>

          <el-form-item label="API Key">
            <div class="input-with-button">
              <el-input
                v-model="geocodeConfig.amap_api_key"
                placeholder="请输入高德地图 API Key"
                type="password"
                show-password
              />
              <el-button @click="openAmapDocs">
                <el-icon><Link /></el-icon>
                申请
              </el-button>
            </div>
            <div class="form-hint">
              访问 <a href="https://lbs.amap.com/" target="_blank">https://lbs.amap.com/</a> 申请 API Key
            </div>
          </el-form-item>

          <el-form-item label="超时时间">
            <el-input-number
              v-model="geocodeConfig.amap_timeout"
              :min="5"
              :max="60"
              style="width: 150px"
            />
            <span style="margin-left: 12px">秒</span>
          </el-form-item>

          <!-- Nominatim Configuration -->
          <el-divider content-position="left">
            <el-icon><Location /></el-icon>
            Nominatim (OpenStreetMap) 配置
          </el-divider>

          <el-form-item label="服务端点">
            <el-input
              v-model="geocodeConfig.nominatim_endpoint"
              placeholder="https://nominatim.openstreetmap.org/reverse"
            />
            <div class="form-hint">
              默认使用官方服务，也可使用自建 Nominatim 服务
            </div>
          </el-form-item>

          <el-form-item label="超时时间">
            <el-input-number
              v-model="geocodeConfig.nominatim_timeout"
              :min="5"
              :max="60"
              style="width: 150px"
            />
            <span style="margin-left: 12px">秒</span>
          </el-form-item>

          <!-- Offline Configuration -->
          <el-divider content-position="left">
            <el-icon><Location /></el-icon>
            离线数据库配置
          </el-divider>

          <el-form-item label="最大搜索距离">
            <el-input-number
              v-model="geocodeConfig.offline_max_distance"
              :min="10"
              :max="500"
              :step="10"
              style="width: 150px"
            />
            <span style="margin-left: 12px">公里</span>
            <div class="form-hint">
              超过此距离的坐标将无法匹配到城市
            </div>
          </el-form-item>

          <el-alert
            title="离线数据库说明"
            type="info"
            :closable="false"
            style="margin-top: 16px"
          >
            <template #default>
              <div>离线提供商需要导入城市数据库才能使用。如未导入，系统会自动使用备用提供商。</div>
              <div style="margin-top: 8px">
                数据源：<a href="https://download.geonames.org/export/dump/" target="_blank">GeoNames</a>
                (推荐使用 <a href="https://download.geonames.org/export/dump/cities500.zip" target="_blank">cities500.zip</a> - 覆盖面更广)
              </div>
            </template>
          </el-alert>
        </el-form>
      </div>
    </el-card>

    <!-- AI Provider Configuration Card -->
    <el-card shadow="never" class="ai-card">
      <template #header>
        <div class="card-header">
          <div>
            <el-icon class="header-icon"><Cpu /></el-icon>
            <span class="header-title">AI 分析服务配置</span>
          </div>
          <el-button type="primary" @click="handleSaveAIConfig" :loading="savingAI">
            <el-icon><Check /></el-icon>
            保存配置
          </el-button>
        </div>
      </template>

      <div v-loading="loadingAI">
        <el-form :model="aiConfig" label-width="140px" class="ai-form">
          <!-- Provider Selection -->
          <el-form-item label="主要提供商">
            <el-select v-model="aiConfig.provider" placeholder="选择 AI 提供商" style="width: 100%">
              <el-option value="" label="未配置">
                <div class="provider-option">
                  <span>未配置</span>
                  <el-tag size="small" type="info">禁用 AI</el-tag>
                </div>
              </el-option>
              <el-option value="qwen" label="通义千问 (Qwen)">
                <div class="provider-option">
                  <span>通义千问 (Qwen)</span>
                  <el-tag size="small" type="success">推荐</el-tag>
                </div>
              </el-option>
              <el-option value="openai" label="OpenAI (GPT-4V)">
                <div class="provider-option">
                  <span>OpenAI (GPT-4V)</span>
                  <el-tag size="small">高质量</el-tag>
                </div>
              </el-option>
              <el-option value="ollama" label="Ollama (本地)">
                <div class="provider-option">
                  <span>Ollama (本地)</span>
                  <el-tag size="small" type="warning">免费</el-tag>
                </div>
              </el-option>
              <el-option value="vllm" label="vLLM (自部署)">
                <div class="provider-option">
                  <span>vLLM (自部署)</span>
                  <el-tag size="small" type="warning">自部署</el-tag>
                </div>
              </el-option>
              <el-option value="hybrid" label="混合模式">
                <div class="provider-option">
                  <span>混合模式</span>
                  <el-tag size="small" type="info">主备切换</el-tag>
                </div>
              </el-option>
            </el-select>
            <div class="form-hint">
              AI 提供商用于照片内容分析和标签生成
            </div>
          </el-form-item>

          <!-- Global Settings -->
          <el-divider content-position="left">全局设置</el-divider>

          <el-form-item label="温度参数">
            <el-slider v-model="aiConfig.temperature" :min="0" :max="1" :step="0.1" show-input style="max-width: 400px" />
            <div class="form-hint">
              较低的值产生更一致的结果，较高的值产生更多样化的结果
            </div>
          </el-form-item>

          <el-form-item label="超时时间">
            <el-input-number v-model="aiConfig.timeout" :min="10" :max="300" style="width: 150px" />
            <span style="margin-left: 12px">秒</span>
          </el-form-item>

          <!-- Qwen Configuration -->
          <el-divider content-position="left">
            <el-icon><Cpu /></el-icon>
            通义千问 (Qwen) 配置
          </el-divider>

          <el-form-item label="API Key">
            <div class="input-with-button">
              <el-input
                v-model="aiConfig.qwen_api_key"
                placeholder="请输入通义千问 API Key"
                type="password"
                show-password
              />
              <el-button @click="openQwenDocs">
                <el-icon><Link /></el-icon>
                申请
              </el-button>
            </div>
            <div class="form-hint">
              访问 <a href="https://dashscope.console.aliyun.com/" target="_blank">阿里云 DashScope</a> 申请 API Key
            </div>
          </el-form-item>

          <el-form-item label="API 端点">
            <el-input v-model="aiConfig.qwen_endpoint" placeholder="默认使用阿里云端点" />
          </el-form-item>

          <el-form-item label="模型">
            <el-select v-model="aiConfig.qwen_model" style="width: 100%">
              <el-option value="qwen-vl-max" label="qwen-vl-max (推荐)" />
              <el-option value="qwen-vl-plus" label="qwen-vl-plus (经济)" />
              <el-option value="qwen3.5-plus" label="qwen3.5-plus (最新，需更长超时)" />
            </el-select>
          </el-form-item>

          <el-form-item label="超时时间(秒)">
            <el-input-number
              v-model="aiConfig.qwen_timeout"
              :min="30"
              :max="300"
              :step="10"
              style="width: 100%"
            />
            <div class="form-hint">
              默认 60 秒，使用 qwen3.5-plus 建议设置为 120 秒或更长
            </div>
          </el-form-item>

          <!-- OpenAI Configuration -->
          <el-divider content-position="left">
            <el-icon><Cpu /></el-icon>
            OpenAI 配置
          </el-divider>

          <el-form-item label="API Key">
            <div class="input-with-button">
              <el-input
                v-model="aiConfig.openai_api_key"
                placeholder="请输入 OpenAI API Key"
                type="password"
                show-password
              />
              <el-button @click="openOpenAIDocs">
                <el-icon><Link /></el-icon>
                申请
              </el-button>
            </div>
            <div class="form-hint">
              访问 <a href="https://platform.openai.com/api-keys" target="_blank">OpenAI Platform</a> 申请 API Key
            </div>
          </el-form-item>

          <el-form-item label="API 端点">
            <el-input v-model="aiConfig.openai_endpoint" placeholder="默认使用 OpenAI 端点，可配置代理" />
          </el-form-item>

          <el-form-item label="模型">
            <el-select v-model="aiConfig.openai_model" style="width: 100%">
              <el-option value="gpt-4-vision-preview" label="GPT-4 Vision (推荐)" />
              <el-option value="gpt-4o" label="GPT-4o" />
              <el-option value="gpt-4o-mini" label="GPT-4o Mini (经济)" />
            </el-select>
          </el-form-item>

          <el-form-item label="最大 Tokens">
            <el-input-number v-model="aiConfig.openai_max_tokens" :min="100" :max="4000" style="width: 150px" />
          </el-form-item>

          <!-- Ollama Configuration -->
          <el-divider content-position="left">
            <el-icon><Cpu /></el-icon>
            Ollama (本地) 配置
          </el-divider>

          <el-form-item label="API 端点">
            <el-input v-model="aiConfig.ollama_endpoint" placeholder="http://localhost:11434/api/generate" />
            <div class="form-hint">
              确保已安装并运行 Ollama，且已下载视觉模型 (如 llava)
            </div>
          </el-form-item>

          <el-form-item label="模型">
            <el-input v-model="aiConfig.ollama_model" placeholder="llava" />
            <div class="form-hint">
              推荐模型: llava, bakllava, moondream
            </div>
          </el-form-item>

          <!-- vLLM Configuration -->
          <el-divider content-position="left">
            <el-icon><Cpu /></el-icon>
            vLLM (自部署) 配置
          </el-divider>

          <el-form-item label="API 端点">
            <el-input v-model="aiConfig.vllm_endpoint" placeholder="http://localhost:8000/v1/chat/completions" />
            <div class="form-hint">
              自部署的 vLLM 服务端点
            </div>
          </el-form-item>

          <el-form-item label="模型名称">
            <el-input v-model="aiConfig.vllm_model" placeholder="模型标识符" />
          </el-form-item>

          <el-form-item label="最大 Tokens">
            <el-input-number v-model="aiConfig.vllm_max_tokens" :min="100" :max="4000" style="width: 150px" />
          </el-form-item>

          <el-form-item label="并发数">
            <el-input-number v-model="aiConfig.vllm_concurrency" :min="1" :max="20" style="width: 150px" />
            <div class="form-hint">
              批量分析时的并发请求数（默认 5）
            </div>
          </el-form-item>

          <el-form-item label="启用思考">
            <el-switch v-model="aiConfig.vllm_enable_thinking" />
            <div class="form-hint">
              是否启用模型的思考功能（默认关闭）
            </div>
          </el-form-item>

          <!-- Hybrid Configuration -->
          <el-divider content-position="left">
            <el-icon><Cpu /></el-icon>
            混合模式配置
          </el-divider>

          <el-form-item label="主提供商">
            <el-select v-model="aiConfig.hybrid_primary" placeholder="选择主提供商" style="width: 100%">
              <el-option value="qwen" label="通义千问 (Qwen)" />
              <el-option value="openai" label="OpenAI" />
              <el-option value="ollama" label="Ollama" />
              <el-option value="vllm" label="vLLM" />
            </el-select>
          </el-form-item>

          <el-form-item label="备用提供商">
            <el-select v-model="aiConfig.hybrid_fallback" placeholder="选择备用提供商" style="width: 100%">
              <el-option value="" label="无备用" />
              <el-option value="qwen" label="通义千问 (Qwen)" />
              <el-option value="openai" label="OpenAI" />
              <el-option value="ollama" label="Ollama" />
              <el-option value="vllm" label="vLLM" />
            </el-select>
          </el-form-item>

          <el-form-item label="失败自动切换">
            <el-switch v-model="aiConfig.hybrid_retry_on_error" />
            <div class="form-hint">
              主提供商失败时自动切换到备用提供商
            </div>
          </el-form-item>

          <el-alert
            title="配置提示"
            type="info"
            :closable="false"
            style="margin-top: 16px"
          >
            <template #default>
              <div>AI 配置保存后立即生效，无需重启服务。</div>
              <div style="margin-top: 8px">
                <strong>推荐配置：</strong>
                <ul style="margin: 4px 0; padding-left: 20px">
                  <li>日常使用：通义千问 (性价比高，¥0.004/张)</li>
                  <li>高质量分析：OpenAI GPT-4V (¥0.07/张)</li>
                  <li>免费方案：Ollama + llava (本地运行)</li>
                </ul>
              </div>
            </template>
          </el-alert>
        </el-form>
      </div>
    </el-card>

    <!-- AI Prompt Configuration Card -->
    <el-card shadow="never" class="prompt-card">
      <template #header>
        <div class="card-header">
          <div>
            <el-icon class="header-icon"><Document /></el-icon>
            <span class="header-title">AI 提示词配置</span>
          </div>
          <div class="header-actions">
            <el-button @click="handleResetPrompts" :loading="resettingPrompts">
              <el-icon><RefreshLeft /></el-icon>
              恢复默认
            </el-button>
            <el-button type="primary" @click="handleSavePromptConfig" :loading="savingPrompts">
              <el-icon><Check /></el-icon>
              保存配置
            </el-button>
          </div>
        </div>
      </template>

      <div v-loading="loadingPrompts">
        <el-form :model="promptConfig" label-width="120px" class="prompt-form">
          <!-- Analysis Prompt -->
          <el-form-item label="分析提示词">
            <div class="prompt-textarea-wrapper">
              <el-input
                v-model="promptConfig.analysis_prompt"
                type="textarea"
                :rows="8"
                placeholder="输入 AI 照片分析的提示词..."
              />
              <div class="prompt-description">
                用于第一次会话，指导 AI 分析照片内容、分类、评分等
              </div>
            </div>
          </el-form-item>

          <!-- Caption Prompt -->
          <el-form-item label="文案生成提示词">
            <div class="prompt-textarea-wrapper">
              <el-input
                v-model="promptConfig.caption_prompt"
                type="textarea"
                :rows="8"
                placeholder="输入 AI 生成照片文案的提示词..."
              />
              <div class="prompt-description">
                用于第二次会话，指导 AI 为照片生成创意旁白短句
              </div>
            </div>
          </el-form-item>

          <!-- Batch Prompt -->
          <el-form-item label="批量分析提示词">
            <div class="prompt-textarea-wrapper">
              <el-input
                v-model="promptConfig.batch_prompt"
                type="textarea"
                :rows="6"
                placeholder="输入批量分析的提示词..."
              />
              <div class="prompt-description">
                仅用于支持批量分析的 provider（如 Qwen），包含 %d 占位符表示照片数量
              </div>
            </div>
          </el-form-item>

          <el-alert
            title="提示词配置说明"
            type="info"
            :closable="false"
            style="margin-top: 16px"
          >
            <template #default>
              <ul style="margin: 8px 0; padding-left: 20px">
                <li>修改提示词后，新的分析将使用新的提示词</li>
                <li>已分析的照片不会自动重新分析</li>
                <li>提示词为空时将使用系统默认值</li>
                <li>批量分析提示词需要包含 <code>%d</code> 占位符表示照片数量</li>
              </ul>
            </template>
          </el-alert>
        </el-form>
      </div>
    </el-card>

    <!-- Add/Edit Path Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑扫描路径' : '添加扫描路径'"
      width="600px"
    >
      <el-form :model="pathForm" label-width="100px" ref="formRef">
        <el-form-item label="名称" required>
          <el-input v-model="pathForm.name" placeholder="例如: iPhone 2025-11" />
        </el-form-item>
        <el-form-item label="路径" required>
          <div class="input-with-button">
            <el-input v-model="pathForm.path" placeholder="/path/to/photos" />
            <el-button @click="handleBrowsePath">
              <el-icon><FolderOpened /></el-icon>
              浏览
            </el-button>
            <el-button @click="handleValidatePath" :loading="validating">验证</el-button>
          </div>
          <div v-if="validationResult" :class="['validation-result', validationResult.valid ? 'valid' : 'invalid']">
            <el-icon v-if="validationResult.valid"><CircleCheck /></el-icon>
            <el-icon v-else><CircleClose /></el-icon>
            <span>{{ validationResult.valid ? '路径有效' : validationResult.error }}</span>
          </div>
        </el-form-item>
        <el-form-item label="设置">
          <el-checkbox v-model="pathForm.is_default">设为默认路径</el-checkbox>
          <el-checkbox v-model="pathForm.enabled">启用此路径</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleSavePath" :loading="saving">保存</el-button>
      </template>
    </el-dialog>

    <!-- API Key 管理 -->
    <ApiKeyManager />

    <!-- Path Browser Dialog -->
    <PathBrowser v-model="pathBrowserVisible" :initial-path="pathForm.path" @select="handlePathSelected" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import ApiKeyManager from './components/ApiKeyManager.vue'
import PathBrowser from '@/components/PathBrowser.vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { configApi, promptApi, type ScanPathConfig, type GeocodeConfig, type AIConfig, type PromptConfig, defaultPrompts } from '@/api/config'
import dayjs from 'dayjs'
import { v4 as uuidv4 } from 'uuid'
import { FolderOpened, CircleCheck, CircleClose, Document, RefreshLeft, Check } from '@element-plus/icons-vue'

// Scan paths state
const scanPaths = ref<ScanPathConfig[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const isEdit = ref(false)
const saving = ref(false)
const validating = ref(false)
const validationResult = ref<{ valid: boolean; error?: string } | null>(null)
const pathBrowserVisible = ref(false)

const pathForm = ref<Partial<ScanPathConfig>>({
  name: '',
  path: '',
  is_default: false,
  enabled: true,
})

// Geocode configuration state
const geocodeConfig = ref<GeocodeConfig>({
  provider: 'offline',
  fallback: 'nominatim',
  cache_enabled: true,
  cache_ttl: 86400,
  amap_api_key: '',
  amap_timeout: 10,
  nominatim_endpoint: 'https://nominatim.openstreetmap.org/reverse',
  nominatim_timeout: 10,
  offline_max_distance: 100
})
const loadingGeocode = ref(false)
const savingGeocode = ref(false)

// AI configuration state
const aiConfig = ref<AIConfig>(configApi.getDefaultAIConfig())
const loadingAI = ref(false)
const savingAI = ref(false)

// Prompt configuration state
const promptConfig = ref<PromptConfig>({ ...defaultPrompts })
const loadingPrompts = ref(false)
const savingPrompts = ref(false)
const resettingPrompts = ref(false)

const formatTime = (time?: string) => {
  if (!time) return '-'
  return dayjs(time).format('YYYY-MM-DD HH:mm:ss')
}

// Scan paths functions
const loadScanPaths = async () => {
  loading.value = true
  try {
    const config = await configApi.getScanPaths()
    scanPaths.value = config.paths || []
  } catch (error: any) {
    ElMessage.error('加载扫描路径失败')
  } finally {
    loading.value = false
  }
}

const handleAddPath = () => {
  isEdit.value = false
  pathForm.value = {
    name: '',
    path: '',
    is_default: scanPaths.value.length === 0, // First path is default
    enabled: true,
  }
  validationResult.value = null
  dialogVisible.value = true
}

const handleEditPath = (path: ScanPathConfig) => {
  isEdit.value = true
  pathForm.value = { ...path }
  validationResult.value = null
  dialogVisible.value = true
}

const handleBrowsePath = () => {
  pathBrowserVisible.value = true
}

const handlePathSelected = (path: string) => {
  pathForm.value.path = path
}

const handleValidatePath = async () => {
  if (!pathForm.value.path) {
    ElMessage.warning('请输入路径')
    return
  }

  validating.value = true
  try {
    const result = await configApi.validatePath(pathForm.value.path)
    validationResult.value = result
    if (result.valid) {
      ElMessage.success('路径验证成功')
    }
  } catch (error: any) {
    ElMessage.error('路径验证失败')
  } finally {
    validating.value = false
  }
}

const handleSavePath = async () => {
  if (!pathForm.value.name || !pathForm.value.path) {
    ElMessage.warning('请填写完整信息')
    return
  }

  saving.value = true
  try {
    const newPaths = [...scanPaths.value]

    if (isEdit.value) {
      // Update existing
      const index = newPaths.findIndex(p => p.id === pathForm.value.id)
      if (index !== -1) {
        // If setting as default, unset others
        if (pathForm.value.is_default) {
          newPaths.forEach(p => p.is_default = false)
        }
        newPaths[index] = pathForm.value as ScanPathConfig
      }
    } else {
      // Add new
      const newPath: ScanPathConfig = {
        id: uuidv4(),
        name: pathForm.value.name!,
        path: pathForm.value.path!,
        is_default: pathForm.value.is_default || false,
        enabled: pathForm.value.enabled ?? true,
        created_at: new Date().toISOString(),
      }

      // If setting as default, unset others
      if (newPath.is_default) {
        newPaths.forEach(p => p.is_default = false)
      }

      newPaths.push(newPath)
    }

    await configApi.updateScanPaths({ paths: newPaths })
    ElMessage.success('保存成功')
    dialogVisible.value = false
    await loadScanPaths()
  } catch (error: any) {
    ElMessage.error('保存失败')
  } finally {
    saving.value = false
  }
}

const handleDeletePath = async (path: ScanPathConfig) => {
  try {
    // 查找路径对应的照片数量
    const photoCount = await getPhotoCountByPath(path.path)

    let message = `确定要删除扫描路径「${path.name}」吗？`
    if (photoCount > 0) {
      message += `<br><br><strong style="color: var(--color-error)">警告：该路径下有 ${photoCount} 张照片，删除路径将同时删除这些照片的数据库记录和缩略图！</strong>`
    }

    await ElMessageBox.confirm(message, '确认删除', {
      type: 'warning',
      dangerouslyUseHTMLString: true,
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })

    // 调用新 API 删除路径及其关联数据
    const result = await configApi.deleteScanPath(path.id)
    ElMessage.success(result.message || '删除成功')
    await loadScanPaths()
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error(error.message || '删除失败')
    }
  }
}

// 获取路径下的照片数量（用于提示）
const getPhotoCountByPath = async (path: string): Promise<number> => {
  // 这里可以通过 API 获取，暂时返回 0，让后端处理
  // 也可以添加一个新 API 来查询路径下的照片数量
  return 0
}

const handleSetDefault = async (path: ScanPathConfig) => {
  const newPaths = scanPaths.value.map(p => ({
    ...p,
    is_default: p.id === path.id,
  }))

  try {
    await configApi.updateScanPaths({ paths: newPaths })
    ElMessage.success('已设为默认路径')
    await loadScanPaths()
  } catch (error: any) {
    ElMessage.error('操作失败')
  }
}

const handleToggleEnabled = async (path: ScanPathConfig) => {
  try {
    await configApi.updateScanPaths({ paths: scanPaths.value })
    ElMessage.success(path.enabled ? '已启用' : '已禁用')
  } catch (error: any) {
    ElMessage.error('操作失败')
    // Revert
    path.enabled = !path.enabled
  }
}

// Geocode configuration functions
const loadGeocodeConfig = async () => {
  loadingGeocode.value = true
  try {
    const config = await configApi.getGeocodeConfig()
    geocodeConfig.value = config
  } catch (error: any) {
    ElMessage.error('加载地理编码配置失败')
  } finally {
    loadingGeocode.value = false
  }
}

const handleSaveGeocodeConfig = async () => {
  savingGeocode.value = true
  try {
    await configApi.updateGeocodeConfig(geocodeConfig.value)
    ElMessage.success('地理编码配置保存成功')
  } catch (error: any) {
    ElMessage.error('保存失败: ' + (error.message || '未知错误'))
  } finally {
    savingGeocode.value = false
  }
}

const openAmapDocs = () => {
  window.open('https://lbs.amap.com/', '_blank')
}

// AI configuration functions
const loadAIConfig = async () => {
  loadingAI.value = true
  try {
    const config = await configApi.getAIConfig()
    aiConfig.value = config
  } catch (error: any) {
    ElMessage.error('加载 AI 配置失败')
  } finally {
    loadingAI.value = false
  }
}

const handleSaveAIConfig = async () => {
  savingAI.value = true
  try {
    await configApi.updateAIConfig(aiConfig.value)
    ElMessage.success('AI 配置保存成功，已立即生效')
  } catch (error: any) {
    ElMessage.error('保存失败: ' + (error.message || '未知错误'))
  } finally {
    savingAI.value = false
  }
}

const openQwenDocs = () => {
  window.open('https://dashscope.console.aliyun.com/', '_blank')
}

const openOpenAIDocs = () => {
  window.open('https://platform.openai.com/api-keys', '_blank')
}

// Prompt configuration functions
const loadPromptConfig = async () => {
  loadingPrompts.value = true
  try {
    const config = await promptApi.getPromptConfig()
    promptConfig.value = config
  } catch (error: any) {
    ElMessage.error('加载提示词配置失败')
  } finally {
    loadingPrompts.value = false
  }
}

const handleSavePromptConfig = async () => {
  savingPrompts.value = true
  try {
    await promptApi.updatePromptConfig(promptConfig.value)
    ElMessage.success('提示词配置保存成功')
  } catch (error: any) {
    ElMessage.error('保存失败: ' + (error.message || '未知错误'))
  } finally {
    savingPrompts.value = false
  }
}

const handleResetPrompts = async () => {
  try {
    await ElMessageBox.confirm(
      '确定要恢复默认提示词吗？这将覆盖当前的自定义提示词。',
      '确认恢复默认',
      {
        type: 'warning',
        confirmButtonText: '恢复默认',
        cancelButtonText: '取消',
      }
    )

    resettingPrompts.value = true
    const config = await promptApi.resetPromptConfig()
    promptConfig.value = config
    ElMessage.success('已恢复默认提示词')
  } catch (error: any) {
    if (error !== 'cancel') {
      ElMessage.error('恢复失败: ' + (error.message || '未知错误'))
    }
  } finally {
    resettingPrompts.value = false
  }
}

onMounted(() => {
  loadScanPaths()
  loadGeocodeConfig()
  loadAIConfig()
  loadPromptConfig()
})
</script>

<style scoped>
.config-page {
  padding: var(--spacing-xl);
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.scan-paths-card,
.geocode-card,
.ai-card,
.prompt-card {
  max-width: 1200px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-icon {
  margin-right: 8px;
  font-size: 18px;
  color: var(--color-primary);
}

.header-title {
  font-size: 16px;
  font-weight: 600;
}

.paths-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.path-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  transition: all 0.3s;
}

.path-item:hover {
  border-color: var(--color-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.path-item.disabled {
  opacity: 0.6;
}

.path-info {
  flex: 1;
}

.path-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
  font-weight: 600;
}

.path-location {
  color: var(--color-text-secondary);
  font-family: monospace;
  margin-bottom: 4px;
}

.path-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--color-text-tertiary);
}

.never-scanned {
  color: var(--color-warning);
}

.path-actions {
  display: flex;
  gap: 8px;
}

.validation-result {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: 14px;
}

.validation-result.valid {
  color: var(--color-success);
}

.validation-result.invalid {
  color: var(--color-error);
}

/* Geocode configuration styles */
.geocode-form,
.ai-form {
  max-width: 800px;
}

.form-hint {
  font-size: 13px;
  color: var(--color-text-tertiary);
  margin-top: 4px;
  line-height: 1.5;
}

.form-hint a {
  color: var(--color-primary);
  text-decoration: none;
}

.form-hint a:hover {
  text-decoration: underline;
}

.provider-option {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

:deep(.el-divider__text) {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

:deep(.el-alert) {
  line-height: 1.8;
}

/* 输入框与按钮并排布局 */
.input-with-button {
  display: flex;
  gap: 12px;
  align-items: center;
  width: 100%;
}

.input-with-button .el-input {
  flex: 1;
  min-width: 0;
}

.input-with-button .el-button {
  flex-shrink: 0;
}

/* Prompt configuration styles */
.prompt-form {
  max-width: 900px;
}

.prompt-textarea-wrapper {
  width: 100%;
}

.prompt-textarea-wrapper .el-textarea {
  width: 100%;
}

.prompt-description {
  font-size: 13px;
  color: var(--color-text-tertiary);
  margin-top: 8px;
  line-height: 1.5;
}

.header-actions {
  display: flex;
  gap: 12px;
}

.prompt-card :deep(.el-form-item__content) {
  width: calc(100% - 120px);
}
</style>
