/** 访客页注入 noindex，避免搜索引擎收录分享链接；返回移除函数。服务端对 /public/* 另有 X-Robots-Tag。 */
export function installNoIndex(doc: Document): () => void {
  if (doc.head.querySelector('meta[name="robots"]')) return () => {}
  const meta = doc.createElement('meta')
  meta.name = 'robots'
  meta.content = 'noindex, nofollow'
  doc.head.appendChild(meta)
  return () => meta.remove()
}
