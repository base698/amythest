// Optional fields mirror the API, which omits empty assignee/milestone entirely
// and can send a null labels array — searching must survive a sparse card.
export interface SearchableCard {
  id: string
  title: string
  description?: string
  assignee?: string
  labels?: string[] | null
  milestone?: string
}

export function searchCards<T extends SearchableCard>(cards: T[], query: string): T[] {
  const terms = normalize(query).split(/\s+/).filter(Boolean)
  if (terms.length === 0) return cards

  return cards.filter((card) => {
    const fields = [
      card.id,
      card.title,
      card.description,
      card.assignee,
      card.milestone,
      ...(card.labels ?? []),
    ].map(normalize)
    return terms.every((term) => fields.some((field) => field.includes(term)))
  })
}

function normalize(value: string | null | undefined): string {
  return (value ?? '').trim().toLocaleLowerCase()
}
