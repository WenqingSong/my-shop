import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, useLocation, useNavigate } from 'react-router'

import { AuthCard } from '@/components/auth/auth-card'
import { PasswordInput } from '@/components/auth/password-input'
import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { describeAuthError } from '@/auth/auth-errors'
import { readAuthLocationState } from '@/auth/redirect'
import { useRegister } from '@/auth/use-auth'
import { registerSchema, type RegisterFormValues } from '@/auth/validation'
import { useDocumentTitle } from '@/hooks/use-document-title'

export function RegisterPage() {
  useDocumentTitle('注册 | SurgeCart 商城')

  const location = useLocation()
  const navigate = useNavigate()
  const state = readAuthLocationState(location.state)
  const registerMutation = useRegister()
  const [serverError, setServerError] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<RegisterFormValues>({
    resolver: zodResolver(registerSchema),
    defaultValues: { username: '', password: '' },
  })

  const onSubmit = handleSubmit(async (values) => {
    setServerError(null)

    try {
      await registerMutation.mutateAsync(values)
      // 后端注册接口不签发 Token：注册成功后回到登录页，并预填用户名。
      navigate('/login', {
        replace: true,
        state: { registeredUsername: values.username, from: state.from },
      })
    } catch (error) {
      setServerError(describeAuthError(error, 'register'))
    }
  })

  return (
    <PageContainer width="narrow" className="flex min-h-[60vh] items-center justify-center py-10">
      <AuthCard
        title="注册"
        description="用户名 3-24 位字母或数字；密码 8-24 个字符。注册后使用用户名和密码登录。"
        footer={
          <p className="text-muted-foreground">
            已有账号？
            <Link
              to="/login"
              state={{ from: state.from }}
              className="ml-1 font-medium text-primary hover:text-primary-hover"
            >
              去登录
            </Link>
          </p>
        }
      >
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
              placeholder="3-24 位字母或数字"
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
              autoComplete="new-password"
              placeholder="8-24 个字符"
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

          <Button type="submit" className="w-full" size="lg" isLoading={registerMutation.isPending}>
            注册
          </Button>
        </form>
      </AuthCard>
    </PageContainer>
  )
}
