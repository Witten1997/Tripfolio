import type { GeoCoordinate } from '@/shared/api/geo'
import { roundedCoordinate } from '@/shared/geo/amap'

/** EXIF GPS 使用 WGS-84；采用建议时转换为地图和业务记录的 GCJ-02。 */
export function wgs84ToGcj02(point: GeoCoordinate): GeoCoordinate {
  const { latitude: lat, longitude: lng } = point
  if (lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271)
    return roundedCoordinate(lat, lng)
  const x = lng - 105,
    y = lat - 35,
    pi = Math.PI
  let dLat = -100 + 2 * x + 3 * y + 0.2 * y * y + 0.1 * x * y + 0.2 * Math.sqrt(Math.abs(x))
  dLat += ((20 * Math.sin(6 * x * pi) + 20 * Math.sin(2 * x * pi)) * 2) / 3
  dLat += ((20 * Math.sin(y * pi) + 40 * Math.sin((y * pi) / 3)) * 2) / 3
  dLat += ((160 * Math.sin((y * pi) / 12) + 320 * Math.sin((y * pi) / 30)) * 2) / 3
  let dLng = 300 + x + 2 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * Math.sqrt(Math.abs(x))
  dLng += ((20 * Math.sin(6 * x * pi) + 20 * Math.sin(2 * x * pi)) * 2) / 3
  dLng += ((20 * Math.sin(x * pi) + 40 * Math.sin((x * pi) / 3)) * 2) / 3
  dLng += ((150 * Math.sin((x * pi) / 12) + 300 * Math.sin((x * pi) / 30)) * 2) / 3
  const rad = (lat / 180) * pi
  const magic = 1 - 0.00669342162296594323 * Math.sin(rad) ** 2
  const sqrtMagic = Math.sqrt(magic)
  dLat = (dLat * 180) / (((6378245 * (1 - 0.00669342162296594323)) / (magic * sqrtMagic)) * pi)
  dLng = (dLng * 180) / ((6378245 / sqrtMagic) * Math.cos(rad) * pi)
  return roundedCoordinate(lat + dLat, lng + dLng)
}
