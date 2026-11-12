import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'

export function CopyButton({ value, label = '复制', size = 'sm' }: { value: string; label?: string; size?: 'sm' | 'icon' }) {
  const [done, setDone] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setDone(true)
      setTimeout(() => setDone(false), 1500)
    } catch {
      toast.error('复制失败，请手动选中复制')
    }
  }
  const Icon = done ? Check : Copy
  if (size === 'icon') {
    return (
      <Button variant="ghost" size="icon" onClick={copy} aria-label={label} title={label}>
        <Icon />
      </Button>
    )
  }
  return (
    <Button variant="outline" size="sm" onClick={copy}>
      <Icon />
      {done ? '已复制' : label}
    </Button>
  )
}
