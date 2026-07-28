export function formatDueDate(value: string, locale?: string): string {
  const [year, month, day] = value.split('-').map(Number)
  if (!year || !month || !day) return value
  const date = new Date(0)
  date.setHours(12, 0, 0, 0)
  date.setFullYear(year, month - 1, day)
  return date.toLocaleDateString(locale, { month: 'short', day: 'numeric', year: 'numeric' })
}
