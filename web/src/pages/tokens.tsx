import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, KeyRound, Plus, TriangleAlert } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { CopyButton } from '@/components/copy-button'
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
import { api, type Token } from '@/lib/api'
import { absoluteTime, relativeTime } from '@/lib/format'

const scopeInfo: { scope: string; label: string; hint: string }[] = [
  { scope: 'read', label: '读取', hint: '列出原型和版本' },
  { scope: 'write', label: '推送', hint: '推送新版本、删除原型' },
  { scope: 'share', label: '分享', hint: '创建、撤销分享链接' },
  { scope: 'proxy', label: '端口代理', hint: '把服务器本机端口注册成原型（能让服务器上的本机服务对外可见，谨慎授予）' },
]

const expiryOptions = [
  { value: 'never', label: '不过期', secs: 0 },
  { value: '30d', label: '30 天', secs: 30 * 86400 },
  { value: '90d', label: '90 天', secs: 90 * 86400 },
  { value: '365d', label: '1 年', secs: 365 * 86400 },
]

const statusLabel: Record<Token['status'], string> = { active: '有效', expired: '已过期', revoked: '已撤销' }

export default function Tokens() {
  const qc = useQueryClient()
  const { data, error, isPending } = useQuery({ queryKey: ['tokens'], queryFn: api.tokens })
  const [creating, setCreating] = useState(false)
  const [revoking, setRevoking] = useState<Token | null>(null)
  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeToken(id),
    onSuccess: () => {
      toast.success('已撤销')
      qc.invalidateQueries({ queryKey: ['tokens'] })
    },
    onError: (e) => toast.error(errorText(e)),
  })

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="font-heading text-2xl font-semibold tracking-tight">API Token</h1>
          <p className="text-muted-foreground text-sm">
            给其他机器上的 CLI 和 agent 用：<code className="bg-muted rounded px-1">easel login --server {window.location.origin}</code>
          </p>
        </div>
        <Button onClick={() => setCreating(true)}>
          <Plus />
          新建 Token
        </Button>
      </div>

      {error && <ErrorState error={error} />}
      {isPending && <Skeleton className="h-32 rounded-lg" />}
      {data && data.length === 0 && <EmptyState title="还没有 token">同一台机器上的 CLI 经本机 socket 连接，不需要 token。</EmptyState>}
      {data && data.length > 0 && (
        <ul className="divide-y rounded-lg border">
          {data.map((t) => (
            <li key={t.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
              <KeyRound className="text-muted-foreground size-4 shrink-0" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{t.name}</span>
                  <code className="text-muted-foreground text-xs">{t.hint}…</code>
                  {t.status !== 'active' && <Badge variant="secondary">{statusLabel[t.status]}</Badge>}
                  {t.scopes.map((s) => (
                    <Badge key={s} variant="outline">
                      {scopeInfo.find((i) => i.scope === s)?.label ?? s}
                    </Badge>
                  ))}
                </div>
                <p className="text-muted-foreground text-xs">
                  <span title={absoluteTime(t.created_at)}>{relativeTime(t.created_at)}创建</span> ·{' '}
                  {t.last_used_at ? `${relativeTime(t.last_used_at)}用过` : '从未使用'}
                  {t.expires_at && ` · ${relativeTime(t.expires_at)}过期`}
                </p>
              </div>
              {t.status === 'active' && (
                <Button size="sm" variant="ghost" onClick={() => setRevoking(t)}>
                  <Ban />
                  撤销
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      <CreateToken open={creating} onOpenChange={setCreating} />

      <AlertDialog open={!!revoking} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>撤销 token「{revoking?.name}」？</AlertDialogTitle>
            <AlertDialogDescription>用它登录的 CLI 和 agent 立即失去访问权，不可恢复。</AlertDialogDescription>
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

function CreateToken({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>(['read', 'write'])
  const [expiry, setExpiry] = useState('90d')
  const [created, setCreated] = useState<Token | null>(null)
  const create = useMutation({
    mutationFn: () =>
      api.createToken({ name: name.trim(), scopes, expires_in_seconds: expiryOptions.find((o) => o.value === expiry)?.secs || undefined }),
    onSuccess: (t) => {
      setCreated(t)
      qc.invalidateQueries({ queryKey: ['tokens'] })
    },
    onError: (e) => toast.error(errorText(e)),
  })
  const close = (o: boolean) => {
    onOpenChange(o)
    if (!o) {
      setName('')
      setScopes(['read', 'write'])
      setExpiry('90d')
      setCreated(null)
    }
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    create.mutate()
  }
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        {created?.token ? (
          <>
            <DialogHeader>
              <DialogTitle>Token 已创建</DialogTitle>
              <DialogDescription>这是唯一一次显示明文，请现在复制保存。</DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-2">
              <Input readOnly value={created.token} className="font-mono text-xs" onFocus={(e) => e.currentTarget.select()} aria-label="token" />
              <CopyButton value={created.token} />
            </div>
            <p className="text-muted-foreground flex items-start gap-1.5 text-xs">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              在另一台机器上运行 easel login --server {window.location.origin}，粘贴这个 token。
            </p>
            <DialogFooter>
              <Button onClick={() => close(false)}>完成</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="space-y-4">
            <DialogHeader>
              <DialogTitle>新建 Token</DialogTitle>
              <DialogDescription>按需授予权限；token 不能管理其他 token。</DialogDescription>
            </DialogHeader>
            <div className="space-y-2">
              <Label htmlFor="tok-name">名字</Label>
              <Input id="tok-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="比如：laptop、ci" maxLength={100} required />
            </div>
            <fieldset className="space-y-2">
              <legend className="mb-2 text-sm font-medium">权限</legend>
              {scopeInfo.map((s) => (
                <div key={s.scope} className="flex items-start gap-2">
                  <Checkbox
                    id={`scope-${s.scope}`}
                    checked={scopes.includes(s.scope)}
                    onCheckedChange={(v) => setScopes((cur) => (v === true ? [...cur, s.scope] : cur.filter((x) => x !== s.scope)))}
                    className="mt-0.5"
                  />
                  <Label htmlFor={`scope-${s.scope}`} className="flex-col items-start gap-0.5 font-normal">
                    <span className="font-medium">{s.label}</span>
                    <span className="text-muted-foreground text-xs">{s.hint}</span>
                  </Label>
                </div>
              ))}
            </fieldset>
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
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => close(false)}>
                取消
              </Button>
              <Button type="submit" disabled={!name.trim() || scopes.length === 0 || create.isPending}>
                创建
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
