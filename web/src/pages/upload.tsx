import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, FileArchive, FolderUp, UploadCloud, X } from 'lucide-react'
import { useRef, useState, type DragEvent, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Link } from 'wouter'
import { errorText } from '@/lib/query'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api, type PushResult } from '@/lib/api'
import { isArchive, readDrop, readPicked } from '@/lib/files'
import { bytes, slugify, validName } from '@/lib/format'
import { tar, type TarEntry } from '@/lib/tar'
import { cn } from '@/lib/utils'

/** 选中的内容：一个压缩包原样上传，或一组文件在浏览器里打成 tar。 */
type Picked = { kind: 'archive'; file: File } | { kind: 'files'; entries: TarEntry[]; root: string | null }

function describe(p: Picked) {
  if (p.kind === 'archive') return { title: p.file.name, detail: bytes(p.file.size), suggested: p.file.name.replace(/\.(zip|tar|tgz|tar\.gz)$/i, '') }
  const total = p.entries.reduce((n, e) => n + e.file.size, 0)
  const title = p.root ?? (p.entries.length === 1 ? p.entries[0].path : `${p.entries.length} 个文件`)
  return { title, detail: `${p.entries.length} 个文件 · ${bytes(total)}`, suggested: p.root ?? (p.entries.length === 1 ? p.entries[0].path.replace(/\.[^.]+$/, '') : '') }
}

export default function UploadPage() {
  const qc = useQueryClient()
  const { data: existing } = useQuery({ queryKey: ['prototypes'], queryFn: api.prototypes })
  const [picked, setPicked] = useState<Picked | null>(null)
  const [name, setName] = useState('')
  const [note, setNote] = useState('')
  const [spa, setSpa] = useState(false)
  const [dragging, setDragging] = useState(false)
  const [result, setResult] = useState<PushResult | null>(null)
  const folderInput = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const choose = (p: Picked) => {
    if (p.kind === 'files' && p.entries.length === 0) {
      toast.error('没有读到文件')
      return
    }
    setPicked(p)
    setResult(null)
    if (!name) setName(slugify(describe(p).suggested))
  }

  const onDrop = async (e: DragEvent) => {
    e.preventDefault()
    setDragging(false)
    const files = Array.from(e.dataTransfer.files)
    if (files.length === 1 && isArchive(files[0])) {
      choose({ kind: 'archive', file: files[0] })
      return
    }
    try {
      choose({ kind: 'files', ...(await readDrop(e.dataTransfer)) })
    } catch (err) {
      toast.error(errorText(err))
    }
  }

  const push = useMutation({
    mutationFn: () => {
      if (!picked) throw new Error('还没有选择文件')
      const body = picked.kind === 'archive' ? picked.file : tar(picked.entries)
      return api.push(name, body, { note: note.trim() || undefined, spa })
    },
    onSuccess: (res) => {
      setResult(res)
      // 清掉已选内容，免得再点一次重复推送；名字留着，下次拖入同一个原型的新版本时不用重填
      setPicked(null)
      setNote('')
      qc.invalidateQueries({ queryKey: ['prototypes'] })
      qc.invalidateQueries({ queryKey: ['prototype', res.prototype.name] })
      qc.invalidateQueries({ queryKey: ['versions', res.prototype.name] })
      toast.success(res.version.unchanged ? '内容没有变化' : `已推送 ${res.prototype.name} v${res.version.number}`)
    },
    onError: (e) => toast.error(errorText(e)),
  })

  const target = existing?.find((p) => p.name === name)
  const nameError = name && !validName(name) ? '只能用小写字母、数字和单个短横线，最多 40 个字符' : target?.serve_kind === 'proxy' ? '这个名字是端口代理，不能推送文件' : ''
  const submit = (e: FormEvent) => {
    e.preventDefault()
    push.mutate()
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <h1 className="font-heading text-2xl font-semibold tracking-tight">上传</h1>
        <p className="text-muted-foreground text-sm">
          拖入构建好的文件夹（如 dist）、zip / tar 包，或者图片、录屏等素材。在服务器上用{' '}
          <code className="bg-muted rounded px-1">easel push</code> 效果相同。
        </p>
      </div>

      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        className={cn(
          'flex flex-col items-center gap-3 rounded-xl border-2 border-dashed p-10 text-center transition-colors',
          dragging ? 'border-primary bg-primary/5' : 'border-border',
        )}
        data-testid="dropzone"
      >
        <UploadCloud className="text-muted-foreground size-10" aria-hidden />
        <p className="font-medium">拖到这里</p>
        <div className="flex flex-wrap justify-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => folderInput.current?.click()}>
            <FolderUp />
            选择文件夹
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => fileInput.current?.click()}>
            <FileArchive />
            选择文件
          </Button>
        </div>
        <input
          ref={folderInput}
          type="file"
          className="hidden"
          // @ts-expect-error 非标准属性，但所有主流浏览器都支持
          webkitdirectory=""
          multiple
          onChange={(e) => e.target.files?.length && choose({ kind: 'files', ...readPicked(e.target.files) })}
        />
        <input
          ref={fileInput}
          type="file"
          className="hidden"
          multiple
          onChange={(e) => {
            const fs = e.target.files
            if (!fs?.length) return
            if (fs.length === 1 && isArchive(fs[0])) choose({ kind: 'archive', file: fs[0] })
            else choose({ kind: 'files', ...readPicked(fs) })
          }}
        />
      </div>

      {picked && (
        <Card className="p-4">
          <form onSubmit={submit} className="space-y-4">
            <div className="flex items-center gap-3">
              <FileArchive className="text-muted-foreground size-5 shrink-0" aria-hidden />
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{describe(picked).title}</p>
                <p className="text-muted-foreground text-xs">
                  {describe(picked).detail}
                  {picked.kind === 'files' && ' · 点文件、node_modules 和密钥文件会被服务端排除'}
                </p>
              </div>
              <Button type="button" variant="ghost" size="icon" aria-label="清除" onClick={() => setPicked(null)}>
                <X />
              </Button>
            </div>
            <div className="space-y-2">
              <Label htmlFor="up-name">原型名</Label>
              <Input
                id="up-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-prototype"
                aria-invalid={!!nameError}
                list="existing-prototypes"
              />
              <datalist id="existing-prototypes">
                {existing?.filter((p) => p.serve_kind === 'static').map((p) => <option key={p.name} value={p.name} />)}
              </datalist>
              <p className={cn('text-xs', nameError ? 'text-destructive' : 'text-muted-foreground')}>
                {nameError ||
                  (target ? `已有原型，将推送为新版本（当前 v${target.current?.number ?? 0}）` : name ? '将创建新原型' : '')}
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="up-note">版本备注</Label>
              <Input id="up-note" value={note} onChange={(e) => setNote(e.target.value)} placeholder="这一版改了什么（可选）" />
            </div>
            <div className="flex items-center gap-2">
              <Checkbox id="up-spa" checked={spa} onCheckedChange={(v) => setSpa(v === true)} />
              <Label htmlFor="up-spa" className="font-normal">
                单页应用：找不到的页面路径回退到入口页
              </Label>
            </div>
            <div className="flex justify-end">
              <Button type="submit" disabled={!name || !!nameError || push.isPending}>
                <UploadCloud />
                {push.isPending ? '上传中…' : '上传'}
              </Button>
            </div>
          </form>
        </Card>
      )}

      {result && (
        <Card className="gap-3 border-emerald-500/40 bg-emerald-500/5 p-4" role="status">
          <div className="flex flex-wrap items-center gap-2">
            <p className="font-medium">
              {result.version.unchanged ? '内容没有变化，仍是' : result.prototype.created ? '已创建' : '已推送'}{' '}
              {result.prototype.name} v{result.version.number}
            </p>
            <Badge variant="secondary">{result.stats.files} 个文件</Badge>
          </div>
          {(result.stats.skipped?.length ?? 0) > 0 && (
            <p className="text-muted-foreground text-xs">
              已排除：{result.stats.skipped.slice(0, 5).join('、')}
              {result.stats.skipped.length > 5 ? ' …' : ''}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button asChild size="sm">
              <Link href={`/p/${result.prototype.name}`}>查看原型</Link>
            </Button>
            <Button asChild size="sm" variant="outline">
              <a href={result.urls.preview} target="_blank" rel="noopener">
                <ExternalLink />
                打开预览
              </a>
            </Button>
          </div>
        </Card>
      )}
    </div>
  )
}
