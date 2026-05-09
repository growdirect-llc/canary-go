# UI components convention

Reusable HTML/template primitives for the Canary merchant UI.

## Where they live

```
internal/web/templates/components/
  form-field.html       defines "components/form-field"
  data-table.html       defines "components/data-table"
  status-pill.html      defines "components/status-pill"
  card.html             defines "components/card"
  drawer.html           defines "components/drawer"
```

Naming convention: `{name}.html` defines `{{define "components/{name}"}}`.

The components directory is mounted into every page template set by
`parseTemplateSet()` in `internal/web/handler.go`. Any template parsed via
`mustParse`, `mustParseShared`, or `mustParseMobile` gets `components/*` and
the `componentFuncs` FuncMap in scope automatically.

## Public API per component

Every component file opens with a header contract:

```
{{/*
  components/<name>

  Renders: <one-line summary of what this produces>

  Params (passed via dict in calling template):
    <param>  <type>  required|optional  <one-line description>

  Slots:
    <name>   description (or "none")

  Example:
    {{template "components/<name>" (dict "key" "value" ...)}}
*/}}
```

The header is the **public API**. Consumers may rely on documented params and
slots and only those. If a consumer needs something outside the documented
contract, the component grows (with the doc updated) — never fork.

## Calling a component

Use the `dict` template func (registered in `componentFuncs`) to pass named
params:

```html
{{template "components/form-field"
   (dict "name" "email" "label" "Email" "type" "email" "required" true)}}
```

For HTML-bearing slots (e.g. `card.body`, `drawer.body`), pass values that
implement `template.HTML` so the markup isn't escaped. The simplest path is
to compose the inner content in Go and pass it as `template.HTML(...)`.

## When to add a component

Extract a new component when **all three** are true:

1. The same primitive appears in **3+ templates** today (or 2+ today and one
   imminent), with similar shape.
2. It has a **stable role** — a label-input pair, a status badge, a slide-out
   panel — not a one-off page section.
3. The variation across uses can be expressed as a small fixed param set, not
   "every consumer wants something a little different."

If the third bullet starts to fail (the param list balloons), consider
splitting into two components rather than growing one.

## Anti-patterns to avoid

| Anti-pattern | Why it's bad | Do this instead |
|---|---|---|
| Forking a component for a one-off styling tweak | Two components drift; bug fixes have to land twice | Add a param to the existing component, document it in the header |
| Inlining a `<label>+<input>+<help>+<error>` block in a new screen | Re-creates form-field locally with subtle differences | Use `components/form-field`; if it doesn't fit, extend it |
| Passing rendered HTML strings instead of structured params | Loses escape-by-default safety; couples consumer to component internals | Pass typed params (`name`, `value`, `error`) and let the component render |
| Adding a template func that only one consumer needs | Grows the public surface of every component | Pre-compute in Go, pass the result as a param |
| New screen with no shared primitives | Compounds the duplication this convention exists to prevent | Audit the screen before merge; extract any 3+-template pattern |

## Adding a component — checklist

1. Write a failing test in `internal/web/templates_components_test.go` that
   exercises one behavior of the new component.
2. Verify the test fails for the right reason (component not found / new
   behavior not implemented).
3. Create `internal/web/templates/components/<name>.html` with the documented
   header contract and minimal markup to pass.
4. Verify the test passes.
5. Iterate (RED → GREEN) for each additional behavior.
6. If consumers need new template funcs, add them to `componentFuncs` in
   `internal/web/template_funcs.go` (sparingly).
7. Find one existing template that would benefit, refactor it, ensure its
   tests still pass.

## Visual parity guarantee

Refactoring a template to consume a component should preserve visual
appearance unless explicitly noted in the PR description. If the refactor
intentionally changes appearance (e.g. consolidating five different button
styles into one canonical button), call it out and include a
before/after screenshot.

## Why this exists

- **Maintainability.** As of `internal/web/templates/`'s 117-template growth,
  every page was bespoke. Bug fixes touched many files; visual drift was
  silent. Components consolidate the surface so a single edit propagates.
- **AtlasView migration.** Per the Platform Architecture Charter (2026-05-08),
  the operator surface is on a glide path to AtlasView. Components are the
  unit of work that survives a re-platforming. Bespoke screens have to be
  re-implemented; documented composable primitives can be lifted.
- **Onboarding.** A new contributor reading the components directory sees
  the entire shared visual vocabulary in one place.

## Cross-references

- Loader: `internal/web/handler.go` — `parseTemplateSet()` and the three
  `mustParse*` callers.
- FuncMap: `internal/web/template_funcs.go` — `componentFuncs` and `dict`.
- Tests: `internal/web/templates_components_test.go` — TDD pattern for new
  components.
- Source dispatch: `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`
  (GRO-922 establishes the substrate).
