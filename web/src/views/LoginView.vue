<template>
  <div class="login-page">
    <div class="login-card card">
      <h1 class="login-card__title">个人记账</h1>
      <p class="login-card__desc">请输入访问令牌（Token）登录</p>
      <el-form @submit.prevent>
        <el-form-item :error="error">
          <el-input
            v-model="token"
            type="password"
            placeholder="访问令牌"
            show-password
            size="large"
            autofocus
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
          登录
        </el-button>
      </el-form>
      <p class="login-card__tip">令牌位于服务端 data 目录 meta（auth_token），详见部署文档</p>
    </div>
  </div>
</template>

<script setup lang="ts">
/** 登录页：token 校验 → localStorage；错误 token 提示 BAD_TOKEN。 */
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { errorText } from '@/api/client'

const token = ref('')
const loading = ref(false)
const error = ref('')
const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

async function submit(): Promise<void> {
  if (!token.value.trim()) {
    error.value = '请输入令牌'
    return
  }
  loading.value = true
  error.value = ''
  try {
    const ok = await auth.login(token.value.trim())
    if (ok) {
      ElMessage.success('登录成功')
      const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
      await router.replace(redirect)
    }
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped lang="scss">
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-page);
}

.login-card {
  width: 360px;
  padding: 32px 28px;

  &__title {
    margin: 0 0 4px;
    font-size: 22px;
    text-align: center;
  }

  &__desc {
    margin: 0 0 20px;
    text-align: center;
    color: var(--text-2);
    font-size: 13px;
  }

  &__tip {
    margin: 16px 0 0;
    font-size: 12px;
    color: var(--text-3);
    text-align: center;
  }
}
</style>
