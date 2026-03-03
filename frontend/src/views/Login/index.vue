<template>
  <div class="login-page">
    <div class="login-container">
      <div class="login-header">
        <el-icon class="logo-icon"><PictureFilled /></el-icon>
        <h1 class="title">Relive</h1>
        <p class="subtitle">智能照片记忆框</p>
      </div>

      <el-card class="login-card" shadow="never">
        <h2 class="login-title">管理员登录</h2>

        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          @keyup.enter="handleLogin"
        >
          <el-form-item prop="username">
            <el-input
              v-model="form.username"
              placeholder="用户名"
              :prefix-icon="User"
              size="large"
            />
          </el-form-item>

          <el-form-item prop="Password">
            <el-input
              v-model="form.Password"
              type="Password"
              placeholder="密码"
              :prefix-icon="Lock"
              size="large"
              show-Password
            />
          </el-form-item>

          <el-form-item>
            <el-button
              type="primary"
              size="large"
              :loading="loading"
              class="login-button"
              @click="handleLogin"
            >
              登录
            </el-button>
          </el-form-item>
        </el-form>

        <div class="login-tips">
          <p>首次登录请使用默认账号</p>
          <p>用户名: admin / 密码: admin</p>
        </div>
      </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock, PictureFilled } from '@element-plus/icons-vue'
import { useUserStore } from '@/stores/user'

const router = useRouter()
const userStore = useUserStore()

const formRef = ref()
const loading = ref(false)

const form = reactive({
  username: '',
  Password: ''
})

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' }
  ],
  Password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 4, message: '密码长度至少4位', trigger: 'blur' }
  ]
}

const handleLogin = async () => {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    const response = await userStore.login(form.username, form.Password)

    if (response.is_first_login) {
      ElMessage.info('首次登录，请先修改密码')
      router.push('/change-Password')
    } else {
      ElMessage.success('登录成功')
      router.push('/')
    }
  } catch (error: any) {
    const message = error?.response?.data?.error?.message || '登录失败'
    ElMessage.error(message)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 20px;
}

.login-container {
  width: 100%;
  max-width: 420px;
}

.login-header {
  text-align: center;
  margin-bottom: 32px;
  color: white;
}

.logo-icon {
  font-size: 64px;
  margin-bottom: 16px;
}

.title {
  font-size: 36px;
  font-weight: 600;
  margin: 0 0 8px 0;
}

.subtitle {
  font-size: 16px;
  opacity: 0.9;
  margin: 0;
}

.login-card {
  border-radius: 12px;
  padding: 8px;
}

.login-title {
  font-size: 20px;
  font-weight: 600;
  text-align: center;
  margin: 0 0 24px 0;
  color: var(--color-text-primary);
}

.login-button {
  width: 100%;
  font-size: 16px;
}

.login-tips {
  margin-top: 20px;
  padding-top: 20px;
  border-top: 1px solid var(--color-border);
  text-align: center;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.login-tips p {
  margin: 4px 0;
}
</style>
