# Products mode

Products mode is Topophany for people who ship things: an indie developer, or a
small team, running a few apps and websites on a mix of Cloudflare (a Worker, a
Pages site, a proxied origin), a server or two of their own and a managed
platform such as Supabase or Vercel. It reads every application in your topology
as a product and lays them out one under another, each with the parts your
users reach and what each part rests on, so the first look answers *is anything
I ship broken, which part, and where in its stack?*

It is a way of looking, not a second monitor. The checks, the collector, the
evidence and every judgment are the ones Topophany already makes; Products mode
only chooses what comes first. Switch back to Network mode at any time.

```text
PRODUCTS · 4 products · 1 incident
   PRODUCT           LINE       RESTS ON                                      P50  RECENT                 UP
 ▲ Habitly    ios  ▲ API        CF workers ● › Habitly DB ▲                 54 ms  ▆▇▅▅▅✖▆▃▃▇▅▄▇▇▃▅▅▄   98.8%
                     └ WARN · some access checks pass; others fail, degrade or lack current evidence
                   ● Site       CF pages ●                                  55 ms  ▅▅▇▆▇▅▅▄▄▆▅▄▇▅▅▆▅▆  98.68%
 ○ Snapmeal   ios  ○ API        CF ● › Hetzner ○ › Snapmeal DB ○           142 ms  ✖✖✖✖✖✖✖✖✖✖✖✖✖✖✖✖✖✖  98.12%
                     └ ACCESS FAIL · for 7m · HTTP 521 from Cloudflare LIS · seen from Lisbon · ⚑ 1 incident
                   ● Site       CF pages ●                                  54 ms  ▄▆▇▆▆▇▄▄▃▅▅▄▆▄▅▅▅▆  98.91%
 ▲ Driftwood  web  ▲ Driftwood  Vercel functions ● › Driftwood DB ○        135 ms  ▅▅▆▅▆▆▇▅▇▆▆▇▇▅▅▇▅▅  97.96%
                     └ WARN · some access checks pass; others fail, degrade or lack current evidence
 ○ Notebook   web  ○ Blog       CF ● › Hetzner ○                           142 ms  ▇▆▆✖✖✖✖✖✖✖✖✖✖✖✖✖✖✖  98.44%
                     └ ACCESS FAIL · for 14m · HTTP 521 from Cloudflare LIS · seen from Lisbon · ⚑ 1 incident

 CERTIFICATES  notebook.example 34 days · api.habitly.example 38 days · api.snapmeal.example 42 days +1
```

<sub>`topo preview --config examples/products.json --mode products`, half an
hour after the server behind Cloudflare stopped answering. Habitly's API and
Driftwood warn because the observer on that server went dark with it: some of
their checks lack current evidence.</sub>

## What it answers, in order

What an owner of several products needs first is not the map of their network
but the state of what their users touch. The board puts what matters in this
order, and keeps all of it within one glance:

1. **Is anything broken for my users right now?** A mark per product and per
   line. Calm products are quiet; color and words appear only where something
   needs a look.
2. **Which product, and which part of it?** One line per part a user reaches:
   the API behind an iOS app, a website, a background job. A product takes as
   many rows as it has lines, so an app with an API and a landing page is two
   rows, and nothing is folded away.
3. **Where in its stack?** Each line's chain, in the order a request meets it:
   the edge in front (Cloudflare), the machine or platform it runs on (your
   Hetzner box, Cloudflare Workers, Vercel), and the services it depends on
   (Supabase, Postgres), each with its own mark.
4. **Since when, and is it confirmed?** How long the failing run has lasted, and
   the open incidents that rest on the product's checks.
5. **Is it slow?** The median latency of the line's lead check over the day,
   its recent samples, and the latency budget it declares.
6. **How reliable has it been?** The lead check's share up over 30 days, or over
   as much as the collector holds ("99.91% · 3d").
7. **What breaks next?** Each product's certificate that expires soonest, at
   the board's foot, soonest first, lit when it is inside the warning window
   its checks declare.

What Products mode leaves out of the first look, and keeps one switch away:
the map, the observers and what each one sees, declared paths and proxy
chains, and time zones. They answer *where on the network*, which matters
once you know which product to look into.

## The board

### Lines, chains and the lead check

- **A product** is an application group, exactly as the Services tab groups
  them: one per application and environment, in the same order. Its mark is the
  group's; services in no application belong to no product and are counted at
  the foot ("2 services in no product").
- **A line** is a service of the product that nothing else in the same product
  depends on. A database your API depends on is not a line of its own: it
  appears in the API's chain. The line's mark is the service's. Its state word
  is the one the Services tab gives it (`affected`, `ACCESS FAIL`, `PATH WARN`,
  `LOCAL FAIL`, `HEALTH WARN`, …), or, where that says no more than up, the
  service's health: `WARN` for an API whose users reach it while some of its
  checks fail or lack current evidence. A line simply up says no word.
- **The chain** is what the line rests on. A node in it (an edge, a machine, a
  platform) shows only its own judgment: up when it answered, unknown (○) when
  nothing proves it. Whether a node is *affected* is left out, because an edge
  or a server shared by several products is affected by any of them, and the
  board must not paint Snapmeal's Cloudflare amber for Habitly's failing Worker.
  A dependency shows that service's state.
- **The lead check** is the one whose numbers the line shows: a failing check
  first, then a warning, then one that passes, and one with nothing current
  last; among equals the one closest to your users (a `user_entry` check, then
  a remote one, a loopback one, a report), an HTTP check before a handshake,
  then the first in the topology. So the line follows trouble when there is
  some, and holds still when there is none.

### Density

The board is built to be read without opening anything:

- **One row per line.** The product's name, kind and mark head its first row;
  the rows under it leave those cells blank, so products read as blocks.
- **Columns line up across products**, so latency and uptime compare down the
  board at a glance.
- **Calm is quiet.** Up marks are drawn at half strength, an up line says no
  word, and a product with nothing to report takes no more than its rows.
- **Trouble opens, never moves.** Products keep the topology's order, so
  nothing changes place while you read. A line in trouble gains one row
  beneath it: its state word, how long its lead check has failed, what the
  check says, where it was seen from and the incidents on its product. Where
  the row is short of room, where it was seen from goes first; how long and
  the incidents stay.
- **What breaks next stays put.** The certificates sit at the board's foot,
  where the lines leave room for them, and never push a line out of sight.
- **Names alike.** Where one line's chain does not fit with full names, every
  chain names its layers short ("CF workers", "Hetzner"), so the same layer
  reads the same on every row; narrower still, a chain keeps its marks alone.
  A provider whose mark is shaped like a state, as Vercel's ▲ is, keeps its
  name, and with `--nerd-font-icons` the providers' logos stand in.
- **Bounded.** At most 64 products and 8 lines each are listed, 64 lines in
  all, with 3 dependencies in a chain; the rest are counted, never silently
  dropped.

### Selecting a line

Selecting a line shows its evidence beside the board: the product, the line's
state and why, each layer of its chain with its role, the lead check with its
latency, budget, recent samples and share up over a day, a week and a month,
the certificate, and the service's own evidence as the Services tab gives it.

## Where it appears

### The terminal UI

Products mode changes the Overview: the products board takes the map's place,
and the status card and the selected line's evidence stand beside it. Every
other tab is as before; the Topology tab still has all three maps.

- `P` turns the Overview to Products, from any tab, and back to Network; the
  switch lasts the session. **Settings** (`,`) offers it as *Overview*, to
  apply now or save as the `mode` preference.
- `topo --mode products` opens in Products mode for one launch; `topo
  preferences set --mode products` makes it the default.
- `↑`/`↓` move through the lines, `Enter` opens a line's evidence, `/` filters
  by product, line, provider or anything in a chain.
- On a phone in `topo preview --device`, each line is a finger tall: its
  product and name over its chain and numbers, with what it says where it asks
  for a look. A tablet keeps the terminal's board.

### For scripts and agents

`topo show products` prints the board as a table, a line per row with its
state, what it rests on (a layer's state named where it is not up), its p50,
its share up and its lead check; then what each line that asks for a look
says, and the certificates soonest first. `--json` gives the view's board as
it is, and `topo preview --config FILE --scenario N show products` reads a
scenario without a collector. `products.list` in `topo monitor stream`
carries the same board to widgets and menu bars; the [metric
reference](REFERENCE.md#template-and-metric-contract) gives its shape.

### The macOS menu bar

[topophany-bar](https://github.com/WhitenWhiten/topophany-bar) has the same
mode: **Settings → General → Mode → Products** lays the panel out for products
(Status, then Products, Needs attention, your servers and This Mac) and keeps
your Network layout as you left it, so switching back restores it.

## Setting it up

Declare each product as an application, its parts as services, and give each
part a check from where your users are:

```sh
topo templates products --output topology.json   # Habitly, Snapmeal, Driftwood and Notebook
topo validate --config topology.json
topo preview --config topology.json --mode products
```

The template is [`examples/products.json`](../examples/products.json): two iOS
apps, one with its API on a Cloudflare Worker and Supabase, one on a Hetzner box
behind Cloudflare with Postgres beside it; a web app on Vercel and Supabase; and
a blog on the same box. Its checks start paused. Rename, readdress, then enable
what you mean to run. In `topo preview` it offers the scenarios that apply to
it: the origin behind Cloudflare down, the edge unreachable from home, a
certificate inside its window, and the box's CPU, memory or disk under pressure.

- **`kind`** on an application says what it is, `ios`, `android`, `web`, `api`,
  `desktop`, `extension` or `bot`, or any other short identifier, and labels
  the product. It changes nothing about how anything is judged. Collectors and
  workers older than this release refuse a topology that sets it; older
  viewers ignore it.
- **A check from where your users are.** Run a worker on your laptop or a small
  VPS and mark its public checks `"availability_scope": "user_entry"`.
- **A check of the origin itself.** A request through Cloudflare proves the
  edge, not your server behind it. On a box you run, add a loopback check
  (`127.0.0.1`) from a worker on the box, so the chain can say which side
  failed.
- **What breaks next.** `tls_min_days` on public HTTPS checks records the
  certificate and warns inside the window; `max_duration_ms` declares a
  latency budget.
- **Supabase.** Check `https://<project>.supabase.co/rest/v1/` with the anon
  key from the worker's environment: `"http": {"request_header_envs":
  {"apikey": "SUPABASE_ANON_KEY"}}`. Never write a key into the topology.

To have your coding agent write it, ask it to read the template and your
deploy configuration:

> Write a Topophany topology of my products to `topology.json`, starting from
> `topo templates products`. Read my `wrangler.toml`, Vercel and Supabase
> projects, Dockerfiles and reverse-proxy configs. Declare each app or site as
> an application with a `kind`, each deployed part as a service on the edge,
> platform or machine it runs on, `depends_on` for its databases, and checks
> from my laptop and my server. Never put a secret in the file. Run `topo
> validate --config topology.json` until it passes.

## What it does not claim

- **Nothing new is concluded.** A product's mark is its application group's,
  which never reads down for the group: a group describes its members, not the
  business. A line's is its service's, a node's its own, a dependency's that
  service's.
- **An edge's answer proves the edge.** A 52x through Cloudflare leaves your
  origin unknown (○) until a check that does not go through the edge proves it.
- **Uptime is over measured time.** A share is never rounded up, so only all of
  it reads 100%, and a window the collector holds only part of says how much.
- **Everything expires.** Each lead check's readings, since and uptime, and each
  certificate's days, go at their own check's deadline; the board keeps its
  names and marks only while the row is current.

## Not in this version

Domain registration expiry (it needs RDAP lookups), deploy and build status,
platform analytics such as Worker error rates or Supabase usage, App Store
review state, and desktop widgets and menu bar pieces for products.
