import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import App from './App'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

test('shows server health', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('ok\n')))
  render(<App />)
  expect(screen.getByRole('heading', { name: 'easel' })).toBeTruthy()
  expect(await screen.findByText('服务端：正常')).toBeTruthy()
})

test('reports unreachable server', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network')))
  render(<App />)
  expect(await screen.findByText('服务端：无法连接')).toBeTruthy()
})
