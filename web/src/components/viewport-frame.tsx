import { Monitor, Smartphone, Tablet } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { fitScale, viewports, type Viewport } from '@/lib/viewport'

export type { Viewport }

export function ViewportPicker({ value, onChange }: { value: Viewport; onChange: (v: Viewport) => void }) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={value}
      onValueChange={(v) => v && onChange(v as Viewport)}
      aria-label="视口"
    >
      <ToggleGroupItem value="mobile" aria-label="手机视口">
        <Smartphone />
      </ToggleGroupItem>
      <ToggleGroupItem value="tablet" aria-label="平板视口">
        <Tablet />
      </ToggleGroupItem>
      <ToggleGroupItem value="desktop" aria-label="桌面视口">
        <Monitor />
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

/** 按设备尺寸渲染 iframe，容器放不下时等比缩小（页面仍以设备宽度布局）。 */
export function ViewportFrame({
  src,
  viewport,
  title,
  reloadKey,
  maxHeight = 720,
}: {
  src: string
  viewport: Viewport
  title: string
  reloadKey?: number
  maxHeight?: number
}) {
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setWidth(e.contentRect.width))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const { w, h } = viewports[viewport]
  // 减去容器的内边距（p-3 = 12px × 2）
  const scale = fitScale(viewport, Math.max(0, width - 24), maxHeight)
  return (
    <div ref={box} className="bg-muted/40 flex w-full justify-center overflow-hidden rounded-lg border p-3">
      <div style={{ width: w * scale, height: h * scale }} className="shrink-0">
        <iframe
          key={`${src}#${reloadKey ?? 0}`}
          src={src}
          title={title}
          width={w}
          height={h}
          style={{ transform: `scale(${scale})`, transformOrigin: '0 0' }}
          className="bg-background rounded-md border shadow-sm"
          // 原型和后台不同源；允许原型自己的脚本、表单、弹窗，不给顶层导航
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-modals allow-downloads"
        />
      </div>
    </div>
  )
}
