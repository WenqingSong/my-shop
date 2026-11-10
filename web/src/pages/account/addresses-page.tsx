import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { MapPin, Pencil, Plus, Star, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { deleteAddress, fetchAddresses, updateAddress } from '@/api/addresses'
import { queryKeys } from '@/api/query-keys'
import type { Address } from '@/api/types'
import { useAuthStatus } from '@/auth/use-auth'
import { AddressFormDialog } from '@/components/account/address-form-dialog'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { describeAddressError } from '@/pages/account/account-errors'

function AddressCard({
  address,
  onEdit,
  onSetDefault,
  onDelete,
  isSettingDefault,
}: {
  address: Address
  onEdit: () => void
  onSetDefault: () => void
  onDelete: () => void
  isSettingDefault: boolean
}) {
  return (
    <article
      aria-label={`收货地址：${address.recipient_name}`}
      className="flex h-full flex-col rounded-xl border border-border bg-surface p-5 shadow-card"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-semibold text-foreground">{address.recipient_name}</h3>
        <span className="text-sm text-muted-foreground">{address.phone}</span>
        {address.is_default ? (
          <span className="inline-flex items-center gap-1 rounded-full bg-primary-soft px-2 py-0.5 text-[11px] font-medium text-primary">
            <Star className="size-3" aria-hidden="true" />
            默认
          </span>
        ) : null}
      </div>

      <p className="mt-3 text-sm leading-6 text-foreground">
        {address.province}
        {address.city}
        {address.district}
        <br />
        {address.detail}
      </p>

      <div className="mt-4 flex flex-wrap items-center gap-1 border-t border-border pt-3">
        <Button variant="ghost" size="sm" onClick={onEdit} aria-label={`编辑地址（${address.recipient_name}）`}>
          <Pencil aria-hidden="true" />
          编辑
        </Button>
        {!address.is_default ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={onSetDefault}
            isLoading={isSettingDefault}
            aria-label={`设为默认地址（${address.recipient_name}）`}
          >
            <Star aria-hidden="true" />
            设为默认
          </Button>
        ) : null}
        <Button
          variant="ghost"
          size="sm"
          onClick={onDelete}
          aria-label={`删除地址（${address.recipient_name}）`}
          className="text-danger hover:bg-danger-soft hover:text-danger"
        >
          <Trash2 aria-hidden="true" />
          删除
        </Button>
      </div>
    </article>
  )
}

export function AddressesPage() {
  useDocumentTitle('收货地址 | SurgeCart 商城')

  const { hasSession } = useAuthStatus()
  const queryClient = useQueryClient()
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingAddress, setEditingAddress] = useState<Address | null>(null)
  const [deletingAddress, setDeletingAddress] = useState<Address | null>(null)
  const [feedback, setFeedback] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  const addressesQuery = useQuery({
    queryKey: queryKeys.addresses.list(),
    queryFn: ({ signal }) => fetchAddresses(signal),
    enabled: hasSession,
    staleTime: 30_000,
  })

  const setDefaultMutation = useMutation({
    mutationFn: (address: Address) => updateAddress(address.id, { is_default: true }),
    onSuccess: async () => {
      setActionError(null)
      setFeedback('已设为默认地址')
      await queryClient.invalidateQueries({ queryKey: queryKeys.addresses.all })
    },
    onError: (error) => {
      setActionError(describeAddressError(error))
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (address: Address) => deleteAddress(address.id),
    onSuccess: async () => {
      setActionError(null)
      setFeedback('收货地址已删除')
      setDeletingAddress(null)
      await queryClient.invalidateQueries({ queryKey: queryKeys.addresses.all })
    },
    onError: (error) => {
      setActionError(describeAddressError(error))
      setDeletingAddress(null)
    },
  })

  const openCreate = () => {
    setEditingAddress(null)
    setEditorOpen(true)
  }

  const openEdit = (address: Address) => {
    setEditingAddress(address)
    setEditorOpen(true)
  }

  const addresses = addressesQuery.data?.items ?? []

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold text-foreground">收货地址</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            地址仅本人可见；每名用户最多一个默认地址。
          </p>
        </div>
        <Button onClick={openCreate}>
          <Plus aria-hidden="true" />
          新增地址
        </Button>
      </div>

      {feedback ? (
        <p role="status" className="rounded-lg border border-success/20 bg-success-soft px-4 py-3 text-sm text-success">
          {feedback}
        </p>
      ) : null}

      {actionError ? (
        <p role="alert" className="rounded-lg border border-danger/20 bg-danger-soft px-4 py-3 text-sm text-danger">
          {actionError}
        </p>
      ) : null}

      {addressesQuery.isPending ? <LoadingState label="正在加载收货地址" /> : null}

      {addressesQuery.isError ? (
        <ErrorState
          title="收货地址加载失败"
          description="地址信息暂时无法获取，请稍后重试。"
          onRetry={() => void addressesQuery.refetch()}
        />
      ) : null}

      {!addressesQuery.isPending && !addressesQuery.isError && addresses.length === 0 ? (
        <EmptyState
          icon={<MapPin className="size-6" aria-hidden="true" />}
          title="还没有收货地址"
          description="新增一个收货地址，方便后续下单时快速选择。"
          action={<Button onClick={openCreate}>新增地址</Button>}
        />
      ) : null}

      {addresses.length > 0 ? (
        <ul className="grid gap-4 sm:grid-cols-2">
          {addresses.map((address) => (
            <li key={address.id}>
              <AddressCard
                address={address}
                onEdit={() => openEdit(address)}
                onSetDefault={() => setDefaultMutation.mutate(address)}
                onDelete={() => {
                  setActionError(null)
                  setDeletingAddress(address)
                }}
                isSettingDefault={
                  setDefaultMutation.isPending && setDefaultMutation.variables?.id === address.id
                }
              />
            </li>
          ))}
        </ul>
      ) : null}

      {editorOpen ? (
        <AddressFormDialog
          key={editingAddress ? `edit-${String(editingAddress.id)}` : 'create'}
          open
          onOpenChange={(open) => {
            setEditorOpen(open)
            if (!open) {
              setEditingAddress(null)
            }
          }}
          address={editingAddress}
          onSaved={setFeedback}
        />
      ) : null}

      <Dialog
        open={deletingAddress !== null}
        onOpenChange={(open) => {
          if (!open && !deleteMutation.isPending) {
            setDeletingAddress(null)
          }
        }}
      >
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>删除收货地址？</DialogTitle>
            <DialogDescription>
              {deletingAddress
                ? `将删除「${deletingAddress.recipient_name} ${deletingAddress.phone}」这条地址，删除后不可恢复。`
                : ''}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setDeletingAddress(null)}
              disabled={deleteMutation.isPending}
            >
              取消
            </Button>
            <Button
              variant="danger"
              onClick={() => {
                if (deletingAddress) {
                  deleteMutation.mutate(deletingAddress)
                }
              }}
              isLoading={deleteMutation.isPending}
            >
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
