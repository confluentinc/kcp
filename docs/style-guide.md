# kcp Documentation Style Guide

This guide sets the writing and formatting rules for the kcp documentation site, which MkDocs builds from the Markdown files under `docs/assets/`. Every page there is customer-facing, so apply these rules to every new page and to every section you edit.

Where this guide is silent, follow the [Google developer documentation style guide](https://developers.google.com/style).

## Audience and tone

- Write for a customer planning or running a migration. State what kcp does and what the reader grants, checks, or runs.
- Don't write blunt "kcp lacks X" or "kcp can't do Y" callouts when you can describe the behavior instead. Keep a plain "X isn't supported" statement when the reader needs it to make a decision.
- Don't use meta language such as "inferred", "unverified", or "at the time of writing".
- Don't reference internal design documents, plans, tickets, or pull requests.
- No promotional language.

## Page structure

- Put nothing above the H1.
- Open with a self-contained paragraph of one or two sentences that says what the page is for. Search engines and AI summaries use the first 50 to 70 words. Keep links out of that paragraph, and don't open with limitations or caveats.
- Admonitions (`!!! note`, `!!! warning`): use them sparingly. Never start a section with one, never place two next to each other, never nest them, and keep them short. Content with steps or long lists belongs in the body.
- End a page with `## Related content` for related links, at most seven. Prefix links to other Confluent sites with the site name, for example `Confluent Documentation: [Title](https://docs.confluent.io/...)` or `GitHub: [Title](...)`. Links to other kcp pages need no prefix. A `## Next steps` section that sends the reader onward can sit above it.
- When you move a page, leave a short page at the old path that links to the new location, so existing links keep working.

## Titles and headings

- **H1 (page title):** title case, following the [APA rule](https://apastyle.apa.org/style-grammar-guidelines/capitalization/title-case): capitalize words of four or more letters, including "With" and "From". Make the title complete and unique across the site ("Credentials in the Migration Manifest", not "Credentials"). Prefer task-based titles, and avoid gerunds ("Run a Migration Cutover", not "Running a Cutover").
- **H2 and below:** sentence case. Prefer task or topic headings over gerunds ("Declare the migration type"). A heading that names a field in code font, such as ``## `spec.source` ``, is fine on reference pages.
- Use punctuation in headings sparingly, and don't put ® in headings.
- Renaming a heading changes its anchor. Search `docs/` for links to the old anchor and update them. `make docs-build` fails on broken links and anchors.

## Voice and wording

- Use the second person ("you"). Don't use "we", "our", "us", "let's", or "the user".
- Use the present tense ("kcp removes the fence"), not "will".
- Prefer the active voice. Passive voice is fine when the system performs the action or when it avoids blaming the reader.
- Put a condition at the start of the sentence: "If the route is dynamic, ...".
- Cut weak modifiers and fillers: "very", "quite", "just", "simply", "easily".
- Don't open a sentence with "There is" or "There are".
- Prefer short words: "uses", not "utilizes".
- Don't mention future or planned features ("not yet supported" becomes "not supported", and "today" goes).

## Word list

Apply these to prose. Code, field names, and quoted output stay verbatim.

| Avoid                            | Use                                                        |
| -------------------------------- | ---------------------------------------------------------- |
| e.g.                             | "for example", "such as", or "like"                        |
| i.e.                             | "that is"                                                  |
| etc.                             | Write the list out, or end it with "such as"               |
| via                              | "through", "by using", or "with"                           |
| may                              | "might" for possibility, "can" for ability or permission   |
| once (meaning after)             | "after"                                                    |
| since (meaning because)          | "because"                                                  |
| config, configs                  | "configuration", "configurations"                          |
| please, and/or, (s)              | Rewrite: drop "please", pick "and" or "or", use the plural |
| read/write and other slash pairs | "read and write" or "read or write"                        |
| retryable, retriable             | Write around it ("errors the client can retry")            |
| on-prem, on-premise              | "on-premises"                                              |
| Ctrl-C, `Ctrl-C`                 | Ctrl+C, in plain text                                      |
| id, Id                           | "ID"                                                       |
| .yaml file, .json file           | "YAML file", "JSON file"                                   |

Write these as one word: backend, frontend, datacenter, dataset, datastore, endpoint, filename, timestamp, runtime, lowercase, uppercase, workflow, keystore, truststore. Hyphenate built-in and multi-tenant. Use American spelling (recognize, behavior, canceled, license).

## Product and component names

- **Apache Kafka®:** mark the first mention in the body of each page, then write "Kafka". Don't put ® in headings or code. Leave a defined term such as "Kafka-family credentials" unchanged.
- Never join a Confluent product name to an Apache project name: write "Kafka cluster on Confluent Cloud", not "Confluent Cloud Kafka cluster".
- Write "Confluent Platform" and "Schema Registry" without "the".
- **Gateway:** write "Confluent Gateway" at the first mention on a page where it reads naturally, then "Gateway", capitalized, for the product, a deployment, its pods, or its custom resource ("Gateway pods", "Gateway CR"). Image names and CRD kinds stay as they are.
- **kcp:** write it in lowercase, as the command is spelled.
- Spell out an abbreviation the reader might not know at its first mention outside a heading, followed by the abbreviation in parentheses: custom resource (CR), custom resource definition (CRD), Confluent for Kubernetes (CFK), access control list (ACL), Metadata Service (MDS). Common terms such as SASL, TLS, REST, API, YAML, and JSON need no expansion.

## Punctuation and formatting

- Don't use em dashes or en dashes in prose. Use a colon, a period, a comma, or parentheses instead. An em dash is fine as the "not applicable" marker in an empty table cell.
- Avoid semicolons. Split the sentence or restructure it.
- Use the serial comma: "source, destination, and cluster link".
- Don't use exclamation points.
- Don't hide important information in parentheses.
- Don't use bold or italics for emphasis. A bold run-in label at the start of a paragraph or list item ("**Fence.** The Gateway ...") is fine. Use italics only when you introduce a term you then define.

## Numbers, units, and versions

- Spell out one through nine in prose. Use numerals for 10 and up, and always for units of measure ("5 seconds"), versions, and literal values.
- Use commas in numbers of four or more digits (1,024), and put a space between a number and its unit (10 GB).
- For version ranges, write "and later" or "and earlier", never "higher" or "lower". Hard-code versions only where they are real requirements.

## Lists and procedures

- Don't make a list of one item.
- Use sentence case for every item.
- End an item with a period if it is a sentence or contains a verb. Leave out end punctuation for single words, fragments without a verb, items entirely in code font, and items that are entirely link text. Be consistent within a list.
- Number the steps of a procedure. Number substeps with lowercase letters, and avoid a third level.

## Code and commands

- Don't prefix commands with `$`.
- Put a command and its output in separate code blocks.
- Write placeholders the reader replaces as `<lowercase_with_underscores>`, for example `<api_key>`.
- Use code font for commands, flags, field names, file paths, and literal values.

## Links

- Use meaningful link text, never "here" or "this page". Say why the reader should follow the link before the link: "To grant the Kubernetes permissions, see [Permissions for Gateway Migrations](...)".
- Use `https` wherever the server supports it.
- Prefer a Confluent documentation page over an Apache Kafka project page on the same subject.

## Navigation

The left navigation comes from the `.pages` files. Write nav titles in title case. A nav title can be shorter than the page's H1, for example "Overview".

## Command reference

`make docs-gen` generates `docs/assets/command-reference/` from the Cobra help text in `cmd/`. The same rules apply to the `Short` and `Long` help strings, because they end up on the docs site.

## Before you open a pull request

Run `make docs-build`. It builds the site in strict mode and fails on broken internal links and anchors. To check a page in the browser, run `make docs-serve` and open <http://localhost:8000>.
