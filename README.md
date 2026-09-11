# Limelit Open

Self-hosted AI visibility tracking. One binary, your keys, your data.

Limelit Open tracks how ChatGPT, Claude, Perplexity, Gemini, Google AI
Overview and Google AI Mode mention and cite your brand against your
competitors, shows it in a dashboard, and exposes all of it as MCP tools so
you can point Claude, or any MCP client, at it and ask in plain language.

It is the open core of [Limelit](https://limelit.co). Same tool names, same
engine ids, same numbers. Moving to the hosted product is one command and
loses nothing.

Status: pre-release. v0.1 is in progress; see the
[issues](https://github.com/limelitgeo/open/issues). The binary builds and
serves `/healthz` today; the wizard, the runner and the MCP server are the
next issues, and every other verb reports that it is not implemented rather
than pretending to work.

## Quick start

```bash
docker run -p 1515:1515 -v limelit:/data ghcr.io/limelitgeo/open
```

or a single static binary from Releases, or

```bash
go install github.com/limelitgeo/open/cmd/limelit@latest
limelit serve
```

Open `http://localhost:1515`. The setup wizard asks for your brand, domain,
category and up to five competitors, fills a starter set of prompts, and
takes one provider key. Press Run. Nothing is spent before that.

## Connect Claude

Claude Desktop or Claude Code, over stdio:

```json
{
  "mcpServers": {
    "limelit": { "command": "limelit", "args": ["mcp"] }
  }
}
```

Remote clients use `limelit serve` and the streamable HTTP endpoint at
`/mcp` with a bearer token from Settings.

Then ask: "How is Acme doing across AI engines this week, and which prompts
are we losing to Globex?" The tool catalog is in [docs/tools.md](docs/tools.md).

## How it works

- You define a property (name, domain, aliases), competitors, and prompts
- You add targets, one per engine and provider:
  `engine:provider[:model][:online]`. API providers call the vendor model
  with web search on. Scraped providers fetch the consumer surface through a
  scraping service. Every number carries an `api` or `scraped` badge because
  the two measure different things. See [docs/providers.md](docs/providers.md)
- On demand or on a schedule, every active prompt runs against every enabled
  target. Each answer is stored as text with its citations
- Mentions are found deterministically: a text search for the names you gave
  us, with rank taken from the enclosing list item. No model call per answer
- Prompts that contain your own brand name are tagged `branded` and left out
  of the headline number by default

## What we count

The brand names you give us, where they appear in each answer, and which
sources the answer cites.

- **Visibility**: share of answers that mention the brand
- **Share of voice**: the brand's mentions over all tracked brands' mentions
  in the same answers
- **Position**: mean rank where the brand appears in a ranked list
- **Citation share**: share of cited sources that are the brand's own site
- **Top sources**: cited domains and URLs, classified as own, competitor,
  social, informational, or other

Every metric carries `n`, the number of answers it rests on, and is
recomputable from stored rows. Answers where a Google surface did not render
at all are excluded from the denominator.

## Cost

Bring your own keys. There is no pricing, no credits and no markup in this
project. Usage counters (calls and tokens per target per day) let you
reconcile against your own provider bill, and `limits.runs_per_day` (default
200) keeps a schedule from surprising you.

## Hosted only

These are Limelit Cloud features and are not in the open core: fan-out
capture and rewrite analysis, prompt generation from your site, personas and
competitor suggestion, discovery of unlisted brands, sentiment and framing,
hallucination guard and correction drafts, perception audits, Search Console
and GA4, agents, sheets and blocks, portfolios and multi-brand, white-label,
digest emails.

`limelit upgrade` moves your property, competitors, prompts and history to
Limelit Cloud and hands back the Cloud MCP configuration.

## Docs

- [docs/tools.md](docs/tools.md): MCP tool catalog
- [docs/providers.md](docs/providers.md): targets, access modes, provider interface, configuration

## Development

Go 1.25 or newer. No other toolchain: no Node, no Docker, no Postgres.

```bash
make build   # bin/limelit
make test    # go test ./...
make lint    # gofmt + go vet
make run     # build, then serve on :1515
```

The database is SQLite at `$LIMELIT_DATA_DIR/limelit.db` (`./data` by
default), created and migrated on first start.

## License

Apache-2.0. See [LICENSE](LICENSE).
