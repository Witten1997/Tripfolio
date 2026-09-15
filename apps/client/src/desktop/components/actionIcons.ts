import {
  ChevronDown,
  ChevronUp,
  Globe,
  House,
  LoaderCircle,
  Palette,
  Pencil,
  Plus,
  ReceiptText,
  RotateCw,
  Trash2,
} from '@lucide/vue'

/** 按需导入 Lucide，尺寸、描边与颜色由 ActionIcon 统一提供。 */
export const actionIcons = {
  home: House,
  edit: Pencil,
  trash: Trash2,
  theme: Palette,
  refresh: RotateCw,
  globe: Globe,
  receipt: ReceiptText,
  plus: Plus,
  loading: LoaderCircle,
  'chevron-down': ChevronDown,
  'chevron-up': ChevronUp,
} as const

export type ActionIconName = keyof typeof actionIcons
