import type { Prototype } from './api'

export type Kind = 'site' | 'assets' | 'proxy'
export type Filter = 'all' | Kind

export function kindOf(p: Prototype): Kind {
  if (p.serve_kind === 'proxy') return 'proxy'
  return p.current?.kind ?? 'site'
}

export const kindLabel: Record<Kind, string> = { site: '站点', assets: '素材', proxy: '端口代理' }

export const pausedText: Record<string, string> = {
  process_changed: '端口上的程序换了',
  unreachable: '长期连不上',
}

/** 按关键字（名字、标题、当前版本备注）和类型筛选。 */
export function filterPrototypes(list: Prototype[], q: string, filter: Filter): Prototype[] {
  const needle = q.trim().toLowerCase()
  return list.filter(
    (p) =>
      (filter === 'all' || kindOf(p) === filter) &&
      (!needle ||
        p.name.includes(needle) ||
        p.title.toLowerCase().includes(needle) ||
        !!p.current?.note.toLowerCase().includes(needle)),
  )
}
