import { useQuery } from '@tanstack/react-query'
import { ExternalLink, Search, Upload } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'wouter'
import { EmptyState, ErrorState } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { KindIcon } from '@/components/kind-icon'
import { api, type Prototype } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import { filterPrototypes, kindLabel, kindOf, pausedText, type Filter } from '@/lib/prototype'

export default function PrototypeList() {
  const { data, error, isPending } = useQuery({ queryKey: ['prototypes'], queryFn: api.prototypes })
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const shown = useMemo(() => filterPrototypes(data ?? [], q, filter), [data, q, filter])

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="font-heading text-2xl font-semibold tracking-tight">原型</h1>
          <p className="text-muted-foreground text-sm">
            {data ? `共 ${data.length} 个` : ' '}
          </p>
        </div>
        <Button asChild>
          <Link href="/upload">
            <Upload />
            上传
          </Link>
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full max-w-xs">
          <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" aria-hidden />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="搜索名字或版本备注"
            className="pl-8"
            aria-label="搜索原型"
          />
        </div>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={filter}
          onValueChange={(v) => v && setFilter(v as Filter)}
          aria-label="按类型筛选"
        >
          <ToggleGroupItem value="all">全部</ToggleGroupItem>
          <ToggleGroupItem value="site">站点</ToggleGroupItem>
          <ToggleGroupItem value="assets">素材</ToggleGroupItem>
          <ToggleGroupItem value="proxy">端口代理</ToggleGroupItem>
        </ToggleGroup>
      </div>

      {error && <ErrorState error={error} />}
      {isPending && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-32 rounded-xl" />
          ))}
        </div>
      )}
      {data && data.length === 0 && (
        <EmptyState title="还没有原型">
          在服务器上运行 <code className="bg-muted rounded px-1">easel push ./dist</code> 或{' '}
          <code className="bg-muted rounded px-1">easel expose 5173</code>，或者
          <Link href="/upload" className="text-foreground underline underline-offset-4">
            拖拽上传
          </Link>
          。
        </EmptyState>
      )}
      {data && data.length > 0 && shown.length === 0 && <EmptyState title="没有匹配的原型" />}

      <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {shown.map((p) => (
          <li key={p.name}>
            <PrototypeCard p={p} />
          </li>
        ))}
      </ul>
    </div>
  )
}

function PrototypeCard({ p }: { p: Prototype }) {
  const kind = kindOf(p)
  return (
    <Card className="group hover:border-foreground/20 relative gap-3 p-4 transition-colors">
      <div className="flex items-start gap-3">
        <div className="bg-muted text-muted-foreground grid size-9 shrink-0 place-items-center rounded-lg">
          <KindIcon kind={kind} className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <Link href={`/p/${p.name}`} className="block truncate font-medium after:absolute after:inset-0">
            {p.name}
          </Link>
          <p className="text-muted-foreground truncate text-xs">{new URL(p.preview_url).host}</p>
        </div>
        <Button
          asChild
          variant="ghost"
          size="icon"
          className="relative z-10 -mt-1 -mr-1 opacity-60 group-hover:opacity-100"
          aria-label={`新标签页打开 ${p.name}`}
        >
          <a href={p.preview_url} target="_blank" rel="noopener">
            <ExternalLink />
          </a>
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <Badge variant="secondary">{kindLabel[kind]}</Badge>
        {p.current && <Badge variant="outline">v{p.current.number}</Badge>}
        {p.proxy && <Badge variant="outline">:{p.proxy.port}</Badge>}
        {p.proxy?.status === 'paused' && (
          <Badge variant="destructive" title={pausedText[p.proxy.paused_reason ?? ''] ?? ''}>
            分享已暂停
          </Badge>
        )}
        <span className="text-muted-foreground ml-auto" title={p.updated_at}>
          {relativeTime(p.updated_at)}更新
        </span>
      </div>
      {p.current?.note && <p className="text-muted-foreground line-clamp-1 text-sm">{p.current.note}</p>}
    </Card>
  )
}
