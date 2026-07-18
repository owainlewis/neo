# neo docs website

Astro + [Starlight](https://starlight.astro.build), with a custom product landing page and a
shared, technical visual system for the documentation. Deployed to Cloudflare Pages at
`neoharness.dev`.

```bash
bun install
bun run dev      # http://localhost:4321
bun run build    # outputs to dist/
bun run preview  # serve the built output locally
```

Configure Cloudflare Pages with `website` as the project root and `dist` as the publish directory.

## How content is organized

- `src/content/docs/index.mdx` — the marketing entry point. Its page UI lives in
  `src/components/LandingPage.astro` and is hand-written.
- `src/content/docs/docs/install.md`, `quick-start.md` — hand-written user-facing guides, adapted
  from the root `README.md`.
- `src/content/docs/docs/reference/**` — **generated, not committed.** `scripts/prepare-docs.mjs`
  copies `../docs/developer/**` (itself produced by `go run ./cmd/neo-docs` in the main repo) into
  this folder before every `dev`/`build`, adding Starlight frontmatter and rewriting `*.md` links
  into site routes. Don't hand-edit files here — they're wiped and regenerated on every run. To
  change this content, edit the Go doc generator or the source docs in `../docs/developer`.

## Theming gotcha (read before touching `src/styles/custom.css`)

Starlight's color tokens always mean "the token closest to text" (`gray-1`) through "the token
closest to background" (`gray-6`/`gray-7`) — **in both themes**, `--sl-color-white` is the *text*
color and `--sl-color-black` is the *background* color, regardless of whether that theme is
visually light or dark. It's easy to instinctively set `--sl-color-black` to a literally dark hex
value in the light-theme block (because "black" reads as "should be dark") — that produces dark
text on a dark background and looks like the whole site lost its styles. If colors ever look
inverted or unreadable after an edit, check that `--sl-color-white`/`--sl-color-black` still point
the right direction for the theme block they're in.

Starlight's own root tokens live in `@layer starlight.base`. This stylesheet is loaded unlayered
via the `customCss` config, which is why unqualified overrides here beat Starlight's defaults
regardless of source order — but you still need one block per `data-theme` value (`light`, `dark`)
because Starlight defines its own `:root[data-theme='light']` override that will otherwise win on
specificity for that specific case.
