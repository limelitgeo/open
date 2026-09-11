<h1 align="center">Limelit Open</h1>

<p align="center">
  <strong>Self-hosted AI visibility tracking. One binary, your keys, your data.</strong>
</p>

<p align="center">
  Track how ChatGPT, Claude, Perplexity, Gemini, Google AI Overview and Google AI Mode
  <br />
  mention and cite your brand against your competitors.
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache 2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"></a>
  <a href="go.mod"><img alt="Go 1.25+" src="https://img.shields.io/badge/go-1.25%2B-00ADD8.svg?logo=go&logoColor=white"></a>
  <a href="#status"><img alt="Status: pre-release" src="https://img.shields.io/badge/status-pre--release-orange.svg"></a>
  <a href="https://limelit.co"><img alt="Limelit Cloud" src="https://img.shields.io/badge/hosted-limelit.co-111.svg"></a>
</p>

---

Also known as **AEO** (Answer Engine Optimization), **GEO** (Generative Engine
Optimization), **AIO** (AI Search Optimization) and **LLMO** (LLM
Optimization). They are four names for the same question: when someone asks an
AI assistant about your category, does your brand come up, and which sources
does the answer trust?

Limelit Open answers that on your own infrastructure. It is the open core of
[Limelit](https://limelit.co), and it shares its tool names, engine ids and
metric definitions, so moving to the hosted product is one command.

## Contents

- [Status](#status)
- [Why this exists](#why-this-exists)
- [Features](#features)
- [Quick start](#quick-start)
- [Connect Claude (MCP)](#connect-claude-mcp)
- [Engines and providers](#engines-and-providers)
- [Configuration](#configuration)
- [How the numbers are computed](#how-the-numbers-are-computed)
- [Cost](#cost)
- [Compared with other AI visibility tools](#compared-with-other-ai-visibility-tools)
- [Hosted only](#hosted-only)
- [Glossary](#glossary)
- [FAQ](#faq)
- [Roadmap](#roadmap)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Status

**Pre-release. v0.1 is being built in the open.**

What works today: the binary builds and installs with no toolchain beyond Go,
creates and migrates its SQLite database on first start, and serves
`GET /healthz`. Every other verb reports that it is not implemented rather
than pretending to work.

Everything marked *planned* below is tracked in
[issues](https://github.com/limelitgeo/open/issues) under the
[v0.1 milestone](https://github.com/limelitgeo/open/milestone/1). Watch
releases to hear when it ships.

## Why this exists

Most AI visibility platforms are closed SaaS. You send them your prompts, they
send you a number, and how that number was computed is theirs. That is an
awkward trade for a metric you are going to put in a board deck.

Limelit Open takes the other side of it:

- **Your infrastructure.** Prompts, competitors and every answer live in a
  SQLite file you own. Nothing leaves the box except the calls to the engines.
- **Your keys, your bill.** Bring your own provider keys. There is no pricing,
  no credits and no markup anywhere in this project, and no per-seat tax on
  looking at your own data.
- **Auditable by construction.** Mentions are found by text search, not by a
  model deciding what it saw. Every metric is derived from stored rows and
  can be recomputed. The formulas are below and the code is right here.
- **Honest about what is measured.** An answer from a vendor API with web
  search on is not the answer a person sees in ChatGPT. Both are useful, they
  are different surfaces, and every number carries which one it came from.

## Features

| | Feature | Status |
|---|---|---|
| 📊 | **Visibility tracking**: how often each engine mentions your brand, per prompt and over time | planned |
| 🏆 | **Share of voice**: your mention rate next to every tracked competitor, on the same prompts | planned |
| 🔗 | **Citation analysis**: every URL an answer cited, classified as your own, a competitor, social, informational or other | planned |
| 🧮 | **Prompt by target grid**: one cell per prompt and engine, click through to the answers behind it | planned |
| 🔌 | **Hybrid providers**: vendor APIs and consumer-surface scrapers behind one interface, labeled on every metric | planned |
| 🤖 | **MCP server**: stdio and streamable HTTP, so Claude can read your visibility data and answer in plain language | planned |
| 🖥️ | **Dashboard**: embedded in the binary, no Node, no separate frontend to deploy | planned |
| ⏱️ | **Scheduler**: daily or cron, with a hard `runs_per_day` ceiling so nothing surprises you | planned |
| 📤 | **Export**: JSON or CSV of everything, the same payload the Cloud upgrade sends | planned |
| ☁️ | **One-command upgrade**: move your property, prompts and history to Limelit Cloud | planned |
| 💾 | **Single binary, SQLite**: no cgo, no Docker requirement, no Postgres | **working** |

## Quick start

### Docker

```bash
docker run -p 1515:1515 -v limelit:/data ghcr.io/limelitgeo/open
```

### Binary

Download from [Releases](https://github.com/limelitgeo/open/releases), or
build from source with Go 1.25 or newer:

```bash
go install github.com/limelitgeo/open/cmd/limelit@latest
limelit serve
```

Then open <http://localhost:1515>.

The setup wizard asks for your brand name, domain, category and up to five
competitors, fills a starter set of prompts, and takes one provider key.
Press Run. Nothing is spent before that.

> Docker images and release binaries land with v0.1. Until then, build from
> source.

### Commands

```
limelit serve     dashboard, JSON API, MCP over HTTP, and the scheduler
limelit mcp       MCP over stdio, for Claude Desktop and Claude Code
limelit run       one evaluation pass, then exit (for cron)
limelit export    write everything this instance knows to stdout
limelit upgrade   move this instance to Limelit Cloud
limelit version   version and build info
```

## Connect Claude (MCP)

Limelit Open is MCP-first. The dashboard shows you the numbers; the MCP server
lets an assistant read them, cross-reference them and quote the evidence.

Claude Desktop or Claude Code:

```json
{
  "mcpServers": {
    "limelit": { "command": "limelit", "args": ["mcp"] }
  }
}
```

Remote clients point at `limelit serve` and its streamable HTTP endpoint at
`/mcp`, with a bearer token from Settings.

Then ask things like:

- "How is Acme doing across AI engines this week?"
- "Which prompts are we losing to Globex, and what do those answers cite instead of us?"
- "Show me the answers behind our visibility drop, with the exact quotes."

Tool names mirror Limelit Cloud, so a conversation or a skill written against
this server keeps working after you upgrade. Full catalog:
[docs/tools.md](docs/tools.md).

## Engines and providers

A **target** is one way of asking one engine, written
`engine:provider[:model][:online]`:

```
chatgpt:openai:gpt-5.5:online     ChatGPT's model through OpenAI's API, web search on
chatgpt:dataforseo:online         chatgpt.com as a user sees it, through a scraper
claude:anthropic:online
perplexity:perplexity:sonar
gemini:google:online
ai_overview:dataforseo
ai_mode:dataforseo
bing_copilot:searchapi
```

Two targets can point at the same engine. They are tracked separately and
never averaged together, because they measure different things.

**Engines**: `chatgpt`, `claude`, `perplexity`, `gemini`, `ai_overview`,
`ai_mode`, `bing_copilot`.

**Providers**:

| Access | Providers | What it measures |
|---|---|---|
| `api` | `openai`, `anthropic`, `perplexity`, `google`, `openrouter` | The vendor's model with web search enabled, billed per token |
| `scraped` | `dataforseo`, `searchapi`, `cloro`, `brightdata`, `oxylabs`, `olostep` | The consumer surface a person actually sees, billed per request |

Adding a provider is one HTTP call, one response parse and a fixture, against
a small Go interface. See [docs/providers.md](docs/providers.md); the open
adapter issues are labeled
[good first issue](https://github.com/limelitgeo/open/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

## Configuration

`limelit.yaml` holds what to track. It is meant to be committed and diffed, so
it never holds a key.

```yaml
property:
  name: Acme
  domain: acme.com
  aliases: [Acme Inc, acme.io]

competitors:
  - { name: Globex, domain: globex.com }
  - { name: Initech, domain: initech.com }

targets:
  - chatgpt:openai:gpt-5.5:online
  - claude:anthropic:online
  - perplexity:perplexity:sonar
  - ai_overview:dataforseo

limits:
  runs_per_day: 200

schedule: daily   # or off, or a cron expression
```

Credentials come from the environment (or the settings store, with the
environment winning):

```
OPENAI_API_KEY, ANTHROPIC_API_KEY, PERPLEXITY_API_KEY, GOOGLE_API_KEY,
OPENROUTER_API_KEY, DATAFORSEO_LOGIN, DATAFORSEO_PASSWORD, SEARCHAPI_KEY,
CLORO_API_KEY, BRIGHTDATA_API_TOKEN, OXYLABS_USERNAME, OXYLABS_PASSWORD,
OLOSTEP_API_KEY
```

`LIMELIT_DATA_DIR` sets where the SQLite database lives (default `./data`).

## How the numbers are computed

Nothing here is a black box, so here is the whole method.

1. **You define the brands.** Your property (name, aliases, domain) and your
   competitors (name and domain; the domain is the identity key).
2. **Every active prompt runs against every enabled target**, on demand or on
   a schedule. Each answer is stored as text with the sources it cited.
3. **Mentions are found by text search.** For the names you gave us, in the
   text we stored. Rank comes from the enclosing list item when the answer is
   a ranked list. No model decides what it saw, so nothing can be invented
   and nothing is missed because a model stayed quiet.
4. **Citations are classified** by host: your own site, a tracked competitor,
   social, informational, or other.
5. **Metrics are aggregations over those rows**, and only those rows:

   | Metric | Definition |
   |---|---|
   | Visibility | share of answers that mention the brand |
   | Share of voice | the brand's mentions over all tracked brands' mentions in the same answers |
   | Position | mean rank where the brand appears in a ranked list |
   | Citation share | share of cited sources that are the brand's own site |

Two denominator rules, because this is where visibility metrics usually go
wrong:

- **Prompts that name your own brand are tagged `branded`** and left out of
  the headline number. Asking an engine about yourself and counting the answer
  is the easiest way to inflate the metric.
- **An answer surface that did not render is not a miss.** If a query produced
  no Google AI Overview at all, that run is excluded from the denominator
  rather than counted as an answer that ignored you.

Every result carries `n`, the number of answers it rests on, so a number from
four runs never gets read as a trend.

## Cost

There is no pricing, no credits and no markup in this project. You pay your
providers directly, at their rates.

What the tool gives you instead:

- **Usage counters**: calls and tokens per target per day, so you can
  reconcile against your own provider bill.
- **A hard ceiling**: `limits.runs_per_day` (default 200). An evaluation that
  would exceed it is refused before anything is spent, and tells you by how
  much.
- **No per-answer model call.** Mention detection is a text search, so the
  only thing you pay for is the answer itself.

## Compared with other AI visibility tools

The commercial platforms in this category are good products with real
capabilities this project does not have. The axis where an open core wins is
control: reading the code behind every number, running it on your own
infrastructure, and keeping the data.

| | Open source | Self-hostable | Auditable metrics | Data ownership | Keys | Pricing |
|---|---|---|---|---|---|---|
| **Limelit Open** (this repo) | Yes, Apache-2.0 | Yes | Yes, the scoring code is this repo | Yours, a SQLite file | Bring your own | Free |
| [**Limelit Cloud**](https://limelit.co) | Open core, this repo | Yes, by self-hosting this repo | Yes, the same definitions, published here | Vendor-hosted, exportable | Included | Commercial |
| [Profound](https://www.tryprofound.com) | No | No | No | Vendor-hosted | Included | Commercial |
| [Peec AI](https://peec.ai) | No | No | No | Vendor-hosted | Included | Commercial |
| [Otterly.AI](https://otterly.ai) | No | No | No | Vendor-hosted | Included | Commercial |
| [Scrunch AI](https://www.scrunchai.com) | No | No | No | Vendor-hosted | Included | Commercial |
| [Ahrefs Brand Radar](https://ahrefs.com/brand-radar) | No | No | No | Vendor-hosted | Included | Commercial, bundled with Ahrefs |
| [Semrush AI Toolkit](https://www.semrush.com) | No | No | No | Vendor-hosted | Included | Commercial, bundled with Semrush |

Limelit is the only one of these with an open-source core: Limelit Cloud is
the managed version of this project, and the metric definitions it uses are
the ones published here. Choosing it is a hosting decision, not a lock-in one,
and `limelit export` moves your data either way.

Current prices change often; check each vendor's own pricing page.

To be fair about where the commercial tools are ahead today: several offer
prompt volume estimates, sentiment analysis, AI crawler analytics, on-page
content optimization, and integration with an established SEO dataset. This
project does none of those, and some of them are deliberately
[hosted only](#hosted-only).

## Hosted only

These are [Limelit Cloud](https://limelit.co) features and are not in the open
core:

- Query fan-out capture and rewrite analysis
- Prompt generation from your site, personas, competitor suggestion
- Discovery of brands you did not list
- Sentiment and framing, hallucination guard, correction drafts
- Perception audits
- Google Search Console and GA4
- Agents, sheets and blocks
- Portfolios and multi-brand, white-label, digest emails

`limelit upgrade` moves your property, competitors, prompts and history to
Limelit Cloud and hands back the Cloud MCP configuration. Nothing is retyped.

## Glossary

- **AI visibility**: whether and how a brand appears in answers generated by
  AI assistants, as distinct from ranking in a list of blue links.
- **AEO, Answer Engine Optimization**: the practice of improving that.
- **GEO, Generative Engine Optimization**: the same practice, different name.
- **AIO, AI Search Optimization**: likewise.
- **LLMO, LLM Optimization**: likewise.
- **Answer engine**: a product that answers a question directly rather than
  returning links. ChatGPT, Perplexity, Google AI Overviews and AI Mode,
  Claude, Gemini, Copilot.
- **Citation**: a source an answer engine attributes or links while answering.
- **Share of voice**: your brand's share of all tracked brand mentions across
  the same set of answers.
- **Query fan-out**: the web searches an engine runs to ground an answer
  before writing it.

## FAQ

**Does this replace my SEO tool?**
No. It measures a different surface. Traditional SEO tools measure ranked
links; this measures what a generated answer says and cites.

**Why does my ChatGPT number differ from what I see in ChatGPT?**
Because an API call with web search on and the consumer product are different
surfaces. Both are tracked, they are labeled `api` and `scraped`, and they are
never averaged together. To measure what a person sees, use a scraped target.

**Do I need a scraper account?**
Not to start. One API key gets you running. Scrapers are what you add when you
want the consumer surface or the Google AI surfaces, which have no API.

**Does any of my data leave my machine?**
Only the prompt text, in the calls to the engines and scrapers you configure.
Answers, mentions, citations and usage stay in your SQLite file.

**Can I track more than one brand?**
Not in v0.1, which is one property per instance. Run several instances, or use
the hosted product, which is built for portfolios.

**What database does it use?**
SQLite, pure Go, no cgo. A Postgres option is on the list, not in v0.1.

## Roadmap

v0.1 is the first usable release: the wizard, the runner, the dashboard, the
MCP server, the API providers and the first scrapers. Tracked in the
[v0.1 milestone](https://github.com/limelitgeo/open/milestone/1).

After that: scraped ChatGPT and Gemini through LLM-scraper endpoints,
multi-property per instance, Postgres as an alternative store.

## Development

Go 1.25 or newer. No other toolchain: no Node, no Docker, no Postgres.

```bash
make build   # bin/limelit
make test    # go test ./...
make lint    # gofmt + go vet
make run     # build, then serve on :1515
```

Layout:

```
cmd/limelit        the binary and its verbs
internal/config    limelit.yaml plus credentials from the environment
internal/store     SQLite, embedded migrations
internal/httpx     HTTP surface: dashboard, JSON API, MCP over HTTP
docs/              the tool catalog and the provider contract
```

## Contributing

Issues are labeled by area, and the provider adapters are deliberately small
and self-contained. Start with
[good first issue](https://github.com/limelitgeo/open/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

Two rules worth knowing before a pull request:

- Original code only. Do not copy code in from other projects.
- Every provider ships with recorded fixtures and tests that run with no
  network and no keys.

## License

Apache-2.0. See [LICENSE](LICENSE).
