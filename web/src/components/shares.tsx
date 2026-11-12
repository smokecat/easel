import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, Link2, Lock, Plus, QrCode as QrIcon } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { CopyButton } from '@/components/copy-button'
import { QrCode } from '@/components/qr-code'
import { EmptyState, ErrorState } from '@/components/states'
import { errorText } from '@/lib/query'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { api, type Share } from '@/lib/api'
import { absoluteTime, relativeTime } from '@/lib/format'

const expiryOptions: { value: string; label: string; secs: number }[] = [
  { value: 'never', label: '不过期', secs: 0 },
  { value: '1d', label: '1 天', secs: 86400 },
  { value: '7d', label: '7 天', secs: 7 * 86400 },
  { value: '30d', label: '30 天', secs: 30 * 86400 },
]

const statusLabel: Record<Share['status'], string> = { active: '有效', expired: '已过期', revoked: '已撤销' }

export function Shares({ name }: { name: string }) {
  const qc = useQueryClient()
  const { data, error, isPending } = useQuery({ queryKey: ['shares', name], queryFn: () => api.shares(name) })
  const [creating, setCreating] = useState(false)
  const [showAll, setShowAll] = useState(false)
  const [qr, setQr] = useState<Share | null>(null)
  const [revoking, setRevoking] = useState<Share | null>(null)

  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeShare(id),
    onSuccess: () => {
      toast.success('已撤销，正在访问的人下一次请求就会失效')
      qc.invalidateQueries({ queryKey: ['shares', name] })
    },
    onError: (e) => toast.error(errorText(e)),
  })

  if (error) return <ErrorState error={error} />
  if (isPending) return <Skeleton className="h-32 rounded-lg" />

  const inactive = data.filter((s) => s.status !== 'active').length
  const shown = showAll ? data : data.filter((s) => s.status === 'active')

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-muted-foreground text-sm">把链接发给别人即可访问，不需要账号。链接随时可以再复制或撤销。</p>
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus />
          新建分享链接
        </Button>
      </div>

      {shown.length === 0 ? (
        <EmptyState title={data.length ? '没有有效的分享链接' : '还没有分享链接'} />
      ) : (
        <ul className="divide-y rounded-lg border">
          {shown.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
              <Link2 className="text-muted-foreground size-4 shrink-0" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{s.note || `链接 #${s.id}`}</span>
                  {s.status !== 'active' && <Badge variant="secondary">{statusLabel[s.status]}</Badge>}
                  {s.has_password && (
                    <Badge variant="outline">
                      <Lock />
                      密码
                    </Badge>
                  )}
                </div>
                <p className="text-muted-foreground truncate text-xs">
                  {relativeTime(s.created_at)}创建 ·{' '}
                  {s.expires_at ? (
                    <span title={absoluteTime(s.expires_at)}>{relativeTime(s.expires_at)}过期</span>
                  ) : (
                    '不过期'
                  )}
                </p>
              </div>
              {s.status === 'active' && (
                <div className="flex items-center gap-1">
                  <CopyButton value={s.url} label="复制链接" />
                  <Button size="sm" variant="ghost" onClick={() => setQr(s)} aria-label="显示二维码">
                    <QrIcon />
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setRevoking(s)}>
                    <Ban />
                    撤销
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {inactive > 0 && (
        <Button variant="link" size="sm" className="px-0" onClick={() => setShowAll((v) => !v)}>
          {showAll ? '只看有效的' : `显示已过期 / 已撤销的（${inactive}）`}
        </Button>
      )}

      <CreateShare name={name} open={creating} onOpenChange={setCreating} />

      <Dialog open={!!qr} onOpenChange={(o) => !o && setQr(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{qr?.note || `链接 #${qr?.id}`}</DialogTitle>
            <DialogDescription>用手机扫码打开。</DialogDescription>
          </DialogHeader>
          {qr && (
            <div className="flex flex-col items-center gap-3">
              <QrCode value={qr.url} size={224} className="rounded-md border" />
              <p className="text-muted-foreground w-full truncate text-center text-xs">{qr.url}</p>
            </div>
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!revoking} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>撤销这个分享链接？</AlertDialogTitle>
            <AlertDialogDescription>链接立即失效，已经打开的人下一次请求就无法访问。不能恢复，需要时请新建。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => revoking && revoke.mutate(revoking.id)}
            >
              撤销
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function CreateShare({ name, open, onOpenChange }: { name: string; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient()
  const [note, setNote] = useState('')
  const [expiry, setExpiry] = useState('7d')
  const [usePassword, setUsePassword] = useState(false)
  const [password, setPassword] = useState('')
  const [created, setCreated] = useState<Share | null>(null)

  const create = useMutation({
    mutationFn: () =>
      api.createShare(name, {
        note: note.trim() || undefined,
        password: usePassword ? password : undefined,
        expires_in_seconds: expiryOptions.find((o) => o.value === expiry)?.secs || undefined,
      }),
    onSuccess: (s) => {
      setCreated(s)
      qc.invalidateQueries({ queryKey: ['shares', name] })
    },
    onError: (e) => toast.error(errorText(e)),
  })

  const reset = (o: boolean) => {
    onOpenChange(o)
    if (!o) {
      setNote('')
      setExpiry('7d')
      setUsePassword(false)
      setPassword('')
      setCreated(null)
    }
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    create.mutate()
  }
  const pwTooShort = usePassword && password.length < 8

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogContent className="sm:max-w-md">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>分享链接已创建</DialogTitle>
              <DialogDescription>
                {created.has_password ? '访客打开后需要输入你设置的密码。' : '拿到链接的人都能访问。'}
                {created.expires_at ? ` ${relativeTime(created.expires_at)}过期。` : ''}
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col items-center gap-3">
              <QrCode value={created.url} size={192} className="rounded-md border" />
              <div className="flex w-full items-center gap-2">
                <Input readOnly value={created.url} onFocus={(e) => e.currentTarget.select()} aria-label="分享链接" />
                <CopyButton value={created.url} />
              </div>
            </div>
            <DialogFooter>
              <Button onClick={() => reset(false)}>完成</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="space-y-4">
            <DialogHeader>
              <DialogTitle>新建分享链接</DialogTitle>
              <DialogDescription>分享的是 {name} 的最新版本，之后推送的新版本也能看到。</DialogDescription>
            </DialogHeader>
            <div className="space-y-2">
              <Label htmlFor="share-note">备注</Label>
              <Input
                id="share-note"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="比如：给设计评审"
                maxLength={200}
              />
            </div>
            <div className="space-y-2">
              <Label>有效期</Label>
              <Select value={expiry} onValueChange={setExpiry}>
                <SelectTrigger className="w-full" aria-label="有效期">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {expiryOptions.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <div className="flex items-center gap-2">
                <Checkbox id="share-pw" checked={usePassword} onCheckedChange={(v) => setUsePassword(v === true)} />
                <Label htmlFor="share-pw">需要密码</Label>
              </div>
              {usePassword && (
                <>
                  <Input
                    type="password"
                    autoComplete="new-password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="至少 8 个字符"
                    aria-label="访客密码"
                    aria-invalid={password.length > 0 && pwTooShort}
                  />
                  <p className="text-muted-foreground text-xs">密码只保存哈希，忘了只能新建链接。</p>
                </>
              )}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => reset(false)}>
                取消
              </Button>
              <Button type="submit" disabled={create.isPending || pwTooShort}>
                创建
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
