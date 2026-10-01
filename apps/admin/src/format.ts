export const formatTime = (value: string | null) =>
  value
    ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
        new Date(value),
      )
    : '暂无记录'

export function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = value / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index++
  }
  return `${size.toFixed(1)} ${units[index]}`
}

const statuses: Record<string, string> = { active: '正常', deleting: '注销中' }
const clients: Record<string, string> = { web: '网页端', android: 'Android', harmony: 'HarmonyOS' }
export const statusLabel = (value: string) => statuses[value] || value
export const clientLabel = (value: string) => clients[value] || value
const phases: Record<string, string> = { planned: '计划中', ongoing: '进行中', ended: '已结束' }
export const phaseLabel = (value: string) => phases[value] || value
