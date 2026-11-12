import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'

type Health = 'checking' | 'ok' | 'down'

export default function App() {
  const [health, setHealth] = useState<Health>('checking')

  useEffect(() => {
    const ctrl = new AbortController()
    fetch('/healthz', { signal: ctrl.signal })
      .then((r) => setHealth(r.ok ? 'ok' : 'down'))
      .catch((e: unknown) => {
        if (!(e instanceof DOMException && e.name === 'AbortError')) setHealth('down')
      })
    return () => ctrl.abort()
  }, [])

  return (
    <main className="mx-auto flex min-h-svh max-w-2xl flex-col justify-center gap-6 p-8">
      <div className="space-y-2">
        <h1 className="font-heading text-3xl font-semibold tracking-tight">easel</h1>
        <p className="text-muted-foreground">上传、管理、预览和分享原型。</p>
      </div>
      <p className="text-sm" data-testid="health">
        服务端：{health === 'checking' ? '检查中…' : health === 'ok' ? '正常' : '无法连接'}
      </p>
      <div>
        <Button asChild variant="outline">
          <a href="https://github.com/smokecat/easel">GitHub</a>
        </Button>
      </div>
    </main>
  )
}
