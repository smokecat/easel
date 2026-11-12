import { Globe, Images, PlugZap } from 'lucide-react'
import type { Kind } from '@/lib/prototype'

export function KindIcon({ kind, className }: { kind: Kind; className?: string }) {
  const Icon = kind === 'proxy' ? PlugZap : kind === 'assets' ? Images : Globe
  return <Icon className={className} aria-hidden />
}
