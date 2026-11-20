// Country flags beside check location names (Untitled UI's rectangle flags, web/src/assets/flags/<CC>.svg). Every file in that folder is picked
// up here, so a new location only needs its country's flag added there; src/flags.test.ts checks infra/regions.json against the folder.
const files = import.meta.glob<string>('../assets/flags/*.svg', { eager: true, query: '?url', import: 'default' })

const FLAGS: Record<string, string> = Object.fromEntries(
  Object.entries(files).map(([path, url]) => [path.slice(path.lastIndexOf('/') + 1, -'.svg'.length), url]),
)

/** The countries with a flag, for the test that checks every location has one. */
export const flagCountries = () => Object.keys(FLAGS)

/** A location's flag, decorative: the location's name beside it says the same thing in text. Nothing for an unknown or missing country. */
export function Flag({ country }: { country?: string }) {
  const src = country ? FLAGS[country.toUpperCase()] : undefined
  if (!src) return null
  return <img className="flag" src={src} alt="" width={18} height={12} />
}

/** A location's name with its flag in front. */
export function LocationName({ name, country }: { name: string; country?: string }) {
  return (
    <span className="location-name">
      <Flag country={country} />
      {name}
    </span>
  )
}
