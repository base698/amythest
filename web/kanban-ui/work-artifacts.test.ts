import test from 'node:test'
import assert from 'node:assert/strict'
import { githubCommitURL, diffLineClass } from './work-artifacts.ts'
const sha = 'a'.repeat(40)
test('GitHub-backed commit identities link to the exact object', () => {
 assert.equal(githubCommitURL(`commit:github.com/example/project:${sha}`, sha), `https://github.com/example/project/commit/${sha}`)
})
test('local, mismatched and unsafe commit identities never become GitHub links', () => {
 for (const key of [`commit:collector:/projects/demo:${sha}`, `commit:github.com/example/project:${'b'.repeat(40)}`, `commit:github.com/../project:${sha}`, `commit:github.com.evil/example/project:${sha}`, `commit:github.com/user@host/project:${sha}`]) assert.equal(githubCommitURL(key, sha), null)
 assert.equal(githubCommitURL(`commit:github.com/example/project:${sha}`, 'HEAD'), null)
})
test('diff lines distinguish additions and removals from file headers', () => {
 assert.equal(diffLineClass('+new line'), 'work-diff-add')
 assert.equal(diffLineClass('-old line'), 'work-diff-remove')
 assert.equal(diffLineClass('+++ b/file.txt'), '')
 assert.equal(diffLineClass('@@ -1 +1 @@'), 'work-diff-heading')
})
