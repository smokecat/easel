import { describe, expect, test } from 'vitest'
import type { Prototype } from './api'
import { bytes, relativeTime, slugify, validName } from './format'
import { filterPrototypes, kindOf } from './prototype'
import { header, splitPath, tar } from './tar'
import { fitScale } from './viewport'

describe('format', () => {
  test('slugify matches the server rules', () => {
    expect(slugify('My Cool App!')).toBe('my-cool-app')
    expect(slugify('--dist--')).toBe('dist')
    expect(slugify('原型')).toBe('')
    expect(slugify('a'.repeat(50))).toHaveLength(40)
    expect(slugify('ab-'.repeat(20))).not.toMatch(/-$/)
  })
  test('validName', () => {
    expect(validName('my-app-2')).toBe(true)
    for (const bad of ['', 'My', 'a--b', '-a', 'a-', 'a_b', 'x'.repeat(41)]) expect(validName(bad)).toBe(false)
  })
  test('bytes', () => {
    expect(bytes(512)).toBe('512 B')
    expect(bytes(1536)).toBe('1.5 KB')
    expect(bytes(25 * 1024 * 1024)).toBe('25 MB')
  })
  test('relativeTime', () => {
    const now = Date.parse('2026-11-13T00:00:00Z')
    expect(relativeTime('2026-11-12T23:59:30Z', now)).toBe('刚刚')
    expect(relativeTime('2026-11-12T23:55:00Z', now)).toBe('5分钟前')
    expect(relativeTime('2026-11-20T00:00:00Z', now)).toBe('下周')
  })
})

describe('tar', () => {
  const decoder = new TextDecoder()
  const field = (h: Uint8Array, off: number, len: number) => decoder.decode(h.slice(off, off + len)).replace(/\0.*$/s, '')

  test('header fields and checksum', () => {
    const h = header('assets/app.js', 1234, 1_700_000_000)
    expect(h).toHaveLength(512)
    expect(field(h, 0, 100)).toBe('assets/app.js')
    expect(parseInt(field(h, 124, 12), 8)).toBe(1234)
    expect(field(h, 257, 6)).toBe('ustar')
    const stored = parseInt(field(h, 148, 8).trim(), 8)
    const copy = h.slice()
    copy.fill(0x20, 148, 156)
    expect(copy.reduce((a, b) => a + b, 0)).toBe(stored)
  })

  test('long paths use the prefix field', () => {
    const long = `${'d'.repeat(60)}/${'e'.repeat(60)}/file.txt`
    const { name, prefix } = splitPath(long)
    expect(`${prefix}/${name}`).toBe(long)
    expect(name.length).toBeLessThanOrEqual(100)
    expect(() => splitPath('x'.repeat(300))).toThrow()
  })

  test('archive layout: header, padded data, two zero blocks', async () => {
    const blob = tar([
      { path: 'index.html', file: new Blob(['<h1>hi</h1>']) },
      { path: 'a/b.txt', file: new Blob(['x'.repeat(600)]) },
    ])
    expect(blob.size).toBe(512 + 512 + (512 + 1024) + 1024)
    const buf = new Uint8Array(await blob.arrayBuffer())
    expect(field(buf, 0, 100)).toBe('index.html')
    expect(decoder.decode(buf.slice(512, 512 + 11))).toBe('<h1>hi</h1>')
    expect(field(buf, 1024, 100)).toBe('a/b.txt')
    expect(buf.slice(-1024).every((b) => b === 0)).toBe(true)
  })
})

describe('prototype', () => {
  const p = (name: string, over: Partial<Prototype> = {}): Prototype => ({
    name,
    serve_kind: 'static',
    title: '',
    preview_url: `https://${name}.example.com/`,
    created_at: '',
    updated_at: '',
    current: { number: 1, kind: 'site', note: '', files: 1, bytes: 1, created_at: '', url: '' },
    versions: 1,
    proxy: null,
    ...over,
  })
  const list = [
    p('landing', { current: { number: 2, kind: 'site', note: 'Hero copy', files: 1, bytes: 1, created_at: '', url: '' } }),
    p('shots', { current: { number: 1, kind: 'assets', note: '', files: 1, bytes: 1, created_at: '', url: '' } }),
    p('app-dev', { serve_kind: 'proxy', current: null }),
  ]
  test('kindOf', () => {
    expect(list.map(kindOf)).toEqual(['site', 'assets', 'proxy'])
  })
  test('filter by text and kind', () => {
    expect(filterPrototypes(list, '', 'all')).toHaveLength(3)
    expect(filterPrototypes(list, 'hero', 'all').map((x) => x.name)).toEqual(['landing'])
    expect(filterPrototypes(list, '', 'proxy').map((x) => x.name)).toEqual(['app-dev'])
    expect(filterPrototypes(list, 'land', 'assets')).toHaveLength(0)
  })
})

test('fitScale never upscales and respects both limits', () => {
  expect(fitScale('mobile', 2000, 2000)).toBe(1)
  expect(fitScale('desktop', 720, 2000)).toBe(0.5)
  expect(fitScale('tablet', 2000, 590)).toBe(0.5)
  expect(fitScale('desktop', 0, 900)).toBe(1)
})
