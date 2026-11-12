import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api'

export function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 401 已经跳登录页，4xx 都不重试
        retry: (n, e) => !(e instanceof ApiError && e.status < 500) && n < 2,
        refetchOnWindowFocus: true,
        staleTime: 5_000,
      },
    },
  })
}

/** 统一的错误信息（toast 用）。 */
export function errorText(e: unknown): string {
  if (e instanceof ApiError) return e.hint ? `${e.message}（${e.hint}）` : e.message
  return e instanceof Error ? e.message : String(e)
}
