# Frontend Architecture

> Tailwind CSS v4 + Alpine.js + HTMX, rendered by `html/template` and embedded
> in the binary. One build step, run locally, never on the server.

Read this when `frontend: server`. [htmx.md](htmx.md) covers server
interaction and [mobile-navigation.md](mobile-navigation.md) covers the mobile
template tree; both assume this document.

---

## Table of Contents

- [Philosophy](#philosophy)
- [File Structure](#file-structure)
- [The Build Step](#the-build-step)
- [Design Tokens](#design-tokens)
- [Visual Design Rules](#visual-design-rules)
- [The Component Layer](#the-component-layer)
- [Alpine.js and the CSP](#alpinejs-and-the-csp)
- [Alpine + HTMX](#alpine--htmx)
- [Component Patterns](#component-patterns)
- [Responsive Design](#responsive-design)
- [RTL Support](#rtl-support)
- [Dark Mode](#dark-mode)
- [Accessibility](#accessibility)
- [Testing the Rules](#testing-the-rules)

---

## Philosophy

- **Utility-first CSS.** Styling lives next to the markup it styles.
- **A thin component layer** for the handful of things repeated everywhere
  (buttons, inputs, cards). Not a framework.
- **Alpine for behaviour, HTMX for server interaction.** No SPA.
- **Build locally, commit the output.** `go build` embeds the compiled
  stylesheet; no server and no deploy step ever runs a CSS toolchain.
- **Progressive enhancement.** Forms and links work without JavaScript.
- **Every user-visible string goes through the catalog** — labels, `aria-label`
  text, `sr-only` text, `hx-confirm` prompts and toast messages included. See
  [i18n.md](core/i18n.md).

### Honest tradeoffs

| | Cost | Benefit |
|---|------|---------|
| Build step | One downloaded binary + `make css` | No npm, no Node, no `node_modules` |
| Components | You own the navbar, modal, dropdown | No fighting someone else's markup contract |
| Accessibility | ARIA is yours to get right | See [Accessibility](#accessibility) — treat it as required |
| CSP | Alpine's CSP build: components live in a JS file | `script-src 'self'` with no `unsafe-eval` and no `unsafe-inline` |
| Browser support | Safari 16.4+, Chrome 111+, Firefox 128+ | Native cascade layers, `oklch()`, container queries |

Tailwind v4 uses modern CSS that older browsers do not implement. If you must
support pre-2023 browsers, this stack is the wrong one; that requirement cannot
be worked around.

**Do not half-migrate.** Tailwind classes with no component layer, or markup
still relying on another framework's JavaScript data attributes, is worse than
either alone.

---

## File Structure

The asset tree is `internal/assets/`, the directory [embed.md](embed.md)
embeds. The Tailwind source sits beside `views/` and `static/`, outside
`static/`, so it is never embedded or served.

```
internal/assets/
├── assets.go              # //go:embed all:views, all:static  (embed.md)
├── input.css              # Tailwind source: tokens + component layer
├── views/                 # html/template files; Tailwind scans these
└── static/
    ├── css/
    │   └── app.css        # BUILT by `make css`: committed, never edited by hand
    └── js/
        ├── htmx.min.js    # vendored, version-locked
        ├── app.js         # Alpine.data components + htmx error hook (yours)
        ├── alpine-focus.min.js  # vendored @alpinejs/focus (x-trap)
        └── alpine.min.js  # vendored @alpinejs/csp, NOT the standard build
```

Vendor rather than hotlink a CDN: no third party in your uptime, no third-party
origin in your CSP, and a version you control. The versions are pinned in the
Makefile ([The Build Step](#the-build-step)) and the files are committed.

---

## The Build Step

Tailwind v4 ships a standalone binary. No Node, no `package.json`.

```makefile
# Makefile
ASSETS           := internal/assets
TAILWIND_VERSION := v4.1.14
TAILWIND_BIN     := .bin/tailwindcss
HTMX_VERSION     := 2.0.4
ALPINE_VERSION   := 3.14.9

$(TAILWIND_BIN):
	@mkdir -p .bin
	@case "$$(uname -s)-$$(uname -m)" in \
	  Darwin-arm64)  F=tailwindcss-macos-arm64 ;; \
	  Darwin-x86_64) F=tailwindcss-macos-x64 ;; \
	  Linux-aarch64) F=tailwindcss-linux-arm64 ;; \
	  Linux-x86_64)  F=tailwindcss-linux-x64 ;; \
	  *) echo "Unsupported platform"; exit 1 ;; \
	esac; \
	curl -fsSL -o $(TAILWIND_BIN) \
	  https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/$$F
	@chmod +x $(TAILWIND_BIN)

css: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(ASSETS)/input.css -o $(ASSETS)/static/css/app.css --minify

css-watch: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(ASSETS)/input.css -o $(ASSETS)/static/css/app.css --watch

css-check: css
	git diff --exit-code $(ASSETS)/static/css/app.css

assets:
	@mkdir -p $(ASSETS)/static/js
	curl -fsSL -o $(ASSETS)/static/js/htmx.min.js \
	  https://unpkg.com/htmx.org@$(HTMX_VERSION)/dist/htmx.min.js
	curl -fsSL -o $(ASSETS)/static/js/alpine.min.js \
	  https://unpkg.com/@alpinejs/csp@$(ALPINE_VERSION)/dist/cdn.min.js
	curl -fsSL -o $(ASSETS)/static/js/alpine-focus.min.js \
	  https://unpkg.com/@alpinejs/focus@$(ALPINE_VERSION)/dist/cdn.min.js
```

```gitignore
.bin/                     # the Tailwind binary is per-platform, downloaded on demand
```

The tool lives in `.bin/`, not `bin/`: `bin/` holds the application binaries
`go build` produces, and a 100MB CSS compiler has no business being copied out
with them.

**Commit `internal/assets/static/css/app.css`.** It is a build artifact, but
`//go:embed` reads it at compile time, so committing it is what lets anyone run
`go build` — CI, a colleague, the deploy host — without the Tailwind binary.
Building CSS during deploy would put a downloader and a compiler in the release
path for no benefit.

CI keeps the committed CSS honest:

```yaml
- run: make css-check
```

Without that check, someone edits a template, forgets `make css`, and ships
markup whose classes were never compiled. The page renders unstyled in
production and nowhere else. In development, run `make css-watch` beside
`make run`; with `APP_ENV=dev` the static tree is read from disk
([embed.md](embed.md#development-vs-production)), so a refresh picks it up.

---

## Design Tokens

Tailwind v4 is configured in CSS. There is no `tailwind.config.js`.

```css
/* internal/assets/input.css */
@import "tailwindcss" source(none);

/* Explicit sources only. Automatic detection would scan the whole repository,
   Go files included. Paths are relative to this file. */
@source "./views";
@source "./static/js/app.js";

@theme {
  /* Brand scale. Every utility compiles to var(--color-brand-N), so a brand
     change is a variable override, not a rebuild. */
  --color-brand-50:  oklch(0.97 0.02 275);
  --color-brand-100: oklch(0.93 0.05 275);
  --color-brand-500: oklch(0.59 0.20 275);
  --color-brand-600: oklch(0.52 0.20 275);
  --color-brand-700: oklch(0.45 0.18 275);

  --font-sans: "Inter", ui-sans-serif, system-ui, sans-serif;
  --radius-card: 0.75rem;   /* becomes the `rounded-card` utility */
}

/* Class-based dark mode; drop this line to follow the OS setting instead. */
@custom-variant dark (&:where(.dark, .dark *));
```

`bg-brand-500` compiles to `background-color: var(--color-brand-500)`. Override
that variable anywhere in the cascade and every utility using it follows. Under
the CSP below, a per-tenant override is served as a stylesheet
(`<link href="/brand.css">`), not an inline `<style>` block.

**Tailwind only sees class names that appear whole in a scanned file.**
`class="text-{{ .tone }}-600"` compiles nothing, and the page renders unstyled.
Write every class out in full, in a template branch or in `app.js`:

```html
<span class="{{ if .Overdue }}text-red-600{{ else }}text-slate-600{{ end }}">…</span>
```

Never assemble class names in Go code either: `.go` files are not scanned.

---

## Visual Design Rules

> Constraints, not suggestions. Break one and say so in the commit message,
> with the reason. The countable ones are enforced by Go tests — see
> [Testing the Rules](#testing-the-rules).

### Which surfaces these apply to

- **App screens** — everything a signed-in user works in. Unconditionally;
  this is the product.
- **Public and marketing pages** — yes, with the larger type budget below.
  They ship with the product and are the first thing a visitor sees.
- **HTML you did not write** — rendered rich text, a tenant's embedded content,
  an email preview. No. It is somebody else's design.

### Type

- **Four sizes and two weights on an app screen.** Reaching for a fifth size
  usually means a hierarchy problem being solved with type instead of spacing.
- **Six sizes on a marketing page.** A hero legitimately needs display sizes
  the app never does. A different budget, not an exemption.
- **No bespoke sizes on either.** `text-[11px]` is how a scale of four becomes a
  scale of nine. Use the scale; if the scale is wrong, change the scale.
- Reuse a size across roles rather than inventing one per role.

### Spacing

- **Every spacing, size and radius divisible by 4, preferably 8.** Tailwind's
  `.5` steps are 2px multiples — `mt-1.5` is 6px, `gap-2.5` is 10px — so they
  break this rule while looking like they belong to the scale.
- Space *inside* elements deliberately, not only between them.
- Group related things tight and unrelated things loose. Proximity does work
  that labels should not have to.

### Color

- **60 / 30 / 10.** Sixty percent neutral, thirty complementary (near-black,
  borders, secondary text), ten brand accent.
- **Build depth with tints of one color, not with more colors.**
- **The accent is a budget.** Spend it on the one thing the reader should look
  at. Everything highlighted means nothing is.

### Visuals

- Flat over gradient, simple over flashy. No stacked shadows.
- Visuals communicate before they decorate.
- **Reuse a motif to connect related parts of a screen.** If a status has a
  color, that color is its pill, its row marker and its icon everywhere, so the
  same color means the same thing in every place.
- Cut one thing before calling it done.

### Copy

These apply to the catalog entries, in every language.

- Shortest phrasing that stays unambiguous. Two words beat four.
- Do not repeat a word from the nearby heading. Under "Categories", the button
  is "Add", not "Add category".
- Name the action, not the abstraction: "Save changes", never "Submit".
- An action keeps its name through the flow: a **Publish** button produces a
  **Published** toast.
- Active voice, sentence case. Errors say what broke and how to fix it; empty
  states invite an action.

### Motion

There are no route transitions to choreograph in a server-rendered app:

- Motion explains a state change — where something came from, what it became.
  One orchestrated moment beats five scattered effects.
- **`prefers-reduced-motion` is honoured globally** in `input.css`, not per
  component, because the components that forget are the ones it is for.
  Durations collapse to a single frame rather than to `none`. Spinners are
  exempt: a spinner moves in place, and one that stops spinning stops saying
  anything.

```css
/* internal/assets/input.css (continued) */
@layer base {
  @media (prefers-reduced-motion: reduce) {
    *:not(.animate-spin), ::before, ::after {
      animation-duration: 1ms !important;
      animation-iteration-count: 1 !important;
      transition-duration: 1ms !important;
      scroll-behavior: auto !important;
    }
  }
}
```

### Standing exceptions

Write each one here with its reason, so nobody deletes it citing a rule and
nobody adds another without arguing for it. The blueprint ships with none. A
common first one: if tenants choose their own brand color, 60/30/10 cannot be
guaranteed — a tenant who picks red gets primary buttons that read as
destructive. Either constrain the hue range in the brand picker or record that
you accept it.

### Self-check before calling a UI change done

The first item is the test; run it. The rest need eyes.

1. `go test ./internal/assets/... ./internal/controllers/...` — type budget,
   bespoke sizes, off-grid spacing, physical directions, CSP-safe Alpine,
   reduced motion.
2. Count accent uses on the screen. More than roughly a tenth of the surface?
3. Read every label aloud. Any word repeated from its own heading? Any label
   describing the system rather than the reader's action?
4. Name the one element the reader should notice first. Is it the most
   emphasized thing there?
5. Remove one accessory. Which one did you remove?

### Budgets are ratchets

The per-page numbers in the test are set at what each page does today, not at
the target. Lower one when you have removed something. Raising one is a
decision to argue for in the commit message, not a way to make a build pass.

---

## The Component Layer

Utility-first does not mean repeating fourteen classes on every button. Define
the few genuinely repeated elements once:

```css
/* internal/assets/input.css (continued) */
@layer components {
  .btn, .btn-primary, .btn-secondary, .btn-danger {
    @apply inline-flex items-center justify-center gap-2 rounded-md px-4 py-2
           text-sm font-medium transition-colors
           focus-visible:outline-2 focus-visible:outline-offset-2
           disabled:pointer-events-none disabled:opacity-50;
  }
  .btn-primary   { @apply bg-brand-600 text-white hover:bg-brand-700
                          focus-visible:outline-brand-600; }
  .btn-secondary { @apply bg-white text-slate-700 ring-1 ring-slate-300
                          hover:bg-slate-50 dark:bg-slate-800 dark:text-slate-100
                          dark:ring-slate-600; }
  .btn-danger    { @apply bg-red-600 text-white hover:bg-red-700; }

  .label { @apply block text-sm font-medium text-slate-700 dark:text-slate-200; }
  .input, .select, .textarea {
    @apply block w-full rounded-md border-0 px-3 py-2 text-slate-900
           ring-1 ring-inset ring-slate-300 placeholder:text-slate-500
           focus:ring-2 focus:ring-inset focus:ring-brand-600
           dark:bg-slate-800 dark:text-slate-100 dark:ring-slate-600;
  }
  .select   { @apply pe-10; }
  .textarea { @apply min-h-24; }
  .form-error { @apply mt-1 text-sm text-red-600 dark:text-red-400; }

  .card {
    @apply rounded-card bg-white p-6 shadow-sm ring-1 ring-slate-200
           dark:bg-slate-800 dark:ring-slate-700;
  }
}
```

Two Tailwind v4 rules shape this block:

- **`@apply` takes utilities only.** `.btn-primary { @apply btn … }` fails the
  build in v4, because `.btn` is a component class, not a utility. Share the
  base by listing the selectors together, as above.
- **The components layer sits below utilities**, so a call site can still say
  `class="btn-primary w-full"` and have `w-full` win.

**Keep this list short.** Buttons, form controls and cards earn a class because
they appear on nearly every page with identical styling. A one-off panel does
not; put the utilities inline where you can see them.

> `.select { @apply pe-10; }` uses `pe-` (padding-inline-end), not `pr-`.
> Logical properties are why RTL needs no second stylesheet. See
> [RTL Support](#rtl-support).

---

## Alpine.js and the CSP

Alpine supplies the behaviour a CSS framework's JS bundle used to: toggles,
dropdowns, modals, dismissible alerts. About 15KB, no build step.

### Why the CSP build

The standard Alpine build evaluates attribute expressions
(`@click="open = !open"`) by compiling strings into functions at runtime, which
requires `'unsafe-eval'` in `script-src`. This blueprint does not allow it.
Use **`@alpinejs/csp`**, Alpine's CSP-compatible build, vendored as
`static/js/alpine.min.js` by `make assets`.

The policy the frontend needs, with nothing loosened
([security.md](core/security.md#content-security-policy)):

```
default-src 'self'; script-src 'self'; style-src 'self';
img-src 'self' data:; font-src 'self'; connect-src 'self';
object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'
```

- **`script-src 'self'`**: no `'unsafe-eval'` (the CSP build), no
  `'unsafe-inline'`, so no inline `<script>` blocks and no `onclick=`
  attributes in any template. A page that needs a few lines of JavaScript gets
  them in `app.js`.
- **`style-src 'self'`**: no `style="…"` attributes and no `<style>` blocks.
  Alpine's `x-show` and `x-transition` set styles through the DOM API, which the
  CSP does not govern, so they still work. Alpine's `:style` binding writes the
  attribute and does not — use `:class` instead. HTMX's built-in indicator
  `<style>` is turned off in the base layout
  ([htmx.md](htmx.md#configuration)).
- **No CDN origins.** Everything is vendored, so nothing outside `'self'` is
  listed.

### The constraint the CSP build imposes

**Every Alpine attribute value is a name, not an expression.** `x-data` names a
component registered with `Alpine.data`; every other directive names a property,
a getter or a method on it, optionally with a dot path (`t.message`).

```html
<!-- Fails silently under the CSP build -->
<div x-data="{ open: false }">
  <button @click="open = !open">…</button>
</div>

<!-- Works -->
<div x-data="disclosure">
  <button @click="toggle" :aria-expanded="open">…</button>
</div>
```

Negations, comparisons, ternaries, arguments and object literals all move into
the component as getters and methods. A method named in `@event` receives the
event as its first argument. `x-for="t in items"` is allowed — Alpine parses the
`in` itself. The test in [Testing the Rules](#testing-the-rules) rejects
anything else, because a violation produces no console error: the button just
does nothing.

### Components live in app.js

```js
// internal/assets/static/js/app.js
document.addEventListener('alpine:init', () => {
  // Open/closed state for dropdowns, menus and the mobile drawer.
  Alpine.data('disclosure', () => ({
    open: false,
    toggle() { this.open = !this.open; },
    close() { this.open = false; },
  }));

  // A flash message the reader can dismiss.
  Alpine.data('dismissible', () => ({
    shown: true,
    dismiss() { this.shown = false; },
  }));

  // A dialog opened by an `open-modal` window event.
  Alpine.data('modal', () => ({
    open: false,
    show() { this.open = true; },
    close() { this.open = false; },
  }));

  // Toasts raised by HX-Trigger: {"toast": {"message": "…", "level": "success"}}.
  // Full class strings, so Tailwind finds them (input.css scans this file).
  const TONES = {
    success: 'bg-green-50 text-green-800 ring-green-200',
    error: 'bg-red-50 text-red-800 ring-red-200',
  };
  Alpine.data('toasts', () => ({
    items: [],
    add(event) {
      const id = Date.now() + Math.random();
      const { message, level } = event.detail;
      this.items.push({ id, message, classes: TONES[level] || TONES.success });
      setTimeout(() => { this.items = this.items.filter((t) => t.id !== id); }, 5000);
    },
  }));
});
```

`app.js` loads **before** Alpine so its `alpine:init` listener is registered in
time; so does the Focus plugin. All scripts use `defer`, which preserves
document order and runs them after the DOM is parsed — Alpine misses the DOM
without it. The htmx error hook that also lives in `app.js` is in
[htmx.md](htmx.md#failures-the-server-did-not-explain).

Add the `x-cloak` rule or every `x-show` element flashes visible on first paint:

```css
/* internal/assets/input.css (continued) */
@layer base {
  [x-cloak] { display: none !important; }
}
```

Use `Alpine.store` for state genuinely shared across components (a global
drawer). Do not reach for it otherwise; a local component is easier to follow.

---

## Alpine + HTMX

They compose well, with two things to know.

**Alpine initializes swapped-in content automatically.** Alpine 3 watches the
DOM with a MutationObserver, so markup HTMX swaps in is initialized without a
hook. Components in it must already be registered in `app.js` — a partial
cannot bring its own script under `script-src 'self'`.

**State inside a swapped element is destroyed with it.** If a component's state
must survive a swap, hoist `x-data` to an ancestor HTMX does not replace:

```html
<!-- x-data survives; only #results is swapped -->
<div x-data="disclosure">
  <input id="q" type="search" name="q" class="input"
         aria-label="{{ t .lang "search.label" }}"
         hx-get="/search" hx-trigger="keyup changed delay:300ms"
         hx-target="#results">
  <div id="results">{{ template "search/_results.html" . }}</div>
</div>
```

### Toasts from the server

`HX-Trigger` lets a handler raise a client-side event, which the `toasts`
component turns into a toast. The Go side, including the non-ASCII escaping a
translated message needs in a header, is in
[htmx.md](htmx.md#toast-notifications).

```html
<!-- internal/assets/views/partials/_toasts.html -->
<div x-data="toasts" @toast.window="add"
     class="fixed bottom-4 end-4 z-50 flex flex-col gap-2"
     role="status" aria-live="polite">
  <template x-for="t in items" :key="t.id">
    <div x-transition class="rounded-md px-4 py-3 text-sm shadow-lg ring-1"
         :class="t.classes" x-text="t.message"></div>
  </template>
</div>
```

`aria-live="polite"` is what makes a toast reach a screen reader. Without it the
message is purely visual.

---

## Component Patterns

Template names follow [embed.md](embed.md): the path under `views/`, so
`partials/_navbar.html`. Every page's data carries `lang`, `isRTL` and `dark`
([i18n.md](core/i18n.md#rtl-support)); inside `range` or `with`, reach them as
`$.lang`.

### Base layout

```html
<!-- internal/assets/views/layouts/base.html -->
<!DOCTYPE html>
<html lang="{{ .lang }}" dir="{{ if .isRTL }}rtl{{ else }}ltr{{ end }}"
      {{ if .dark }}class="dark"{{ end }}>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="htmx-config"
        content='{"includeIndicatorStyles":false,"allowEval":false,"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"422","swap":true},{"code":"[45]..","swap":false,"error":true}]}'>
  <title>{{ block "title" . }}{{ t .lang "app.name" }}{{ end }}</title>

  <link href="/static/css/app.css" rel="stylesheet">
  <script defer src="/static/js/htmx.min.js"></script>
  <script defer src="/static/js/app.js"></script>
  <script defer src="/static/js/alpine-focus.min.js"></script>
  <script defer src="/static/js/alpine.min.js"></script>
  {{ block "head" . }}{{ end }}
</head>
<body class="min-h-screen bg-slate-50 text-slate-900 dark:bg-slate-900 dark:text-slate-100"
      hx-headers='{"X-CSRF-Token": "{{ csrf }}"}'
      data-error-message="{{ t .lang "errors.request_failed" }}">

  <a href="#main" class="sr-only focus:not-sr-only focus:absolute focus:m-4
                         focus:rounded focus:bg-white focus:p-4">
    {{ t .lang "nav.skip_to_content" }}
  </a>

  {{ template "partials/_navbar.html" . }}

  <main id="main" class="mx-auto max-w-5xl px-4 py-8">
    {{ template "partials/_flash.html" . }}
    {{ block "content" . }}{{ end }}
  </main>

  {{ template "partials/_toasts.html" . }}
</body>
</html>
```

One `hx-headers` on `<body>` attaches the CSRF token to every HTMX request
([security.md](core/security.md#csrf-protection)); that requires a token valid
for the session, not one consumed on first use. The `htmx-config` meta and
`data-error-message` are explained in [htmx.md](htmx.md#configuration).

### Navbar with mobile toggle and user dropdown

```html
<!-- internal/assets/views/partials/_navbar.html -->
<nav x-data="disclosure"
     class="border-b border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-800">
  <div class="mx-auto flex max-w-5xl items-center gap-4 px-4 py-3">
    <a href="/" class="font-semibold">{{ t .lang "app.name" }}</a>

    <div class="hidden gap-4 md:flex">
      <a href="/dashboard" class="text-sm hover:text-brand-600">{{ t .lang "nav.dashboard" }}</a>
      <a href="/settings" class="text-sm hover:text-brand-600">{{ t .lang "nav.settings" }}</a>
    </div>

    <div class="ms-auto flex items-center gap-2">
      {{ if .user }}
      <!-- Its own disclosure: `open` here is the dropdown, not the drawer. -->
      <div x-data="disclosure" class="relative">
        <button type="button" @click="toggle" @keydown.escape="close"
                :aria-expanded="open" aria-haspopup="menu" aria-controls="user-menu"
                class="btn-secondary">{{ .user.Name }}</button>

        <div id="user-menu" x-show="open" @click.outside="close" x-cloak x-transition
             role="menu"
             class="absolute end-0 mt-2 w-48 rounded-md bg-white py-1 shadow-lg
                    ring-1 ring-black/5 dark:bg-slate-800">
          <a href="/profile" role="menuitem"
             class="block px-4 py-2 text-sm hover:bg-slate-50 dark:hover:bg-slate-700">
            {{ t .lang "nav.profile" }}</a>
          <form action="/logout" method="POST">
            <input type="hidden" name="csrf_token" value="{{ csrf }}">
            <button type="submit" role="menuitem"
                    class="block w-full px-4 py-2 text-start text-sm
                           hover:bg-slate-50 dark:hover:bg-slate-700">
              {{ t .lang "nav.logout" }}</button>
          </form>
        </div>
      </div>
      {{ else }}
      <a href="/login" class="btn-primary">{{ t .lang "nav.login" }}</a>
      {{ end }}

      <button type="button" @click="toggle" :aria-expanded="open"
              aria-controls="mobile-menu" class="btn-secondary md:hidden">
        <span class="sr-only">{{ t .lang "nav.toggle_menu" }}</span>
        {{ template "partials/_icon.html" "menu" }}
      </button>
    </div>
  </div>

  <div id="mobile-menu" x-show="open" x-cloak class="border-t md:hidden">
    <a href="/dashboard" class="block px-4 py-3 text-sm">{{ t .lang "nav.dashboard" }}</a>
    <a href="/settings" class="block px-4 py-3 text-sm">{{ t .lang "nav.settings" }}</a>
  </div>
</nav>
```

Logout is a POST form, not a link: a GET logout is triggerable by any image tag
on any page. Icons are inline SVG from one partial
([mobile-navigation.md](mobile-navigation.md#icons)).

### Dismissible flash messages

```html
<!-- internal/assets/views/partials/_flash.html -->
{{ with .flash }}
<div x-data="dismissible" x-show="shown" x-transition role="alert"
     class="mb-4 flex items-start gap-4 rounded-md p-4 text-sm ring-1
            {{ if eq .type "error" }}bg-red-50 text-red-800 ring-red-200{{ else }}bg-green-50 text-green-800 ring-green-200{{ end }}">
  <span class="flex-1">{{ .message }}</span>
  <button type="button" @click="dismiss" class="opacity-60 hover:opacity-100">
    <span class="sr-only">{{ t $.lang "common.dismiss" }}</span>
    {{ template "partials/_icon.html" "close" }}
  </button>
</div>
{{ end }}
```

`.flash.message` is already translated by the handler that set it.

### Modal

```html
<!-- internal/assets/views/partials/_modal.html -->
<div x-data="modal" @open-modal.window="show" @close-modal.window="close">
  <div x-show="open" x-cloak class="fixed inset-0 z-50 flex items-center justify-center">
    <div class="fixed inset-0 bg-black/50" @click="close" aria-hidden="true"></div>

    <div role="dialog" aria-modal="true" aria-labelledby="modal-title"
         @keydown.escape.window="close" x-trap.noscroll="open"
         x-transition class="card relative z-10 w-full max-w-md">
      <!-- Content is swapped in by HTMX and starts with <h2 id="modal-title">. -->
      <div id="modal-body"></div>
      <div class="mt-6 flex justify-end gap-2">
        <button type="button" @click="close" class="btn-secondary">{{ t .lang "common.cancel" }}</button>
      </div>
    </div>
  </div>
</div>
```

HTMX swaps a form into `#modal-body` and the handler opens the dialog with
`HX-Trigger-After-Swap: open-modal`; a successful submit closes it with a
`close-modal` event ([htmx.md](htmx.md#modal-loaded-by-htmx)). `x-trap.noscroll` moves focus in on open, traps it while open,
restores it on close and stops the page scrolling underneath — Escape and
`aria-modal` alone are not enough.

### Forms

```html
<form method="POST" action="/profile" class="space-y-4">
  <input type="hidden" name="csrf_token" value="{{ csrf }}">

  <div>
    <label for="email" class="label">{{ t .lang "user.email" }}</label>
    <input type="email" id="email" name="email" class="input mt-1"
           value="{{ .form.Email }}"
           {{ with .errors.email }}aria-invalid="true" aria-describedby="email-error"{{ end }}>
    {{ with .errors.email }}
    <p id="email-error" class="form-error">{{ . }}</p>
    {{ end }}
  </div>

  <button type="submit" class="btn-primary">{{ t .lang "common.save_changes" }}</button>
</form>
```

`aria-invalid` and `aria-describedby` are what connect an error message to its
field for assistive technology. Colour alone does not. Validation messages in
`.errors` come from the catalog, translated by the model or handler.

---

## Responsive Design

Mobile-first: unprefixed utilities apply everywhere, prefixed ones apply upward.

```html
<div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">…</div>

<div class="hidden md:block">…</div>
<div class="md:hidden">…</div>
```

| Prefix | Min width |
|--------|-----------|
| (none) | 0 |
| `sm:` | 640px |
| `md:` | 768px |
| `lg:` | 1024px |
| `xl:` | 1280px |
| `2xl:` | 1536px |

When the mobile layout differs in structure rather than in sizing, use a
separate template — see [mobile-navigation.md](mobile-navigation.md).

---

## RTL Support

**Use logical properties and RTL is automatic.** One `dir` attribute, set from
`.isRTL` ([i18n.md](core/i18n.md#rtl-support)), one stylesheet, no
per-direction overrides.

```html
<html lang="{{ .lang }}" dir="{{ if .isRTL }}rtl{{ else }}ltr{{ end }}">
```

| Use | Not | Meaning |
|-----|-----|---------|
| `ms-4` / `me-4` | `ml-4` / `mr-4` | margin-inline-start / end |
| `ps-4` / `pe-4` | `pl-4` / `pr-4` | padding-inline-start / end |
| `text-start` / `text-end` | `text-left` / `text-right` | |
| `start-0` / `end-0` | `left-0` / `right-0` | |
| `border-s` / `border-e` | `border-l` / `border-r` | |
| `rounded-s` / `rounded-e` | `rounded-l` / `rounded-r` | |

Reserve physical properties for things that are genuinely physical — a drop
shadow offset, an icon that must not mirror. Physical classes fail silently: the
page looks right in English and wrong in Arabic, which nobody on the team
reads. The test below rejects them.

Directional icons (back, forward, chevrons) do mirror: add `rtl:rotate-180`, or
keep separate start/end icons.

There is no `[dir="rtl"]` override file. If you find yourself writing one, a
physical class is hiding somewhere.

---

## Dark Mode

With the `@custom-variant` from [Design Tokens](#design-tokens), `dark:` follows
a `dark` class on `<html>`:

```html
<html {{ if .dark }}class="dark"{{ end }}>
```

`.dark` is a bool, set from a user setting stored server-side so it survives
across devices and does not flash on first paint. A bool rather than
`eq .theme "dark"`: a page whose data lacks the key renders light instead of
failing with a comparison error. To follow the OS instead, delete the
`@custom-variant` line and `dark:` follows `prefers-color-scheme` with no markup.

Apply `dark:` in the component layer, not at every call site. That is most of
the work, and it is why `.card` and `.input` carry their own dark variants.

---

## Accessibility

A CSS framework's JS used to ship ARIA wiring inside its components. Owning the
components means owning that too. Tailwind ships none. Budget for it.

**Required on every interactive component you write:**

| Component | Needs |
|-----------|-------|
| Toggle button | `:aria-expanded`, `aria-controls` |
| Dropdown | `aria-haspopup="menu"`, `role="menu"`/`menuitem`, Escape to close |
| Modal | `role="dialog"`, `aria-modal`, `aria-labelledby`, `x-trap` (focus in, trap, restore) |
| Alert / toast | `role="alert"` or `role="status"` + `aria-live` |
| Icon-only button | `<span class="sr-only">` label from the catalog |
| Decorative icon | `aria-hidden="true"` |
| Form error | `aria-invalid` + `aria-describedby` |
| Current nav item | `aria-current="page"`, not colour alone |

**Focus trapping** is the one thing Alpine does not give you declaratively. The
Focus plugin provides `x-trap`; it is vendored by `make assets` and loads
**before** Alpine core, because plugins register against `window.Alpine` during
its initialization.

**Focus visibility:** never remove outlines without replacing them. The
component layer uses `focus-visible:outline-2`, which shows for keyboard users
and not for mouse clicks.

**Contrast:** Tailwind's default palette is not automatically WCAG AA. On white,
`text-slate-500` is borderline for body text and `text-slate-400` fails. Use
`text-slate-600` or darker for anything a user must read, and check the brand
scale against its background rather than assuming.

**Language:** `lang` on `<html>` is what makes a screen reader pronounce the
page in the right voice. It comes from the same `.lang` that selects the
catalog.

---

## Testing the Rules

Each rule below fails silently — the page renders, nothing logs — so each is a
Go test, run by `go test ./...` like everything else
([testing.md](core/testing.md)).

### Counting helpers

```go
// internal/platform/uirules/uirules.go
// Package uirules counts what the visual rules in patterns/frontend.md limit.
// It is imported only by tests.
package uirules

import (
	"regexp"
	"sort"
)

var (
	// Bracketed lengths count as sizes; text-[#333] is a color and does not.
	sizeRe   = regexp.MustCompile(`\btext-(\[[0-9.]+(?:px|rem|em)\]|(?:xs|sm|base|lg|xl|[2-9]xl)\b)`)
	weightRe = regexp.MustCompile(`\bfont-(thin|extralight|light|normal|medium|semibold|bold|extrabold|black)\b`)
	// Tailwind's .5 steps are 2px multiples: mt-1.5 is 6px. None divisible by 4.
	offGridRe = regexp.MustCompile(`\b(?:gap|gap-[xy]|p|px|py|pt|pb|ps|pe|m|mx|my|mt|mb|ms|me|space-[xy]|w|h|size|inset|top|bottom|start|end)-[0-9]+\.5\b`)
)

func distinct(re *regexp.Regexp, s string) []string {
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		seen[m[len(m)-1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FontSizes returns the distinct font sizes used, e.g. ["base", "sm", "[11px]"].
func FontSizes(html string) []string { return distinct(sizeRe, html) }

// FontWeights returns the distinct font weights used.
func FontWeights(html string) []string { return distinct(weightRe, html) }

// OffGrid returns every off-grid spacing class, duplicates included.
func OffGrid(html string) []string { return offGridRe.FindAllString(html, -1) }

// Bespoke returns the arbitrary font sizes among sizes.
func Bespoke(sizes []string) []string {
	var out []string
	for _, s := range sizes {
		if s[0] == '[' {
			out = append(out, s)
		}
	}
	return out
}
```

### Rendered pages: the type and spacing budgets

Counted on rendered HTML rather than on templates, because a screen is
assembled from several templates — layout, navbar, page, partials — and no
single file owns it. Only what a visitor receives is countable.

```go
// internal/controllers/ui_budgets_test.go
package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"yourapp/internal/platform/uirules"
)

type budget struct{ sizes, weights, offGrid int }

// Ratchets: what each page draws today. Lower a number when you remove
// something; raising one is a decision for the commit message.
var budgets = map[string]budget{
	"/dashboard": {4, 2, 0},
	"/settings":  {4, 2, 0},
	"/":          {6, 2, 0}, // marketing page: the six-size budget
}

func TestPagesStayWithinTheirBudgets(t *testing.T) {
	router := setupRouter() // core/testing.md: register the pages measured here
	for path, b := range budgets {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s: status %d", path, w.Code)
			}
			html := w.Body.String()

			sizes := uirules.FontSizes(html)
			if len(sizes) > b.sizes {
				t.Errorf("%d font sizes, budget %d: %v", len(sizes), b.sizes, sizes)
			}
			if bespoke := uirules.Bespoke(sizes); len(bespoke) > 0 {
				t.Errorf("bespoke font sizes: %v", bespoke)
			}
			if weights := uirules.FontWeights(html); len(weights) > b.weights {
				t.Errorf("%d font weights, budget %d: %v", len(weights), b.weights, weights)
			}
			if off := uirules.OffGrid(html); len(off) > b.offGrid {
				t.Errorf("%d off-grid values, budget %d: %v", len(off), b.offGrid, off)
			}
		})
	}
}
```

Render each page with fixtures that make its conditional components appear — a
pinned item, an unread badge, a validation error. A component that never
renders in the test is a component whose sizes are never counted.

### Source files: everything a rendered page cannot show

```go
// internal/assets/frontend_test.go
package assets

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"yourapp/internal/platform/uirules"
)

var (
	classAttr = regexp.MustCompile(`class="([^"]*)"`)
	// Physical left/right utilities, with or without a variant prefix (md:ml-4).
	physical = regexp.MustCompile(`(?:^|[\s:])-?(?:(?:ml|mr|pl|pr|left|right|scroll-m[lr]|scroll-p[lr])-\S+|(?:border|rounded)-(?:[lr]|[tb][lr])(?:-\S+)?(?:\s|$)|(?:text|float|clear)-(?:left|right)\b)`)
	// Alpine directives: x-*, :attr, @event. x-transition and x-cloak take no expression.
	alpineAttr = regexp.MustCompile(`\s((?:x-[\w:.-]+|[:@][\w:.-]+))="([^"]*)"`)
	name       = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$`)
	forExpr    = regexp.MustCompile(`^\(?[A-Za-z_$][\w$]*(?:,\s*[A-Za-z_$][\w$]*)?\)?\s+in\s+[A-Za-z_$][\w$.]*$`)
	inlineCode = regexp.MustCompile(`<script(?:\s[^>]*)?>\s*[^<\s]|<style[\s>]|\sstyle="|\son[a-z]+="|\shx-on[:-]`)
)

func eachTemplate(t *testing.T, fn func(path, src string)) {
	t.Helper()
	err := fs.WalkDir(viewsFS, "views", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, err := fs.ReadFile(viewsFS, path)
		if err != nil {
			return err
		}
		fn(path, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Physical directions render correctly in English and break in RTL.
func TestTemplatesUseLogicalProperties(t *testing.T) {
	eachTemplate(t, func(path, src string) {
		for _, m := range classAttr.FindAllStringSubmatch(src, -1) {
			if bad := physical.FindAllString(m[1], -1); len(bad) > 0 {
				t.Errorf("%s: physical direction %v; use ms-/me-/ps-/pe-/start-/end-/text-start", path, bad)
			}
		}
	})
}

// The Alpine CSP build evaluates names, not expressions. Anything else does
// nothing, with no console error.
func TestAlpineExpressionsAreNames(t *testing.T) {
	eachTemplate(t, func(path, src string) {
		for _, m := range alpineAttr.FindAllStringSubmatch(src, -1) {
			attr, val := m[1], strings.TrimSpace(m[2])
			switch {
			case strings.HasPrefix(attr, "x-transition"), attr == "x-cloak", attr == "x-ref", attr == "x-id":
				continue
			case attr == "x-for":
				if !forExpr.MatchString(val) {
					t.Errorf(`%s: x-for="%s" is not "item in list"`, path, val)
				}
			case !name.MatchString(val):
				t.Errorf(`%s: %s="%s" is an expression; move it into Alpine.data in app.js`, path, attr, val)
			}
		}
	})
}

// script-src 'self' and style-src 'self': inline code is blocked in production.
func TestTemplatesHaveNoInlineScriptOrStyle(t *testing.T) {
	eachTemplate(t, func(path, src string) {
		if m := inlineCode.FindString(src); m != "" {
			t.Errorf("%s: inline script, style or handler (%q) is blocked by the CSP", path, strings.TrimSpace(m))
		}
	})
}

// The component layer never appears in a page as a utility, so the page
// budgets cannot see a bespoke size or off-grid value written inside it.
func TestComponentLayerStaysOnTheScale(t *testing.T) {
	src, err := os.ReadFile("input.css")
	if err != nil {
		t.Fatal(err)
	}
	if bespoke := regexp.MustCompile(`\btext-\[[^\]]+\]`).FindAllString(string(src), -1); len(bespoke) > 0 {
		t.Errorf("input.css: bespoke font sizes %v", bespoke)
	}
	const componentOffGridBudget = 0 // ratchet: may fall, must not rise
	if off := uirules.OffGrid(string(src)); len(off) > componentOffGridBudget {
		t.Errorf("input.css: %d off-grid values, budget %d: %v", len(off), componentOffGridBudget, off)
	}
}

// Asserted on the built CSS, not the source: the built file is what the
// binary embeds, and a stale app.css is the failure worth catching.
func TestBuiltStylesheetHonoursReducedMotion(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "prefers-reduced-motion") {
		t.Error("app.css has no prefers-reduced-motion rule; run make css")
	}
}
```

These tests sit in package `assets` so they read the same `viewsFS` and
`staticFS` the binary serves ([embed.md](embed.md#implementation)), and
`input.css` relative to the package directory, where `go test` runs.

What a test cannot check — accent share, copy, emphasis — is the self-check in
[Visual Design Rules](#self-check-before-calling-a-ui-change-done).

---

**Next:** [HTMX Patterns](htmx.md) | [Mobile Navigation](mobile-navigation.md) | [Security](core/security.md)
