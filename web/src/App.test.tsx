import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import App from './App'
import type { Prototype } from './lib/api'

const proto = (over: Partial<Prototype>): Prototype => ({
  name: 'demo',
  serve_kind: 'static',
  title: '',
  preview_url: 'https://demo-abc123.easel.example.com/',
  created_at: '2026-11-12T00:00:00Z',
  updated_at: '2026-11-12T00:00:00Z',
  current: { number: 3, kind: 'site', note: 'new header', files: 4, bytes: 2048, created_at: '2026-11-12T00:00:00Z', url: 'https://demo-abc123--v3.easel.example.com/' },
  versions: 3,
  proxy: null,
  ...over,
})

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  window.history.replaceState(null, '', '/')
  // jsdom 没有 ResizeObserver / matchMedia
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: false, media: q, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }))
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('lists prototypes with kind and proxy status', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      json({
        prototypes: [
          proto({}),
          proto({
            name: 'app-dev',
            serve_kind: 'proxy',
            current: null,
            versions: 0,
            preview_url: 'https://app-dev-xyz.easel.example.com/',
            proxy: { port: 5173, host_mode: 'localhost', status: 'paused', paused_reason: 'process_changed', last_seen_at: null },
          }),
        ],
      }),
    ),
  )
  render(<App />)
  expect(await screen.findByRole('link', { name: 'demo' })).toBeTruthy()
  expect(screen.getByText('共 2 个')).toBeTruthy()
  expect(screen.getByText('v3')).toBeTruthy()
  expect(screen.getByText(':5173')).toBeTruthy()
  expect(screen.getByText('分享已暂停')).toBeTruthy()
  expect(screen.getByText('new header')).toBeTruthy()
})

test('shows an empty state with CLI hints', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ prototypes: [] })))
  render(<App />)
  expect(await screen.findByText('还没有原型')).toBeTruthy()
})

test('redirects to sign-in on 401', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ error: { code: 'unauthorized', message: 'authentication required' } }, 401)))
  const assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, pathname: '/tokens', search: '', assign })
  window.history.replaceState(null, '', '/tokens')
  render(<App />)
  await vi.waitFor(() => expect(assign).toHaveBeenCalledWith('/auth/login?next=%2Ftokens'))
})

test('shows API errors with hints', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ error: { code: 'forbidden', message: 'API tokens cannot do this', hint: 'use the admin web UI' } }, 403)))
  render(<App />)
  expect(await screen.findByText('API tokens cannot do this')).toBeTruthy()
  expect(screen.getByText('use the admin web UI')).toBeTruthy()
})
