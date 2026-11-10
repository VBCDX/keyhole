# Examples

`example-litellm.json` is a `keyhole load` fixture: one org, a workspace, an agent, a Local store,
an LLM gateway tool (`https://llm.example.com`) and its grant. It holds no value; put that with
`keyhole keys put`.

**Its org id is `org_example`.** A Keyhole store resolves only under the control plane's own org id
(`org_…`, not the slug), so replace `org_example` (the `orgs` row's `id` and every `org_id`) with
that id before you load it. [../local-store.md](../local-store.md), "The org id", says how to read
it. Change the tool's base URL to your gateway's too.
