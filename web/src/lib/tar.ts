// 极简 tar（ustar）打包：浏览器里把拖进来的文件夹打成一个流交给整包推送端点。
// 只写普通文件；路径超过 100 字节时用 ustar 的 prefix 字段（最长 255），再长就报错。

export type TarEntry = { path: string; file: Blob }

const BLOCK = 512
const encoder = new TextEncoder()

function writeString(buf: Uint8Array, offset: number, length: number, s: string) {
  const bytes = encoder.encode(s)
  if (bytes.length > length) throw new Error(`tar: field too long: ${s}`)
  buf.set(bytes, offset)
}

function writeOctal(buf: Uint8Array, offset: number, length: number, n: number) {
  // length-1 位八进制数字 + NUL
  writeString(buf, offset, length, n.toString(8).padStart(length - 1, '0') + '\0')
}

/** 把 path 拆成 ustar 的 name（≤100 字节）和 prefix（≤155 字节），在 / 处切开。 */
export function splitPath(path: string): { name: string; prefix: string } {
  if (encoder.encode(path).length <= 100) return { name: path, prefix: '' }
  for (let i = path.lastIndexOf('/'); i > 0; i = path.lastIndexOf('/', i - 1)) {
    const prefix = path.slice(0, i)
    const name = path.slice(i + 1)
    if (encoder.encode(name).length <= 100 && encoder.encode(prefix).length <= 155) return { name, prefix }
  }
  throw new Error(`路径太长，无法打包：${path}`)
}

export function header(path: string, size: number, mtime = Math.floor(Date.now() / 1000)): Uint8Array {
  const h = new Uint8Array(BLOCK)
  const { name, prefix } = splitPath(path)
  writeString(h, 0, 100, name)
  writeOctal(h, 100, 8, 0o644) // mode
  writeOctal(h, 108, 8, 0) // uid
  writeOctal(h, 116, 8, 0) // gid
  writeOctal(h, 124, 12, size)
  writeOctal(h, 136, 12, mtime)
  h.fill(0x20, 148, 156) // 计算校验和时 chksum 字段按空格算
  h[156] = 0x30 // typeflag '0'：普通文件
  writeString(h, 257, 6, 'ustar\0')
  writeString(h, 263, 2, '00')
  writeString(h, 345, 155, prefix)
  let sum = 0
  for (const b of h) sum += b
  writeString(h, 148, 8, sum.toString(8).padStart(6, '0') + '\0 ')
  return h
}

/** 打包成一个 Blob（文件内容按引用拼接，不读进内存）。 */
export function tar(entries: TarEntry[]): Blob {
  const parts: BlobPart[] = []
  for (const { path, file } of entries) {
    parts.push(header(path, file.size) as BlobPart, file)
    const pad = (BLOCK - (file.size % BLOCK)) % BLOCK
    if (pad) parts.push(new Uint8Array(pad) as BlobPart)
  }
  parts.push(new Uint8Array(BLOCK * 2) as BlobPart) // 两个全零块表示结尾
  return new Blob(parts, { type: 'application/x-tar' })
}
