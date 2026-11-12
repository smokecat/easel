import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, ExternalLink, QrCode as QrIcon, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { Link, useLocation, useParams } from 'wouter'
import { CopyButton } from '@/components/copy-button'
import { QrCode } from '@/components/qr-code'
import { Shares } from '@/components/shares'
import { ErrorState } from '@/components/states'
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
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Versions } from '@/components/versions'
import { ViewportFrame, ViewportPicker, type Viewport } from '@/components/viewport-frame'
import { api, type Prototype } from '@/lib/api'
import { absoluteTime, bytes, relativeTime } from '@/lib/format'
import { KindIcon } from '@/components/kind-icon'
import { kindLabel, kindOf, pausedText } from '@/lib/prototype'

export default function PrototypeDetail() {
  const { name } = useParams<{ name: string }>()
  const { data: p, error, isPending } = useQuery({ queryKey: ['prototype', name], queryFn: () => api.prototype(name) })

  return (
    <div className="space-y-6">
      <Link href="/" className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-sm">
        <ArrowLeft className="size-4" />
        全部原型
      </Link>
      {error && <ErrorState error={error} />}
      {isPending && <Skeleton className="h-96 rounded-xl" />}
      {p && <Detail p={p} />}
    </div>
  )
}

function Detail({ p }: { p: Prototype }) {
  const kind = kindOf(p)
  return (
    <>
      <div className="flex flex-wrap items-start gap-4">
        <div className="bg-muted text-muted-foreground grid size-11 shrink-0 place-items-center rounded-xl">
          <KindIcon kind={kind} className="size-5" />
        </div>
        <div className="min-w-0 flex-1 space-y-1">
          <h1 className="font-heading truncate text-2xl font-semibold tracking-tight">{p.name}</h1>
          <div className="text-muted-foreground flex flex-wrap items-center gap-2 text-sm">
            <Badge variant="secondary">{kindLabel[kind]}</Badge>
            {p.current && (
              <span>
                v{p.current.number} · {p.current.files} 个文件 · {bytes(p.current.bytes)}
              </span>
            )}
            {p.proxy && <span>端口 {p.proxy.port}</span>}
            <span title={absoluteTime(p.updated_at)}>{relativeTime(p.updated_at)}更新</span>
          </div>
        </div>
        <DeleteButton name={p.name} />
      </div>

      {p.proxy?.status === 'paused' && (
        <div role="status" className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
          <p className="font-medium">分享已暂停：{pausedText[p.proxy.paused_reason ?? ''] ?? p.proxy.paused_reason}</p>
          <p className="text-muted-foreground mt-1">
            你自己仍然可以访问。确认端口 {p.proxy.port} 上运行的是正确的程序后，在服务器上重新运行{' '}
            <code className="bg-muted rounded px-1">easel expose {p.proxy.port} --name {p.name}</code> 恢复。
          </p>
        </div>
      )}

      <Tabs defaultValue="preview">
        <TabsList>
          <TabsTrigger value="preview">预览</TabsTrigger>
          {p.serve_kind === 'static' && <TabsTrigger value="versions">版本（{p.versions}）</TabsTrigger>}
          <TabsTrigger value="shares">分享</TabsTrigger>
        </TabsList>
        <TabsContent value="preview" className="pt-4">
          <Preview p={p} />
        </TabsContent>
        {p.serve_kind === 'static' && (
          <TabsContent value="versions" className="pt-4">
            <Versions name={p.name} />
          </TabsContent>
        )}
        <TabsContent value="shares" className="pt-4">
          <Shares name={p.name} />
        </TabsContent>
      </Tabs>
    </>
  )
}

function Preview({ p }: { p: Prototype }) {
  const [viewport, setViewport] = useState<Viewport>('desktop')
  const [reload, setReload] = useState(0)
  const [qr, setQr] = useState(false)
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <ViewportPicker value={viewport} onChange={setViewport} />
        <Button variant="outline" size="sm" onClick={() => setReload((n) => n + 1)}>
          <RefreshCw />
          刷新
        </Button>
        {/* 窄屏时地址栏单独占一行 */}
        <div className="order-last flex min-w-0 basis-full items-center gap-1 sm:order-none sm:flex-1 sm:basis-auto">
          <Input readOnly value={p.preview_url} className="h-8 min-w-0 text-xs" aria-label="预览地址" onFocus={(e) => e.currentTarget.select()} />
          <CopyButton value={p.preview_url} size="icon" label="复制预览地址" />
        </div>
        <Button variant="outline" size="sm" onClick={() => setQr(true)}>
          <QrIcon />
          手机打开
        </Button>
        <Button asChild size="sm">
          <a href={p.preview_url} target="_blank" rel="noopener">
            <ExternalLink />
            新标签页
          </a>
        </Button>
      </div>
      <ViewportFrame src={p.preview_url} viewport={viewport} title={`${p.name} 预览`} reloadKey={reload} />
      <p className="text-muted-foreground text-xs">
        预览区域显示空白或登录页时（比如后台和内容不在同一个注册域），请用「新标签页」打开。
      </p>

      <Dialog open={qr} onOpenChange={setQr}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>在手机上打开</DialogTitle>
            <DialogDescription>
              这是预览地址，手机上需要登录 easel。给别人看请用「分享」里的链接。
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col items-center gap-3">
            <QrCode value={p.preview_url} size={224} className="rounded-md border" />
            <p className="text-muted-foreground w-full truncate text-center text-xs">{p.preview_url}</p>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function DeleteButton({ name }: { name: string }) {
  const qc = useQueryClient()
  const [, navigate] = useLocation()
  const [open, setOpen] = useState(false)
  const [confirm, setConfirm] = useState('')
  const del = useMutation({
    mutationFn: () => api.deletePrototype(name),
    onSuccess: () => {
      toast.success(`已删除 ${name}`)
      qc.invalidateQueries({ queryKey: ['prototypes'] })
      qc.removeQueries({ queryKey: ['prototype', name] })
      navigate('/')
    },
    onError: (e) => toast.error(errorText(e)),
  })
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <Trash2 />
        删除
      </Button>
      <AlertDialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) setConfirm('')
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除 {name}？</AlertDialogTitle>
            <AlertDialogDescription>
              所有版本和分享链接一并删除，预览地址立即失效，不可恢复。输入原型名确认。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} placeholder={name} aria-label="输入原型名确认" autoFocus />
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              disabled={confirm !== name || del.isPending}
              onClick={(e) => {
                e.preventDefault()
                del.mutate()
              }}
            >
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
