// 读取拖进来 / 选中的文件，统一成「相对路径 + 文件」的列表。

import type { TarEntry } from './tar'

/** 是否是可以原样上传的压缩包（服务端识别 tar / tar.gz / zip）。 */
export function isArchive(f: File): boolean {
  return /\.(zip|tar|tgz|tar\.gz)$/i.test(f.name)
}

/** 从 DataTransfer 读取文件，文件夹递归展开。只拖了一个文件夹时去掉这一层，路径相对文件夹内部。 */
export async function readDrop(dt: DataTransfer): Promise<{ entries: TarEntry[]; root: string | null }> {
  const items = Array.from(dt.items)
    .map((i) => i.webkitGetAsEntry?.())
    .filter((e): e is FileSystemEntry => !!e)
  if (items.length === 0) {
    return { entries: Array.from(dt.files).map((f) => ({ path: f.name, file: f })), root: null }
  }
  const entries: TarEntry[] = []
  for (const e of items) await walk(e, '', entries)
  if (items.length === 1 && items[0].isDirectory) {
    const root = items[0].name
    return { entries: entries.map((x) => ({ ...x, path: x.path.slice(root.length + 1) })), root }
  }
  return { entries, root: null }
}

async function walk(entry: FileSystemEntry, prefix: string, out: TarEntry[]) {
  const path = prefix ? `${prefix}/${entry.name}` : entry.name
  if (entry.isFile) {
    const file = await new Promise<File>((res, rej) => (entry as FileSystemFileEntry).file(res, rej))
    out.push({ path, file })
    return
  }
  if (entry.isDirectory) {
    const reader = (entry as FileSystemDirectoryEntry).createReader()
    // readEntries 每次最多返回一批，要读到空为止
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej))
      if (batch.length === 0) break
      for (const child of batch) await walk(child, path, out)
    }
  }
}

/** 从 <input webkitdirectory> 选中的文件读取：去掉最外层文件夹名。 */
export function readPicked(files: FileList): { entries: TarEntry[]; root: string | null } {
  const list = Array.from(files)
  const rel = list.map((f) => f.webkitRelativePath || f.name)
  const first = rel[0]?.split('/')[0]
  const shared = first && rel.every((p) => p.startsWith(first + '/'))
  return {
    entries: list.map((f, i) => ({ path: shared ? rel[i].slice(first.length + 1) : rel[i], file: f })),
    root: shared ? first : null,
  }
}
