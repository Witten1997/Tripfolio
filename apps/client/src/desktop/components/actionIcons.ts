import {
  Copy,
  Share2,
  ChevronDown,
  ChevronUp,
  Globe,
  House,
  List,
  LoaderCircle,
  Map as MapIcon,
  Palette,
  Pencil,
  Plus,
  ReceiptText,
  RotateCw,
  Trash2,
  User,
} from '@lucide/vue'

/** 按需导入 Lucide，尺寸、描边与颜色由 ActionIcon 统一提供。 */
export const actionIcons = {
  home: House,
  share: Share2,
  copy: Copy,
  edit: Pencil,
  trash: Trash2,
  theme: Palette,
  refresh: RotateCw,
  globe: Globe,
  receipt: ReceiptText,
  plus: Plus,
  map: MapIcon,
  list: List,
  loading: LoaderCircle,
  'chevron-down': ChevronDown,
  'chevron-up': ChevronUp,
  user: User,
} as const

export type ActionIconName = keyof typeof actionIcons
