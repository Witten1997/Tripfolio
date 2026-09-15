import type { components } from '@tripfolio/contracts/openapi/v1'

export type Problem = components['schemas']['Problem']

/** 从 problem+json 取可展示文案：字段错误优先，其次 detail、title。 */
export function problemMessage(problem: Problem | undefined, fallback = '请求失败'): string {
  if (!problem) return fallback
  const field = problem.errors?.[0]
  if (field?.message) return field.message
  return problem.detail || problem.title || fallback
}

/** 业务错误：携带 problem+json，页面用 message 展示、用 problem.code 分支。 */
export class ApiError extends Error {
  readonly problem: Problem | undefined
  constructor(problem: Problem | undefined, fallback = '请求失败，请稍后再试') {
    super(problemMessage(problem, fallback))
    this.name = 'ApiError'
    this.problem = problem
  }
  get code(): string | undefined {
    return this.problem?.code
  }
}
