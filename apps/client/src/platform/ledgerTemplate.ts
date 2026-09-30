import { Capacitor, registerPlugin } from '@capacitor/core'

const nativeTemplate = registerPlugin<{
  save(options: { data: string }): Promise<{ saved: boolean }>
}>('LedgerTemplate')

export async function saveLedgerTemplate(blob: Blob): Promise<void> {
  if (Capacitor.getPlatform() === 'android') {
    const data = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result).split(',')[1] ?? '')
      reader.onerror = () => reject(new Error('无法读取模板文件'))
      reader.readAsDataURL(blob)
    })
    await nativeTemplate.save({ data })
    return
  }
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'Tripfolio-账单导入模板.xlsx'
  document.body.append(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000)
}
