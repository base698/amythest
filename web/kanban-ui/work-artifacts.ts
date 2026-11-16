export function githubCommitURL(identity: string, sha: string): string | null {
 if (!/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(sha)) return null
 const match = /^commit:github\.com\/([a-z0-9_.-]+)\/([a-z0-9_.-]+):([a-f0-9]{40}|[a-f0-9]{64})$/i.exec(identity)
 if (!match || match[3].toLowerCase() !== sha.toLowerCase() || [match[1], match[2]].some(s => s === '.' || s === '..')) return null
 return `https://github.com/${encodeURIComponent(match[1])}/${encodeURIComponent(match[2])}/commit/${sha}`
}
export function diffLineClass(line: string): string {
 if (line.startsWith('diff --git ') || line.startsWith('@@')) return 'work-diff-heading'
 if (line.startsWith('+') && !line.startsWith('+++')) return 'work-diff-add'
 if (line.startsWith('-') && !line.startsWith('---')) return 'work-diff-remove'
 return ''
}
