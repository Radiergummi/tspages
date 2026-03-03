---
description: Remove obvious and redundant comments from uncommitted changes
---

🧹 Remove obvious and redundant comments from uncommitted code changes.

📍 Only consider and only edit local code changes that are not yet committed to git.

## Comments to Remove

- Commented out code
- Comments that describe edits like "added", "removed", or "changed" something
- Obvious explanations
  - Comments that are obvious because they're close to method names

## Comments to Keep

- TODO comments
- Comments preventing empty blocks
  - Empty catch blocks, empty else blocks
  - `// deliberately empty`
- Linter/formatter directives
  - `// prettier-ignore`
  - `// eslint-disable-next-line`
  - `// @ts-ignore`

## Comment Position

Move end-of-line comments above the code they describe.
