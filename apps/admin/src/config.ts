// Go 在入口 HTML 注入 base；同一份产物随服务端配置切换页面、资源及 API 地址。
export const adminBasePath = new URL(document.baseURI).pathname
export const adminAPIPath = '/api/v1' + adminBasePath.replace(/\/$/, '')
