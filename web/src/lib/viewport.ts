export type Viewport = 'mobile' | 'tablet' | 'desktop'

export const viewports: Record<Viewport, { label: string; w: number; h: number }> = {
  mobile: { label: '手机', w: 390, h: 844 },
  tablet: { label: '平板', w: 820, h: 1180 },
  desktop: { label: '桌面', w: 1440, h: 900 },
}

/** iframe 的缩放比例：按设备尺寸布局，放不进容器（宽 width、限高 maxHeight）时等比缩小，不放大。 */
export function fitScale(viewport: Viewport, width: number, maxHeight: number): number {
  const { w, h } = viewports[viewport]
  return width ? Math.min(1, width / w, maxHeight / h) : 1
}
