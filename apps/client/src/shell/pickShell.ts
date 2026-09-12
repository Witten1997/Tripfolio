export type Shell = 'desktop' | 'mobile'

/** 视口宽度不超过该值时使用移动壳。 */
export const MOBILE_MAX_WIDTH = 767

export interface ShellHints {
  /** 是否运行在 Capacitor 原生容器内。 */
  isNative: boolean
  /** 启动时的视口宽度（CSS 像素）。 */
  viewportWidth: number
}

/**
 * 决定加载哪一个壳。原生环境一律移动壳；浏览器按启动视口宽度判断，运行期间不切换，
 * 避免两套页面状态互相迁移。
 */
export function pickShell({ isNative, viewportWidth }: ShellHints): Shell {
  if (isNative) return 'mobile'
  return viewportWidth <= MOBILE_MAX_WIDTH ? 'mobile' : 'desktop'
}
