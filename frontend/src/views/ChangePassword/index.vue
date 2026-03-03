<template>
  <div class="change-Password-page">
    <div class="change-Password-container">
      <div class="login-header">
        <el-icon class="logo-icon"><PictureFilled /></el-icon>
        <h1 class="title">Relive</h1>
        <p class="subtitle">智能照片记忆框</p>
      </div>

      <el-card class="change-Password-card" shadow="never">
        <el-alert
          v-if="userStore.isFirstLogin"
          title="首次登录需要修改密码"
          description="为了安全起见，请修改默认密码后再继续使用系统。"
          type="warning"
          :closable="false"
          show-icon
          style="margin-bottom: 24px"
        />

        <h2 class="change-Password-title">{{ userStore.isFirstLogin ? '修改初始密码' : '修改密码' }}</h2>

        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          @keyup.enter="handleSubmit"
        >
          <el-form-item prop="old_Password">
            <el-input
              v-model="form.old_Password"
              type="Password"
              placeholder="旧密码"
              :prefix-icon="Lock"
              size="large"
              show-Password
            />
          </el-form-item>

          <el-form-item prop="new_Password">
            <el-input
              v-model="form.new_Password"
              type="Password"
              placeholder="新密码"
              :prefix-icon="Key"
              size="large"
              show-Password
            />
          </el-form-item>

          <el-form-item prop="confirm_Password">
            <el-input
              v-model="form.confirm_Password"
              type="Password"
              placeholder="确认新密码"
              :prefix-icon="CircleCheck"
              size="large"
              show-Password
            />
          </el-form-item>

          <el-form-item>
            <el-button
              type="primary"
              size="large"
              :loading="loading"
              class="submit-button"
              @click="handleSubmit"
            >
              {{ userStore.isFirstLogin ? '确认修改并进入系统' : '确认修改' }}
            </el-button>
          </el-form-item>

          <el-form-item v-if="!userStore.isFirstLogin">
            <el-button
              size="large"
              class="cancel-button"
              @click="handleCancel"
            >
              取消
            </el-button>
          </el-form-item>
        </el-form>

        <div class="tips">
          <p>密码要求：</p>
          <ul>
            <li>至少6位字符</li>
            <li>建议包含字母和数字</li>
            <li>新密码不能与旧密码相同</li>
          </ul>
        </div>
      </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Lock, Key, CircleCheck, PictureFilled } from '@element-plus/icons-vue'
import { useUserStore } from '@/stores/user'

const router = useRouter()
const userStore = useUserStore()

const formRef = ref()
const loading = ref(false)

const form = reactive({
  old_Password: '',
  new_Password: '',
  confirm_Password: ''
})

const validateConfirmPassword = (rule: any, value: string, callback: any) => {
  if (value !== form.new_Password) {
    callback(new Error('两次输入的新密码不一致'))
  } else {
    callback()
  }
}

const validateNewPassword = (rule: any, value: string, callback: any) => {
  if (value === form.old_Password) {
    callback(new Error('新密码不能与旧密码相同'))
  } else {
    callback()
  }
}

const rules = {
  old_Password: [
    { required: true, message: '请输入旧密码', trigger: 'blur' }
  ],
  new_Password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 6, message: '密码长度至少6位', trigger: 'blur' },
    { validator: validateNewPassword, trigger: 'blur' }
  ],
  confirm_Password: [
    { required: true, message: '请确认新密码', trigger: 'blur' },
    { validator: validateConfirmPassword, trigger: 'blur' }
  ]
}

const handleSubmit = async () => {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    await userStore.changePassword(form.old_Password, form.new_Password)
    ElMessage.success('密码修改成功')
    router.push('/')
  } catch (error: any) {
    const message = error?.response?.data?.error?.message || '修改失败'
    ElMessage.error(message)
  } finally {
    loading.value = false
  }
}

const handleCancel = () => {
  router.push('/')
}
</script>

<style scoped>
.change-Password-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 20px;
}

.change-Password-container {
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

.change-Password-card {
  border-radius: 12px;
  padding: 8px;
}

.change-Password-title {
  font-size: 20px;
  font-weight: 600;
  text-align: center;
  margin: 0 0 24px 0;
  color: var(--color-text-primary);
}

.submit-button {
  width: 100%;
  font-size: 16px;
}

.cancel-button {
  width: 100%;
  font-size: 16px;
}

.tips {
  margin-top: 20px;
  padding-top: 20px;
  border-top: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.tips p {
  margin: 0 0 8px 0;
  font-weight: 500;
}

.tips ul {
  margin: 0;
  padding-left: 20px;
}

.tips li {
  margin: 4px 0;
}
</style>
