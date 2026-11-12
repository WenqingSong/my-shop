import { Info } from 'lucide-react'

import { useAuthStatus } from '@/auth/use-auth'
import { useDocumentTitle } from '@/hooks/use-document-title'

/**
 * 个人资料（只读）。
 *
 * 后端当前只有 GET /me，返回 id/username；没有资料更新接口。
 * 因此本页不提供编辑表单，更不通过本地状态伪造“保存成功”。
 */
export function ProfilePage() {
  useDocumentTitle('个人资料 | SurgeCart 商城')

  const { user } = useAuthStatus()

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold text-foreground">个人资料</h2>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          当前账号的基本信息来自服务端 <code className="font-mono text-xs">GET /me</code>。
        </p>
      </div>

      <section
        aria-labelledby="profile-fields"
        className="rounded-2xl border border-border bg-surface p-6 shadow-card"
      >
        <h3 id="profile-fields" className="text-base font-semibold text-foreground">
          账户信息
        </h3>
        <dl className="mt-4 grid gap-x-6 gap-y-4 text-sm sm:grid-cols-[120px_minmax(0,1fr)]">
          <dt className="text-muted-foreground">用户 ID</dt>
          <dd className="tabular-nums font-medium text-foreground">{user?.id ?? '—'}</dd>
          <dt className="text-muted-foreground">用户名</dt>
          <dd className="font-medium text-foreground">{user?.username ?? '—'}</dd>
        </dl>
      </section>

      <div
        role="note"
        className="flex items-start gap-2.5 rounded-lg border border-border bg-gray-25 px-4 py-3"
      >
        <Info className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <div className="min-w-0 text-sm leading-6 text-muted-foreground">
          <p className="font-medium text-foreground">资料修改暂未开放</p>
          <p className="mt-0.5 text-xs leading-5">
            后端当前仅提供只读用户信息，没有昵称、头像、手机号或邮箱等资料修改接口。
            本页不会伪造保存操作；待后端契约补齐后再接入编辑能力。
          </p>
        </div>
      </div>
    </div>
  )
}
