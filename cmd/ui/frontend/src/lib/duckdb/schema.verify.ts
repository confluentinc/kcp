/**
 * schema.verify.ts — sanity-check for schema.ts
 *
 * Run with: yarn schema:verify
 *
 * Checks:
 *   - TABLE_METADATA, TABLE_NAMES, CREATE_STATEMENTS all have 16 entries
 *   - Every CREATE TABLE has balanced parentheses
 *   - No trailing comma before the closing paren
 *   - Every statement ends with a semicolon
 *   - Prints the first CREATE TABLE for visual inspection
 */

import { TABLE_METADATA, TABLE_NAMES, CREATE_STATEMENTS } from './schema.ts'

let failed = false

function fail(msg: string) {
  console.error(`FAIL: ${msg}`)
  failed = true
}

// Count checks
if (TABLE_METADATA.length !== 16) fail(`TABLE_METADATA.length = ${TABLE_METADATA.length}, want 16`)
if (TABLE_NAMES.length !== 16) fail(`TABLE_NAMES.length = ${TABLE_NAMES.length}, want 16`)
if (CREATE_STATEMENTS.length !== 16) fail(`CREATE_STATEMENTS.length = ${CREATE_STATEMENTS.length}, want 16`)

console.log(`TABLE_METADATA.length    = ${TABLE_METADATA.length}`)
console.log(`TABLE_NAMES.length       = ${TABLE_NAMES.length}`)
console.log(`CREATE_STATEMENTS.length = ${CREATE_STATEMENTS.length}`)

// Structural checks on each statement
for (const sql of CREATE_STATEMENTS) {
  const open = (sql.match(/\(/g) ?? []).length
  const close = (sql.match(/\)/g) ?? []).length
  if (open !== close) fail(`Unbalanced parentheses in:\n${sql}`)
  if (/,\s*\n\);/.test(sql)) fail(`Trailing comma before closing paren in:\n${sql}`)
  if (!sql.trim().endsWith(';')) fail(`Missing trailing semicolon in:\n${sql}`)
}

// Print first statement for visual inspection
console.log('\n--- clusters CREATE TABLE ---')
console.log(CREATE_STATEMENTS[0])

if (failed) {
  console.error('\nOne or more checks failed.')
  process.exit(1)
} else {
  console.log('\nAll checks passed.')
}
