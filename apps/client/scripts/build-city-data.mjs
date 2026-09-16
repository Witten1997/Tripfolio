import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

const source = process.argv[2]
const adminSource = process.argv[3]

if (!source || !adminSource) {
  throw new Error('用法：node scripts/build-city-data.mjs <cities*.txt> <admin1CodesASCII.txt>')
}

const outputDirectory = resolve('src/shared/cities/data')
const [cityText, adminText] = await Promise.all([
  readFile(resolve(source), 'utf8'),
  readFile(resolve(adminSource), 'utf8'),
])

const adminNames = new Map(
  adminText
    .trim()
    .split('\n')
    .map((line) => line.replace(/\r$/, '').split('\t'))
    .map(([code, name]) => [code, name]),
)

function normalize(value) {
  return value
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLocaleLowerCase('en-US')
    .replace(/[^\p{L}\p{N}]+/gu, '')
}

function displayName(name, alternateNames, countryCode) {
  if (countryCode !== 'CN') return name
  const chineseNames = alternateNames.filter(
    (item) => /^[\p{Script=Han}·]+$/u.test(item) && item.length >= 2 && item.length <= 12,
  )
  const officialCityName = chineseNames
    .filter((item) => item.endsWith('市'))
    .sort((left, right) => left.length - right.length)[0]
  return (
    officialCityName?.slice(0, -1) ??
    chineseNames.sort((left, right) => left.length - right.length)[0] ??
    name
  )
}

const rows = cityText
  .trim()
  .split('\n')
  .map((line) => line.replace(/\r$/, '').split('\t'))
  .map((fields) => {
    const [id, name, asciiName, alternateRaw, , , , , countryCode, , adminCode] = fields
    const aliases = alternateRaw ? alternateRaw.split(',') : []
    const display = displayName(name, aliases, countryCode)
    const searchableAliases =
      countryCode === 'CN'
        ? aliases
        : [...aliases.filter((item) => /\p{Script=Han}/u.test(item)), ...aliases.slice(0, 12)]
    const search = [
      ...new Set([display, name, asciiName, ...searchableAliases].map(normalize).filter(Boolean)),
    ]
      .slice(0, countryCode === 'CN' ? 80 : 24)
      .join('|')
    const population = Number(fields[14] ?? 0)
    const region = adminNames.get(`${countryCode}.${adminCode}`) ?? ''
    return [id, display, asciiName, countryCode, region, population, search]
  })
  .sort((left, right) => right[5] - left[5])

const domestic = rows.filter((row) => row[3] === 'CN')
const international = rows.filter((row) => row[3] !== 'CN')

await mkdir(outputDirectory, { recursive: true })
await Promise.all([
  writeFile(resolve(outputDirectory, 'domestic.json'), JSON.stringify(domestic)),
  writeFile(resolve(outputDirectory, 'international.json'), JSON.stringify(international)),
])

console.log(`已生成国内 ${domestic.length} 条、国外 ${international.length} 条城市数据。`)
