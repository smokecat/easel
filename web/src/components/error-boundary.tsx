import { Component, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'

/** 页面渲染出错时显示错误和重试按钮，而不是整个面板白屏。调用方用 key 在换页时重建它。 */
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div role="alert" className="border-destructive/30 bg-destructive/5 space-y-3 rounded-lg border p-4 text-sm">
        <p className="text-destructive font-medium">页面出错了：{this.state.error.message}</p>
        <Button size="sm" variant="outline" onClick={() => this.setState({ error: null })}>
          重试
        </Button>
      </div>
    )
  }
}
