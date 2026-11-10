import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'

import { createAddress, updateAddress } from '@/api/addresses'
import { queryKeys } from '@/api/query-keys'
import type { Address } from '@/api/types'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { describeAddressError } from '@/pages/account/account-errors'
import { addressFormSchema, type AddressFormValues } from '@/pages/account/address-schema'

export interface AddressFormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** null 表示新增；非 null 表示编辑。 */
  address: Address | null
  onSaved: (message: string) => void
}

/** 新增/编辑收货地址表单。首条地址由后端自动置默认，UI 只做提示。 */
export function AddressFormDialog({
  open,
  onOpenChange,
  address,
  onSaved,
}: AddressFormDialogProps) {
  const queryClient = useQueryClient()
  const [serverError, setServerError] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<AddressFormValues>({
    resolver: zodResolver(addressFormSchema),
    defaultValues: address
      ? {
          recipient_name: address.recipient_name,
          phone: address.phone,
          province: address.province,
          city: address.city,
          district: address.district,
          detail: address.detail,
          is_default: address.is_default,
        }
      : {
          recipient_name: '',
          phone: '',
          province: '',
          city: '',
          district: '',
          detail: '',
          is_default: false,
        },
  })

  const saveMutation = useMutation({
    mutationFn: (values: AddressFormValues) =>
      address
        ? updateAddress(address.id, values)
        : createAddress({
            ...values,
            is_default: values.is_default,
          }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.addresses.all })
      onSaved(address ? '收货地址已更新' : '收货地址已新增')
      onOpenChange(false)
    },
    onError: (error) => {
      setServerError(describeAddressError(error))
    },
  })

  const onSubmit = handleSubmit((values) => {
    setServerError(null)
    saveMutation.mutate(values)
  })

  const handleOpenChange = (nextOpen: boolean) => {
    if (!saveMutation.isPending) {
      onOpenChange(nextOpen)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{address ? '编辑收货地址' : '新增收货地址'}</DialogTitle>
          <DialogDescription>
            省/市/区为文本输入，请填写完整地址。首条地址会自动设为默认地址。
          </DialogDescription>
        </DialogHeader>

        {serverError ? (
          <div
            role="alert"
            className="rounded-lg border border-danger/20 bg-danger-soft px-4 py-3 text-sm text-danger"
          >
            {serverError}
          </div>
        ) : null}

        <form onSubmit={onSubmit} noValidate className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label htmlFor="address-recipient" className="mb-1.5 block text-sm font-medium text-foreground">
                收货人姓名
              </label>
              <Input
                id="address-recipient"
                autoComplete="name"
                placeholder="例如：张三"
                aria-invalid={Boolean(errors.recipient_name) || undefined}
                aria-describedby={errors.recipient_name ? 'address-recipient-error' : undefined}
                {...register('recipient_name')}
              />
              {errors.recipient_name ? (
                <p id="address-recipient-error" className="mt-1.5 text-xs text-danger">
                  {errors.recipient_name.message}
                </p>
              ) : null}
            </div>

            <div>
              <label htmlFor="address-phone" className="mb-1.5 block text-sm font-medium text-foreground">
                手机号
              </label>
              <Input
                id="address-phone"
                type="tel"
                inputMode="tel"
                autoComplete="tel"
                placeholder="11 位手机号"
                aria-invalid={Boolean(errors.phone) || undefined}
                aria-describedby={errors.phone ? 'address-phone-error' : undefined}
                {...register('phone')}
              />
              {errors.phone ? (
                <p id="address-phone-error" className="mt-1.5 text-xs text-danger">
                  {errors.phone.message}
                </p>
              ) : null}
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            {(
              [
                ['province', '省份', '例如：广东省', 'address-level1'],
                ['city', '城市', '例如：深圳市', 'address-level2'],
                ['district', '区/县', '例如：南山区', 'address-level3'],
              ] as const
            ).map(([field, label, placeholder, autoComplete]) => (
              <div key={field}>
                <label htmlFor={`address-${field}`} className="mb-1.5 block text-sm font-medium text-foreground">
                  {label}
                </label>
                <Input
                  id={`address-${field}`}
                  autoComplete={autoComplete}
                  placeholder={placeholder}
                  aria-invalid={Boolean(errors[field]) || undefined}
                  aria-describedby={errors[field] ? `address-${field}-error` : undefined}
                  {...register(field)}
                />
                {errors[field] ? (
                  <p id={`address-${field}-error`} className="mt-1.5 text-xs text-danger">
                    {errors[field]?.message}
                  </p>
                ) : null}
              </div>
            ))}
          </div>

          <div>
            <label htmlFor="address-detail" className="mb-1.5 block text-sm font-medium text-foreground">
              详细地址
            </label>
            <Textarea
              id="address-detail"
              autoComplete="street-address"
              placeholder="街道、门牌号、楼层与房间号"
              aria-invalid={Boolean(errors.detail) || undefined}
              aria-describedby={errors.detail ? 'address-detail-error' : undefined}
              {...register('detail')}
            />
            {errors.detail ? (
              <p id="address-detail-error" className="mt-1.5 text-xs text-danger">
                {errors.detail.message}
              </p>
            ) : null}
          </div>

          <label className="flex items-center gap-2 text-sm text-foreground">
            <input
              type="checkbox"
              className="size-4 rounded border-border-strong text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
              {...register('is_default')}
            />
            设为默认地址
          </label>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => handleOpenChange(false)}
              disabled={saveMutation.isPending}
            >
              取消
            </Button>
            <Button type="submit" isLoading={saveMutation.isPending}>
              保存地址
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
