import { useMemo } from 'react'
import { encode } from 'uqr'

/** 二维码：自己画 SVG path（不用 innerHTML），白底，四周留 2 格静区。 */
export function QrCode({ value, size = 176, className }: { value: string; size?: number; className?: string }) {
  const { d, n } = useMemo(() => {
    const qr = encode(value, { ecc: 'M', border: 2 })
    let path = ''
    qr.data.forEach((row, y) =>
      row.forEach((on, x) => {
        if (on) path += `M${x} ${y}h1v1h-1z`
      }),
    )
    return { d: path, n: qr.size }
  }, [value])
  return (
    <svg
      role="img"
      aria-label={`二维码：${value}`}
      viewBox={`0 0 ${n} ${n}`}
      width={size}
      height={size}
      shapeRendering="crispEdges"
      className={className}
    >
      <rect width={n} height={n} fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  )
}
