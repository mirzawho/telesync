# Hand-written documentation

This directory is the part of `docs/api/` the generator never writes.

- `components/` holds one paragraph per topic (authentication, errors,
  pagination, health and the documentation site, Telegram routes, categories,
  users). The generator inserts them into the group pages and lists them on the
  endpoint pages; a component is ordinary Markdown and can link freely.
- `legacy/` holds the layout this documentation had before the generator: one
  page per area, with worked curl commands and field-by-field tables. It is a
  reference, kept because its prose is more detailed than the paragraphs, and
  nothing in the generator reads or rewrites it.

To change what a page says about an endpoint, edit the component, not the
generated page: `go run ./cmd/docsgen` will put the component back into the page
on its next run.
