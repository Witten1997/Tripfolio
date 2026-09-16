export type CityScope = 'domestic' | 'international'

export type CityRecord = [
  id: string,
  name: string,
  asciiName: string,
  countryCode: string,
  region: string,
  population: number,
  searchText: string,
]

const loaders: Record<CityScope, () => Promise<CityRecord[]>> = {
  domestic: () =>
    import('./data/domestic.json').then((module) => module.default as unknown as CityRecord[]),
  international: () =>
    import('./data/international.json').then((module) => module.default as unknown as CityRecord[]),
}

const cache = new Map<CityScope, CityRecord[]>()

export function normalizeCityQuery(value: string) {
  return value
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLocaleLowerCase('en-US')
    .replace(/[^\p{L}\p{N}]+/gu, '')
}

export function isCityQueryReady(value: string) {
  const normalized = normalizeCityQuery(value)
  return normalized.length >= 2 || /\p{Script=Han}/u.test(normalized)
}

export async function searchCities(scope: CityScope, query: string, limit = 60) {
  const normalized = normalizeCityQuery(query)
  if (!isCityQueryReady(normalized)) return []
  let cities = cache.get(scope)
  if (!cities) {
    cities = await loaders[scope]()
    cache.set(scope, cities)
  }
  return cities
    .filter((city) => city[6].includes(normalized))
    .sort((left, right) => {
      const leftName = normalizeCityQuery(left[1])
      const rightName = normalizeCityQuery(right[1])
      const leftAscii = normalizeCityQuery(left[2])
      const rightAscii = normalizeCityQuery(right[2])
      const leftRank =
        leftName === normalized || leftAscii === normalized
          ? 0
          : leftName.startsWith(normalized) || leftAscii.startsWith(normalized)
            ? 1
            : 2
      const rightRank =
        rightName === normalized || rightAscii === normalized
          ? 0
          : rightName.startsWith(normalized) || rightAscii.startsWith(normalized)
            ? 1
            : 2
      return leftRank - rightRank || right[5] - left[5]
    })
    .slice(0, limit)
}
