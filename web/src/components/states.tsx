import type { ReactNode } from 'react'
import { ApiError } from '@/lib/api'

export function ErrorState({ error, children }: { error: unknown; children?: ReactNode }) {
  const msg = error instanceof Error ? error.message : String(error)
  const hint = error instanceof ApiError ? error.hint : undefined
  return (
    <div role="alert" className="border-destructive/30 bg-destructive/5 rounded-lg border p-4 text-sm">
      <p className="text-destructive font-medium">{msg}</p>
      {hint && <p className="text-muted-foreground mt-1">{hint}</p>}
      {children}
    </div>
  )
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-xl border border-dashed p-10 text-center">
      <p className="font-medium">{title}</p>
      {children && <div className="text-muted-foreground mt-2 text-sm">{children}</div>}
    </div>
  )
}
