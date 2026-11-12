import { QueryClientProvider } from '@tanstack/react-query'
import { KeyRound, LayoutGrid, LogOut, Upload } from 'lucide-react'
import { lazy, Suspense, useState, type ReactNode } from 'react'
import { Link, Route, Switch, useLocation } from 'wouter'
import { ErrorBoundary } from '@/components/error-boundary'
import { Button } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeQueryClient } from '@/lib/query'
import { cn } from '@/lib/utils'
import PrototypeList from '@/pages/prototype-list'

// 首页之外的页面按需加载（后台和前置代理都不压缩，首屏小一点）
const PrototypeDetail = lazy(() => import('@/pages/prototype-detail'))
const Tokens = lazy(() => import('@/pages/tokens'))
const UploadPage = lazy(() => import('@/pages/upload'))

export default function App() {
  const [queryClient] = useState(makeQueryClient)
  return (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <Shell>
          <Suspense fallback={null}>
          <Switch>
            <Route path="/" component={PrototypeList} />
            <Route path="/p/:name" component={PrototypeDetail} />
            <Route path="/upload" component={UploadPage} />
            <Route path="/tokens" component={Tokens} />
            <Route>
              <p className="text-muted-foreground">页面不存在。</p>
            </Route>
          </Switch>
          </Suspense>
        </Shell>
        <Toaster richColors position="top-center" />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

function NavLink({ href, icon, children }: { href: string; icon: ReactNode; children: string }) {
  const [loc] = useLocation()
  const active = href === '/' ? loc === '/' || loc.startsWith('/p/') : loc.startsWith(href)
  return (
    <Link
      href={href}
      aria-label={children}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'text-muted-foreground hover:text-foreground flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm transition-colors [&_svg]:size-4',
        active && 'bg-muted text-foreground',
      )}
    >
      {icon}
      {/* 窄屏只显示图标 */}
      <span className="hidden sm:inline">{children}</span>
    </Link>
  )
}

function Shell({ children }: { children: ReactNode }) {
  const [loc] = useLocation()
  return (
    <div className="min-h-svh">
      <header className="bg-background/80 sticky top-0 z-40 border-b backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-2 px-4 sm:gap-4">
          <Link href="/" className="font-heading flex items-center gap-2 text-lg font-semibold tracking-tight">
            <span aria-hidden className="bg-primary text-primary-foreground grid size-7 place-items-center rounded-md text-sm">
              e
            </span>
            easel
          </Link>
          <nav className="flex items-center gap-1">
            <NavLink href="/" icon={<LayoutGrid />}>
              原型
            </NavLink>
            <NavLink href="/upload" icon={<Upload />}>
              上传
            </NavLink>
            <NavLink href="/tokens" icon={<KeyRound />}>
              API Token
            </NavLink>
          </nav>
          <form method="post" action="/auth/logout" className="ml-auto">
            <Button type="submit" variant="ghost" size="sm" aria-label="退出">
              <LogOut />
              <span className="hidden sm:inline">退出</span>
            </Button>
          </form>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-8">
        <ErrorBoundary key={loc}>{children}</ErrorBoundary>
      </main>
    </div>
  )
}
