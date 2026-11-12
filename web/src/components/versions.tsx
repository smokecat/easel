import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Columns2, ExternalLink, History, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
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
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { ViewportFrame, ViewportPicker, type Viewport } from '@/components/viewport-frame'
import { api, type VersionInfo } from '@/lib/api'
import { absoluteTime, bytes, relativeTime } from '@/lib/format'

const originLabel: Record<string, string> = { upload: '推送', rollback: '回滚', build: '构建' }

export function Versions({ name }: { name: string }) {
  const qc = useQueryClient()
  const { data, error, isPending } = useQuery({ queryKey: ['versions', name], queryFn: () => api.versions(name) })
  const [picked, setPicked] = useState<number[]>([])
  const [comparing, setComparing] = useState(false)
  const [rollbackTo, setRollbackTo] = useState<VersionInfo | null>(null)

  const rollback = useMutation({
    mutationFn: (v: number) => api.rollback(name, v),
    onSuccess: (res) => {
      toast.success(res.version.unchanged ? '已经是这个版本的内容' : `已回滚，新版本 v${res.version.number}`)
      qc.invalidateQueries({ queryKey: ['versions', name] })
      qc.invalidateQueries({ queryKey: ['prototype', name] })
      qc.invalidateQueries({ queryKey: ['prototypes'] })
    },
    onError: (e) => toast.error(errorText(e)),
  })

  const toggle = (n: number) =>
    setPicked((p) => (p.includes(n) ? p.filter((x) => x !== n) : [...p.slice(-1), n]))

  if (error) return <ErrorState error={error} />
  if (isPending) return <Skeleton className="h-40 rounded-lg" />

  const pair = picked.length === 2 ? data.filter((v) => picked.includes(v.number)).sort((a, b) => a.number - b.number) : []

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-muted-foreground text-sm">
          勾选两个版本可以并排对比。回滚会新建一个版本，历史不会被改写。
        </p>
        <Button size="sm" variant="outline" disabled={pair.length !== 2} onClick={() => setComparing(true)}>
          <Columns2 />
          对比{pair.length === 2 ? ` v${pair[0].number} 和 v${pair[1].number}` : ''}
        </Button>
      </div>
      <ol className="divide-y rounded-lg border">
        {data.map((v) => (
          <li key={v.number} className="flex flex-wrap items-center gap-3 px-4 py-3">
            <Checkbox
              checked={picked.includes(v.number)}
              onCheckedChange={() => toggle(v.number)}
              aria-label={`选择 v${v.number} 用于对比`}
            />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium tabular-nums">v{v.number}</span>
                {v.current && <Badge>当前</Badge>}
                {v.origin && v.origin !== 'upload' && <Badge variant="secondary">{originLabel[v.origin] ?? v.origin}</Badge>}
                {v.note && <span className="truncate text-sm">{v.note}</span>}
              </div>
              <p className="text-muted-foreground text-xs">
                <span title={absoluteTime(v.created_at)}>{relativeTime(v.created_at)}</span> · {v.files} 个文件 ·{' '}
                {bytes(v.bytes)}
              </p>
            </div>
            <div className="flex items-center gap-1">
              <Button asChild size="sm" variant="ghost">
                <a href={v.url} target="_blank" rel="noopener">
                  <ExternalLink />
                  打开
                </a>
              </Button>
              {!v.current && (
                <Button size="sm" variant="ghost" onClick={() => setRollbackTo(v)} disabled={rollback.isPending}>
                  <RotateCcw />
                  回滚到此版本
                </Button>
              )}
            </div>
          </li>
        ))}
      </ol>

      <AlertDialog open={!!rollbackTo} onOpenChange={(o) => !o && setRollbackTo(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>回滚到 v{rollbackTo?.number}？</AlertDialogTitle>
            <AlertDialogDescription>
              会新建一个内容与 v{rollbackTo?.number} 相同的版本，预览地址和分享链接立即显示它。之前的版本都保留。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => rollbackTo && rollback.mutate(rollbackTo.number)}>
              <History />
              回滚
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {pair.length === 2 && <Compare open={comparing} onOpenChange={setComparing} a={pair[0]} b={pair[1]} />}
    </div>
  )
}

function Compare({
  open,
  onOpenChange,
  a,
  b,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  a: VersionInfo
  b: VersionInfo
}) {
  const [viewport, setViewport] = useState<Viewport>('mobile')
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[95svh] w-[min(96vw,1600px)] max-w-none overflow-y-auto sm:max-w-none">
        <DialogHeader className="flex-row flex-wrap items-center justify-between gap-2 pr-10">
          <div>
            <DialogTitle>
              对比 v{a.number} 和 v{b.number}
            </DialogTitle>
            <DialogDescription>两边是固定版本的地址，内容不会再变。</DialogDescription>
          </div>
          <ViewportPicker value={viewport} onChange={setViewport} />
        </DialogHeader>
        <div className="grid gap-4 md:grid-cols-2">
          {[a, b].map((v) => (
            <figure key={v.number} className="space-y-2">
              <figcaption className="text-sm">
                <span className="font-medium">v{v.number}</span>
                {v.note && <span className="text-muted-foreground"> · {v.note}</span>}
              </figcaption>
              <ViewportFrame src={v.url} viewport={viewport} title={`v${v.number}`} maxHeight={640} />
            </figure>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
