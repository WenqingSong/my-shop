import { zodResolver } from '@hookform/resolvers/zod'
import { CheckCircle2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, useLocation } from 'react-router'

import { AuthCard } from '@/components/auth/auth-card'
import { PasswordInput } from '@/components/auth/password-input'
import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { readAuthLocationState } from '@/auth/redirect'
import { useLogin } from '@/auth/use-auth'
import { describeAuthError } from '@/auth/auth-errors'
import { loginSchema, type LoginFormValues } from '@/auth/validation'
import { useDocumentTitle } from '@/hooks/use-document-title'

export function LoginPage() {
  useDocumentTitle('登录 | SurgeCart 商城')

  const location = useLocation()
  const state = readAuthLocationState(location.state)
  const login = useLogin()
  const [serverError, setServerError] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: {
      username: state.registeredUsername ?? '',
      password: '',
    },
  })

  const onSubmit = handleSubmit(async (values) => {
    setServerError(null)

    try {
      // 成功后 GuestOnly 会读取 location.state.from，把用户送回原受保护页面。
      await login.mutateAsync(values)
    } catch (error) {
      setServerError(describeAuthError(error, 'login'))
    }
  })

  return (
    <PageContainer width="narrow" className="flex min-h-[60vh] items-center justify-center py-10">
      <AuthCard
        title="登录"
        description="登录后可进入账户页面。商品浏览无需登录。"
        footer={
          <p className="text-muted-foreground">
            还没有账号？
            <Link
              to="/register"
              state={{ from: state.from }}
              className="ml-1 font-medium text-primary hover:text-primary-hover"
            >
              立即注册
            </Link>
          </p>
        }
      >
        {state.registeredUsername ? (
          <div
            role="status"
            className="mb-4 flex items-start gap-2 rounded-lg border border-success/20 bg-success-soft px-4 py-3 text-sm text-success"
          >
            <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
            <p>注册成功，请使用新账号登录。</p>
          </div>
        ) : null}

        {serverError ? (
          <div
            role="alert"
            className="mb-4 rounded-lg border border-danger/20 bg-danger-soft px-4 py-3 text-sm text-danger"
          >
            {serverError}
          </div>
        ) : null}

        <form onSubmit={onSubmit} noValidate className="space-y-4">
          <div>
            <label htmlFor="username" className="mb-1.5 block text-sm font-medium text-foreground">
              用户名
            </label>
            <Input
              id="username"
              type="text"
              autoComplete="username"
              placeholder="请输入用户名"
              aria-invalid={Boolean(errors.username) || undefined}
              aria-describedby={errors.username ? 'username-error' : undefined}
              {...register('username')}
            />
            {errors.username ? (
              <p id="username-error" className="mt-1.5 text-xs text-danger">
                {errors.username.message}
              </p>
            ) : null}
          </div>

          <div>
            <label htmlFor="password" className="mb-1.5 block text-sm font-medium text-foreground">
              密码
            </label>
            <PasswordInput
              id="password"
              autoComplete="current-password"
              placeholder="请输入密码"
              aria-invalid={Boolean(errors.password) || undefined}
              aria-describedby={errors.password ? 'password-error' : undefined}
              {...register('password')}
            />
            {errors.password ? (
              <p id="password-error" className="mt-1.5 text-xs text-danger">
                {errors.password.message}
              </p>
            ) : null}
          </div>

          <Button type="submit" className="w-full" size="lg" isLoading={login.isPending}>
            登录
          </Button>
        </form>
      </AuthCard>
    </PageContainer>
  )
}
