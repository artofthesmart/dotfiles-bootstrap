# Embedded model catalog

`models.json` is the reviewed, release-embedded catalog for deterministic local model selection. It conforms to [`model-catalog-v1.schema.json`](../schemas/local-ai-manager/model-catalog-v1.schema.json) and is additionally checked by Go domain validation.

The October 20, 2026 review intentionally contains only exact local Ollama tags whose GGUF metadata advertises at least 65,536 native context tokens. Cloud-only tags and known sub-65K tags are omitted. Status is conservative:

- `recommended` follows current official local coding guidance but still requires runtime acceptance;
- `fallback` favors a smaller reviewed transfer and memory plan;
- `experimental` has relevant upstream evidence but lacks exact-tag physical acceptance;
- `deprecated` remains identifiable for existing installations but is excluded by default;
- `blocked` is reserved for reviewed tags that must never be selected.

Every entry pins the reviewed model-layer digest and links model, tool, license, and release evidence. Memory values are conservative planning ranges, not measured runtime facts. Shared and unified memory are one pool; dedicated system and accelerator pools are evaluated separately.

Run the dependency-free, read-only drift checker with:

```sh
go run ./cmd/catalog-drift -pretty
```

It compares manifest size, model-layer digest, and bounded GGUF native-context metadata. It emits JSON and exits nonzero on drift. It never updates `models.json`; maintainers must review source and license changes manually. Exact registry checks before acquisition and local metadata, context, tool-call, and offload verification after installation remain authoritative.
