export const IMAGE_ACCEPT = 'image/jpeg,image/png,image/webp'
export const DOCUMENT_ACCEPT = IMAGE_ACCEPT + ',application/pdf'
export function validateAssetFile(file: File, allowPdf = false): string | null {
  const pdf = file.type === 'application/pdf'
  if (!IMAGE_ACCEPT.split(',').includes(file.type) && !(allowPdf && pdf))
    return allowPdf ? '请选择 JPEG、PNG、WebP 图片或 PDF。' : '请选择 JPEG、PNG 或 WebP 图片。'
  if (!file.size) return '不能上传空文件。'
  if (file.size > (pdf ? 50 : 20) * 1024 * 1024)
    return pdf ? 'PDF 不能超过 50 MiB。' : '图片不能超过 20 MiB。'
  if (file.name.length > 255) return '文件名不能超过 255 个字符。'
  return null
}
